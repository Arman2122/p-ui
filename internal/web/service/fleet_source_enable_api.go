package service

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func ownedFleetEnableTargets(tx *gorm.DB, records map[string]*model.ClientRecord) (map[int]model.FleetClient, error) {
	ids := make([]int, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Id)
	}
	result := make(map[int]model.FleetClient)
	for _, chunk := range chunkInts(ids, sqlInChunk) {
		var rows []model.FleetClient
		if err := tx.Where("owned AND panel_client_id IN ?", chunk).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result[*row.PanelClientID] = row
		}
	}
	return result, nil
}

func fleetEnableDispatcher() (webruntime.SourceEnableRuntime, error) {
	manager := webruntime.GetManager()
	if manager == nil {
		return nil, webruntime.ErrFleetDispatcher
	}
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		return nil, err
	}
	enable, capable := dispatch.(webruntime.SourceEnableRuntime)
	if !capable {
		return nil, webruntime.ErrFleetDispatcher
	}
	return enable, nil
}

func dispatchFleetSourceEnable(ctx context.Context, db *gorm.DB, dispatch webruntime.SourceEnableRuntime, actor *model.User, record model.ClientRecord, client model.FleetClient, enabled bool) (FleetSourceMutationResult, error) {
	var result FleetSourceMutationResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.ClientRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, record.Id).Error; err != nil {
			return err
		}
		if current.Email != record.Email || client.PanelClientID == nil || *client.PanelClientID != current.Id {
			return ErrFleetSource
		}
		var err error
		result, err = dispatch.SetClientEnabledTx(ctx, tx, actor, webruntime.SourceEnableRequest{
			OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(client.Revision), Enabled: enabled,
		})
		return err
	})
	return result, err
}

func lockLegacyEnableSources(tx *gorm.DB, clients []model.Client, changed []string) error {
	emails := make([]string, 0, len(clients))
	for _, client := range clients {
		emails = append(emails, client.Email)
	}
	var ids []int
	identities := make(map[int]string)
	known := make(map[string]bool)
	for _, chunk := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := tx.Select("id,email").Where("email IN ?", chunk).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			ids = append(ids, row.Id)
			identities[row.Id], known[row.Email] = row.Email, true
		}
	}
	for _, email := range changed {
		if !known[email] {
			return ErrFleetSource
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, chunk := range chunkInts(ids, sqlInChunk) {
		var rows []model.ClientRecord
		if err := tx.Select("id,email").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrFleetSource
		}
		for _, row := range rows {
			if identities[row.Id] != row.Email {
				return ErrFleetSource
			}
		}
	}
	return requireLegacyClientLifecycle(tx, changed)
}
