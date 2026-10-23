package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var (
	ErrFleetResetSchedule = errors.New("fleet reset requires an owned client and a valid plan or client schedule")
	fleetResetNow         = time.Now
)

type FleetResetScheduleRequest = webruntime.ResetScheduleRequest

type FleetResetScheduleView = webruntime.ResetScheduleView

func validFleetResetCycle(cycle string) bool {
	switch cycle {
	case "none", "hourly", "daily", "weekly", "monthly":
		return true
	default:
		return false
	}
}

func nextFleetResetDue(cycle string, anchor, after int64) (int64, error) {
	if !validFleetResetCycle(cycle) || anchor < 0 || after < 0 {
		return 0, ErrFleetResetSchedule
	}
	if cycle == "none" || anchor == 0 {
		return 0, nil
	}
	if cycle == "monthly" {
		start, cursor := time.UnixMilli(anchor).UTC(), time.UnixMilli(max(anchor, after)).UTC()
		months := max(1, (cursor.Year()-start.Year())*12+int(cursor.Month()-start.Month()))
		for range 2 {
			first := time.Date(start.Year(), start.Month()+time.Month(months), 1, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), time.UTC)
			last := first.AddDate(0, 1, -1).Day()
			next := first.AddDate(0, 0, min(start.Day(), last)-1).UnixMilli()
			if next > after && next > anchor {
				return next, nil
			}
			months++
		}
		return 0, ErrFleetResetSchedule
	}
	step := int64(time.Hour / time.Millisecond)
	switch cycle {
	case "daily":
		step *= 24
	case "weekly":
		step *= 7 * 24
	}
	periods := max(int64(0), (after-anchor)/step) + 1
	if periods > (math.MaxInt64-anchor)/step {
		return 0, ErrFleetResetSchedule
	}
	return anchor + periods*step, nil
}

func fleetResetCycleTx(tx *gorm.DB, source model.ClientRecord, override *string) (string, error) {
	if override != nil {
		if !validFleetResetCycle(*override) {
			return "", ErrFleetResetSchedule
		}
		return *override, nil
	}
	var assignment model.ClientPolicy
	found := tx.Where("email = ?", source.Email).Find(&assignment)
	if found.Error != nil {
		return "", found.Error
	}
	if found.RowsAffected == 0 || assignment.OverrideQuota || assignment.AppliedResetCycle == "none" {
		return "none", nil
	}
	if assignment.PolicyId == nil || assignment.AppliedResetCycle != "weekly" && assignment.AppliedResetCycle != "monthly" {
		return "", ErrFleetResetSchedule
	}
	var count int64
	if err := tx.Model(&model.Policy{}).Where("id = ?", *assignment.PolicyId).Count(&count).Error; err != nil {
		return "", err
	}
	if count != 1 {
		return "", ErrFleetResetSchedule
	}
	return assignment.AppliedResetCycle, nil
}

func fleetResetScheduleView(row model.FleetResetSchedule) FleetResetScheduleView {
	view := FleetResetScheduleView{
		ClientID: row.ClientID, Revision: uint64(row.Revision), Inherited: row.OverrideCycle == nil,
		Cycle: row.EffectiveCycle, AnchorUnixMilli: row.AnchorUnixMilli, NextDueUnixMilli: row.NextDueUnixMilli,
		NextAttemptUnixMilli: row.ReadyUnixMilli, Diagnostic: row.Diagnostic,
	}
	if row.PendingOperationID != nil {
		view.PendingOperationID = *row.PendingOperationID
	}
	return view
}

func configureFleetResetSchedule(ctx context.Context, db *gorm.DB, actor *model.User, request FleetResetScheduleRequest) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	if db == nil || !fleetID(request.OperationID) || !fleetID(request.ClientID) || request.ExpectedRevision >= math.MaxInt64 ||
		request.Override && !validFleetResetCycle(request.Cycle) || !request.Override && request.Cycle != "" {
		return result, ErrFleetResetSchedule
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("fleet-reset-schedule/v1:"), encoded...))
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		client, source, err := lockFleetSourceClient(tx, request.ClientID)
		if err != nil {
			return err
		}
		operation := model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claimed := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
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
		var schedule model.FleetResetSchedule
		found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", client.ID).Find(&schedule)
		if found.Error != nil {
			return found.Error
		}
		if uint64(schedule.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		if schedule.PendingOperationID != nil || client.PendingTermID != nil {
			return controlplane.ErrDrain
		}
		var override *string
		if request.Override {
			cycle := request.Cycle
			override = &cycle
		}
		cycle, err := fleetResetCycleTx(tx, source, override)
		if err != nil {
			return err
		}
		changed := found.RowsAffected == 0 || (schedule.OverrideCycle == nil) != (override == nil) ||
			(schedule.OverrideCycle != nil && override != nil && *schedule.OverrideCycle != *override) || schedule.EffectiveCycle != cycle
		if changed {
			schedule.Diagnostic = ""
			schedule.ClientID, schedule.Revision, schedule.OverrideCycle, schedule.EffectiveCycle = client.ID, schedule.Revision+1, override, cycle
			schedule.AnchorUnixMilli = fleetResetNow().UnixMilli()
			schedule.NextDueUnixMilli, err = nextFleetResetDue(cycle, schedule.AnchorUnixMilli, schedule.AnchorUnixMilli)
			if err != nil {
				return err
			}
			schedule.BoundTermID, schedule.ReadyUnixMilli = client.CurrentTermID, 0
			if client.CurrentTermID != nil {
				var term model.FleetTerm
				if err := tx.First(&term, "id = ? AND client_id = ?", *client.CurrentTermID, client.ID).Error; err != nil {
					return err
				}
				if term.State == "active" && source.Enable && (term.ExpiresUnixMilli == 0 || schedule.NextDueUnixMilli < term.ExpiresUnixMilli) {
					schedule.ReadyUnixMilli = schedule.NextDueUnixMilli
				}
			} else {
				schedule.AnchorUnixMilli, schedule.NextDueUnixMilli = 0, 0
			}
			if err := tx.Save(&schedule).Error; err != nil {
				return err
			}
		}
		result = fleetResetScheduleView(schedule)
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", encoded).Error
	})
	return result, err
}
