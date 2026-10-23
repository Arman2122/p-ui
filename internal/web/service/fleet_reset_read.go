package service

import (
	"context"
	"database/sql"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func ReadFleetResetSchedule(ctx context.Context, actor *model.User, clientID string) (FleetResetScheduleView, error) {
	var result FleetResetScheduleView
	if !fleetID(clientID) {
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
		var client model.FleetClient
		if err := tx.First(&client, "id = ?", clientID).Error; err != nil {
			return err
		}
		if !client.Owned || client.PanelClientID == nil {
			return ErrFleetSource
		}
		var row model.FleetResetSchedule
		found := tx.Where("client_id = ?", clientID).Find(&row)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 {
			var source model.ClientRecord
			if err := tx.First(&source, *client.PanelClientID).Error; err != nil {
				return err
			}
			cycle, err := fleetResetCycleTx(tx, source, nil)
			if err != nil {
				return err
			}
			row.ClientID, row.EffectiveCycle = clientID, cycle
		}
		result = fleetResetScheduleView(row)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if database.GetDB() != db {
		return FleetResetScheduleView{}, ErrFleetRuntimeDatabase
	}
	return result, err
}
