package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

// ProcessFleetSourceWork owns the node lock and commits the publication with its final source-work CAS.
func StopFleetSourceWorkTx(ctx context.Context, tx *gorm.DB, work model.FleetSourceWork) error {
	if tx == nil || !work.Pending || !work.RequiresStop || !fleetID(work.OperationID) || !fleetID(work.ClientID) || !fleetID(work.PlacementID) || !fleetID(work.NodeID) || !fleetID(work.InstallationID) {
		return controlplane.ErrInvalid
	}
	if _, transactional := tx.Statement.ConnPool.(gorm.TxCommitter); !transactional {
		return controlplane.ErrInvalid
	}
	var current model.FleetSourceWork
	if err := tx.First(&current, "placement_id = ?", work.PlacementID).Error; err != nil {
		return err
	}
	if !matchingFleetSourceWork(current, work) || !current.RequiresStop {
		return controlplane.ErrRevision
	}
	var node model.FleetNode
	if err := tx.First(&node, "id = ? AND installation_id = ?", work.NodeID, work.InstallationID).Error; err != nil {
		return err
	}
	if node.AppliedRevision == 0 || node.AppliedRevision != node.DesiredRevision {
		return controlplane.ErrRevision
	}
	grants, err := fleetSourcePendingStopGrants(tx, node, work)
	if err != nil || len(grants) == 0 {
		return err
	}
	var journal model.FleetRuntimeJournal
	if err := tx.First(&journal, "node_id = ? AND installation_id = ? AND runtime_id = ?", node.ID, node.InstallationID, work.RuntimeID).Error; err != nil {
		return err
	}
	store, err := controlplane.NewStore(tx)
	if err != nil {
		return err
	}
	if nodeadmission.SupportsRuntime(journal.CoreID) {
		_, source, err := lockFleetSourceClient(tx, work.ClientID)
		if err != nil {
			return err
		}
		if source.Enable != work.Enabled {
			return controlplane.ErrRevision
		}
		for _, grant := range grants {
			if err := queueFleetSourceStop(ctx, tx, store, node, work, grant); err != nil {
				return err
			}
		}
		return nil
	}
	if journal.CoreID != "native" || len(grants) > 1024 {
		return controlplane.ErrScope
	}
	var deployment model.FleetDeployment
	if err := tx.First(&deployment, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
		return err
	}
	ids := make([]string, len(grants))
	for index, grant := range grants {
		ids[index] = grant.ID
	}
	operation := uuid.NewSHA1(uuid.MustParse(work.OperationID), []byte("native-source-stop/"+node.ID+"/"+strconv.FormatInt(node.AppliedRevision, 10))).String()
	_, err = store.StopConfiguration(ctx, identity.Principal{NodeID: node.ID, InstallationID: node.InstallationID}, controlplane.StopConfigurationRequest{
		OperationID: operation, ExpectedBaseRevision: uint64(node.AppliedRevision), ExpectedBaseSHA256: deployment.Digest, GrantIDs: ids,
	}, controlplane.StopConfigurationValidators{Authority: ValidateFleetAuthorityDeliveryTx, Credit: ValidateFleetCreditDeliveryTx})
	return err
}

func fleetSourcePendingStopGrants(tx *gorm.DB, node model.FleetNode, work model.FleetSourceWork) ([]model.FleetGrant, error) {
	var rows []struct {
		model.FleetGrant
		ShareRevision, AppliedRevision, StopRevision, StopDeployment                     int64
		ShareNodeID, ShareInstallationID, ShareRuntimeID, BudgetID, Desired, Outstanding string
	}
	if err := tx.Table("fleet_grants g").Select(`g.*,s.revision AS share_revision,s.applied_revision,s.node_id AS share_node_id,s.installation_id AS share_installation_id,
s.runtime_id AS share_runtime_id,s.budget_id,s.desired,s.outstanding,stop.rate_revision AS stop_revision,stop.deployment_revision AS stop_deployment`).
		Joins("LEFT JOIN fleet_rate_shares s ON s.id = g.id").Joins("LEFT JOIN fleet_grant_stops stop ON stop.grant_id = g.id").
		Where("g.client_id = ? AND g.node_id = ? AND g.installation_id = ? AND g.runtime_id = ?", work.ClientID, node.ID, node.InstallationID, work.RuntimeID).
		Order("g.id").Limit(32769).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > 32768 {
		return nil, controlplane.ErrInvalid
	}
	var pending []model.FleetGrant
	for _, row := range rows {
		if row.PlacementID == nil || *row.PlacementID != work.PlacementID || row.ShareNodeID != node.ID || row.ShareInstallationID != node.InstallationID || row.ShareRuntimeID != work.RuntimeID || row.BudgetID != work.ClientID || row.ShareRevision <= 0 {
			return nil, controlplane.ErrScope
		}
		var desired, outstanding controlplane.Rates
		if json.Unmarshal([]byte(row.Desired), &desired) != nil || json.Unmarshal([]byte(row.Outstanding), &outstanding) != nil {
			return nil, controlplane.ErrRecovery
		}
		if row.StopRevision > 0 && row.StopRevision <= row.AppliedRevision && row.ShareRevision == row.AppliedRevision && row.StopDeployment > 0 && row.StopDeployment <= node.AppliedRevision && desired == (controlplane.Rates{}) && outstanding == (controlplane.Rates{}) {
			continue
		}
		pending = append(pending, row.FleetGrant)
	}
	return pending, nil
}

func queueFleetSourceStop(ctx context.Context, tx *gorm.DB, store *controlplane.Store, node model.FleetNode, work model.FleetSourceWork, grant model.FleetGrant) error {
	var share model.FleetRateShare
	if err := tx.First(&share, "id = ?", grant.ID).Error; err != nil {
		return err
	}
	var desired controlplane.Rates
	if json.Unmarshal([]byte(share.Desired), &desired) != nil {
		return controlplane.ErrRecovery
	}
	if desired == (controlplane.Rates{}) {
		err := store.QueueAuthorityUpdate(ctx, grant.ID, uint64(node.AppliedRevision), false)
		if err == nil || !errors.Is(err, controlplane.ErrRevision) && !errors.Is(err, controlplane.ErrCreditDeliveryRequired) {
			return err
		}
	}
	operation := uuid.NewSHA1(uuid.MustParse(work.OperationID), []byte("stream-source-stop/"+grant.ID+"/"+strconv.FormatInt(share.Revision, 10))).String()
	if _, err := store.RequestRateShare(ctx, controlplane.RateRequest{OperationID: operation, GrantID: grant.ID, BudgetID: work.ClientID, NodeID: node.ID, InstallationID: node.InstallationID, RuntimeID: work.RuntimeID}); err != nil {
		return err
	}
	return store.QueueAuthorityUpdate(ctx, grant.ID, uint64(node.AppliedRevision), false)
}
