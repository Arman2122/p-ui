package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type refusedRestoreInput struct{ reads int }

func (f *refusedRestoreInput) Read([]byte) (int, error) {
	f.reads++
	return 0, errors.New("restore fixture must never be consumed")
}

func (*refusedRestoreInput) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("restore fixture must never be consumed")
}

func (*refusedRestoreInput) Seek(int64, int) (int64, error) {
	return 0, errors.New("restore fixture must never be consumed")
}

func (*refusedRestoreInput) Close() error { return nil }

func assertFleetRestoreRefused(t *testing.T) {
	t.Helper()
	before := database.GetDB()
	input := new(refusedRestoreInput)
	if err := (&ServerService{}).restorePostgresDump(input); !errors.Is(err, ErrFleetRestore) {
		t.Fatalf("fleet restore reached legacy processing: %v", err)
	}
	if input.reads != 0 || database.GetDB() != before {
		t.Fatalf("refused restore consumed input or replaced database: reads=%d", input.reads)
	}
}

func TestFleetRestoreRejectsRoleAndConfiguredControl(t *testing.T) {
	t.Setenv("PUI_FLEET_CONFIG", filepath.Join(t.TempDir(), "absent-control.json"))
	for _, role := range []string{"master", "combined", "node", "invalid"} {
		t.Run(role, func(t *testing.T) {
			t.Setenv("PUI_ROLE", role)
			assertFleetRestoreRefused(t)
		})
	}
	t.Setenv("PUI_ROLE", "")
	t.Run("configured", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.json")
		if err := os.WriteFile(path, []byte("synthetic configured authority"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PUI_FLEET_CONFIG", path)
		assertFleetRestoreRefused(t)
	})
	t.Run("stat error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), strings.Repeat("x", 300))
		if _, err := os.Stat(path); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fixture did not produce an unknown control-path state: %v", err)
		}
		t.Setenv("PUI_FLEET_CONFIG", path)
		assertFleetRestoreRefused(t)
	})
}

func TestFleetRestoreRejectsPersistedStatePostgres(t *testing.T) {
	t.Setenv("PUI_ROLE", "")
	t.Setenv("PUI_FLEET_CONFIG", filepath.Join(t.TempDir(), "absent-control.json"))
	cases := []struct {
		name string
		row  func() any
	}{
		{"authority", func() any {
			return &model.FleetAuthority{ID: 1, ControlPlaneID: uuid.NewString(), KeyDigest: make([]byte, 32)}
		}},
		{"disabled node", func() any {
			return &model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "disabled-installation", Enabled: false}
		}},
		{"owned orphan", func() any {
			return &model.FleetClient{ID: uuid.NewString(), Revision: 1, CredentialGeneration: 1, CredentialDigest: make([]byte, 32), Owned: true}
		}},
		{"recovery fence", func() any { return &model.FleetFence{ID: 1, RecoveryRequired: true, Reason: "OPERATOR_RESTORE"} }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			initTestDB(t)
			db := database.GetDB()
			row := test.row()
			if err := db.Clauses(clause.OnConflict{UpdateAll: true}).Create(row).Error; err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			assertFleetRestoreRefused(t)
			pool, err := db.DB()
			if err != nil || pool.PingContext(t.Context()) != nil {
				t.Fatalf("restore refusal closed the current database: %v", err)
			}
			if err := db.First(row).Error; err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(row)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("restore refusal changed fleet state: before=%s after=%s err=%v", before, after, err)
			}
		})
	}
}

func TestFleetRestoreLegacyEligibilityAndUnknownDatabasePostgres(t *testing.T) {
	t.Setenv("PUI_ROLE", "")
	t.Setenv("PUI_FLEET_CONFIG", filepath.Join(t.TempDir(), "absent-control.json"))
	initTestDB(t)
	db := database.GetDB()
	if err := db.Create(&model.FleetFence{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.FleetClient{ID: uuid.NewString(), Revision: 1, CredentialGeneration: 1, CredentialDigest: make([]byte, 32)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := requireLegacyDatabaseRestore(t.Context(), db); err != nil {
		t.Fatalf("unused legacy fleet tables blocked eligibility: %v", err)
	}
	if err := requireLegacyDatabaseRestore(t.Context(), nil); !errors.Is(err, ErrFleetRestore) {
		t.Fatalf("missing database was considered safe: %v", err)
	}
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	assertFleetRestoreRefused(t)
	if err := requireLegacyDatabaseRestore(t.Context(), &gorm.DB{}); !errors.Is(err, ErrFleetRestore) {
		t.Fatalf("invalid database was considered safe: %v", err)
	}
}
