package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (s *Store) IngestAuthority(ctx context.Context, p identity.Principal, report *nodev1.AuthorityReport) (*nodev1.AuthorityReceipt, error) {
	if authority.ValidateReport(report, true) != nil {
		return nil, ErrInvalid
	}
	if p.Require(report.NodeId, report.InstallationId) != nil {
		return nil, ErrScope
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(report)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 1<<20 {
		return nil, ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	held := false
	rateWitness := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, report.AgentStateJournalId); err != nil {
			if errors.Is(err, ErrRecovery) {
				held = true
				return holdNodeJournal(tx, p, "NODE_STATE_JOURNAL_REPLACED")
			}
			return err
		}
		var journal model.FleetRuntimeJournal
		if err := tx.First(&journal, "node_id = ? AND runtime_id = ?", p.NodeID, report.RuntimeId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrScope
			}
			return err
		}
		if journal.InstallationID != p.InstallationID || journal.StateJournalID != report.StateJournalId || journal.CreditJournalID != report.CreditJournalId {
			held = true
			return holdNodeJournal(tx, p, "RUNTIME_JOURNAL_REPLACED")
		}
		if report.Sequence == uint64(journal.Sequence) {
			if !bytes.Equal(journal.Digest, digest[:]) {
				return ErrConflict
			}
			return nil
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		ids := make([]string, len(report.Grants))
		var rateIDs []string
		for i, grant := range report.Grants {
			ids[i] = grant.GrantId
			if grant.AuthorityRevision != 0 {
				rateIDs = append(rateIDs, grant.GrantId)
			}
		}
		var grants []model.FleetGrant
		var previous []model.FleetGrantAuthority
		var shares []model.FleetRateShare
		if len(ids) != 0 {
			if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", ids, p.NodeID, p.InstallationID, report.RuntimeId).Find(&grants).Error; err != nil {
				return err
			}
			if len(grants) != len(ids) {
				return ErrScope
			}
			if err := tx.Where("grant_id IN ?", ids).Find(&previous).Error; err != nil {
				return err
			}
		}
		if len(rateIDs) != 0 {
			if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", rateIDs, p.NodeID, p.InstallationID, report.RuntimeId).Find(&shares).Error; err != nil {
				return err
			}
		}
		byGrant := make(map[string]model.FleetGrant, len(grants))
		for _, grant := range grants {
			byGrant[grant.ID] = grant
		}
		byPrevious := make(map[string]model.FleetGrantAuthority, len(previous))
		byShare := make(map[string]model.FleetRateShare, len(shares))
		var rateBudgets []string
		for _, share := range shares {
			byShare[share.ID] = share
			rateBudgets = append(rateBudgets, share.BudgetID)
		}
		budgetClients := make(map[string]string, len(rateBudgets))
		if len(rateBudgets) != 0 {
			var budgets []model.FleetRateBudget
			if err := tx.Select("id,client_id").Where("id IN ?", rateBudgets).Find(&budgets).Error; err != nil {
				return err
			}
			for _, budget := range budgets {
				budgetClients[budget.ID] = budget.ClientID
			}
		}
		for _, observation := range previous {
			byPrevious[observation.GrantID] = observation
		}
		now := time.Now().UnixMilli()
		observations := make([]model.FleetGrantAuthority, 0, len(report.Grants))
		ahead := false
		for _, observed := range report.Grants {
			grant := byGrant[observed.GrantId]
			if grant.ClientID != observed.ClientId || grant.TermID != observed.TermId || grant.Unlimited != observed.QuotaUnlimited {
				return ErrScope
			}
			rowAhead := observed.CreditCeiling > uint64(grant.IssuedBytes)
			old := byPrevious[observed.GrantId]
			if observed.AuthorityRevision != 0 {
				share, exists := byShare[grant.ID]
				if !exists || budgetClients[share.BudgetID] != grant.ClientID {
					return ErrScope
				}
				if observed.AuthorityRevision > uint64(share.Revision) {
					rowAhead = true
					rateWitness = true
				}
			}
			if observed.AuthorityRevision < uint64(old.AuthorityRevision) || observed.PolicyRevision < uint64(old.PolicyRevision) || observed.CreditCeiling < uint64(old.CreditCeiling) || observed.ReservedHighWater < uint64(old.ReservedHighWater) || observed.BurnedHighWater < uint64(old.BurnedHighWater) || observed.AdmissionKnown && observed.AdmissionHighWater < uint64(old.AdmissionHighWater) {
				return ErrConflict
			}
			if rowAhead {
				ahead = true
				continue
			}
			if !grant.Unlimited && observed.ReservedHighWater > uint64(grant.IssuedBytes) || observed.PolicyRevision > uint64(max(node.AppliedRevision, node.SentRevision)) {
				return ErrScope
			}
			admitted := old.AdmissionHighWater
			if observed.AdmissionKnown {
				admitted = int64(observed.AdmissionHighWater)
			}
			observations = append(observations, model.FleetGrantAuthority{GrantID: grant.ID, NodeID: p.NodeID, RuntimeID: report.RuntimeId, ReportSequence: int64(report.Sequence), PolicyRevision: int64(observed.PolicyRevision), AuthorityRevision: int64(observed.AuthorityRevision), CreditCeiling: int64(observed.CreditCeiling), QuotaUnlimited: observed.QuotaUnlimited, ReservedHighWater: int64(observed.ReservedHighWater), BurnedHighWater: int64(observed.BurnedHighWater), AdmissionHighWater: admitted, AdmissionKnown: observed.AdmissionKnown, SpendableBytes: int64(observed.SpendableBytes), SpendableKnown: observed.SpendableKnown, AuthorizationActive: observed.AuthorizationActive, ReceivedUnixMilli: now})
		}
		if ahead {
			return errAuthorityWitness
		}
		if report.Sequence != uint64(journal.Sequence)+1 {
			return ErrSequence
		}
		if len(observations) != 0 {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(observations, 256).Error; err != nil {
				return err
			}
			if err := queueCreditCandidates(tx, observations, byPrevious, byGrant); err != nil {
				return err
			}
		}
		return tx.Model(&model.FleetRuntimeJournal{}).Where("node_id = ? AND runtime_id = ?", p.NodeID, report.RuntimeId).Updates(map[string]any{"sequence": int64(report.Sequence), "digest": digest[:], "ready": report.RuntimeReady, "received_unix_milli": now, "observed_unix_milli": report.ObservedUnixMilli}).Error
	})
	if errors.Is(err, errAuthorityWitness) {
		return nil, s.holdAuthorityRollback(ctx, p, report, rateWitness)
	}
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrRecovery
	}
	return &nodev1.AuthorityReceipt{RuntimeId: report.RuntimeId, StateJournalId: report.StateJournalId, CreditJournalId: report.CreditJournalId, Sequence: report.Sequence}, nil
}
