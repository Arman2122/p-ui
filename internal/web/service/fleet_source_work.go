package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func requireFleetSourceStops(tx *gorm.DB, node model.FleetNode, manifest FleetNetworkManifest) error {
	active := make(map[string]bool, len(manifest.Placements))
	for _, pin := range manifest.Placements {
		active[pin.PlacementID] = true
	}
	if len(active) == 0 {
		return nil
	}
	var pending []string
	if err := tx.Model(&model.FleetSourceWork{}).Where("node_id = ? AND installation_id = ? AND pending AND requires_stop", node.ID, node.InstallationID).
		Order("placement_id").Limit(60001).Pluck("placement_id", &pending).Error; err != nil {
		return err
	}
	if len(pending) > 60000 {
		return controlplane.ErrInvalid
	}
	for _, id := range pending {
		if active[id] {
			return controlplane.ErrDrain
		}
	}
	return nil
}

func fleetPlacementStopPending(tx *gorm.DB, placementID string) (bool, error) {
	var count int64
	err := tx.Model(&model.FleetSourceWork{}).Where("placement_id = ? AND pending AND requires_stop", placementID).Count(&count).Error
	return count != 0, err
}

// Callbacks publish database intent only and own their source-lock order; preflight locks are released first.
// A root database handle is required because savepoints cannot release target-client locks.
func ProcessFleetSourceWork(ctx context.Context, db *gorm.DB, expected model.FleetSourceWork, action func(context.Context, *gorm.DB, model.FleetSourceWork) error) (model.FleetSourceWork, error) {
	var work model.FleetSourceWork
	if db == nil || action == nil || !fleetID(expected.PlacementID) || !fleetID(expected.ClientID) || !fleetID(expected.NodeID) || !fleetID(expected.InstallationID) || expected.Revision <= 0 {
		return work, controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return work, controlplane.ErrInvalid
	}
	ready := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFleetNode(tx, expected.NodeID, expected.InstallationID); err != nil {
			return err
		}
		var err error
		work, err = lockFleetSourceWork(tx, expected)
		if err != nil {
			return err
		}
		ready, err = advanceFleetSourceProof(tx, &work)
		if err != nil || !ready {
			return err
		}
		return saveFleetSourceProgress(tx, &work)
	})
	if err != nil || ready {
		return work, err
	}
	var actionErr error
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFleetNode(tx, expected.NodeID, expected.InstallationID); err != nil {
			return err
		}
		if err := tx.First(&work, "placement_id = ?", expected.PlacementID).Error; err != nil {
			return err
		}
		if !matchingFleetSourceWork(work, expected) {
			return controlplane.ErrRevision
		}
		if err := action(ctx, tx, work); err != nil {
			actionErr = err
			return err
		}
		var err error
		work, err = lockFleetSourceWork(tx, expected)
		if err != nil {
			return err
		}
		if work.Diagnostic != fleetSourceNetworkApprovalRequired || !work.Enabled || work.RequiresStop {
			if _, err := advanceFleetSourceProof(tx, &work); err != nil {
				return err
			}
		}
		return saveFleetSourceProgress(tx, &work)
	})
	if actionErr != nil {
		err = errors.Join(err, DeferFleetSourceWork(ctx, db, expected, time.Second))
	}
	return work, err
}

func DeferFleetSourceWork(ctx context.Context, db *gorm.DB, expected model.FleetSourceWork, delay time.Duration) error {
	if db == nil || !fleetID(expected.PlacementID) || expected.Revision <= 0 || delay < 100*time.Millisecond || delay > 30*time.Second {
		return controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return controlplane.ErrInvalid
	}
	return db.WithContext(ctx).Model(&model.FleetSourceWork{}).
		Where("placement_id = ? AND client_id = ? AND node_id = ? AND installation_id = ? AND runtime_id = ? AND revision = ? AND operation_id = ? AND enabled = ? AND requires_stop = ? AND pending",
			expected.PlacementID, expected.ClientID, expected.NodeID, expected.InstallationID, expected.RuntimeID, expected.Revision, expected.OperationID, expected.Enabled, expected.RequiresStop).
		Update("available_unix_milli", gorm.Expr("GREATEST(available_unix_milli + 1, ?)", time.Now().Add(delay).UnixMilli())).Error
}

func matchingFleetSourceWork(work, expected model.FleetSourceWork) bool {
	return work.Pending && work.Revision == expected.Revision && work.OperationID == expected.OperationID && work.Enabled == expected.Enabled &&
		work.ClientID == expected.ClientID && work.NodeID == expected.NodeID && work.InstallationID == expected.InstallationID && work.RuntimeID == expected.RuntimeID
}

func lockFleetSourceWork(tx *gorm.DB, expected model.FleetSourceWork) (model.FleetSourceWork, error) {
	var work model.FleetSourceWork
	client, source, err := lockFleetSourceClient(tx, expected.ClientID)
	if err != nil {
		return work, err
	}
	var placement model.FleetPlacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&placement, "id = ?", expected.PlacementID).Error; err != nil {
		return work, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&work, "placement_id = ?", expected.PlacementID).Error; err != nil {
		return work, err
	}
	if !matchingFleetSourceWork(work, expected) || work.ClientID != client.ID || source.Enable != work.Enabled || placement.ClientID != work.ClientID ||
		placement.NodeID != work.NodeID || placement.InstallationID != work.InstallationID || placement.RuntimeID != work.RuntimeID {
		return work, controlplane.ErrRevision
	}
	return work, nil
}

func advanceFleetSourceProof(tx *gorm.DB, work *model.FleetSourceWork) (bool, error) {
	if work.RequiresStop {
		stopped, revision, err := fleetSourceWorkStopped(tx, *work)
		if err != nil || !stopped {
			return false, err
		}
		work.RequiresStop, work.Pending = false, work.Enabled
		if work.Enabled {
			var placement model.FleetPlacement
			if err := tx.First(&placement, "id = ?", work.PlacementID).Error; err != nil {
				return false, err
			}
			assigned, err := fleetPlacementAssignedTx(tx, placement)
			if err != nil {
				return false, err
			}
			work.Pending = assigned
		}
		work.AppliedStopRevision = max(work.AppliedStopRevision, revision)
		return true, nil
	}
	if !work.Enabled {
		return false, controlplane.ErrRecovery
	}
	var placement model.FleetPlacement
	if err := tx.First(&placement, "id = ?", work.PlacementID).Error; err != nil {
		return false, err
	}
	assigned, err := fleetPlacementAssignedTx(tx, placement)
	if err != nil {
		return false, err
	}
	if !assigned {
		stopped, revision, err := fleetSourceWorkStopped(tx, *work)
		if err != nil || !stopped {
			return false, err
		}
		work.Pending, work.AppliedStopRevision = false, max(work.AppliedStopRevision, revision)
		return true, nil
	}
	converged, err := fleetSourceWorkPublished(tx, *work)
	if errors.Is(err, ErrFleetSource) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	work.Pending = !converged
	return converged, nil
}

func saveFleetSourceProgress(tx *gorm.DB, work *model.FleetSourceWork) error {
	if work.Pending {
		work.AvailableUnixMilli = max(time.Now().UnixMilli(), work.AvailableUnixMilli+1)
	} else {
		work.Diagnostic = ""
	}
	return tx.Model(work).Updates(map[string]any{"requires_stop": work.RequiresStop, "applied_stop_revision": work.AppliedStopRevision, "pending": work.Pending, "available_unix_milli": work.AvailableUnixMilli, "diagnostic": work.Diagnostic}).Error
}

func fleetSourceWorkStopped(tx *gorm.DB, work model.FleetSourceWork) (bool, int64, error) {
	var node model.FleetNode
	if err := tx.First(&node, "id = ?", work.NodeID).Error; err != nil {
		return false, 0, err
	}
	if node.AppliedRevision != node.DesiredRevision {
		return false, 0, nil
	}
	var rows []struct {
		model.FleetGrant
		ShareNode, ShareInstallation, ShareRuntime, BudgetID         string
		ShareRevision, AppliedRevision, StopRevision, StopDeployment int64
		Desired, Outstanding                                         string
	}
	if err := tx.Table("fleet_grants g").Select(`g.*,s.node_id AS share_node,s.installation_id AS share_installation,s.runtime_id AS share_runtime,s.budget_id,
s.revision AS share_revision,s.applied_revision,s.desired,s.outstanding,stop.rate_revision AS stop_revision,stop.deployment_revision AS stop_deployment`).
		Joins("LEFT JOIN fleet_rate_shares s ON s.id = g.id").Joins("LEFT JOIN fleet_grant_stops stop ON stop.grant_id = g.id").
		Where("g.client_id = ? AND g.node_id = ? AND g.installation_id = ? AND g.runtime_id = ?", work.ClientID, work.NodeID, work.InstallationID, work.RuntimeID).
		Order("g.id").Limit(32769).Scan(&rows).Error; err != nil {
		return false, 0, err
	}
	if len(rows) > 32768 {
		return false, 0, controlplane.ErrInvalid
	}
	for _, row := range rows {
		var desired, outstanding controlplane.Rates
		if json.Unmarshal([]byte(row.Desired), &desired) != nil || json.Unmarshal([]byte(row.Outstanding), &outstanding) != nil ||
			row.PlacementID == nil || *row.PlacementID != work.PlacementID || row.ClientID != work.ClientID || row.NodeID != work.NodeID || row.InstallationID != work.InstallationID || row.RuntimeID != work.RuntimeID ||
			row.ShareNode != work.NodeID || row.ShareInstallation != work.InstallationID || row.ShareRuntime != work.RuntimeID || row.BudgetID != work.ClientID ||
			row.StopRevision <= 0 || row.StopRevision > row.AppliedRevision || row.AppliedRevision != row.ShareRevision || row.StopDeployment <= 0 || row.StopDeployment > node.AppliedRevision ||
			desired != (controlplane.Rates{}) || outstanding != (controlplane.Rates{}) {
			return false, 0, nil
		}
	}
	return true, node.AppliedRevision, nil
}

func fleetSourceWorkPublished(tx *gorm.DB, work model.FleetSourceWork) (bool, error) {
	var node model.FleetNode
	if err := tx.First(&node, "id = ?", work.NodeID).Error; err != nil {
		return false, err
	}
	return fleetPlacementPublished(tx, node, work.PlacementID, work.AppliedStopRevision)
}

func fleetPlacementPublished(tx *gorm.DB, node model.FleetNode, placementID string, afterRevision int64) (bool, error) {
	if node.AppliedRevision != node.DesiredRevision || node.AppliedRevision <= afterRevision {
		return false, nil
	}
	snapshot, placement, _, err := fleetPlacementSnapshot(tx, placementID)
	if err != nil {
		return false, err
	}
	if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
		if errors.Is(err, controlplane.ErrDrain) {
			return false, nil
		}
		return false, err
	}
	if !snapshot.Client.Enable || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || snapshot.Term != nil && snapshot.Term.State != "active" {
		return false, nil
	}
	var compilation model.FleetNetworkCompilation
	columns := "c.*"
	if nodeadmission.SupportsRuntime(placement.CoreID) {
		columns = "c.id,c.node_id,c.installation_id"
	}
	query := tx.Table("fleet_network_compilations c").Select(columns).Joins("JOIN fleet_network_publications p ON p.compilation_id = c.id").
		Where("p.node_id = ? AND p.deployment_revision = ? AND c.node_id = ? AND c.installation_id = ?", node.ID, node.AppliedRevision, node.ID, node.InstallationID).
		Order("p.created_unix_milli DESC,p.operation_id DESC").Limit(1).Find(&compilation)
	if query.Error != nil || query.RowsAffected == 0 {
		return false, query.Error
	}
	if nodeadmission.SupportsRuntime(placement.CoreID) {
		scope, err := controlplane.PublishedAuthorityScopeTx(tx, node.ID, uint64(node.AppliedRevision), placement.RuntimeID, snapshot.Fleet.ID)
		if errors.Is(err, controlplane.ErrScope) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if base := scope.BaseGrant; base != nil && (!base.Policy.Enabled || snapshot.Term == nil || base.Policy.Term != snapshot.Term.ID || placement.CurrentGrantID == nil || base.ID != *placement.CurrentGrantID) {
			return false, nil
		}
		return scope.CoreID == placement.CoreID && scope.Descriptor.PlacementID == placement.ID && scope.Descriptor.PlacementRevision == uint64(placement.Revision) && scope.Descriptor.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration), nil
	}
	if len(compilation.Manifest) > 32<<20 || len(compilation.RuntimeDefinitions) > 32<<20 {
		return false, controlplane.ErrRecovery
	}
	var manifest FleetNetworkManifest
	if json.Unmarshal([]byte(compilation.Manifest), &manifest) != nil || manifest.NodeID != node.ID || manifest.InstallationID != node.InstallationID {
		return false, controlplane.ErrRecovery
	}
	identityMatches := false
	for _, identity := range manifest.Identities {
		if identity.PlacementID == placement.ID && identity.ClientID == placement.ClientID && identity.RuntimeID == placement.RuntimeID && identity.PlacementRevision == uint64(placement.Revision) &&
			identity.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration) && (identity.State == "active" || identity.State == "authority_required" || identity.State == "dormant") &&
			(snapshot.Term == nil && identity.TermID == "" || snapshot.Term != nil && identity.TermID == snapshot.Term.ID) {
			identityMatches = true
		}
	}
	if !identityMatches {
		return false, nil
	}
	for _, pin := range manifest.Placements {
		if pin.Owned && pin.PlacementID == placement.ID && pin.ClientID == placement.ClientID && pin.Revision == uint64(placement.Revision) && pin.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration) &&
			pin.PolicySHA256 == hex.EncodeToString(snapshot.PolicyDigest) && pin.CredentialSHA256 == hex.EncodeToString(snapshot.CredentialDigest) && pin.DefinitionSHA256 == hex.EncodeToString(snapshot.DefinitionDigest) {
			return fleetSourceRuntimePublished(tx, node, placement, snapshot, compilation)
		}
	}
	return false, nil
}

func fleetSourceRuntimePublished(tx *gorm.DB, node model.FleetNode, placement model.FleetPlacement, snapshot fleetClientSnapshot, compilation model.FleetNetworkCompilation) (bool, error) {
	var definitions []nodeconfig.RuntimeDefinition
	if json.Unmarshal([]byte(compilation.RuntimeDefinitions), &definitions) != nil {
		return false, controlplane.ErrRecovery
	}
	var deployment model.FleetDeployment
	if err := tx.First(&deployment, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
		return false, err
	}
	digest := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(digest[:], deployment.Digest) {
		return false, controlplane.ErrRecovery
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return false, err
	}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID != placement.RuntimeID || runtime.Core != placement.CoreID {
			continue
		}
		configurationJSON, _, err := canonicalFleetArtifact(runtime.Configuration, 32<<20)
		if err != nil {
			return false, err
		}
		definitionMatches := slices.ContainsFunc(definitions, func(definition nodeconfig.RuntimeDefinition) bool {
			if definition.ID != runtime.ID || definition.Core != runtime.Core || !slices.Contains(definition.ClientIDs, snapshot.Fleet.ID) {
				return false
			}
			compiledJSON, _, err := canonicalFleetArtifact(definition.Configuration, 32<<20)
			return err == nil && bytes.Equal(compiledJSON, configurationJSON)
		})
		if !definitionMatches {
			return false, nil
		}
		for _, admission := range runtime.Admissions {
			if admission.ClientID == snapshot.Fleet.ID && admission.PlacementID == placement.ID && admission.PlacementRevision == uint64(placement.Revision) && admission.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration) {
				return true, nil
			}
		}
		for _, grant := range runtime.Grants {
			if grant.ClientID == snapshot.Fleet.ID && grant.Policy.Enabled && snapshot.Term != nil && grant.Policy.Term == snapshot.Term.ID && placement.CurrentGrantID != nil && grant.ID == *placement.CurrentGrantID {
				return fleetSourceNativeUsable(tx, node, placement, grant)
			}
		}
	}
	return false, nil
}
