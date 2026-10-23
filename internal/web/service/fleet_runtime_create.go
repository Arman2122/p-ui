package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var _ webruntime.SourceCreateRuntime = (*fleetRuntime)(nil)

func (r *fleetRuntime) CreateClientTx(ctx context.Context, tx *gorm.DB, actor *model.User, request entity.ClientCreatePayload) (entity.FleetClientCreateResult, error) {
	if err := r.checkTransaction(tx); err != nil {
		return entity.FleetClientCreateResult{}, err
	}
	result, err := CreateFleetClientTx(ctx, tx, actor, request)
	if err != nil {
		return entity.FleetClientCreateResult{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return entity.FleetClientCreateResult{}, err
	}
	return result, nil
}
