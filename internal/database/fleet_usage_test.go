package database

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetUsageMigrationRejectsAmbiguousOpeningPostgres(t *testing.T) {
	initTestDB(t)
	digest := sha256.Sum256([]byte("migration source"))
	client := model.FleetClient{ID: uuid.NewString(), Revision: 1, CredentialGeneration: 1, CredentialDigest: digest[:]}
	budget := model.FleetBudget{ID: uuid.NewString(), LimitBytes: 1000, UsedBytes: 100}
	node := model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "usage-migration", Enabled: true}
	for _, value := range []any{&client, &budget, &node} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	term := model.FleetTerm{ID: uuid.NewString(), ClientID: client.ID, Generation: 1, Kind: "activation", State: "active", CredentialGeneration: 1, SourcePolicyDigest: digest[:], BudgetID: budget.ID, Rates: "{}"}
	if err := db.Create(&term).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.FleetGrant{ID: uuid.NewString(), NodeID: node.ID, InstallationID: node.InstallationID, RuntimeID: "xray", ClientID: client.ID, TermID: term.ID, Revision: 1, IssuedBytes: 100, ConsumedBytes: 50}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.FleetMeter{GrantID: grant.ID, MeterID: "xray", Incarnation: uuid.NewString(), Uploaded: 30, Downloaded: 20}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateFleetUsageProjection(); err == nil || err.Error() != "fleet opening traffic directions require explicit reconciliation before projection" {
		t.Fatalf("ambiguous opening migration=%v", err)
	}
	var count int64
	if err := db.Model(&model.FleetTermUsage{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("ambiguous migration invented totals count=%d err=%v", count, err)
	}
	if err := db.Model(&term).Updates(map[string]any{"opening_uploaded": 20, "opening_downloaded": 30}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateFleetUsageProjection(); err != nil {
			t.Fatal(err)
		}
	}
	var usage model.FleetTermUsage
	if err := db.First(&usage, "term_id = ?", term.ID).Error; err != nil || usage.Uploaded != 30 || usage.Downloaded != 20 || usage.ObservedUnixMilli != 0 || usage.ReceivedUnixMilli != 0 {
		t.Fatalf("backfill fabricated measurements=%+v err=%v", usage, err)
	}
	if err := db.Model(&model.FleetTermUsageDay{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("backfill fabricated reporting time count=%d err=%v", count, err)
	}
}
