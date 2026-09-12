package database

import (
	_ "embed"

	"gorm.io/gorm"
)

//go:embed reseller_usage.sql
var resellerUsageSQL string

//go:embed reseller_constraints.sql
var resellerConstraintsSQL string

//go:embed reseller_allocations.sql
var resellerAllocationsSQL string

func migrateResellerUsage() error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(resellerUsageSQL).Error; err != nil {
			return err
		}
		if err := tx.Exec(resellerConstraintsSQL).Error; err != nil {
			return err
		}
		return tx.Exec(resellerAllocationsSQL).Error
	})
}
