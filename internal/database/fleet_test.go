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

func TestFleetMigrationReferencesPostgres(t *testing.T) {
	initTestDB(t)
	node := model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "fleet-test", Enabled: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.FleetBudget{ID: uuid.NewString(), LimitBytes: 1000, ReservedBytes: 1000}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.FleetGrant{
		ID: uuid.NewString(), NodeID: node.ID, InstallationID: node.InstallationID,
		ClientID: uuid.NewString(), TermID: uuid.NewString(), IssuedBytes: 1000, Revision: 1,
		RuntimeID: "xray",
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.FleetGrantBudget{GrantID: grant.ID, BudgetID: budget.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateFleetReferences(); err != nil {
		t.Fatal(err)
	}
	var restored model.FleetGrant
	if err := db.First(&restored, "id = ?", grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.IssuedBytes != 1000 || restored.NodeID != node.ID {
		t.Fatalf("migration changed grant: %+v", restored)
	}
	orphan := grant
	orphan.ID = uuid.NewString()
	orphan.NodeID = uuid.NewString()
	err := db.Create(&orphan).Error
	var pgError *pgconn.PgError
	if !errors.Is(err, gorm.ErrForeignKeyViolated) && (!errors.As(err, &pgError) || pgError.Code != "23503") {
		t.Fatalf("orphan error=%v", err)
	}
	err = db.Delete(&node).Error
	if !errors.Is(err, gorm.ErrForeignKeyViolated) && (!errors.As(err, &pgError) || pgError.Code != "23503") {
		t.Fatalf("accounting parent deletion=%v", err)
	}
}

func TestFleetClientRatePoolMigrationPostgres(t *testing.T) {
	initTestDB(t)
	if err := db.Exec("DROP INDEX idx_fleet_rate_client").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_fleet_rate_client_term ON fleet_rate_budgets (client_id, term_id)").Error; err != nil {
		t.Fatal(err)
	}
	client := uuid.NewString()
	first := model.FleetRateBudget{ID: uuid.NewString(), ClientID: client, TermID: uuid.NewString(), Revision: 1, Desired: "{}"}
	second := model.FleetRateBudget{ID: uuid.NewString(), ClientID: client, TermID: uuid.NewString(), Revision: 1, Desired: "{}"}
	if err := db.Create(&[]model.FleetRateBudget{first, second}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateFleetClientRatePool(); err == nil || err.Error() != "fleet client has multiple rate pools; reconcile outstanding authority before upgrading" {
		t.Fatalf("ambiguous authority migration=%v", err)
	}
	var count int64
	if err := db.Model(&model.FleetRateBudget{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("migration changed ambiguous authority: count=%d err=%v", count, err)
	}
	if !db.Migrator().HasIndex(&model.FleetRateBudget{}, "idx_fleet_rate_client_term") || db.Migrator().HasIndex(&model.FleetRateBudget{}, "idx_fleet_rate_client") {
		t.Fatal("failed migration changed rate indexes")
	}
	if err := db.Delete(&second).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateFleetClientRatePool(); err != nil {
			t.Fatal(err)
		}
	}
	var restored model.FleetRateBudget
	if err := db.First(&restored, "id = ?", first.ID).Error; err != nil || restored != first {
		t.Fatalf("migration changed sole authority: %+v err=%v", restored, err)
	}
	if db.Migrator().HasIndex(&model.FleetRateBudget{}, "idx_fleet_rate_client_term") || !db.Migrator().HasIndex(&model.FleetRateBudget{}, "idx_fleet_rate_client") {
		t.Fatal("migration did not replace rate index")
	}
	err := db.Create(&second).Error
	var pgError *pgconn.PgError
	if !errors.Is(err, gorm.ErrDuplicatedKey) && (!errors.As(err, &pgError) || pgError.Code != "23505") {
		t.Fatalf("duplicate successor client rate pool=%v", err)
	}
}

func TestFleetClientDeletionRetainsAccountingIdentityPostgres(t *testing.T) {
	initTestDB(t)
	panel := model.ClientRecord{Email: "fleet-client", UUID: uuid.NewString()}
	if err := db.Create(&panel).Error; err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(panel.UUID))
	client := model.FleetClient{ID: uuid.NewString(), PanelClientID: &panel.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: digest[:]}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.FleetBudget{ID: uuid.NewString(), LimitBytes: 1000, UsedBytes: 100, ReservedBytes: 900}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	term := model.FleetTerm{
		ID: uuid.NewString(), ClientID: client.ID, Generation: 1, Kind: "activation", State: "active",
		CredentialGeneration: 1, BudgetID: budget.ID, StartedUnixMilli: 1, Rates: "{}", SourcePolicyDigest: digest[:],
	}
	if err := db.Create(&term).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&client).Update("current_term_id", term.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&panel).Error; err != nil {
		t.Fatal(err)
	}
	var retained model.FleetClient
	if err := db.First(&retained, "id = ?", client.ID).Error; err != nil || retained.PanelClientID != nil || retained.CurrentTermID == nil || *retained.CurrentTermID != term.ID {
		t.Fatalf("client deletion erased or rebound identity=%+v err=%v", retained, err)
	}
	var retainedBudget model.FleetBudget
	if err := db.First(&retainedBudget, "id = ?", budget.ID).Error; err != nil || retainedBudget != budget {
		t.Fatalf("client deletion changed accounting=%+v err=%v", retainedBudget, err)
	}
	err := db.Delete(&retained).Error
	var pgError *pgconn.PgError
	if !errors.Is(err, gorm.ErrForeignKeyViolated) && (!errors.As(err, &pgError) || pgError.Code != "23503") {
		t.Fatalf("retained accounting parent deletion=%v", err)
	}
}
