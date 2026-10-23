package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type fleetPlacementCursor struct {
	After string `json:"after,omitempty"`
}

func claimFleetPlacementFanout(ctx context.Context, db *gorm.DB) (model.FleetPlacementFanout, error) {
	var work model.FleetPlacementFanout
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UnixMilli()
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("pending AND available_unix_milli <= ? AND lease_until_unix_milli <= ?", now, now).
			Order("available_unix_milli,scope,scope_id").Limit(1).Find(&work)
		if query.Error != nil || query.RowsAffected == 0 {
			return query.Error
		}
		lease := uuid.NewString()
		work.LeaseID, work.LeaseUntilUnixMilli = &lease, now+45000
		return tx.Model(&work).Updates(map[string]any{"lease_id": work.LeaseID, "lease_until_unix_milli": work.LeaseUntilUnixMilli}).Error
	})
	return work, err
}

func lockFleetPlacementFanout(tx *gorm.DB, expected model.FleetPlacementFanout, cursor string) (model.FleetPlacementFanout, error) {
	var current model.FleetPlacementFanout
	if expected.LeaseID == nil {
		return current, controlplane.ErrInvalid
	}
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope = ? AND scope_id = ? AND cursor = ?::jsonb", expected.Scope, expected.ScopeID, cursor).Find(&current)
	if query.Error != nil {
		return current, query.Error
	}
	if query.RowsAffected == 0 || !current.Pending || current.Revision != expected.Revision || current.LeaseID == nil || *current.LeaseID != *expected.LeaseID ||
		current.LeaseUntilUnixMilli <= time.Now().UnixMilli() {
		return current, controlplane.ErrRevision
	}
	return current, nil
}

func ProcessFleetPlacementFanout(ctx context.Context, db *gorm.DB, limit int) (bool, error) {
	if db == nil || limit < 1 || limit > 32 {
		return false, controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return false, controlplane.ErrInvalid
	}
	work, err := claimFleetPlacementFanout(ctx, db)
	if err != nil || work.LeaseID == nil {
		return false, err
	}
	err = processFleetPlacementFanoutLease(ctx, db, work, limit)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		deferErr := db.WithContext(cleanup).Model(&model.FleetPlacementFanout{}).
			Where("scope = ? AND scope_id = ? AND revision = ? AND lease_id = ?", work.Scope, work.ScopeID, work.Revision, *work.LeaseID).
			Updates(map[string]any{"available_unix_milli": time.Now().Add(2 * time.Second).UnixMilli(), "lease_id": nil, "lease_until_unix_milli": 0}).Error
		if deferErr != nil {
			return true, deferErr
		}
	}
	return true, err
}

func processFleetPlacementFanoutLease(ctx context.Context, db *gorm.DB, work model.FleetPlacementFanout, limit int) error {
	var cursor fleetPlacementCursor
	if json.Unmarshal([]byte(work.Cursor), &cursor) != nil || cursor.After != "" && !fleetID(cursor.After) {
		return controlplane.ErrRecovery
	}
	ctx, cancel := context.WithDeadline(ctx, time.UnixMilli(work.LeaseUntilUnixMilli))
	defer cancel()
	members, err := fleetPlacementFanoutMembers(db.WithContext(ctx), work, cursor.After, limit+1)
	if err != nil {
		return err
	}
	lastCursor := work.Cursor
	processed := 0
	start := time.Now()
	for _, clientID := range members[:min(limit, len(members))] {
		deferred := false
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			complete := false
			_, _, sourceErr := lockFleetSourceClient(tx, clientID)
			if sourceErr == nil {
				sourceErr = tx.Transaction(func(inner *gorm.DB) error {
					var err error
					complete, err = reconcileFleetPlacementClientTx(inner, clientID)
					return err
				})
			}
			if sourceErr != nil {
				if ctx.Err() != nil || transientFleetCandidate(sourceErr) || !deferFleetSourceError(sourceErr) {
					return sourceErr
				}
				complete = false
			}
			current, err := lockFleetPlacementFanout(tx, work, lastCursor)
			if err != nil {
				return err
			}
			if !complete && work.Scope == "client" {
				deferred = true
				return tx.Model(&current).Updates(map[string]any{"available_unix_milli": time.Now().Add(30 * time.Second).UnixMilli(), "lease_id": nil, "lease_until_unix_milli": 0}).Error
			}
			if !complete {
				if err := controlplane.QueuePlacementReevaluationTx(tx, "client", clientID); err != nil {
					return err
				}
				if err := tx.Model(&model.FleetPlacementFanout{}).Where("scope = 'client' AND scope_id = ?", clientID).Update("available_unix_milli", time.Now().Add(30*time.Second).UnixMilli()).Error; err != nil {
					return err
				}
			}
			encoded, err := json.Marshal(fleetPlacementCursor{After: clientID})
			if err != nil {
				return err
			}
			lastCursor = string(encoded)
			return tx.Model(&current).Update("cursor", lastCursor).Error
		})
		if err != nil || deferred {
			return err
		}
		processed++
		if time.Since(start) >= 2*time.Second {
			break
		}
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockFleetPlacementFanout(tx, work, lastCursor)
		if err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]any{"pending": processed < len(members), "available_unix_milli": time.Now().UnixMilli(), "lease_id": nil, "lease_until_unix_milli": 0}).Error
	})
}
