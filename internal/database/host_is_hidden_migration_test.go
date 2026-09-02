package database

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// The hosts.is_hidden column, written by the form and read by nothing, is
// dropped from an upgraded panel. F146.
func TestMigrateDropHostIsHiddenRemovesTheColumn(t *testing.T) {
	initTestDB(t)
	if err := db.Exec(`ALTER TABLE hosts ADD COLUMN IF NOT EXISTS is_hidden boolean DEFAULT false`).Error; err != nil {
		t.Fatalf("recreate the legacy column: %v", err)
	}
	if err := migrateDropHostIsHidden(); err != nil {
		t.Fatalf("migrateDropHostIsHidden: %v", err)
	}
	if db.Migrator().HasColumn(&model.Host{}, "is_hidden") {
		t.Fatal("hosts.is_hidden survived the migration")
	}
}
