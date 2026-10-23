package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var _ webruntime.SourceResetScheduleRuntime = (*fleetRuntime)(nil)

func (r *fleetRuntime) ConfigureResetScheduleTx(ctx context.Context, tx *gorm.DB, actor *model.User, request webruntime.ResetScheduleRequest) (webruntime.ResetScheduleView, error) {
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	result, err := configureFleetResetSchedule(ctx, tx, actor, request)
	if err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	return result, nil
}

func ConfigureFleetResetSchedule(ctx context.Context, actor *model.User, request FleetResetScheduleRequest) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	manager := webruntime.GetManager()
	if manager == nil {
		return result, webruntime.ErrFleetDispatcher
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return result, err
	}
	reset, capable := dispatch.(webruntime.SourceResetScheduleRuntime)
	if !capable {
		return result, webruntime.ErrFleetDispatcher
	}
	db := database.GetDB()
	if db == nil {
		return result, ErrFleetRuntimeDatabase
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = reset.ConfigureResetScheduleTx(ctx, tx, actor, request)
		return err
	})
	return result, err
}

func (r *fleetRuntime) CancelResetScheduleTx(ctx context.Context, tx *gorm.DB, actor *model.User, request webruntime.ResetScheduleCancelRequest) (webruntime.ResetScheduleView, error) {
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	result, err := cancelFleetResetSchedule(ctx, tx, actor, request)
	if err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.ResetScheduleView{}, err
	}
	return result, nil
}

func CancelFleetResetSchedule(ctx context.Context, actor *model.User, request webruntime.ResetScheduleCancelRequest) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	manager := webruntime.GetManager()
	if manager == nil {
		return result, webruntime.ErrFleetDispatcher
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return result, err
	}
	reset, capable := dispatch.(webruntime.SourceResetScheduleRuntime)
	if !capable {
		return result, webruntime.ErrFleetDispatcher
	}
	db := database.GetDB()
	if db == nil {
		return result, ErrFleetRuntimeDatabase
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = reset.CancelResetScheduleTx(ctx, tx, actor, request)
		return err
	})
	return result, err
}
