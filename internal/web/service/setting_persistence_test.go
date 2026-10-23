package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestEmptySessionSecretIsPersistentlyRepaired(t *testing.T) {
	initTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting("secret", ""); err != nil {
		t.Fatal(err)
	}
	secret, err := s.GetSecret()
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != defaultValueMap["secret"] || len(secret) == 0 {
		t.Fatalf("session secret = %q, want generated process default", secret)
	}
	stored, err := s.getString("secret")
	if err != nil || stored != string(secret) {
		t.Fatalf("stored session secret = %q, err = %v", stored, err)
	}
}

func TestMissingWarpClockIsNotAReadFailure(t *testing.T) {
	initTestDB(t)
	clock, err := (&SettingService{}).GetWarpLastUpdate()
	if err != nil || clock != 0 {
		t.Fatalf("missing WARP clock = %d, %v; want 0, nil", clock, err)
	}
}

func TestMigrationRollsBackFailedTrafficBackfill(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	for i, email := range []string{"migration-first", "migration-failure"} {
		inbound := &model.Inbound{
			UserId: 1, Tag: email, Enable: true, Port: 30080 + i, Protocol: model.VLESS,
			Settings:       `{"clients":[{"email":"` + email + `","id":"ce8d33df-3a64-4f10-8f9b-91c3a8e0c010","enable":true}]}`,
			StreamSettings: `{"network":"tcp","security":"none"}`,
		}
		if err := db.Create(inbound).Error; err != nil {
			t.Fatal(err)
		}
	}
	rejected := errors.New("traffic backfill rejected")
	const callback = "test:reject_migration_backfill"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if traffic, ok := tx.Statement.Dest.(*xray.ClientTraffic); ok && traffic.Email == "migration-failure" {
			_ = tx.AddError(rejected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	if err := (&InboundService{}).MigrationRequirements(); !errors.Is(err, rejected) {
		t.Fatalf("migration error = %v, want rejected backfill", err)
	}
	var count int64
	if err := db.Model(&xray.ClientTraffic{}).Where("email LIKE ?", "migration-%").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration committed %d traffic rows, want complete rollback", count)
	}
}
