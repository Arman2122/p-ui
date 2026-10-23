package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

type FleetClientEnableRequest = webruntime.SourceEnableRequest

type FleetSourceMutationResult = webruntime.SourceEnableResult

// Call before acquiring node or network-head locks; source invalidation and its work commit together.
// Pending records durable intent, while the worker requires actual applied convergence.
func SetFleetClientEnabledTx(ctx context.Context, db *gorm.DB, actor *model.User, request FleetClientEnableRequest) (FleetSourceMutationResult, error) {
	var result FleetSourceMutationResult
	if db == nil || !fleetID(request.OperationID) || !fleetID(request.ClientID) || request.ExpectedRevision == 0 || request.ExpectedRevision >= math.MaxInt64 {
		return result, controlplane.ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("fleet-source-enable/v1:"), encoded...))
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var previous model.FleetOperation
		found := tx.Where("id = ?", request.OperationID).Find(&previous)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected != 0 {
			return replayFleetSourceMutation(previous, digest[:], &result)
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
			return replayFleetSourceMutation(operation, digest[:], &result)
		}
		if uint64(client.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		result = FleetSourceMutationResult{ClientID: client.ID, Revision: uint64(client.Revision), Enabled: request.Enabled}
		if source.Enable != request.Enabled {
			if err := mutateFleetSourceEnable(tx, &client, source, request, &result); err != nil {
				return err
			}
		} else {
			var pending int64
			if err := tx.Model(&model.FleetSourceWork{}).Where("client_id = ? AND pending", client.ID).Count(&pending).Error; err != nil {
				return err
			}
			result.Targets, result.Pending = int(pending), pending != 0
		}
		if err := refreshFleetResetEnabledTx(tx, client, request.Enabled); err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", encoded).Error
	})
	return result, err
}

func replayFleetSourceMutation(operation model.FleetOperation, digest []byte, result *FleetSourceMutationResult) error {
	if !bytes.Equal(operation.Digest, digest) {
		return controlplane.ErrConflict
	}
	if err := json.Unmarshal(operation.Result, result); err != nil {
		return err
	}
	result.Replayed = true
	return nil
}

func lockFleetSourceClient(tx *gorm.DB, id string) (model.FleetClient, model.ClientRecord, error) {
	var client model.FleetClient
	var source model.ClientRecord
	if err := tx.First(&client, "id = ?", id).Error; err != nil {
		return client, source, err
	}
	if client.PanelClientID == nil || !client.Owned {
		return client, source, ErrFleetSource
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, *client.PanelClientID).Error; err != nil {
		return client, source, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&client, "id = ?", id).Error; err != nil {
		return client, source, err
	}
	if client.PanelClientID == nil || *client.PanelClientID != source.Id || !client.Owned {
		return client, source, ErrFleetSource
	}
	return client, source, nil
}

func mutateFleetSourceEnable(tx *gorm.DB, client *model.FleetClient, source model.ClientRecord, request FleetClientEnableRequest, result *FleetSourceMutationResult) error {
	var placements []model.FleetPlacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", client.ID).Order("id").Limit(32769).Find(&placements).Error; err != nil {
		return err
	}
	if len(placements) > 32768 || client.Revision == math.MaxInt64 {
		return controlplane.ErrRevision
	}
	for _, placement := range placements {
		if placement.Revision == math.MaxInt64 || !fleetID(placement.NodeID) || !fleetID(placement.InstallationID) || !fleetRuntimeName.MatchString(placement.RuntimeID) {
			return controlplane.ErrRevision
		}
	}
	now := time.Now().UnixMilli()
	if err := tx.Model(&source).Updates(map[string]any{"enable": request.Enabled, "updated_at": now}).Error; err != nil {
		return err
	}
	client.Revision++
	if err := tx.Model(client).Update("revision", client.Revision).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.FleetPlacement{}).Where("client_id = ?", client.ID).
		Updates(map[string]any{"enabled": false, "graph_digest": nil, "revision": gorm.Expr("revision + 1")}).Error; err != nil {
		return err
	}
	for _, placement := range placements {
		work := model.FleetSourceWork{
			PlacementID: placement.ID, ClientID: client.ID, NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID,
			OperationID: request.OperationID, Revision: 1, SourceRevision: client.Revision, Enabled: request.Enabled, RequiresStop: true, Pending: true, QueuedUnixMilli: now, AvailableUnixMilli: now,
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "placement_id"}}, DoUpdates: clause.Assignments(map[string]any{
			"operation_id": work.OperationID, "revision": gorm.Expr("fleet_source_works.revision + 1"), "source_revision": work.SourceRevision,
			"enabled": work.Enabled, "requires_stop": true, "pending": true, "queued_unix_milli": now, "available_unix_milli": now,
			"diagnostic": "",
		})}).Create(&work).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&model.FleetAdmissionDemand{}).Where("client_id = ? AND pending", client.ID).Updates(map[string]any{"pending": false, "outcome": "rejected", "diagnostic": "source enable state changed"}).Error; err != nil {
		return err
	}
	if err := blockFleetSourceAuthority(tx, client.ID); err != nil {
		return err
	}
	result.Revision, result.Targets, result.Pending = uint64(client.Revision), len(placements), len(placements) != 0
	if request.Enabled {
		if err := controlplane.QueuePlacementReevaluationTx(tx, "client", client.ID); err != nil {
			return err
		}
		result.Pending = true
	}
	return nil
}

func blockFleetSourceAuthority(tx *gorm.DB, clientID string) error {
	grants := tx.Model(&model.FleetGrant{}).Select("id").Where("client_id = ?", clientID)
	var rows []model.FleetAuthorityUpdate
	if err := tx.Where("grant_id IN (?)", grants).Order("grant_id").Limit(32769).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) > 32768 {
		return controlplane.ErrInvalid
	}
	var enabled []string
	for _, row := range rows {
		update := new(nodev1.AuthorityUpdate)
		digest := sha256.Sum256(row.Payload)
		if len(row.Payload) > 4096 || !bytes.Equal(digest[:], row.PayloadSHA256) || proto.Unmarshal(row.Payload, update) != nil || update.GrantId != row.GrantID || update.ClientId != clientID {
			return controlplane.ErrRecovery
		}
		if update.Enabled {
			enabled = append(enabled, row.GrantID)
		}
	}
	for offset := 0; offset < len(enabled); offset += 512 {
		if err := tx.Model(&model.FleetAuthorityUpdate{}).Where("grant_id IN ?", enabled[offset:min(offset+512, len(enabled))]).Update("delivery_blocked", true).Error; err != nil {
			return err
		}
	}
	return nil
}

func PendingFleetSourceWork(ctx context.Context, db *gorm.DB, nodeID string, limit int) ([]model.FleetSourceWork, error) {
	if db == nil || !fleetID(nodeID) || limit < 1 || limit > 128 {
		return nil, controlplane.ErrInvalid
	}
	var work []model.FleetSourceWork
	now := time.Now().UnixMilli()
	for _, requiresStop := range []bool{true, false} {
		var selected []model.FleetSourceWork
		if err := db.WithContext(ctx).Where("node_id = ? AND installation_id = (SELECT installation_id FROM fleet_nodes WHERE id = ?) AND pending AND requires_stop = ? AND available_unix_milli <= ?", nodeID, nodeID, requiresStop, now).
			Order("available_unix_milli,placement_id").Limit(limit - len(work)).Find(&selected).Error; err != nil {
			return nil, err
		}
		work = append(work, selected...)
		if len(work) == limit {
			break
		}
	}
	return work, nil
}
