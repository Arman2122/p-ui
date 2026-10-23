package database

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetTermGroupMigrationPreservesUnknownAndRetainsHistoryPostgres(t *testing.T) {
	initTestDB(t)
	digest := sha256.Sum256([]byte("group-provenance-fixture"))
	client := model.FleetClient{ID: uuid.NewString(), Revision: 1, CredentialGeneration: 1, CredentialDigest: digest[:], Owned: true}
	budget := model.FleetBudget{ID: uuid.NewString(), Unlimited: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	term := model.FleetTerm{ID: budget.ID, BudgetID: budget.ID, ClientID: client.ID, Generation: 1, Kind: "activation", State: "active", CredentialGeneration: 1, SourcePolicyDigest: digest[:], Rates: `{}`}
	if err := db.Create(&term).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateFleetClientReferences(); err != nil {
			t.Fatal(err)
		}
	}
	var pins int64
	if err := db.Model(&model.FleetTermGroup{}).Count(&pins).Error; err != nil || pins != 0 {
		t.Fatalf("migration guessed historical group membership: %d %v", pins, err)
	}
	group := model.ClientGroup{Name: "historical-group"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.FleetTermGroup{TermID: term.ID, GroupID: &group.Id}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&group, &term} {
		err := db.Delete(row).Error
		var pgError *pgconn.PgError
		if !errors.Is(err, gorm.ErrForeignKeyViolated) && (!errors.As(err, &pgError) || pgError.Code != "23503") {
			t.Fatalf("historical group attribution parent was deleted: %T %v", row, err)
		}
	}
}
