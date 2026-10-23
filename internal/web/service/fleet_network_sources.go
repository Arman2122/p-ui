package service

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

type fleetNetworkState struct {
	Head               model.FleetNodeNetworkHead
	Node               model.FleetNode
	Revision, Defaults model.FleetNetworkRevision
	Overrides          FleetNetworkOverrides
	Resolved           FleetNetworkSpec
	Origins            map[string]string
}

func loadFleetNetworkState(tx *gorm.DB, nodeID, installationID string) (fleetNetworkState, error) {
	var state fleetNetworkState
	if err := lockFleetNetworkNode(tx, nodeID, installationID); err != nil {
		return state, err
	}
	if err := tx.First(&state.Node, "id = ?", nodeID).Error; err != nil {
		return state, err
	}
	var global model.FleetNetworkDefaultsHead
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&global, "id = 1").Error; err != nil {
		return state, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state.Head, "node_id = ?", nodeID).Error; err != nil {
		return state, err
	}
	if state.Head.InstallationID != installationID || state.Head.FollowDefaults && state.Head.DesiredDefaultsID != global.CurrentRevisionID {
		return state, ErrFleetNetworkRevision
	}
	var err error
	state.Revision, err = loadNetworkRevision(tx, state.Head.CurrentRevisionID, "node")
	if err != nil {
		return state, err
	}
	if state.Revision.NodeID == nil || state.Revision.InstallationID == nil || *state.Revision.NodeID != nodeID || *state.Revision.InstallationID != installationID {
		return state, ErrFleetNetworkRevision
	}
	state.Defaults, err = loadNetworkRevision(tx, state.Head.DesiredDefaultsID, "defaults")
	if err != nil {
		return state, err
	}
	var spec FleetNetworkSpec
	if json.Unmarshal([]byte(state.Revision.Specification), &state.Overrides) != nil || json.Unmarshal([]byte(state.Defaults.Specification), &spec) != nil {
		return state, ErrFleetNetwork
	}
	state.Resolved, state.Origins, err = resolveFleetNetwork(spec, state.Overrides)
	return state, err
}

func fleetNetworkUser(snapshot fleetClientSnapshot, inboundID int) (core.User, error) {
	client := snapshot.Client.ToClient()
	for _, link := range snapshot.Attachments {
		if link.InboundId == inboundID {
			client.Flow = link.FlowOverride
			break
		}
	}
	credentials := map[string]string{}
	for _, row := range snapshot.Credentials {
		if row.InboundId == inboundID {
			credentials[row.Key] = row.Value
		}
	}
	applyCoreCredentials(client, credentials)
	encoded, err := json.Marshal(client)
	if err != nil {
		return core.User{}, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil {
		return core.User{}, ErrFleetNetwork
	}
	return webruntime.ProjectUser(fields), nil
}

func fleetNetworkInbound(source model.Inbound, binding FleetInboundNetwork, instanceID string) (nodeconfig.Inbound, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(source.Settings), &fields) != nil {
		return nodeconfig.Inbound{}, ErrFleetNetwork
	}
	delete(fields, "clients")
	settings, err := json.Marshal(fields)
	if err != nil {
		return nodeconfig.Inbound{}, err
	}
	instance := core.Instance{
		ID: source.Id, Kind: core.Kind(source.Protocol), Tag: source.Tag, Listen: source.Listen, Port: source.Port, Enable: true,
		Settings: string(settings), StreamSettings: source.StreamSettings, Sniffing: source.Sniffing,
	}
	if binding.Listen != nil {
		instance.Listen = *binding.Listen
	}
	if binding.Port != nil {
		instance.Port = int(*binding.Port)
	}
	if binding.Settings != nil {
		instance.Settings = string(*binding.Settings)
	}
	if binding.StreamSettings != nil {
		instance.StreamSettings = string(*binding.StreamSettings)
	}
	if binding.Sniffing != nil {
		instance.Sniffing = string(*binding.Sniffing)
	}
	target := binding.TargetID
	if target == 0 {
		if source.Id > 999999999 {
			return nodeconfig.Inbound{}, ErrFleetNetwork
		}
		target = uint32(source.Id)
	}
	tag := binding.TargetTag
	if tag == "" {
		tag = "node-" + instanceID
	}
	egress := binding.EgressMode
	if egress == "" {
		if source.EgressID != nil {
			return nodeconfig.Inbound{}, ErrFleetNetworkDependency
		}
	}
	result := nodeconfig.Inbound{
		Instance: instance, TargetID: target, TargetTag: tag, Assets: binding.Assets, Egress: nodeconfig.EgressBinding{Mode: egress, BridgeID: binding.BridgeID},
		Native: nodeconfig.NativeInboundBinding{Addresses: binding.Addresses, Subnet: binding.Subnet, MTU: binding.MTU},
	}
	if binding.MTProto != nil {
		result.MTProto = &nodeconfig.MTProtoBinding{TrustedProxies: binding.MTProto.TrustedProxies}
		if web := binding.MTProto.WEB; web != nil {
			result.MTProto.WEB = &nodeconfig.MTProtoWEBBinding{Listen: web.Listen, Port: web.Port, PublicPort: web.PublicPort}
		}
	}
	return result, nil
}

func prepareFleetNetworkSources(tx *gorm.DB, state fleetNetworkState) (nodeconfig.Input, FleetNetworkManifest, []FleetNetworkDiagnostic, error) {
	input := nodeconfig.Input{NodeID: state.Node.ID, NativeNetwork: state.Resolved.NativeNetwork}
	manifest := FleetNetworkManifest{
		Version: 1, NodeID: state.Node.ID, InstallationID: state.Node.InstallationID, NodeRevisionID: state.Revision.ID,
		DefaultsRevisionID: state.Defaults.ID, NodeRevision: uint64(state.Head.Revision), BaseDeploymentRevision: uint64(state.Node.DesiredRevision),
	}
	var issues []FleetNetworkDiagnostic
	type selected struct {
		PlacementID   string
		PanelClientID *int
	}
	var selectedPlacements []selected
	if err := tx.Table("fleet_placements p").Select("p.id AS placement_id, c.panel_client_id").Joins("JOIN fleet_clients c ON c.id = p.client_id").
		Where("p.node_id = ? AND p.installation_id = ?", state.Node.ID, state.Node.InstallationID).Order("c.panel_client_id, p.id").Limit(60001).Scan(&selectedPlacements).Error; err != nil {
		return input, manifest, nil, err
	}
	if len(selectedPlacements) > 60000 {
		return input, manifest, nil, ErrFleetNetwork
	}
	disabled, err := completedFleetSourceDisables(tx, state.Node)
	if err != nil {
		return input, manifest, nil, err
	}
	retirements, err := completedFleetPlacementRetirements(tx, state.Node)
	if err != nil {
		return input, manifest, nil, err
	}
	byInbound := map[int]int{}
	seen := map[string]bool{}
	for _, selected := range selectedPlacements {
		if selected.PanelClientID == nil {
			issues = append(issues, FleetNetworkDiagnostic{Code: "PLACEMENT_SOURCE_DELETED", PlacementID: selected.PlacementID})
			continue
		}
		if work, exists := disabled[selected.PlacementID]; exists {
			identity, retired, err := fleetSourceDisabledRetirement(tx, state.Node, work)
			if err != nil {
				return input, manifest, nil, err
			}
			if retired {
				manifest.Identities = append(manifest.Identities, identity)
				continue
			}
		}
		if intent, exists := retirements[selected.PlacementID]; exists {
			identity, retired, err := fleetPlacementRetirementIdentity(tx, state.Node, intent)
			if err != nil {
				return input, manifest, nil, err
			}
			if retired {
				manifest.Identities = append(manifest.Identities, identity)
				continue
			}
		}
		snapshot, placement, references, err := fleetPlacementSnapshot(tx, selected.PlacementID)
		if err != nil {
			if errors.Is(err, ErrFleetSource) || errors.Is(err, ErrResellerForbidden) || errors.Is(err, ErrResellerBudget) {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PLACEMENT_SOURCE_CHANGED", PlacementID: selected.PlacementID})
				continue
			}
			return input, manifest, nil, err
		}
		pin := FleetNetworkPlacementPin{
			PlacementID: placement.ID, ClientID: snapshot.Fleet.ID, Revision: uint64(placement.Revision), CredentialGeneration: uint64(snapshot.Fleet.CredentialGeneration),
			PolicySHA256: hex.EncodeToString(snapshot.PolicyDigest), DefinitionSHA256: hex.EncodeToString(snapshot.DefinitionDigest), CredentialSHA256: hex.EncodeToString(snapshot.CredentialDigest),
			GraphSHA256: hex.EncodeToString(placement.GraphDigest), Owned: snapshot.Fleet.Owned, Enabled: placement.Enabled,
		}
		identity := FleetNetworkAuthorityIdentity{
			ClientID: snapshot.Fleet.ID, CredentialGeneration: uint64(snapshot.Fleet.CredentialGeneration), PlacementID: placement.ID,
			PlacementRevision: uint64(placement.Revision), RuntimeID: placement.RuntimeID, State: "active",
			InboundTags: map[string]string{},
		}
		instances := map[int]string{}
		for _, reference := range references {
			if reference.PanelInbound != nil {
				instances[*reference.PanelInbound] = reference.InstanceID
				identity.InboundInstanceIDs = append(identity.InboundInstanceIDs, reference.InstanceID)
				tag := state.Resolved.Inbounds[*reference.PanelInbound].TargetTag
				if tag == "" {
					tag = "node-" + reference.InstanceID
				}
				identity.InboundTags[reference.InstanceID] = tag
			}
		}
		slices.Sort(identity.InboundInstanceIDs)
		if snapshot.Term != nil {
			pin.TermID, identity.TermID = snapshot.Term.ID, snapshot.Term.ID
		}
		manifest.Placements = append(manifest.Placements, pin)
		diagnostic := FleetNetworkDiagnostic{PlacementID: placement.ID, ClientID: snapshot.Fleet.ID, RuntimeID: placement.RuntimeID}
		switch {
		case !snapshot.Fleet.Owned:
			identity.State = "cutover_required"
			diagnostic.Code = "OWNERSHIP_CUTOVER_REQUIRED"
		case !snapshot.Client.Enable || snapshot.Client.ExpiryTime > 0 && snapshot.Client.ExpiryTime <= time.Now().UnixMilli() ||
			snapshot.Term != nil && (snapshot.Term.State != "active" || snapshot.Term.ExpiresUnixMilli != 0 && snapshot.Term.ExpiresUnixMilli <= time.Now().UnixMilli()):
			identity.State = "inactive"
			diagnostic.Code = "CLIENT_AUTHORIZATION_INACTIVE"
		case snapshot.Term == nil:
			identity.State = "dormant"
			if !nodeadmission.SupportsRuntime(placement.CoreID) {
				diagnostic.Code = "DORMANT_AUTHENTICATED_DEMAND_REQUIRED"
			}
		case fleetEntitlementMatches(snapshot) != nil:
			identity.State = "reconciliation_required"
			diagnostic.Code = "TERM_RECONCILIATION_REQUIRED"
		case placement.CurrentGrantID == nil || placement.CurrentGrantTermID == nil || *placement.CurrentGrantTermID != snapshot.Term.ID:
			identity.State = "authority_required"
		}
		if identity.State == "inactive" && placement.RuntimeID == "native" && placement.CoreID == "native" {
			stopped, err := stoppedNativePlacements(tx, state.Node, map[string]string{placement.ID: snapshot.Fleet.ID})
			if err != nil {
				return input, manifest, nil, err
			}
			if stopped[placement.ID] {
				identity.State = "retired"
				manifest.Placements = manifest.Placements[:len(manifest.Placements)-1]
				manifest.Identities = append(manifest.Identities, identity)
				continue
			}
		}
		if diagnostic.Code == "" && snapshot.Term != nil {
			var budget model.FleetBudget
			if err := tx.First(&budget, "id = ?", snapshot.Term.BudgetID).Error; err != nil {
				return input, manifest, nil, err
			}
			var rate model.FleetRateBudget
			if err := tx.First(&rate, "id = ?", snapshot.Fleet.ID).Error; err != nil {
				return input, manifest, nil, err
			}
			var desired controlplane.Rates
			if json.Unmarshal([]byte(rate.Desired), &desired) != nil {
				return input, manifest, nil, ErrFleetNetwork
			}
			if budget.Unlimited != (snapshot.Client.TotalGB == 0) || !budget.Unlimited && budget.LimitBytes != snapshot.Client.TotalGB {
				identity.State = "reconciliation_required"
				diagnostic.Code = "TERM_RECONCILIATION_REQUIRED"
			} else if desired != fleetRates(snapshot.Tiers, budget.UsedBytes) {
				identity.State = "reconciliation_required"
				diagnostic.Code = "RATE_RECONCILIATION_REQUIRED"
			}
		}
		manifest.Identities = append(manifest.Identities, identity)
		if diagnostic.Code != "" {
			issues = append(issues, diagnostic)
			continue
		}
		for _, source := range snapshot.Inbounds {
			key := strconv.Itoa(source.Id) + "/" + snapshot.Fleet.ID
			if seen[key] {
				return input, manifest, nil, ErrFleetNetwork
			}
			seen[key] = true
			index, exists := byInbound[source.Id]
			if !exists {
				inbound, err := fleetNetworkInbound(source, state.Resolved.Inbounds[source.Id], instances[source.Id])
				if err != nil {
					issues = append(issues, FleetNetworkDiagnostic{Code: "INBOUND_NETWORK_BINDING_REQUIRED", InboundID: source.Id, RuntimeID: placement.RuntimeID})
					continue
				}
				index = len(input.Inbounds)
				byInbound[source.Id] = index
				input.Inbounds = append(input.Inbounds, inbound)
			}
			user, err := fleetNetworkUser(snapshot, source.Id)
			if err != nil {
				return input, manifest, nil, err
			}
			input.Inbounds[index].Clients = append(input.Inbounds[index].Clients, nodeconfig.Client{ClientID: snapshot.Fleet.ID, User: user})
		}
	}
	for id, base := range state.Resolved.Bases {
		input.Bases = append(input.Bases, nodeconfig.Base{RuntimeID: id, Core: base.Core, Configuration: base.Configuration, Assets: base.Assets})
	}
	for id, bridge := range state.Resolved.Bridges {
		input.Bridges = append(input.Bridges, nodeconfig.Bridge{ID: id, NodeID: state.Node.ID, RuntimeID: bridge.RuntimeID, Core: bridge.Core, Endpoint: bridge.Endpoint, UDP: bridge.UDP})
	}
	if keys := state.Resolved.SSHHostKeys; keys != nil {
		input.SSHHostKeys = nodeconfig.HostKeyBinding{GenerateLocally: keys.GenerateLocally, AssetID: keys.AssetSlot}
	}
	slices.SortFunc(input.Inbounds, func(a, b nodeconfig.Inbound) int { return a.Instance.ID - b.Instance.ID })
	slices.SortFunc(input.Bases, func(a, b nodeconfig.Base) int { return strings.Compare(a.RuntimeID, b.RuntimeID) })
	slices.SortFunc(input.Bridges, func(a, b nodeconfig.Bridge) int { return strings.Compare(a.ID, b.ID) })
	bindingIssues, err := prepareFleetNativeBindings(tx, state, &input, &manifest)
	if err != nil {
		return input, manifest, nil, err
	}
	issues = append(issues, bindingIssues...)
	providerIssues, err := prepareFleetNetworkProviders(tx, state, &input, &manifest)
	if err != nil {
		return input, manifest, nil, err
	}
	issues = append(issues, providerIssues...)
	return input, manifest, issues, nil
}
