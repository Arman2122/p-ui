package controlplane

import (
	"context"
	"errors"

	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

var errAuthorityWitness = errors.New("bound runtime authority is ahead of master")

func (s *Store) holdAuthorityRollback(ctx context.Context, p identity.Principal, report *nodev1.AuthorityReport, validatedRateWitness bool) error {
	if authority.ValidateReport(report, true) != nil || p.Require(report.NodeId, report.InstallationId) != nil {
		return ErrScope
	}
	held := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, true); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, report.AgentStateJournalId); err != nil {
			return err
		}
		var journal model.FleetRuntimeJournal
		if err := tx.First(&journal, "node_id = ? AND runtime_id = ?", p.NodeID, report.RuntimeId).Error; err != nil {
			return err
		}
		if journal.StateJournalID != report.StateJournalId || journal.CreditJournalID != report.CreditJournalId || journal.InstallationID != p.InstallationID {
			return ErrScope
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		ids := make([]string, len(report.Grants))
		var rateIDs []string
		for i, observed := range report.Grants {
			ids[i] = observed.GrantId
			if observed.AuthorityRevision != 0 {
				rateIDs = append(rateIDs, observed.GrantId)
			}
		}
		var grants []model.FleetGrant
		if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", ids, p.NodeID, p.InstallationID, report.RuntimeId).Find(&grants).Error; err != nil {
			return err
		}
		if len(grants) != len(ids) {
			return ErrScope
		}
		var shares []model.FleetRateShare
		if len(rateIDs) != 0 {
			if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", rateIDs, p.NodeID, p.InstallationID, report.RuntimeId).Find(&shares).Error; err != nil {
				return err
			}
		}
		byGrant := make(map[string]model.FleetGrant, len(grants))
		for _, grant := range grants {
			byGrant[grant.ID] = grant
		}
		byShare := make(map[string]model.FleetRateShare, len(shares))
		var budgetIDs []string
		for _, share := range shares {
			byShare[share.ID] = share
			budgetIDs = append(budgetIDs, share.BudgetID)
		}
		owners := make(map[string]string, len(budgetIDs))
		if len(budgetIDs) != 0 {
			var budgets []model.FleetRateBudget
			if err := tx.Select("id,client_id").Where("id IN ?", budgetIDs).Find(&budgets).Error; err != nil {
				return err
			}
			for _, budget := range budgets {
				owners[budget.ID] = budget.ClientID
			}
		}
		var previous []model.FleetGrantAuthority
		if err := tx.Where("grant_id IN ?", ids).Find(&previous).Error; err != nil {
			return err
		}
		byPrevious := make(map[string]model.FleetGrantAuthority, len(previous))
		for _, old := range previous {
			byPrevious[old.GrantID] = old
		}
		creditAhead, rateAhead := false, validatedRateWitness
		for _, observed := range report.Grants {
			grant, old := byGrant[observed.GrantId], byPrevious[observed.GrantId]
			if grant.ClientID != observed.ClientId || grant.TermID != observed.TermId || grant.Unlimited != observed.QuotaUnlimited {
				return ErrScope
			}
			rowCredit, rowRate := observed.CreditCeiling > uint64(grant.IssuedBytes), false
			if observed.AuthorityRevision != 0 {
				share, exists := byShare[grant.ID]
				if !exists || owners[share.BudgetID] != grant.ClientID {
					return ErrScope
				}
				rowRate = observed.AuthorityRevision > uint64(share.Revision)
			}
			// A newly reused revision has no policy hash proving it matches the already validated node witness.
			if !validatedRateWitness && (observed.AuthorityRevision < uint64(old.AuthorityRevision) || observed.PolicyRevision < uint64(old.PolicyRevision) || observed.CreditCeiling < uint64(old.CreditCeiling) || observed.ReservedHighWater < uint64(old.ReservedHighWater) || observed.BurnedHighWater < uint64(old.BurnedHighWater) || observed.AdmissionKnown && observed.AdmissionHighWater < uint64(old.AdmissionHighWater)) {
				return ErrConflict
			}
			if !validatedRateWitness && !rowCredit && !rowRate && (!grant.Unlimited && observed.ReservedHighWater > uint64(grant.IssuedBytes) || observed.PolicyRevision > uint64(max(node.AppliedRevision, node.SentRevision))) {
				return ErrScope
			}
			creditAhead, rateAhead = creditAhead || rowCredit, rateAhead || rowRate
		}
		reason := "NODE_RATE_HIGH_WATER_AHEAD"
		if creditAhead {
			reason = "NODE_CREDIT_HIGH_WATER_AHEAD"
		}
		if !creditAhead && !rateAhead {
			return nil
		}
		held = true
		return tx.Model(&model.FleetFence{}).Where("id = 1").Updates(map[string]any{"recovery_required": true, "reason": reason, "witness_node_id": p.NodeID}).Error
	})
	if err != nil {
		return err
	}
	if held {
		return ErrRecovery
	}
	return ErrScope
}
