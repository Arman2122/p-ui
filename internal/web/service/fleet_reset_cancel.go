package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func cancelFleetResetSchedule(ctx context.Context, db *gorm.DB, actor *model.User, request webruntime.ResetScheduleCancelRequest) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	if db == nil || !fleetID(request.OperationID) || !fleetID(request.ClientID) || !fleetID(request.PendingOperationID) || request.ExpectedRevision == 0 || request.ExpectedRevision >= math.MaxInt64 {
		return result, controlplane.ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("fleet-cancel-scheduled-reset/v1:"), encoded...))
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
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
			if err := tx.First(&operation, "id = ?", operation.ID).Error; err != nil {
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
		var schedule model.FleetResetSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&schedule, "client_id = ?", client.ID).Error; err != nil {
			return err
		}
		if uint64(schedule.Revision) != request.ExpectedRevision || schedule.PendingOperationID == nil || *schedule.PendingOperationID != request.PendingOperationID || client.PendingTermID == nil {
			return controlplane.ErrRevision
		}
		var pending model.FleetOperation
		if err := tx.First(&pending, "id = ?", request.PendingOperationID).Error; err != nil {
			return err
		}
		var record fleetScheduledResetRecord
		if len(pending.Result) > 65536 || json.Unmarshal(pending.Result, &record) != nil {
			return controlplane.ErrRecovery
		}
		encoded, err := json.Marshal(record.Request)
		if err != nil {
			return err
		}
		pendingDigest := sha256.Sum256(append([]byte("fleet-scheduled-reset/v1:"), encoded...))
		if record.CancelledByOperationID != "" || !bytes.Equal(pendingDigest[:], pending.Digest) || record.Request.ClientID != client.ID || record.Request.ScheduleRevision != schedule.Revision ||
			record.Request.DueUnixMilli != schedule.NextDueUnixMilli || record.Request.Cycle != schedule.EffectiveCycle || !record.Result.PendingDrain ||
			schedule.BoundTermID == nil || *schedule.BoundTermID != record.Request.PreviousTermID || !sameFleetResetID(client.CurrentTermID, schedule.BoundTermID) || *client.PendingTermID != record.Result.Term.ID {
			return controlplane.ErrRecovery
		}
		cancelled, err := store.CancelTransition(ctx, controlplane.CancelTransitionRequest{
			OperationID: uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("cancel")).String(), ClientID: client.ID, PendingTermID: record.Result.Term.ID,
			ExpectedRevision: uint64(client.Revision), ExpectedCredentialGeneration: uint64(client.CredentialGeneration),
		})
		if err != nil {
			return err
		}
		if err := queueFleetResetSourceWork(tx, cancelled.Client, source.Enable, request.OperationID, true); err != nil {
			return err
		}
		if err := blockFleetSourceAuthority(tx, client.ID); err != nil {
			return err
		}
		record.CancelledByOperationID = request.OperationID
		if err := saveFleetScheduledReset(tx, pending.ID, record); err != nil {
			return err
		}
		schedule.PendingOperationID, schedule.Diagnostic = nil, "RESET_CANCELLED"
		schedule.Revision++
		schedule.NextDueUnixMilli, err = nextFleetResetDue(schedule.EffectiveCycle, schedule.AnchorUnixMilli, max(fleetResetNow().UnixMilli(), schedule.NextDueUnixMilli))
		if err != nil {
			return err
		}
		schedule.ReadyUnixMilli = 0
		if source.Enable && (cancelled.Term.ExpiresUnixMilli == 0 || schedule.NextDueUnixMilli < cancelled.Term.ExpiresUnixMilli) {
			schedule.ReadyUnixMilli = schedule.NextDueUnixMilli
		}
		if err := tx.Save(&schedule).Error; err != nil {
			return err
		}
		result = fleetResetScheduleView(schedule)
		encoded, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", encoded).Error
	})
	return result, err
}
