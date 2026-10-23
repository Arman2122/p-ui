package service

import (
	"math"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func fleetGroupOpeningTx(tx *gorm.DB, group model.ClientGroup) (int64, error) {
	const members = `WITH members AS (SELECT fc.id,fc.current_term_id,fc.pending_term_id,fc.credential_claims_version,c.email
FROM clients c JOIN fleet_clients fc ON fc.panel_client_id=c.id AND fc.owned
WHERE c.group_name=? AND c.owner_user_id IS NULL) `
	var unknown bool
	if err := tx.Raw(members+`SELECT EXISTS (`+fleetUnknownTermUsageSQL+`
AND (p.group_id=? OR t.client_id IN (SELECT id FROM members)))
OR EXISTS (SELECT 1 FROM members m WHERE NOT EXISTS (SELECT 1 FROM fleet_terms t WHERE t.client_id=m.id)
AND (m.current_term_id IS NOT NULL OR m.pending_term_id IS NOT NULL
OR m.credential_claims_version<>1 AND NOT EXISTS (SELECT 1 FROM client_traffics ct WHERE ct.email=m.email AND ct.up=0 AND ct.down=0)))`, group.Name, group.Id).Scan(&unknown).Error; err != nil {
		return 0, err
	}
	if unknown {
		return 0, ErrFleetUsage
	}
	var totals struct{ Up, Down int64 }
	if err := tx.Raw(members+`, measured AS (
SELECT u.uploaded::numeric+t.opening_uploaded AS up,u.downloaded::numeric+t.opening_downloaded AS down
FROM fleet_term_groups p JOIN fleet_terms t ON t.id=p.term_id JOIN fleet_clients fc ON fc.id=t.client_id AND fc.owned
JOIN fleet_term_usages u ON u.term_id=t.id WHERE p.group_id=?
UNION ALL
SELECT CASE WHEN m.credential_claims_version=1 THEN 0 ELSE ct.up END,CASE WHEN m.credential_claims_version=1 THEN 0 ELSE ct.down END
FROM members m LEFT JOIN client_traffics ct ON ct.email=m.email WHERE NOT EXISTS (SELECT 1 FROM fleet_terms t WHERE t.client_id=m.id)
) SELECT GREATEST(COALESCE(SUM(up),0)-?,0) AS up,GREATEST(COALESCE(SUM(down),0)-?,0) AS down FROM measured`, group.Name, group.Id, group.ResetUp, group.ResetDown).Scan(&totals).Error; err != nil {
		return 0, err
	}
	if totals.Up < 0 || totals.Down < 0 || totals.Up > math.MaxInt64-totals.Down {
		return 0, ErrFleetUsage
	}
	return totals.Up + totals.Down, nil
}
