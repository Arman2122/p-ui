package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

type FleetPlacementPreferenceRequest = webruntime.SourcePlacementRequest

type FleetPlacementPreferenceResult = webruntime.SourcePlacementResult

func canonicalPlacementInboundIDs(ids []int) ([]int, error) {
	if len(ids) > 4096 {
		return nil, controlplane.ErrInvalid
	}
	result := append([]int{}, ids...)
	slices.Sort(result)
	for i, id := range result {
		if id <= 0 || i > 0 && result[i-1] == id {
			return nil, controlplane.ErrInvalid
		}
	}
	return result, nil
}

// Source preferences and fanout commit together without acquiring target-node or network-head locks.
func SetFleetClientPlacementTx(ctx context.Context, db *gorm.DB, actor *model.User, request FleetPlacementPreferenceRequest) (FleetPlacementPreferenceResult, error) {
	var result FleetPlacementPreferenceResult
	if db == nil || !fleetID(request.OperationID) || !fleetID(request.ClientID) || request.ExpectedRevision == 0 || request.ExpectedRevision >= math.MaxInt64 ||
		!slices.Contains([]string{"restrict", "attach", "detach"}, request.Action) || request.Action == "restrict" && request.NodeID != "" ||
		request.Action != "restrict" && (!fleetID(request.NodeID) || request.InboundIDs == nil || len(*request.InboundIDs) == 0) {
		return result, controlplane.ErrInvalid
	}
	if request.InboundIDs != nil {
		ids, err := canonicalPlacementInboundIDs(*request.InboundIDs)
		if err != nil {
			return result, err
		}
		request.InboundIDs = &ids
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("fleet-placement-preferences/v1:"), encoded...))
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		client, source, err := lockFleetSourceClient(tx, request.ClientID)
		if err != nil {
			return err
		}
		operation := model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&operation, "id = ?", request.OperationID).Error; err != nil {
				return err
			}
			if !bytes.Equal(operation.Digest, digest[:]) {
				return controlplane.ErrConflict
			}
			if err := json.Unmarshal(operation.Result, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		if uint64(client.PlacementRevision) != request.ExpectedRevision || client.PlacementRevision == math.MaxInt64 {
			return controlplane.ErrRevision
		}
		_, preferences, err := fleetPlacementPreferencesTx(tx, client.ID)
		if err != nil {
			return err
		}
		if err := preserveManualFleetPlacements(tx, client.ID, &preferences); err != nil {
			return err
		}
		if err := mutateFleetPlacementPreferences(tx, client, request, &preferences); err != nil {
			return err
		}
		if err := queueFleetPlacementSourceStopsTx(tx, client, source, request.OperationID, preferences); err != nil {
			return err
		}
		encoded, err := json.Marshal(preferences)
		if err != nil {
			return err
		}
		if len(encoded) > 1<<20 || len(preferences.ManualTargets) > 1000 || len(preferences.SuppressedTargets) > 1000 {
			return controlplane.ErrInvalid
		}
		client.PlacementRevision++
		if err := tx.Model(&client).Updates(map[string]any{"placement_revision": client.PlacementRevision, "placement_preferences": string(encoded)}).Error; err != nil {
			return err
		}
		if err := controlplane.QueuePlacementReevaluationTx(tx, "client", client.ID); err != nil {
			return err
		}
		result = FleetPlacementPreferenceResult{ClientID: client.ID, Revision: uint64(client.PlacementRevision), Pending: true}
		encoded, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", encoded).Error
	})
	return result, err
}

func queueFleetPlacementSourceStopsTx(tx *gorm.DB, client model.FleetClient, source model.ClientRecord, operationID string, preferences FleetPlacementPreferences) error {
	var placements []model.FleetPlacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", client.ID).Order("id").Limit(32769).Find(&placements).Error; err != nil {
		return err
	}
	if len(placements) > 32768 {
		return controlplane.ErrInvalid
	}
	for _, placement := range placements {
		ids, err := placementInboundIDs(tx, placement.ID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(ids, func(id int) bool { return !placementInboundAllowed(preferences, placement.NodeID, id) }) {
			continue
		}
		if err := queueFleetPlacementStopTx(tx, client, source, placement, operationID); err != nil {
			return err
		}
	}
	return nil
}

func queueFleetPlacementStopTx(tx *gorm.DB, client model.FleetClient, source model.ClientRecord, placement model.FleetPlacement, operationID string) error {
	if placement.Enabled || len(placement.GraphDigest) != 0 {
		if placement.Revision == math.MaxInt64 {
			return controlplane.ErrRevision
		}
		if err := tx.Model(&placement).Updates(map[string]any{"revision": placement.Revision + 1, "enabled": false, "graph_digest": nil}).Error; err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	work := model.FleetSourceWork{
		PlacementID: placement.ID, ClientID: client.ID, NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID,
		OperationID: operationID, Revision: 1, SourceRevision: client.Revision, Enabled: source.Enable, RequiresStop: true, Pending: true, QueuedUnixMilli: now, AvailableUnixMilli: now,
	}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "placement_id"}}, DoUpdates: clause.Assignments(map[string]any{
		"operation_id": operationID, "revision": gorm.Expr("fleet_source_works.revision + 1"), "source_revision": client.Revision,
		"enabled": source.Enable, "requires_stop": true, "pending": true, "available_unix_milli": now,
		"diagnostic": "",
	}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "fleet_source_works.revision < ?", Vars: []any{int64(math.MaxInt64)}}}}}).Create(&work)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return controlplane.ErrRevision
	}
	return nil
}

func preserveManualFleetPlacements(tx *gorm.DB, clientID string, preferences *FleetPlacementPreferences) error {
	if preferences.ManualTargets == nil {
		preferences.ManualTargets = map[string][]int{}
	}
	var rows []struct {
		NodeID    string
		InboundID int
	}
	if err := tx.Table("fleet_placements p").Select("p.node_id, r.panel_inbound AS inbound_id").
		Joins("JOIN fleet_placement_inbounds r ON r.placement_id = p.id").
		Where("p.client_id = ? AND r.panel_inbound IS NOT NULL AND NOT EXISTS (SELECT 1 FROM fleet_placement_intents i WHERE i.client_id=p.client_id AND i.node_id=p.node_id AND i.installation_id=p.installation_id AND i.runtime_id=p.runtime_id)", clientID).
		Order("p.node_id,r.panel_inbound").Limit(32769).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) > 32768 {
		return controlplane.ErrInvalid
	}
	for _, row := range rows {
		ids := append(preferences.ManualTargets[row.NodeID], row.InboundID)
		slices.Sort(ids)
		preferences.ManualTargets[row.NodeID] = slices.Compact(ids)
	}
	return nil
}

func mutateFleetPlacementPreferences(tx *gorm.DB, client model.FleetClient, request FleetPlacementPreferenceRequest, preferences *FleetPlacementPreferences) error {
	if request.Action == "restrict" {
		preferences.InboundIDs = request.InboundIDs
		return nil
	}
	var node model.FleetNode
	if err := tx.Select("id").First(&node, "id = ?", request.NodeID).Error; err != nil {
		return err
	}
	if preferences.SuppressedTargets == nil {
		preferences.SuppressedTargets = map[string][]int{}
	}
	ids := *request.InboundIDs
	if request.Action == "attach" {
		source, err := loadFleetPlacementSource(tx, client.ID)
		if err != nil {
			return err
		}
		var attached []int
		for _, inbound := range source.inbounds {
			if slices.Contains(ids, inbound.Id) {
				attached = append(attached, inbound.Id)
			}
		}
		if !slices.Equal(attached, ids) {
			return ErrFleetSource
		}
		manual := append(preferences.ManualTargets[node.ID], ids...)
		slices.Sort(manual)
		preferences.ManualTargets[node.ID] = slices.Compact(manual)
		preferences.SuppressedTargets[node.ID] = slices.DeleteFunc(preferences.SuppressedTargets[node.ID], func(id int) bool { return slices.Contains(ids, id) })
	} else {
		suppressed := append(preferences.SuppressedTargets[node.ID], ids...)
		slices.Sort(suppressed)
		preferences.SuppressedTargets[node.ID] = slices.Compact(suppressed)
	}
	if len(preferences.ManualTargets[node.ID]) > 4096 || len(preferences.SuppressedTargets[node.ID]) > 4096 {
		return controlplane.ErrInvalid
	}
	return nil
}
