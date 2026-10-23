package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func (s *FleetPlacementService) ConfigureClient(ctx context.Context, actor *model.User, request FleetPlacementPreferenceRequest) (FleetPlacementPreferenceResult, error) {
	var result FleetPlacementPreferenceResult
	manager := webruntime.GetManager()
	if manager == nil {
		return result, webruntime.ErrFleetDispatcher
	}
	runtime, err := manager.RuntimeFor(nil)
	if err != nil {
		return result, err
	}
	dispatcher, ok := runtime.(webruntime.SourcePlacementRuntime)
	if !ok {
		return result, webruntime.ErrFleetDispatcher
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = dispatcher.SetClientPlacementTx(ctx, tx, actor, request)
		return err
	})
	return result, err
}
