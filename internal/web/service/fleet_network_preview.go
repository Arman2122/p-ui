package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
)

const fleetNetworkCompilerVersion = "node-network-v3"

func fleetCompilerDiagnostic(err error) string {
	switch {
	case errors.Is(err, nodeconfig.ErrClientBinding):
		return "NATIVE_CLIENT_BINDING_REQUIRED"
	case errors.Is(err, nodeconfig.ErrDependency):
		return "COMPILER_DEPENDENCY_REQUIRED"
	case errors.Is(err, nodeconfig.ErrUnsupported):
		return "COMPILER_FEATURE_UNQUALIFIED"
	case errors.Is(err, nodeconfig.ErrConflict):
		return "NETWORK_RESOURCE_CONFLICT"
	default:
		return "INVALID_COMPILER_INPUT"
	}
}

func fleetNetworkDependencies(compiled nodeconfig.Result) []FleetNetworkDiagnostic {
	var issues []FleetNetworkDiagnostic
	for _, claim := range compiled.Assets {
		if claim.Delivery == nodeconfig.AssetFile {
			issues = append(issues, FleetNetworkDiagnostic{Code: "ASSET_MATERIALIZATION_REQUIRED", RuntimeID: claim.RuntimeID, InboundID: claim.SourceID, AssetSlot: claim.AssetID})
		}
	}
	for _, claim := range compiled.Bridges {
		issues = append(issues, FleetNetworkDiagnostic{Code: "BRIDGE_PRODUCER_UNQUALIFIED", RuntimeID: claim.RuntimeID, InboundID: claim.SourceID})
	}
	for _, omission := range compiled.Omissions {
		issues = append(issues, FleetNetworkDiagnostic{Code: "COMPILED_SOURCE_OMITTED", ClientID: omission.ClientID, InboundID: omission.SourceID})
	}
	return issues
}

func (s *FleetNetworkService) Preview(ctx context.Context, actor *model.User, request FleetNetworkPreviewRequest) (FleetNetworkPreview, error) {
	var view FleetNetworkPreview
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var err error
		view, err = previewFleetNetworkTx(tx, request, actor.Id)
		return err
	})
	return view, err
}

func previewFleetNetworkTx(tx *gorm.DB, request FleetNetworkPreviewRequest, actorID int) (FleetNetworkPreview, error) {
	var view FleetNetworkPreview
	err := func() error {
		var fence model.FleetFence
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&fence, "id = 1").Error; err != nil {
			return err
		}
		state, err := loadFleetNetworkState(tx, request.NodeID, request.InstallationID)
		if err != nil {
			return err
		}
		if uint64(state.Head.Revision) != request.ExpectedRevision || state.Head.DesiredDefaultsID != request.ExpectedDefaultsID {
			return ErrFleetNetworkRevision
		}
		input, manifest, issues, err := prepareFleetNetworkSources(tx, state)
		if err != nil {
			return err
		}
		pins, assetIssues, err := prepareFleetNetworkAssets(tx, state, &input)
		if err != nil {
			return err
		}
		manifest.Assets = pins
		issues = append(issues, assetIssues...)
		var compiled nodeconfig.Result
		if len(assetIssues) == 0 {
			compiled, err = nodeconfig.Compile(input)
			if err != nil {
				issues = append(issues, FleetNetworkDiagnostic{Code: fleetCompilerDiagnostic(err)})
			}
		}
		issues = append(issues, fleetNetworkDependencies(compiled)...)
		available := map[string]map[string]bool{}
		coreByRuntime := map[string]string{}
		for _, runtime := range compiled.Runtimes {
			coreByRuntime[runtime.ID] = runtime.Core
			available[runtime.ID] = map[string]bool{}
			for _, clientID := range runtime.ClientIDs {
				available[runtime.ID][clientID] = true
			}
		}
		for _, identity := range manifest.Identities {
			if (identity.State == "active" || identity.State == "authority_required" || identity.State == "dormant" && len(issues) == 0) && !available[identity.RuntimeID][identity.ClientID] {
				issues = append(issues, FleetNetworkDiagnostic{Code: "ACTIVE_IDENTITY_NOT_COMPILED", PlacementID: identity.PlacementID, ClientID: identity.ClientID, RuntimeID: identity.RuntimeID})
			}
		}
		compiledReady := len(issues) == 0
		for _, identity := range manifest.Identities {
			if identity.State == "authority_required" && !nodeadmission.SupportsRuntime(coreByRuntime[identity.RuntimeID]) {
				issues = append(issues, FleetNetworkDiagnostic{Code: "AUTHORITY_FUNDING_REQUIRED", PlacementID: identity.PlacementID, ClientID: identity.ClientID, RuntimeID: identity.RuntimeID})
			}
		}
		runtimeJSON, graphDigest, err := canonicalFleetArtifact(compiled.Runtimes, 32<<20)
		if err != nil {
			return err
		}
		manifestJSON, _, err := canonicalFleetArtifact(manifest, 32<<20)
		if err != nil {
			return err
		}
		view = FleetNetworkPreview{
			ID: uuid.NewString(), NodeID: state.Node.ID, InstallationID: state.Node.InstallationID, NodeRevision: networkRevisionView(state.Revision), Defaults: networkRevisionView(state.Defaults),
			SHA256: hex.EncodeToString(graphDigest), Ready: len(issues) == 0, Origins: state.Origins, Assets: pins, Identities: manifest.Identities, Diagnostics: issues,
			NativeBindings: manifest.NativeBindings,
			Runtimes:       []FleetNetworkRuntimeView{}, Listeners: []FleetNetworkListenerView{},
		}
		for _, runtime := range compiled.Runtimes {
			digest := sha256.Sum256(runtime.Configuration)
			view.Runtimes = append(view.Runtimes, FleetNetworkRuntimeView{ID: runtime.ID, Core: runtime.Core, SHA256: hex.EncodeToString(digest[:]), Clients: len(runtime.ClientIDs)})
		}
		for _, listener := range compiled.Listeners {
			view.Listeners = append(view.Listeners, FleetNetworkListenerView{RuntimeID: listener.RuntimeID, InboundID: listener.SourceID, Address: listener.Address.String(), Protocol: listener.Protocol, Port: listener.Port, Role: listener.Role})
		}
		slices.SortFunc(view.Diagnostics, func(a, b FleetNetworkDiagnostic) int {
			if a.Code < b.Code {
				return -1
			}
			if a.Code > b.Code {
				return 1
			}
			if a.PlacementID < b.PlacementID {
				return -1
			}
			if a.PlacementID > b.PlacementID {
				return 1
			}
			return a.InboundID - b.InboundID
		})
		previewJSON, err := json.Marshal(view)
		if err != nil {
			return err
		}
		row := model.FleetNetworkCompilation{
			ID: view.ID, NodeID: state.Node.ID, InstallationID: state.Node.InstallationID, NodeRevisionID: state.Revision.ID, DefaultsRevisionID: state.Defaults.ID,
			CompilerVersion: fleetNetworkCompilerVersion, Manifest: string(manifestJSON), RuntimeDefinitions: string(runtimeJSON), Preview: string(previewJSON), Digest: graphDigest, Ready: compiledReady, CreatedUnixMilli: time.Now().UnixMilli(), ActorID: actorID,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Model(&state.Head).Update("latest_compilation_id", row.ID).Error
	}()
	return view, err
}
