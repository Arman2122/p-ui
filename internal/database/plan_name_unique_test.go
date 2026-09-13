package database

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// Two plans sharing a name in one scope made every picker ambiguous; the boot
// renames the younger ones, names the renames, and then the index refuses a third.
func TestPlanNamesAreMadeUniqueOnBoot(t *testing.T) {
	initTestDB(t)
	// The database an upgrade meets: the design migration's case-sensitive index,
	// under which "Monthly" and "monthly" both exist.
	if err := db.Exec(`DROP INDEX IF EXISTS idx_policies_owner_name`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_policies_owner_name ON policies(COALESCE(owner_user_id,0),name)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Monthly", "monthly", "Monthly (2)", "Yearly"} {
		if err := db.Exec(`INSERT INTO policies (name, tiers) VALUES (?, '[]')`, name).Error; err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	if err := initModels(); err != nil {
		t.Fatalf("initModels on colliding plan names: %v", err)
	}
	var names []string
	if err := db.Model(&model.Policy{}).Order("id").Pluck("name", &names).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"Monthly", "monthly (2)", "Monthly (2) (2)", "Yearly"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	var definition string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'policies' AND indexname = 'idx_policies_owner_name'`).Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "lower(") {
		t.Fatalf("the name index is not case-insensitive: %s", definition)
	}
	if err := db.Exec(`INSERT INTO policies (name, tiers) VALUES ('MONTHLY', '[]')`).Error; err == nil {
		t.Fatal("a case variant of an existing plan name was inserted past the index")
	}
	if err := initModels(); err != nil {
		t.Fatalf("second boot on a clean catalogue: %v", err)
	}
}
