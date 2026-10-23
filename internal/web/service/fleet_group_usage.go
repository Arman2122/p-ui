package service

import (
	"bytes"
	"database/sql"
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func recordFleetTermGroupTx(tx *gorm.DB, snapshot fleetClientSnapshot, term model.FleetTerm) error {
	if !snapshot.Fleet.Owned || term.ClientID != snapshot.Fleet.ID || !bytes.Equal(term.SourcePolicyDigest, snapshot.PolicyDigest) {
		return ErrFleetUsage
	}
	row := model.FleetTermGroup{TermID: term.ID}
	if snapshot.Client.Group != "" {
		if snapshot.Client.OwnerUserID != nil {
			return ErrFleetUsage
		}
		group := snapshot.Group
		if group == nil {
			group = &model.ClientGroup{Name: snapshot.Client.Group}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(group).Error; err != nil {
				return err
			}
			if err := tx.Where("name = ?", snapshot.Client.Group).First(group).Error; err != nil {
				return err
			}
		}
		if group.Name != snapshot.Client.Group || group.Id <= 0 {
			return ErrFleetUsage
		}
		row.GroupID = &group.Id
	}
	if term.Kind != controlplane.Activation && (term.OpeningUploaded != 0 || term.OpeningDownloaded != 0) {
		return ErrFleetUsage
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	var saved model.FleetTermGroup
	if err := tx.First(&saved, "term_id = ?", term.ID).Error; err != nil {
		return err
	}
	if !sameOwner(saved.GroupID, row.GroupID) {
		return ErrFleetUsage
	}
	return nil
}

const fleetUnknownTermUsageSQL = `SELECT 1 FROM fleet_terms t JOIN fleet_clients fc ON fc.id=t.client_id
LEFT JOIN fleet_term_groups p ON p.term_id=t.id
LEFT JOIN fleet_term_usages u ON u.term_id=t.id LEFT JOIN fleet_budgets b ON b.id=t.budget_id
WHERE fc.owned AND (p.term_id IS NULL OR u.term_id IS NULL OR b.id IS NULL
OR t.kind <> 'activation' AND (t.opening_uploaded <> 0 OR t.opening_downloaded <> 0)
OR b.used_bytes::numeric <> t.opening_uploaded::numeric+t.opening_downloaded+u.uploaded+u.downloaded
OR EXISTS (SELECT 1 FROM fleet_grants g JOIN fleet_grant_budgets gb ON gb.grant_id=g.id
JOIN fleet_budget_scopes s ON s.budget_id=gb.budget_id AND s.kind='group'
WHERE g.term_id=t.id AND (p.group_id IS NULL OR p.group_id<>s.source_id)))`

func requireFleetGroupUsageTx(tx *gorm.DB) error {
	var unknown bool
	if err := tx.Raw(`SELECT EXISTS (` + fleetUnknownTermUsageSQL + `)
OR EXISTS (SELECT 1 FROM fleet_clients fc JOIN clients c ON c.id=fc.panel_client_id
WHERE fc.owned AND c.group_name<>'' AND NOT EXISTS (SELECT 1 FROM fleet_terms t WHERE t.client_id=fc.id)
AND (fc.current_term_id IS NOT NULL OR fc.pending_term_id IS NOT NULL
OR fc.credential_claims_version<>1 AND NOT EXISTS (SELECT 1 FROM client_traffics ct WHERE ct.email=c.email)))`).Scan(&unknown).Error; err != nil {
		return err
	}
	if unknown {
		return ErrFleetUsage
	}
	return nil
}

func readFleetGroupSummaries(db *gorm.DB, managed bool) ([]GroupSummary, error) {
	var rows []GroupSummary
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		rows, err = readFleetGroupSummariesTx(tx, managed)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return rows, err
}

func readFleetGroupSummariesTx(db *gorm.DB, managed bool) ([]GroupSummary, error) {
	if err := requireFleetGroupUsageTx(db); err != nil {
		return nil, err
	}
	var rows []GroupSummary
	err := db.Raw(`WITH member_counts AS (
SELECT c.group_name AS name,COUNT(*) AS client_count FROM clients c
WHERE c.group_name<>'' AND (? OR c.owner_user_id IS NULL) GROUP BY c.group_name
), legacy AS (
SELECT c.group_name AS name,
COALESCE(SUM(CASE WHEN fc.credential_claims_version=1 THEN 0 ELSE ct.up END),0) AS up,
COALESCE(SUM(CASE WHEN fc.credential_claims_version=1 THEN 0 ELSE ct.down END),0) AS down
FROM clients c LEFT JOIN client_traffics ct ON ct.email=c.email
LEFT JOIN fleet_clients fc ON fc.panel_client_id=c.id AND fc.owned
WHERE c.group_name<>'' AND (? OR c.owner_user_id IS NULL)
AND (fc.id IS NULL OR NOT EXISTS (SELECT 1 FROM fleet_terms t WHERE t.client_id=fc.id)) GROUP BY c.group_name
), measured AS (
SELECT p.group_id,SUM(u.uploaded::numeric+t.opening_uploaded) AS up,SUM(u.downloaded::numeric+t.opening_downloaded) AS down
FROM fleet_term_groups p JOIN fleet_terms t ON t.id=p.term_id JOIN fleet_clients fc ON fc.id=t.client_id AND fc.owned
JOIN fleet_term_usages u ON u.term_id=t.id WHERE p.group_id IS NOT NULL GROUP BY p.group_id
), names AS (SELECT name FROM client_groups UNION SELECT name FROM member_counts)
SELECT names.name,COALESCE(c.client_count,0) AS client_count,
GREATEST(COALESCE(l.up,0)+COALESCE(m.up,0)-COALESCE(g.reset_up,0),0) AS up,
GREATEST(COALESCE(l.down,0)+COALESCE(m.down,0)-COALESCE(g.reset_down,0),0) AS down
FROM names LEFT JOIN client_groups g ON g.name=names.name LEFT JOIN member_counts c ON c.name=names.name
LEFT JOIN legacy l ON l.name=names.name LEFT JOIN measured m ON m.group_id=g.id ORDER BY LOWER(names.name),names.name`, !managed, !managed).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Up < 0 || rows[i].Down < 0 || rows[i].Up > math.MaxInt64-rows[i].Down {
			return nil, ErrFleetUsage
		}
		rows[i].TrafficUsed = rows[i].Up + rows[i].Down
	}
	return rows, nil
}
