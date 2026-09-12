package database

import (
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func migrateDesignProductReferences() error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, statement := range []string{
			"ALTER TABLE policies DROP CONSTRAINT IF EXISTS uni_policies_name",
			"DROP INDEX IF EXISTS idx_policies_name",
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_policies_owner_name ON policies(COALESCE(owner_user_id,0),name)",
			"UPDATE policies SET reset_cycle = 'none' WHERE reset_cycle IS NULL OR reset_cycle = ''",
			"UPDATE policies SET on_depletion = 'disable' WHERE on_depletion IS NULL OR on_depletion = ''",
		} {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		for _, fk := range []struct {
			table           any
			name, statement string
		}{
			{&model.Policy{}, "fk_policy_reseller_owner", "ALTER TABLE policies ADD CONSTRAINT fk_policy_reseller_owner FOREIGN KEY (owner_user_id) REFERENCES reseller_accounts(user_id) ON DELETE RESTRICT"},
			{&model.GroupQuota{}, "fk_group_quota_group", "ALTER TABLE group_quota ADD CONSTRAINT fk_group_quota_group FOREIGN KEY (name) REFERENCES client_groups(name) ON UPDATE CASCADE ON DELETE CASCADE"},
			{&model.GroupInbound{}, "fk_group_inbound_group", "ALTER TABLE group_inbounds ADD CONSTRAINT fk_group_inbound_group FOREIGN KEY (name) REFERENCES client_groups(name) ON UPDATE CASCADE ON DELETE CASCADE"},
			{&model.GroupInbound{}, "fk_group_inbound_inbound", "ALTER TABLE group_inbounds ADD CONSTRAINT fk_group_inbound_inbound FOREIGN KEY (inbound_id) REFERENCES inbounds(id) ON DELETE CASCADE"},
			{&model.ClientTrafficSample{}, "fk_client_history_email", "ALTER TABLE client_traffic_samples ADD CONSTRAINT fk_client_history_email FOREIGN KEY (email) REFERENCES clients(email) ON UPDATE CASCADE ON DELETE CASCADE"},
			{&model.ClientHistoryCursor{}, "fk_client_cursor_email", "ALTER TABLE client_history_cursors ADD CONSTRAINT fk_client_cursor_email FOREIGN KEY (email) REFERENCES clients(email) ON UPDATE CASCADE ON DELETE CASCADE"},
		} {
			if !tx.Migrator().HasConstraint(fk.table, fk.name) {
				if err := tx.Exec(fk.statement).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
