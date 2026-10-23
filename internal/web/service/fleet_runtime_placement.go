package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var _ webruntime.SourcePlacementRuntime = (*fleetRuntime)(nil)

func (r *fleetRuntime) SetClientPlacementTx(ctx context.Context, tx *gorm.DB, actor *model.User, request webruntime.SourcePlacementRequest) (webruntime.SourcePlacementResult, error) {
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.SourcePlacementResult{}, err
	}
	result, err := SetFleetClientPlacementTx(ctx, tx, actor, request)
	if err != nil {
		return webruntime.SourcePlacementResult{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.SourcePlacementResult{}, err
	}
	return result, nil
}
