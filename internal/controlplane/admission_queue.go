package controlplane

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func (s *Store) ClaimAdmissionNodes(ctx context.Context, limit int) ([]model.FleetAdmissionNodeQueue, error) {
	if limit < 1 || limit > 4 {
		return nil, ErrInvalid
	}
	now, claim := time.Now().UnixMilli(), uuid.NewString()
	var nodes []model.FleetAdmissionNodeQueue
	err := s.db.WithContext(ctx).Raw(`WITH due AS (
SELECT node_id FROM fleet_admission_node_queues WHERE pending AND available_unix_milli <= ? AND lease_until_unix_milli <= ?
ORDER BY available_unix_milli,node_id FOR UPDATE SKIP LOCKED LIMIT ?
) UPDATE fleet_admission_node_queues q SET claim_id = ?, lease_until_unix_milli = ?
FROM due WHERE q.node_id = due.node_id RETURNING q.*`, now, now, limit, claim, now+60_000).Scan(&nodes).Error
	return nodes, err
}

func validAdmissionLease(lease model.FleetAdmissionNodeQueue) bool {
	return validID(lease.NodeID) && lease.ClaimID != nil && validID(*lease.ClaimID)
}

func (s *Store) DueAdmissionDemands(ctx context.Context, lease model.FleetAdmissionNodeQueue) ([]model.FleetAdmissionDemand, error) {
	if !validAdmissionLease(lease) {
		return nil, ErrInvalid
	}
	now := time.Now().UnixMilli()
	var demands []model.FleetAdmissionDemand
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetAdmissionDemand{}).Where(`node_id = ? AND pending AND deadline_unix_milli <= ?
AND EXISTS (SELECT 1 FROM fleet_admission_node_queues q WHERE q.node_id = fleet_admission_demands.node_id AND q.claim_id = ? AND q.lease_until_unix_milli > ?)`,
			lease.NodeID, now, *lease.ClaimID, now).Updates(map[string]any{"pending": false, "outcome": "expired"}).Error; err != nil {
			return err
		}
		return tx.Table("fleet_admission_demands d").Select("d.*").Joins("JOIN fleet_admission_node_queues q ON q.node_id = d.node_id").
			Where("d.node_id = ? AND d.pending AND d.deadline_unix_milli > ? AND d.available_unix_milli <= ? AND q.claim_id = ? AND q.lease_until_unix_milli > ?", lease.NodeID, now, now, *lease.ClaimID, now).
			Order("d.available_unix_milli,d.placement_id").Limit(8).Scan(&demands).Error
	})
	return demands, err
}

func (s *Store) SettleAdmissionDemand(ctx context.Context, lease model.FleetAdmissionNodeQueue, demand model.FleetAdmissionDemand, retry bool) error {
	if !validAdmissionLease(lease) || demand.NodeID != lease.NodeID || !validID(demand.PlacementID) || !validID(demand.RequestID) || demand.Revision <= 0 {
		return ErrInvalid
	}
	now := time.Now().UnixMilli()
	updates := map[string]any{"pending": false}
	if retry && demand.DeadlineUnixMilli > now {
		attempts := min(demand.Attempts+1, 5)
		updates = map[string]any{"attempts": attempts, "available_unix_milli": min(demand.DeadlineUnixMilli, now+int64(min(30, 1<<attempts))*1000)}
	}
	return s.db.WithContext(ctx).Model(&model.FleetAdmissionDemand{}).Where(`placement_id = ? AND node_id = ? AND request_id = ? AND revision = ? AND pending
AND EXISTS (SELECT 1 FROM fleet_admission_node_queues q WHERE q.node_id = fleet_admission_demands.node_id AND q.claim_id = ? AND q.lease_until_unix_milli > ?)`,
		demand.PlacementID, lease.NodeID, demand.RequestID, demand.Revision, *lease.ClaimID, now).Updates(updates).Error
}

func (s *Store) ReleaseAdmissionNode(ctx context.Context, lease model.FleetAdmissionNodeQueue) error {
	if !validAdmissionLease(lease) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.FleetAdmissionNodeQueue
		found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ? AND claim_id = ?", lease.NodeID, *lease.ClaimID).Find(&current)
		if found.Error != nil || found.RowsAffected == 0 {
			return found.Error
		}
		now := time.Now().UnixMilli()
		var remaining struct{ Earliest *int64 }
		if err := tx.Raw(`SELECT MIN(available_unix_milli) AS earliest FROM (
SELECT available_unix_milli FROM fleet_admission_demands WHERE node_id = ? AND pending AND deadline_unix_milli > ?
UNION ALL SELECT available_unix_milli FROM fleet_rate_reconciliations WHERE node_id = ? AND pending AND deadline_unix_milli > ?
) work`, lease.NodeID, now, lease.NodeID, now).Scan(&remaining).Error; err != nil {
			return err
		}
		updates := map[string]any{"pending": remaining.Earliest != nil, "claim_id": nil, "lease_until_unix_milli": 0}
		if remaining.Earliest != nil {
			updates["available_unix_milli"] = max(now+250, *remaining.Earliest)
		}
		return tx.Model(&current).Updates(updates).Error
	})
}

// Queue insertion runs outside client transactions because its node foreign key takes a row lock.
func (s *Store) WakeRateReconciliationNodes(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if err := s.db.WithContext(ctx).Exec(`WITH expired AS (
SELECT grant_id FROM fleet_rate_reconciliations WHERE pending AND deadline_unix_milli <= ?
ORDER BY deadline_unix_milli,grant_id FOR UPDATE SKIP LOCKED LIMIT 128
) UPDATE fleet_rate_reconciliations r SET pending = FALSE FROM expired WHERE r.grant_id = expired.grant_id`, now).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO fleet_admission_node_queues (node_id,pending,available_unix_milli,lease_until_unix_milli)
SELECT r.node_id,TRUE,MIN(r.available_unix_milli),0 FROM fleet_rate_reconciliations r JOIN fleet_nodes n ON n.id = r.node_id
WHERE r.pending AND r.deadline_unix_milli > ? AND r.available_unix_milli <= ?
GROUP BY r.node_id ORDER BY MIN(r.available_unix_milli),r.node_id LIMIT 128
ON CONFLICT (node_id) DO UPDATE SET pending = TRUE,available_unix_milli = CASE WHEN fleet_admission_node_queues.pending
THEN LEAST(fleet_admission_node_queues.available_unix_milli,EXCLUDED.available_unix_milli) ELSE EXCLUDED.available_unix_milli END`, now, now).Error
}

func (s *Store) DueRateReconciliations(ctx context.Context, lease model.FleetAdmissionNodeQueue) ([]model.FleetRateReconciliation, error) {
	if !validAdmissionLease(lease) {
		return nil, ErrInvalid
	}
	now := time.Now().UnixMilli()
	var work []model.FleetRateReconciliation
	err := s.db.WithContext(ctx).Table("fleet_rate_reconciliations r").Select("r.*").Joins("JOIN fleet_admission_node_queues q ON q.node_id = r.node_id").
		Where("r.node_id = ? AND r.pending AND r.deadline_unix_milli > ? AND r.available_unix_milli <= ? AND q.claim_id = ? AND q.lease_until_unix_milli > ?", lease.NodeID, now, now, *lease.ClaimID, now).
		Order("r.available_unix_milli,r.grant_id").Limit(8).Scan(&work).Error
	return work, err
}

func (s *Store) SettleRateReconciliation(ctx context.Context, lease model.FleetAdmissionNodeQueue, work model.FleetRateReconciliation, retry bool) error {
	if !validAdmissionLease(lease) || work.NodeID != lease.NodeID || !validID(work.GrantID) || !validID(work.RequestID) || work.DemandRevision <= 0 {
		return ErrInvalid
	}
	now := time.Now().UnixMilli()
	return s.db.WithContext(ctx).Model(&model.FleetRateReconciliation{}).Where(`grant_id = ? AND node_id = ? AND request_id = ? AND demand_revision = ? AND pending
AND EXISTS (SELECT 1 FROM fleet_admission_node_queues q WHERE q.node_id = fleet_rate_reconciliations.node_id AND q.claim_id = ? AND q.lease_until_unix_milli > ?)`,
		work.GrantID, work.NodeID, work.RequestID, work.DemandRevision, *lease.ClaimID, now).
		Updates(map[string]any{"pending": retry && work.DeadlineUnixMilli > now, "available_unix_milli": now + 500}).Error
}
