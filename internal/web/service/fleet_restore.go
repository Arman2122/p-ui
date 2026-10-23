package service

import (
	"context"
	"errors"
	"os"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/config"
)

var ErrFleetRestore = errors.New("legacy database restore is unavailable with fleet state; an explicit fleet recovery workflow is required")

func requireLegacyDatabaseRestore(ctx context.Context, db *gorm.DB) error {
	role, err := config.RoleFromEnv()
	if err != nil {
		return errors.Join(ErrFleetRestore, err)
	}
	if role != config.RoleLegacy {
		return ErrFleetRestore
	}
	path := os.Getenv("PUI_FLEET_CONFIG")
	if path == "" {
		path = "/etc/penhoon/control.json"
	}
	if _, err := os.Stat(path); err == nil {
		return ErrFleetRestore
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrFleetRestore, err)
	}
	if db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.Name() != "postgres" {
		return ErrFleetRestore
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var present bool
	err = db.WithContext(ctx).Raw(`SELECT
EXISTS (SELECT 1 FROM fleet_authorities) OR
EXISTS (SELECT 1 FROM fleet_nodes) OR
EXISTS (SELECT 1 FROM fleet_clients WHERE owned) OR
EXISTS (SELECT 1 FROM fleet_fences WHERE recovery_required)`).Scan(&present).Error
	if err != nil {
		return errors.Join(ErrFleetRestore, err)
	}
	if present {
		return ErrFleetRestore
	}
	return nil
}
