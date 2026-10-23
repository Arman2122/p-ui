package service

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
)

// Durable source work authorizes this system publication; public APIs retain administrator checks.
func ReconcileFleetSourceWorkTx(ctx context.Context, tx *gorm.DB, expected model.FleetSourceWork) error {
	if tx == nil || !expected.Enabled || expected.RequiresStop {
		return controlplane.ErrInvalid
	}
	if _, transactional := tx.Statement.ConnPool.(gorm.TxCommitter); !transactional {
		return controlplane.ErrInvalid
	}
	state, err := loadFleetNetworkState(tx, expected.NodeID, expected.InstallationID)
	if err != nil {
		return err
	}
	if err := lockFleetSourceNodeClients(tx, state.Node); err != nil {
		return err
	}
	work, err := lockFleetSourceWork(tx, expected)
	if err != nil {
		return err
	}
	if work.RequiresStop || !work.Enabled {
		return controlplane.ErrDrain
	}
	var placement model.FleetPlacement
	if err := tx.First(&placement, "id = ?", work.PlacementID).Error; err != nil {
		return err
	}
	if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
		return err
	}
	if !nodeadmission.SupportsRuntime(placement.CoreID) {
		return ErrFleetNetworkFunding
	}
	if state.Node.DesiredRevision != state.Node.AppliedRevision {
		return nil
	}
	converged, err := fleetSourceWorkPublished(tx, work)
	if err != nil && !errors.Is(err, ErrFleetSource) {
		return err
	}
	if converged {
		return setFleetSourceApprovalHoldTx(tx, work, false)
	}
	client, _, err := lockFleetSourceClient(tx, work.ClientID)
	if err != nil {
		return err
	}
	var references []model.FleetPlacementInbound
	if err := tx.Where("placement_id = ?", placement.ID).Order("instance_id").Find(&references).Error; err != nil {
		return err
	}
	request := FleetMaterializeRequest{
		PanelClientID: *client.PanelClientID, NodeID: placement.NodeID, InstallationID: placement.InstallationID,
		RuntimeID: placement.RuntimeID, CoreID: placement.CoreID,
	}
	for _, reference := range references {
		if reference.PanelInbound == nil {
			return ErrFleetSource
		}
		request.InboundIDs = append(request.InboundIDs, *reference.PanelInbound)
	}
	snapshot, err := fleetSnapshot(tx, request.PanelClientID, request.InboundIDs, request.CoreID)
	if err != nil {
		return err
	}
	if !bytes.Equal(snapshot.CredentialDigest, client.CredentialDigest) {
		return ErrFleetSource
	}
	if err := requireFleetPlacementApplicationTx(tx, snapshot, placement); err != nil {
		return err
	}
	materialized, err := materializeFleetClientTx(ctx, tx, request)
	if err != nil {
		return err
	}
	if materialized.Placement.ID != placement.ID {
		return controlplane.ErrRevision
	}
	if err := requireAppliedFleetNetworkTx(tx, state); err != nil {
		if errors.Is(err, ErrFleetNetworkApproval) {
			return setFleetSourceApprovalHoldTx(tx, work, true)
		}
		return err
	}
	if err := setFleetSourceApprovalHoldTx(tx, work, false); err != nil {
		return err
	}
	preview, err := previewFleetNetworkTx(tx, FleetNetworkPreviewRequest{
		NodeID: state.Node.ID, InstallationID: state.Node.InstallationID,
		ExpectedRevision: uint64(state.Head.Revision), ExpectedDefaultsID: state.Head.DesiredDefaultsID,
	}, 0)
	if err != nil {
		return err
	}
	if !preview.Ready {
		return ErrFleetNetworkDependency
	}
	operationID := uuid.NewSHA1(uuid.MustParse(work.OperationID), []byte("source-enable/"+work.PlacementID+"/"+strconv.FormatInt(work.Revision, 10)+"/"+strconv.FormatInt(state.Node.AppliedRevision, 10))).String()
	_, err = publishFleetNetworkTx(ctx, tx, FleetNetworkPublishRequest{OperationID: operationID, CompilationID: preview.ID}, nil)
	return err
}

func lockFleetSourceNodeClients(tx *gorm.DB, node model.FleetNode) error {
	var clients []model.FleetClient
	if err := tx.Model(&model.FleetClient{}).Where("id IN (?)", tx.Model(&model.FleetPlacement{}).Select("client_id").Where("node_id = ? AND installation_id = ?", node.ID, node.InstallationID)).
		Order("id").Limit(60001).Find(&clients).Error; err != nil {
		return err
	}
	if len(clients) > 60000 {
		return controlplane.ErrInvalid
	}
	panelIDs := make([]int, 0, len(clients))
	ids := make([]string, 0, len(clients))
	for _, client := range clients {
		if client.PanelClientID == nil {
			return ErrFleetSource
		}
		panelIDs, ids = append(panelIDs, *client.PanelClientID), append(ids, client.ID)
	}
	slices.Sort(panelIDs)
	panelIDs = slices.Compact(panelIDs)
	selected := make(map[int]bool, len(panelIDs))
	for _, id := range panelIDs {
		selected[id] = true
	}
	for offset := 0; offset < len(panelIDs); offset += 1024 {
		var rows []model.ClientRecord
		chunk := panelIDs[offset:min(offset+1024, len(panelIDs))]
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrFleetSource
		}
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		var rows []model.FleetClient
		chunk := ids[offset:min(offset+1024, len(ids))]
		if err := tx.Select("id,panel_client_id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrFleetSource
		}
		for _, row := range rows {
			if row.PanelClientID == nil || !selected[*row.PanelClientID] {
				return ErrFleetSource
			}
		}
	}
	return nil
}
