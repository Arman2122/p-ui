package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
)

type FleetPlacementTarget struct {
	NodeID              string                  `json:"nodeId"`
	InstallationID      string                  `json:"installationId"`
	RuntimeID           string                  `json:"runtimeId"`
	CoreID              string                  `json:"coreId"`
	NodePolicyRevision  uint64                  `json:"nodePolicyRevision"`
	DefaultRevision     uint64                  `json:"defaultRevision"`
	CapabilityRevision  uint64                  `json:"capabilityRevision"`
	InboundIDs          []int                   `json:"inboundIds"`
	ManualInboundIDs    []int                   `json:"manualInboundIds"`
	Rules               []FleetPlacementRulePin `json:"rules"`
	ApplicationEligible bool                    `json:"applicationEligible"`
	Diagnostic          string                  `json:"diagnostic,omitempty"`
}

type FleetPlacementProjection struct {
	ClientID             string                 `json:"clientId"`
	SourceRevision       uint64                 `json:"sourceRevision"`
	CredentialGeneration uint64                 `json:"credentialGeneration"`
	Targets              []FleetPlacementTarget `json:"targets"`
}

type fleetPlacementSource struct {
	client      model.FleetClient
	source      model.ClientRecord
	preferences FleetPlacementPreferences
	planID      *int
	groupID     *int
	inbounds    []model.Inbound
}

func (s *FleetPlacementService) PreviewClient(ctx context.Context, actor *model.User, clientID string) (FleetPlacementProjection, error) {
	var projection FleetPlacementProjection
	if !fleetID(clientID) {
		return projection, controlplane.ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		source, err := loadFleetPlacementSource(tx, clientID)
		if err != nil {
			return err
		}
		projection, err = projectFleetPlacementTx(tx, source)
		return err
	})
	return projection, err
}

func loadFleetPlacementSource(tx *gorm.DB, clientID string) (fleetPlacementSource, error) {
	var source fleetPlacementSource
	var err error
	source.client, source.source, err = lockFleetSourceClient(tx, clientID)
	if err != nil {
		return source, err
	}
	_, source.preferences, err = fleetPlacementPreferencesTx(tx, clientID)
	if err != nil {
		return source, err
	}
	if err := preserveManualFleetPlacements(tx, clientID, &source.preferences); err != nil {
		return source, err
	}
	var assignment model.ClientPolicy
	assignmentQuery := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("email = ?", source.source.Email).Find(&assignment)
	if assignmentQuery.Error != nil {
		return source, assignmentQuery.Error
	}
	if assignmentQuery.RowsAffected != 0 && (assignment.PolicyId == nil || assignment.ResolvedTiers != nil) {
		return source, ErrFleetSource
	}
	source.planID = assignment.PolicyId
	var ids []int
	if err := tx.Model(&model.ClientInbound{}).Where("client_id = ?", source.source.Id).Order("inbound_id").Limit(4097).Pluck("inbound_id", &ids).Error; err != nil {
		return source, err
	}
	var selected *model.ClientPolicy
	if assignmentQuery.RowsAffected != 0 {
		selected = &assignment
	}
	ids, err = fleetEffectiveTemplateIDsTx(tx, source.source, selected, ids)
	if err != nil {
		return source, err
	}
	if source.source.Group != "" && source.source.OwnerUserID == nil {
		var group model.ClientGroup
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&group, "name = ?", source.source.Group).Error; err != nil {
			return source, err
		}
		source.groupID = &group.Id
	}
	if len(ids) > 0 {
		err = tx.Where("id IN ? AND node_id IS NULL", ids).Order("id").Find(&source.inbounds).Error
	}
	return source, err
}

func projectFleetPlacementTx(tx *gorm.DB, source fleetPlacementSource) (FleetPlacementProjection, error) {
	projection := FleetPlacementProjection{ClientID: source.client.ID, SourceRevision: uint64(source.client.PlacementRevision), CredentialGeneration: uint64(source.client.CredentialGeneration), Targets: []FleetPlacementTarget{}}
	var rules []model.FleetPlacementRule
	if source.planID != nil || source.groupID != nil {
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("enabled AND (plan_id = ? OR group_id = ?)", source.planID, source.groupID).Order("id").Limit(129).Find(&rules).Error; err != nil {
			return projection, err
		}
		if len(rules) > 128 {
			return projection, controlplane.ErrInvalid
		}
	}
	targets := map[string]*FleetPlacementTarget{}
	for _, rule := range rules {
		var nodes []string
		var labels map[string]string
		var ids []int
		if json.Unmarshal([]byte(rule.NodeIDs), &nodes) != nil || json.Unmarshal([]byte(rule.Labels), &labels) != nil || json.Unmarshal([]byte(rule.InboundIDs), &ids) != nil || len(nodes) == 0 && len(labels) == 0 {
			return projection, controlplane.ErrRecovery
		}
		query := tx.Where("enabled")
		if len(nodes) > 0 {
			query = query.Where("node_id IN ?", nodes)
		}
		if len(labels) > 0 {
			query = query.Where("labels @> ?::jsonb", rule.Labels)
		}
		var policies []model.FleetNodePlacementPolicy
		if err := query.Order("node_id").Limit(1001).Find(&policies).Error; err != nil {
			return projection, err
		}
		if len(policies) > 1000 {
			return projection, controlplane.ErrInvalid
		}
		for _, policy := range policies {
			if err := addFleetPlacementTargets(targets, source, policy, ids, &FleetPlacementRulePin{ID: rule.ID, Revision: uint64(rule.Revision)}, 0, false); err != nil {
				return projection, err
			}
		}
	}
	for nodeID, ids := range source.preferences.ManualTargets {
		var policy model.FleetNodePlacementPolicy
		query := tx.Where("node_id = ? AND enabled", nodeID).Find(&policy)
		if query.Error != nil {
			return projection, query.Error
		}
		if query.RowsAffected != 0 {
			if err := addFleetPlacementTargets(targets, source, policy, ids, nil, 0, true); err != nil {
				return projection, err
			}
		}
	}
	if source.planID == nil && source.groupID == nil && len(source.preferences.ManualTargets) == 0 {
		var defaults model.FleetPlacementDefaults
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = 1").Find(&defaults).Error; err != nil {
			return projection, err
		}
		if defaults.NodeID != nil && defaults.InstallationID != nil {
			var policy model.FleetNodePlacementPolicy
			query := tx.Where("node_id = ? AND installation_id = ? AND enabled", *defaults.NodeID, *defaults.InstallationID).Find(&policy)
			if query.Error != nil {
				return projection, query.Error
			}
			if query.RowsAffected != 0 {
				ids := make([]int, 0, len(source.inbounds))
				for _, inbound := range source.inbounds {
					ids = append(ids, inbound.Id)
				}
				if err := addFleetPlacementTargets(targets, source, policy, ids, nil, defaults.Revision, false); err != nil {
					return projection, err
				}
			}
		}
	}
	if len(targets) > 8000 {
		return projection, controlplane.ErrInvalid
	}
	for _, target := range targets {
		slices.Sort(target.InboundIDs)
		target.InboundIDs = slices.Compact(target.InboundIDs)
		slices.Sort(target.ManualInboundIDs)
		target.ManualInboundIDs = slices.Compact(target.ManualInboundIDs)
		if err := validateFleetPlacementCapabilities(tx, source, target); err != nil {
			return projection, err
		}
		projection.Targets = append(projection.Targets, *target)
	}
	slices.SortFunc(projection.Targets, func(a, b FleetPlacementTarget) int {
		return strings.Compare(a.NodeID+"/"+a.RuntimeID, b.NodeID+"/"+b.RuntimeID)
	})
	return projection, nil
}

func addFleetPlacementTargets(targets map[string]*FleetPlacementTarget, source fleetPlacementSource, policy model.FleetNodePlacementPolicy, desired []int, pin *FleetPlacementRulePin, defaultRevision int64, manual bool) error {
	var eligible []int
	if json.Unmarshal([]byte(policy.InboundIDs), &eligible) != nil || len(eligible) > 4096 {
		return controlplane.ErrRecovery
	}
	for _, inbound := range source.inbounds {
		if !slices.Contains(desired, inbound.Id) || !slices.Contains(eligible, inbound.Id) || !placementInboundAllowed(source.preferences, policy.NodeID, inbound.Id) {
			continue
		}
		family, known := cores.NodeRuntimeFor(core.Kind(inbound.Protocol))
		if !known {
			continue
		}
		key := policy.NodeID + "/" + policy.InstallationID + "/" + family
		target := targets[key]
		if target == nil {
			target = &FleetPlacementTarget{NodeID: policy.NodeID, InstallationID: policy.InstallationID, RuntimeID: family, CoreID: family, NodePolicyRevision: uint64(policy.Revision), InboundIDs: []int{}, ManualInboundIDs: []int{}, Rules: []FleetPlacementRulePin{}}
			targets[key] = target
		}
		target.InboundIDs = append(target.InboundIDs, inbound.Id)
		if manual {
			target.ManualInboundIDs = append(target.ManualInboundIDs, inbound.Id)
		}
		target.DefaultRevision = max(target.DefaultRevision, uint64(defaultRevision))
		if pin != nil && !slices.Contains(target.Rules, *pin) {
			target.Rules = append(target.Rules, *pin)
		}
	}
	return nil
}

func validateFleetPlacementCapabilities(tx *gorm.DB, source fleetPlacementSource, target *FleetPlacementTarget) error {
	resolved, err := resolveFleetPlacementRuntime(tx, source, target)
	if err != nil || !resolved {
		return err
	}
	if target.RuntimeID != target.CoreID {
		target.Diagnostic = "RUNTIME_ALIAS_UNQUALIFIED"
		return nil
	}
	var node model.FleetNode
	if err := tx.First(&node, "id = ?", target.NodeID).Error; err != nil {
		return err
	}
	target.Diagnostic = "CAPABILITIES_UNAVAILABLE"
	if node.InstallationID != target.InstallationID || !node.Enabled {
		target.Diagnostic = "NODE_UNAVAILABLE"
		return nil
	}
	var snapshot model.FleetNodeCapabilitySnapshot
	query := tx.Where("node_id = ? AND installation_id = ?", node.ID, node.InstallationID).Find(&snapshot)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected == 0 || snapshot.SessionGeneration != node.AuthorityGeneration || node.LastSeenUnixMilli < time.Now().Add(-2*time.Minute).UnixMilli() {
		return nil
	}
	target.CapabilityRevision = uint64(snapshot.Revision)
	var capabilities []controlplane.PlacementCapability
	if json.Unmarshal([]byte(snapshot.Capabilities), &capabilities) != nil {
		return controlplane.ErrRecovery
	}
	for _, capability := range capabilities {
		if capability.CoreID != target.CoreID || !capability.ClientRateEnforcement || !capability.ClientQuotaEnforcement {
			continue
		}
		for _, inbound := range source.inbounds {
			if slices.Contains(target.InboundIDs, inbound.Id) && !slices.Contains(capability.Protocols, string(inbound.Protocol)) {
				target.Diagnostic = "PROTOCOL_UNAVAILABLE"
				return nil
			}
		}
		if !nodeadmission.SupportsRuntime(target.CoreID) {
			target.Diagnostic = "NATIVE_FUNDING_REQUIRED"
			return nil
		}
		if !capability.ActivationAdmission {
			target.Diagnostic = "ACTIVATION_UNAVAILABLE"
			return nil
		}
		target.ApplicationEligible, target.Diagnostic = true, ""
		return nil
	}
	return nil
}

func requireFleetPlacementApplicationTx(tx *gorm.DB, snapshot fleetClientSnapshot, placement model.FleetPlacement) error {
	_, exists, err := fleetPlacementIntentTx(tx, placement)
	if err != nil || !exists {
		return err
	}
	ids := make([]int, 0, len(snapshot.Inbounds))
	for _, inbound := range snapshot.Inbounds {
		ids = append(ids, inbound.Id)
	}
	target := FleetPlacementTarget{NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID, CoreID: placement.CoreID, InboundIDs: ids}
	if err := validateFleetPlacementCapabilities(tx, fleetPlacementSource{client: snapshot.Fleet, inbounds: snapshot.Inbounds}, &target); err != nil {
		return err
	}
	if !target.ApplicationEligible || target.RuntimeID != placement.RuntimeID {
		return ErrFleetNetworkDependency
	}
	return nil
}
