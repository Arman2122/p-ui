package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func EditFleetClientMetadata(ctx context.Context, actor *model.User, request entity.FleetClientMetadataRequest) (entity.FleetClientMetadataResult, error) {
	var result entity.FleetClientMetadataResult
	manager := webruntime.GetManager()
	if manager == nil {
		return result, webruntime.ErrFleetDispatcher
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return result, err
	}
	metadata, capable := dispatch.(webruntime.SourceMetadataRuntime)
	if !capable {
		return result, webruntime.ErrFleetDispatcher
	}
	db := database.GetDB()
	if db == nil {
		return result, ErrFleetRuntimeDatabase
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = metadata.EditClientMetadataTx(ctx, tx, actor, request)
		return err
	})
	if err != nil {
		return entity.FleetClientMetadataResult{}, err
	}
	return result, nil
}
