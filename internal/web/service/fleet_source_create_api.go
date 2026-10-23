package service

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func fleetCreateDispatcher() (webruntime.SourceCreateRuntime, error) {
	manager := webruntime.GetManager()
	if manager == nil {
		return nil, webruntime.ErrFleetDispatcher
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return nil, err
	}
	create, capable := dispatch.(webruntime.SourceCreateRuntime)
	if !capable {
		return nil, webruntime.ErrFleetDispatcher
	}
	return create, nil
}

func FleetClientCreationCapabilities(actor *model.User) entity.FleetClientCreateCapabilities {
	if actor == nil {
		return entity.FleetClientCreateCapabilities{}
	}
	_, err := fleetCreateDispatcher()
	return entity.FleetClientCreateCapabilities{Supported: err == nil, ActorID: actor.Id}
}

func CreateFleetClient(ctx context.Context, actor *model.User, request entity.ClientCreatePayload) (entity.FleetClientCreateResult, error) {
	var result entity.FleetClientCreateResult
	if actor == nil || request.ExpectedActorID <= 0 || request.ExpectedActorID != actor.Id {
		return result, controlplane.ErrScope
	}
	dispatch, err := fleetCreateDispatcher()
	if err != nil {
		return result, err
	}
	db := database.GetDB()
	if db == nil {
		return result, ErrFleetRuntimeDatabase
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = dispatch.CreateClientTx(ctx, tx, actor, request)
		return err
	})
	if err != nil {
		return entity.FleetClientCreateResult{}, err
	}
	return result, nil
}

func ReadFleetClientCreation(ctx context.Context, actor *model.User, operationID string) (entity.FleetClientCreateResult, error) {
	var result entity.FleetClientCreateResult
	if !fleetID(operationID) {
		return result, controlplane.ErrInvalid
	}
	db := database.GetDB()
	if db == nil {
		return result, ErrFleetRuntimeDatabase
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var operation model.FleetOperation
		if err := tx.First(&operation, "id = ?", operationID).Error; err != nil {
			return err
		}
		var receipt fleetCreateReceipt
		if json.Unmarshal(operation.Result, &receipt) != nil || receipt.Kind != "fleet-source-create/v1" || receipt.ActorID != actor.Id {
			return controlplane.ErrScope
		}
		return replayFleetCreate(operation, actor.Id, operation.Digest, &result)
	})
	return result, err
}

func requireLegacyClientCreation(request *entity.ClientCreatePayload) error {
	if request != nil && (request.OperationID != "" || request.ExpectedActorID != 0 || len(request.CredentialBindings) > 0) {
		return ErrFleetRuntimeCommand
	}
	manager := webruntime.GetManager()
	if manager == nil {
		return nil
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return err
	}
	if _, capable := dispatch.(webruntime.SourceCreateRuntime); capable || dispatch.Name() == "fleet" {
		return ErrFleetRuntimeCommand
	}
	return nil
}
