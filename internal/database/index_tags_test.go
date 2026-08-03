package database

import (
	"testing"

	"github.com/Arman2122/p-ui/v3/internal/database/model"
	"github.com/Arman2122/p-ui/v3/internal/xray"
)

// AutoMigrate must create the hot-path indexes added for client group filters
// and client_traffics inbound lookups. gorm creates missing indexes on migrate,
// so this also protects existing DBs after upgrade.
func TestAutoMigrateCreatesHotPathIndexes(t *testing.T) {
	initTestDB(t)
	migrator := GetDB().Migrator()

	cases := []struct {
		model any
		index string
	}{
		{&model.ClientRecord{}, "idx_client_record_group"},
		{&xray.ClientTraffic{}, "idx_client_traffics_inbound"},
		{&xray.ClientTraffic{}, "idx_client_traffics_renew"},
		{&model.ClientGlobalTraffic{}, "idx_client_global_email"},
	}
	for _, c := range cases {
		if !migrator.HasIndex(c.model, c.index) {
			t.Errorf("expected index %q to exist after AutoMigrate", c.index)
		}
	}
}
