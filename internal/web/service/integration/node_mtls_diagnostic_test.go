package integration

import (
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestNodeMtlsTrustPoolDistinguishesInvalidBundleFromStorageFailure(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	if err := db.Create(&model.Setting{Key: "nodeMtlsClientCAPem", Value: "broken certificate bundle"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &service.SettingService{}
	if pool, err := svc.NodeMtlsClientCAPool(); pool != nil || !errors.Is(err, service.ErrNodeMtlsTrustBundleInvalid) {
		t.Errorf("malformed trust bundle returned pool=%v, err=%v; want typed invalid-bundle error", pool, err)
	}
	if err := db.Exec("ALTER TABLE settings RENAME TO unavailable_settings").Error; err != nil {
		t.Fatal(err)
	}
	pool, err := svc.NodeMtlsClientCAPool()
	var sqlState interface{ SQLState() string }
	if pool != nil || errors.Is(err, service.ErrNodeMtlsTrustBundleInvalid) || !errors.As(err, &sqlState) || sqlState.SQLState() != "42P01" {
		t.Fatalf("settings read returned pool=%v, err=%v; want untagged missing-table error", pool, err)
	}
}
