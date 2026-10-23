package controlplane

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

const (
	CreditRefillTargetBytes   int64 = 64 << 20
	CreditRefillLowWaterBytes int64 = 32 << 20
)

func queueCreditCandidates(tx *gorm.DB, observations []model.FleetGrantAuthority, previous map[string]model.FleetGrantAuthority, grants map[string]model.FleetGrant) error {
	var queued []model.FleetCreditCandidate
	var unchanged, removed []string
	var sequence int64
	now := time.Now().UnixMilli()
	for _, observed := range observations {
		grant := grants[observed.GrantID]
		sequence = observed.ReportSequence
		if grant.PlacementID == nil || grant.Unlimited || observed.QuotaUnlimited || !observed.AuthorizationActive || !observed.SpendableKnown || observed.SpendableBytes > CreditRefillLowWaterBytes || observed.CreditCeiling != grant.IssuedBytes {
			removed = append(removed, observed.GrantID)
			continue
		}
		old := previous[observed.GrantID]
		old.ReportSequence, old.ReceivedUnixMilli = observed.ReportSequence, observed.ReceivedUnixMilli
		if old == observed {
			unchanged = append(unchanged, observed.GrantID)
			continue
		}
		queued = append(queued, model.FleetCreditCandidate{GrantID: grant.ID, NodeID: grant.NodeID, AuthoritySequence: sequence, AvailableUnixMilli: now})
	}
	if len(removed) != 0 {
		if err := tx.Delete(&model.FleetCreditCandidate{}, "grant_id IN ?", removed).Error; err != nil {
			return err
		}
	}
	if len(unchanged) != 0 {
		if err := tx.Model(&model.FleetCreditCandidate{}).Where("grant_id IN ?", unchanged).Update("authority_sequence", sequence).Error; err != nil {
			return err
		}
	}
	if len(queued) == 0 {
		return nil
	}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "grant_id"}}, DoUpdates: clause.AssignmentColumns([]string{"authority_sequence"})}).CreateInBatches(queued, 256).Error; err != nil {
		return err
	}
	node := model.FleetCreditNodeQueue{NodeID: queued[0].NodeID, Pending: true, AvailableUnixMilli: now}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}}, DoUpdates: clause.Assignments(map[string]any{
		"pending": true, "available_unix_milli": gorm.Expr("CASE WHEN fleet_credit_node_queues.pending THEN LEAST(fleet_credit_node_queues.available_unix_milli, EXCLUDED.available_unix_milli) ELSE EXCLUDED.available_unix_milli END"),
	})}).Create(&node).Error
}

// Configuration reconciliation can wake eligible authority even when its rendered graph is unchanged.
func (s *Store) RequeueNodeCredit(ctx context.Context, nodeID, after string, limit int) (string, error) {
	if !validID(nodeID) || after != "" && !validID(after) || limit < 1 || limit > 128 {
		return "", ErrInvalid
	}
	var next string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		if err := lockNode(tx, nodeID, node.InstallationID); err != nil {
			return err
		}
		query := tx.Table("fleet_grants g").Select("g.*").
			Joins("JOIN fleet_clients c ON c.id = g.client_id AND c.current_term_id = g.term_id AND c.owned").
			Joins("JOIN fleet_terms t ON t.id = g.term_id AND t.state = 'active'").
			Joins("JOIN fleet_placements p ON p.id = g.placement_id AND p.current_grant_id = g.id AND p.current_grant_term_id = g.term_id AND p.enabled").
			Where("g.node_id = ? AND g.installation_id = ?", nodeID, node.InstallationID)
		if after != "" {
			query = query.Where("g.id > ?", after)
		}
		var grants []model.FleetGrant
		if err := query.Order("g.id").Limit(limit).Scan(&grants).Error; err != nil {
			return err
		}
		if len(grants) == 0 {
			return nil
		}
		ids := make([]string, len(grants))
		byID := make(map[string]model.FleetGrant, len(grants))
		for i, grant := range grants {
			ids[i], byID[grant.ID] = grant.ID, grant
		}
		var observations []model.FleetGrantAuthority
		if err := tx.Where("grant_id IN ?", ids).Order("grant_id").Find(&observations).Error; err != nil {
			return err
		}
		if err := queueCreditCandidates(tx, observations, nil, byID); err != nil {
			return err
		}
		if len(grants) == limit {
			next = grants[len(grants)-1].ID
		}
		return nil
	})
	return next, err
}

func (s *Store) ClaimCreditNodes(ctx context.Context, limit int) ([]model.FleetCreditNodeQueue, error) {
	if limit < 1 || limit > 4 {
		return nil, ErrInvalid
	}
	claim := uuid.NewString()
	now := time.Now().UnixMilli()
	var nodes []model.FleetCreditNodeQueue
	err := s.db.WithContext(ctx).Raw(`WITH due AS (
SELECT node_id FROM fleet_credit_node_queues WHERE pending AND available_unix_milli <= ? AND lease_until_unix_milli <= ?
ORDER BY available_unix_milli, node_id FOR UPDATE SKIP LOCKED LIMIT ?
) UPDATE fleet_credit_node_queues q SET claim_id = ?, lease_until_unix_milli = ?
FROM due WHERE q.node_id = due.node_id RETURNING q.*`, now, now, limit, claim, now+60_000).Scan(&nodes).Error
	return nodes, err
}

func (s *Store) DueCreditCandidates(ctx context.Context, lease model.FleetCreditNodeQueue) ([]model.FleetCreditCandidate, error) {
	if lease.ClaimID == nil || !validID(lease.NodeID) || !validID(*lease.ClaimID) {
		return nil, ErrInvalid
	}
	var candidates []model.FleetCreditCandidate
	now := time.Now().UnixMilli()
	err := s.db.WithContext(ctx).Table("fleet_credit_candidates c").Select("c.*").
		Joins("JOIN fleet_credit_node_queues q ON q.node_id = c.node_id").
		Where("c.node_id = ? AND q.claim_id = ? AND q.lease_until_unix_milli > ? AND c.available_unix_milli <= ?", lease.NodeID, *lease.ClaimID, now, now).
		Order("c.available_unix_milli, c.grant_id").Limit(8).Scan(&candidates).Error
	return candidates, err
}

func (s *Store) SettleCreditCandidate(ctx context.Context, lease model.FleetCreditNodeQueue, candidate model.FleetCreditCandidate, retry bool) error {
	if lease.ClaimID == nil || candidate.NodeID != lease.NodeID || !validID(candidate.GrantID) {
		return ErrInvalid
	}
	query := s.db.WithContext(ctx).Where(`grant_id = ? AND node_id = ? AND authority_sequence = ?
AND EXISTS (SELECT 1 FROM fleet_credit_node_queues q WHERE q.node_id = fleet_credit_candidates.node_id AND q.claim_id = ?)`, candidate.GrantID, lease.NodeID, candidate.AuthoritySequence, *lease.ClaimID)
	if !retry {
		return query.Delete(&model.FleetCreditCandidate{}).Error
	}
	attempts := min(candidate.Attempts+1, 5)
	delay := min(30, 1<<attempts)
	return query.Model(&model.FleetCreditCandidate{}).Updates(map[string]any{"attempts": attempts, "available_unix_milli": time.Now().UnixMilli() + int64(delay)*1000}).Error
}

func (s *Store) ReleaseCreditNode(ctx context.Context, lease model.FleetCreditNodeQueue) error {
	if lease.ClaimID == nil || !validID(lease.NodeID) || !validID(*lease.ClaimID) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.FleetCreditNodeQueue
		found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ? AND claim_id = ?", lease.NodeID, *lease.ClaimID).Find(&current)
		if found.Error != nil || found.RowsAffected == 0 {
			return found.Error
		}
		var remaining struct{ Earliest *int64 }
		if err := tx.Model(&model.FleetCreditCandidate{}).Select("MIN(available_unix_milli) AS earliest").Where("node_id = ?", lease.NodeID).Scan(&remaining).Error; err != nil {
			return err
		}
		available := time.Now().UnixMilli() + 250
		if remaining.Earliest != nil {
			available = max(available, *remaining.Earliest)
		}
		return tx.Model(&current).Updates(map[string]any{"pending": remaining.Earliest != nil, "available_unix_milli": available, "lease_until_unix_milli": 0, "claim_id": nil}).Error
	})
}
