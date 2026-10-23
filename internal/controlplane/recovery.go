package controlplane

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func lockAuthority(tx *gorm.DB, exclusive bool) error {
	strength := "SHARE"
	if exclusive {
		strength = "UPDATE"
	}
	var fence model.FleetFence
	if err := tx.Clauses(clause.Locking{Strength: strength}).First(&fence, "id = 1").Error; err != nil {
		return err
	}
	if fence.RecoveryRequired {
		return ErrRecovery
	}
	return nil
}

func (s *Store) HoldForRecovery(ctx context.Context) error {
	return s.db.WithContext(ctx).Model(&model.FleetFence{}).Where("id = 1").Updates(map[string]any{
		"recovery_required": true, "reason": "OPERATOR_RESTORE", "witness_node_id": nil,
	}).Error
}
