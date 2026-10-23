package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type fleetScheduledResetRequest struct {
	ClientID, PreviousTermID, Cycle string
	ScheduleRevision, DueUnixMilli  int64
}

type fleetScheduledResetRecord struct {
	Request                fleetScheduledResetRequest
	CancelledByOperationID string
	CredentialGeneration   int64
	SourcePolicyDigest     []byte
	Result                 controlplane.TransitionResult
}

// The persisted client schedule authorizes this system transition; no node publication occurs under its locks.
func advanceFleetResetSchedule(ctx context.Context, db *gorm.DB, expected model.FleetResetSchedule) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	if db == nil || !fleetID(expected.ClientID) || expected.Revision <= 0 {
		return result, controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return result, controlplane.ErrInvalid
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		var fence model.FleetFence
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&fence, "id = 1").Error; err != nil {
			return err
		}
		if fence.RecoveryRequired {
			return controlplane.ErrRecovery
		}
		client, source, err := lockFleetSourceClient(tx, expected.ClientID)
		if err != nil {
			return err
		}
		var schedule model.FleetResetSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&schedule, "client_id = ?", client.ID).Error; err != nil {
			return err
		}
		if !sameFleetResetWork(schedule, expected) {
			return controlplane.ErrRevision
		}
		if schedule.PendingOperationID != nil {
			if err := lockFleetResetGroupTx(tx, source.Group); err != nil {
				return err
			}
			err = finishFleetScheduledReset(ctx, tx, store, client, &schedule)
		} else {
			err = beginFleetScheduledReset(ctx, tx, store, client, source, &schedule)
		}
		if err != nil {
			return err
		}
		result = fleetResetScheduleView(schedule)
		return nil
	})
	return result, err
}

func sameFleetResetWork(actual, expected model.FleetResetSchedule) bool {
	return actual.ClientID == expected.ClientID && actual.Revision == expected.Revision && actual.NextDueUnixMilli == expected.NextDueUnixMilli &&
		actual.ReadyUnixMilli == expected.ReadyUnixMilli && sameFleetResetID(actual.PendingOperationID, expected.PendingOperationID) && sameFleetResetID(actual.BoundTermID, expected.BoundTermID)
}

func sameFleetResetID(first, second *string) bool {
	return first == nil && second == nil || first != nil && second != nil && *first == *second
}

func fleetResetSnapshotTx(tx *gorm.DB, client model.FleetClient) (fleetClientSnapshot, error) {
	snapshot, err := fleetBillingSnapshotTx(tx, *client.PanelClientID)
	if err != nil {
		return snapshot, err
	}
	if snapshot.Fleet.ID != client.ID || !bytes.Equal(snapshot.CredentialDigest, client.CredentialDigest) {
		return snapshot, ErrFleetSource
	}
	return snapshot, nil
}

func beginFleetScheduledReset(ctx context.Context, tx *gorm.DB, store *controlplane.Store, client model.FleetClient, source model.ClientRecord, schedule *model.FleetResetSchedule) error {
	now := fleetResetNow().UnixMilli()
	if schedule.ReadyUnixMilli == 0 || schedule.ReadyUnixMilli > now {
		return nil
	}
	if client.PendingTermID != nil {
		return controlplane.ErrDrain
	}
	if client.CurrentTermID == nil {
		schedule.ReadyUnixMilli = 0
		return tx.Save(schedule).Error
	}
	var previous model.FleetTerm
	if err := tx.First(&previous, "id = ? AND client_id = ?", *client.CurrentTermID, client.ID).Error; err != nil {
		return err
	}
	if !source.Enable || previous.State != "active" || previous.ExpiresUnixMilli != 0 && previous.ExpiresUnixMilli <= now {
		schedule.ReadyUnixMilli = 0
		return tx.Save(schedule).Error
	}
	cycle, err := fleetResetCycleTx(tx, source, schedule.OverrideCycle)
	if err != nil {
		return err
	}
	if !sameFleetResetID(client.CurrentTermID, schedule.BoundTermID) || cycle != schedule.EffectiveCycle {
		if err := refreshFleetResetTermTx(tx, client, source, previous); err != nil {
			return err
		}
		return tx.First(schedule, "client_id = ?", client.ID).Error
	}
	if schedule.NextDueUnixMilli == 0 || schedule.NextDueUnixMilli > now || cycle == "none" {
		schedule.ReadyUnixMilli = schedule.NextDueUnixMilli
		return tx.Save(schedule).Error
	}
	snapshot, err := fleetResetSnapshotTx(tx, client)
	if err != nil {
		return err
	}
	if source.ExpiryTime != previous.ExpiresUnixMilli || previous.StartedUnixMilli > schedule.NextDueUnixMilli {
		return ErrFleetSource
	}
	spec, err := fleetTermSpec(snapshot, controlplane.TrafficReset)
	if err != nil {
		return err
	}
	request := fleetScheduledResetRequest{client.ID, previous.ID, cycle, schedule.Revision, schedule.NextDueUnixMilli}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(append([]byte("fleet-scheduled-reset/v1:"), encoded...))
	opID := uuid.NewSHA1(uuid.MustParse(client.ID), []byte("scheduled-reset/v1/"+strconv.FormatInt(schedule.Revision, 10)+"/"+strconv.FormatInt(schedule.NextDueUnixMilli, 10)+"/"+previous.ID)).String()
	operation := model.FleetOperation{ID: opID, Digest: digest[:], Result: []byte{}}
	claimed := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
	if claimed.Error != nil {
		return claimed.Error
	}
	if claimed.RowsAffected != 1 {
		return controlplane.ErrRecovery
	}
	record := fleetScheduledResetRecord{Request: request, CredentialGeneration: client.CredentialGeneration, SourcePolicyDigest: bytes.Clone(snapshot.PolicyDigest)}
	record.Result, err = store.Transition(ctx, controlplane.TransitionRequest{
		OperationID: uuid.NewSHA1(uuid.MustParse(opID), []byte("begin")).String(), ClientID: client.ID, Kind: controlplane.TrafficReset,
		ExpectedRevision: uint64(client.Revision), ExpectedCredentialGeneration: uint64(client.CredentialGeneration),
		ExpectedTermID: previous.ID, Spec: spec, SourcePolicyDigest: snapshot.PolicyDigest,
	})
	if err != nil {
		return err
	}
	if !record.Result.Replayed {
		if err := recordFleetTermGroupTx(tx, snapshot, record.Result.Term); err != nil {
			return err
		}
	}
	if err := queueFleetResetSourceWork(tx, record.Result.Client, source.Enable, opID, true); err != nil {
		return err
	}
	if err := blockFleetSourceAuthority(tx, client.ID); err != nil {
		return err
	}
	if err := tx.Model(&model.FleetAdmissionDemand{}).Where("client_id = ? AND pending", client.ID).
		Updates(map[string]any{"pending": false, "outcome": "rejected", "diagnostic": "shared traffic reset pending"}).Error; err != nil {
		return err
	}
	schedule.PendingOperationID, schedule.ReadyUnixMilli = &opID, now+250
	schedule.Diagnostic = "WAITING_FOR_STOP"
	if err := tx.Save(schedule).Error; err != nil {
		return err
	}
	return saveFleetScheduledReset(tx, opID, record)
}

func finishFleetScheduledReset(ctx context.Context, tx *gorm.DB, store *controlplane.Store, client model.FleetClient, schedule *model.FleetResetSchedule) error {
	var operation model.FleetOperation
	if err := tx.First(&operation, "id = ?", *schedule.PendingOperationID).Error; err != nil {
		return err
	}
	var record fleetScheduledResetRecord
	if len(operation.Result) > 65536 || json.Unmarshal(operation.Result, &record) != nil {
		return controlplane.ErrRecovery
	}
	encoded, err := json.Marshal(record.Request)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(append([]byte("fleet-scheduled-reset/v1:"), encoded...))
	if record.CancelledByOperationID != "" || !bytes.Equal(digest[:], operation.Digest) || record.Request.ClientID != client.ID || record.Request.ScheduleRevision != schedule.Revision ||
		record.Request.DueUnixMilli != schedule.NextDueUnixMilli || record.Request.Cycle != schedule.EffectiveCycle ||
		schedule.BoundTermID == nil || *schedule.BoundTermID != record.Request.PreviousTermID || !record.Result.PendingDrain ||
		client.CurrentTermID == nil || *client.CurrentTermID != record.Request.PreviousTermID || client.PendingTermID == nil || *client.PendingTermID != record.Result.Term.ID {
		return controlplane.ErrRecovery
	}
	snapshot, err := fleetResetSnapshotTx(tx, client)
	if err != nil {
		return err
	}
	if client.CredentialGeneration != record.CredentialGeneration || !bytes.Equal(snapshot.PolicyDigest, record.SourcePolicyDigest) ||
		snapshot.Term == nil || snapshot.Client.ExpiryTime != snapshot.Term.ExpiresUnixMilli {
		return ErrFleetSource
	}
	record.Result, err = store.CompleteTransition(ctx, controlplane.CompleteTransitionRequest{
		OperationID: uuid.NewSHA1(uuid.MustParse(operation.ID), []byte("finish")).String(), ClientID: client.ID, PendingTermID: record.Result.Term.ID,
		ExpectedRevision: uint64(client.Revision), ExpectedCredentialGeneration: uint64(client.CredentialGeneration), SourcePolicyDigest: record.SourcePolicyDigest,
	})
	if err != nil {
		return err
	}
	if err := applyFleetTermProjection(tx, snapshot, record.Result.Term); err != nil {
		return err
	}
	if err := queueFleetResetSourceWork(tx, record.Result.Client, snapshot.Client.Enable, operation.ID, false); err != nil {
		return err
	}
	schedule.PendingOperationID, schedule.LastCompletedOperationID, schedule.BoundTermID = nil, &operation.ID, &record.Result.Term.ID
	schedule.Diagnostic = ""
	schedule.NextDueUnixMilli, err = nextFleetResetDue(schedule.EffectiveCycle, schedule.AnchorUnixMilli, max(fleetResetNow().UnixMilli(), record.Result.Term.StartedUnixMilli, record.Request.DueUnixMilli))
	if err != nil {
		return err
	}
	schedule.ReadyUnixMilli = 0
	if snapshot.Client.Enable && (record.Result.Term.ExpiresUnixMilli == 0 || schedule.NextDueUnixMilli < record.Result.Term.ExpiresUnixMilli) {
		schedule.ReadyUnixMilli = schedule.NextDueUnixMilli
	}
	if err := tx.Save(schedule).Error; err != nil {
		return err
	}
	return saveFleetScheduledReset(tx, operation.ID, record)
}

func saveFleetScheduledReset(tx *gorm.DB, id string, record fleetScheduledResetRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return tx.Model(&model.FleetOperation{}).Where("id = ?", id).Update("result", encoded).Error
}

func queueFleetResetSourceWork(tx *gorm.DB, client model.FleetClient, enabled bool, operationID string, stopping bool) error {
	var placements []model.FleetPlacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", client.ID).Order("id").Limit(32769).Find(&placements).Error; err != nil {
		return err
	}
	if len(placements) > 32768 {
		return controlplane.ErrInvalid
	}
	now := time.Now().UnixMilli()
	for _, placement := range placements {
		pending := true
		if !stopping {
			if placement.Revision == math.MaxInt64 {
				return controlplane.ErrRevision
			}
			if err := tx.Model(&placement).Updates(map[string]any{"current_grant_id": nil, "current_grant_term_id": nil, "enabled": false, "graph_digest": nil, "revision": placement.Revision + 1}).Error; err != nil {
				return err
			}
			assigned, err := fleetPlacementAssignedTx(tx, placement)
			if err != nil {
				return err
			}
			pending = enabled && assigned
		}
		work := model.FleetSourceWork{
			PlacementID: placement.ID, ClientID: client.ID, NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID,
			OperationID: operationID, Revision: 1, SourceRevision: client.Revision, Enabled: enabled, RequiresStop: stopping, Pending: pending, QueuedUnixMilli: now, AvailableUnixMilli: now,
		}
		updated := map[string]any{
			"operation_id": work.OperationID, "revision": gorm.Expr("fleet_source_works.revision + 1"), "source_revision": client.Revision,
			"diagnostic": "",
			"enabled":    enabled, "requires_stop": gorm.Expr("fleet_source_works.requires_stop OR ?", stopping),
			"pending": gorm.Expr("fleet_source_works.requires_stop OR ?", pending), "queued_unix_milli": now, "available_unix_milli": now,
		}
		saved := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "placement_id"}}, DoUpdates: clause.Assignments(updated),
			Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "fleet_source_works.revision < ?", Vars: []any{int64(math.MaxInt64)}}}},
		}).Create(&work)
		if saved.Error != nil {
			return saved.Error
		}
		if saved.RowsAffected != 1 {
			return controlplane.ErrRevision
		}
	}
	return nil
}
