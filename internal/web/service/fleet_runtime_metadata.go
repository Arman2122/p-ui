package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var _ webruntime.SourceMetadataRuntime = (*fleetRuntime)(nil)

func (r *fleetRuntime) EditClientMetadataTx(ctx context.Context, tx *gorm.DB, actor *model.User, request entity.FleetClientMetadataRequest) (entity.FleetClientMetadataResult, error) {
	if err := r.checkTransaction(tx); err != nil {
		return entity.FleetClientMetadataResult{}, err
	}
	result, err := EditFleetClientMetadataTx(ctx, tx, actor, request)
	if err != nil {
		return entity.FleetClientMetadataResult{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return entity.FleetClientMetadataResult{}, err
	}
	return result, nil
}
