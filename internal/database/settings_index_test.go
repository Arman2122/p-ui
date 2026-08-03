package database

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// settings.key is read on nearly every request and job tick (getSetting
// WHERE key=?); AutoMigrate must create the index so those lookups don't
// full-scan the settings table past the large xrayTemplateConfig blob. gorm
// creates missing indexes on migrate, so this also covers existing DBs.
func TestAutoMigrateCreatesSettingsKeyIndex(t *testing.T) {
	initTestDB(t)
	if !GetDB().Migrator().HasIndex(&model.Setting{}, "idx_settings_key") {
		t.Errorf("expected idx_settings_key to exist after AutoMigrate")
	}
}
