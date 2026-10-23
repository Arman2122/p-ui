package database

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestClientRenewalGenerationUpgradePreservesUsage(t *testing.T) {
	for _, shape := range []string{"missing", "nullable"} {
		t.Run(shape, func(t *testing.T) {
			initTestDB(t)
			if err := db.Exec("ALTER TABLE client_traffics DROP COLUMN IF EXISTS reset_count").Error; err != nil {
				t.Fatal(err)
			}
			if shape == "nullable" {
				if err := db.Exec("ALTER TABLE client_traffics ADD COLUMN reset_count bigint").Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Exec("INSERT INTO client_traffics(email,up,down) VALUES ('existing',11,22)").Error; err != nil {
				t.Fatal(err)
			}
			if err := initModels(); err != nil {
				t.Fatal(err)
			}
			var count *int64
			if err := db.Raw("SELECT reset_count FROM client_traffics WHERE email = 'existing'").Scan(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count == nil || *count != 0 {
				t.Fatalf("generation=%v, want persisted zero", count)
			}
			var row core.ClientTraffic
			if err := db.Where("email = ?", "existing").First(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.Up != 11 || row.Down != 22 {
				t.Fatalf("usage changed: %+v", row)
			}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire["resetCount"] != float64(0) {
				t.Fatalf("node payload omits tracked zero generation: %s", raw)
			}
		})
	}
}
