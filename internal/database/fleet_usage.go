package database

import (
	"errors"

	"gorm.io/gorm"
)

func migrateFleetUsageProjection() error {
	var missing bool
	if err := db.Raw("SELECT EXISTS (SELECT 1 FROM fleet_terms t LEFT JOIN fleet_term_usages u ON u.term_id = t.id WHERE u.term_id IS NULL)").Scan(&missing).Error; err != nil {
		return err
	}
	if !missing {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("LOCK TABLE fleet_terms, fleet_term_usages, fleet_meters, fleet_grants, fleet_budgets IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return err
		}
		const totals = `WITH totals AS (
SELECT g.term_id, COALESCE(SUM(m.uploaded),0) AS up, COALESCE(SUM(m.downloaded),0) AS down
FROM fleet_grants g LEFT JOIN fleet_meters m ON m.grant_id = g.id GROUP BY g.term_id
) `
		var ambiguous bool
		if err := tx.Raw(totals + `SELECT EXISTS (SELECT 1 FROM fleet_terms t
JOIN fleet_budgets b ON b.id = t.budget_id LEFT JOIN totals v ON v.term_id = t.id
LEFT JOIN fleet_term_usages u ON u.term_id = t.id WHERE u.term_id IS NULL
AND b.used_bytes::numeric <> t.opening_uploaded::numeric + t.opening_downloaded + COALESCE(v.up,0) + COALESCE(v.down,0))`).Scan(&ambiguous).Error; err != nil {
			return err
		}
		if ambiguous {
			return errors.New("fleet opening traffic directions require explicit reconciliation before projection")
		}
		return tx.Exec(totals + `INSERT INTO fleet_term_usages(term_id, uploaded, downloaded, observed_unix_milli, received_unix_milli)
SELECT t.id, COALESCE(v.up,0), COALESCE(v.down,0), 0, 0 FROM fleet_terms t
LEFT JOIN totals v ON v.term_id = t.id LEFT JOIN fleet_term_usages u ON u.term_id = t.id
WHERE u.term_id IS NULL ON CONFLICT (term_id) DO NOTHING`).Error
	})
}
