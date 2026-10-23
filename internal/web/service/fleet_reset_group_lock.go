package service

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// Lock before fleetSnapshot takes SHARE; missing derived groups also need a row to lock.
func lockFleetResetGroupTx(tx *gorm.DB, name string) error {
	if name == "" {
		return nil
	}
	group := model.ClientGroup{Name: name}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(&group).Error; err != nil {
		return err
	}
	return tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", name).Take(&group).Error
}
