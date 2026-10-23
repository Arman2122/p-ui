package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func RunFleetResetScheduler(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return controlplane.ErrInvalid
	}
	for ctx.Err() == nil {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		var due []model.FleetResetSchedule
		err := db.WithContext(query).Where("ready_unix_milli > 0 AND ready_unix_milli <= ?", fleetResetNow().UnixMilli()).
			Order("ready_unix_milli,client_id").Limit(32).Find(&due).Error
		cancel()
		if err != nil {
			return err
		}
		for _, schedule := range due {
			if err := ctx.Err(); err != nil {
				return err
			}
			work, stop := context.WithTimeout(ctx, 5*time.Second)
			_, err := advanceFleetResetSchedule(work, db, schedule)
			stop()
			if err == nil {
				continue
			}
			if !settleFleetAdmissionError(err) && !errors.Is(err, ErrFleetResetSchedule) {
				return err
			}
			retry, stop := context.WithTimeout(ctx, 2*time.Second)
			err = deferFleetResetSchedule(retry, db, schedule, err)
			stop()
			if err != nil {
				return err
			}
		}
		if len(due) == 32 {
			continue
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func deferFleetResetSchedule(ctx context.Context, db *gorm.DB, expected model.FleetResetSchedule, failure error) error {
	diagnostic, delay := "DEPENDENCY_UNAVAILABLE", 10*time.Second
	switch {
	case errors.Is(failure, controlplane.ErrDrain):
		diagnostic, delay = "WAITING_FOR_STOP", 250*time.Millisecond
	case errors.Is(failure, controlplane.ErrRecovery):
		diagnostic = "RECOVERY_REQUIRED"
	case errors.Is(failure, ErrFleetSource), errors.Is(failure, ErrFleetResetSchedule):
		diagnostic = "SOURCE_CHANGED"
	}
	return db.WithContext(ctx).Model(&model.FleetResetSchedule{}).
		Where("client_id = ? AND revision = ? AND next_due_unix_milli = ? AND ready_unix_milli = ? AND pending_operation_id IS NOT DISTINCT FROM ? AND bound_term_id IS NOT DISTINCT FROM ?",
			expected.ClientID, expected.Revision, expected.NextDueUnixMilli, expected.ReadyUnixMilli, expected.PendingOperationID, expected.BoundTermID).
		Updates(map[string]any{"ready_unix_milli": fleetResetNow().Add(delay).UnixMilli(), "diagnostic": diagnostic}).Error
}
