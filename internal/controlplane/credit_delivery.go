package controlplane

import (
	"context"
	"errors"
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type CreditDeliveryValidator func(context.Context, *gorm.DB, []model.FleetCreditUpdate) (map[string]bool, error)

var errCreditJournal = errors.New("credit runtime journal changed")

func (s *Store) QueueCreditUpdate(ctx context.Context, grantID string, baseRevision uint64) error {
	if !validID(grantID) || baseRevision == 0 || baseRevision > math.MaxInt64 {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ?", grantID).Error; err != nil {
			return err
		}
		if err := lockNode(tx, grant.NodeID, grant.InstallationID); err != nil {
			return err
		}
		if err := tx.First(&grant, "id = ?", grantID).Error; err != nil {
			return err
		}
		if grant.Unlimited || grant.PlacementID == nil {
			return ErrScope
		}
		if _, err := managedAuthority(tx, grant.ClientID, grant.TermID, true); err != nil {
			return err
		}
		if err := RequireRuntimeJournalTx(tx, grant.NodeID, grant.InstallationID, grant.RuntimeID); err != nil {
			return err
		}
		var node model.FleetNode
		var runtime model.FleetRuntimeJournal
		if err := tx.First(&node, "id = ?", grant.NodeID).Error; err != nil {
			return err
		}
		if node.AppliedRevision != int64(baseRevision) || node.DesiredRevision != node.AppliedRevision {
			return ErrRevision
		}
		if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", grant.NodeID, grant.RuntimeID).Error; err != nil {
			return err
		}
		incoming := model.FleetCreditUpdate{
			GrantID: grant.ID, NodeID: grant.NodeID, InstallationID: grant.InstallationID,
			RuntimeID: grant.RuntimeID, StateJournalID: runtime.StateJournalID, CreditJournalID: runtime.CreditJournalID,
			ClientID: grant.ClientID, TermID: grant.TermID, BaseConfigurationRevision: int64(baseRevision), Revision: grant.Revision, CreditCeiling: grant.IssuedBytes,
		}
		var previous model.FleetCreditUpdate
		found := tx.Find(&previous, "grant_id = ?", grant.ID)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected != 0 {
			if previous.Revision > incoming.Revision || previous.CreditCeiling > incoming.CreditCeiling || previous.BaseConfigurationRevision > incoming.BaseConfigurationRevision {
				return ErrRecovery
			}
			if previous.BaseConfigurationRevision == incoming.BaseConfigurationRevision {
				incoming.AcknowledgedRevision, incoming.AcknowledgedCeiling = previous.AcknowledgedRevision, previous.AcknowledgedCeiling
			}
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&incoming).Error
	})
}

func creditScope(tx *gorm.DB, p identity.Principal, request *nodev1.CreditDeliveryRequest) (model.FleetNode, error) {
	var node model.FleetNode
	if err := lockAuthority(tx, false); err != nil {
		return node, err
	}
	if err := lockAuthenticatedNode(tx, p); err != nil {
		return node, err
	}
	if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, request.AgentStateJournalId); err != nil {
		return node, err
	}
	var runtime model.FleetRuntimeJournal
	if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", p.NodeID, request.RuntimeId).Error; err != nil {
		return node, err
	}
	if runtime.InstallationID != p.InstallationID || runtime.StateJournalID != request.StateJournalId || runtime.CreditJournalID != request.CreditJournalId {
		return node, errCreditJournal
	}
	err := tx.First(&node, "id = ?", p.NodeID).Error
	return node, err
}

func (s *Store) FetchCredit(ctx context.Context, p identity.Principal, request *nodev1.CreditDeliveryRequest, validators ...CreditDeliveryValidator) (*nodev1.CreditUpdateBatch, error) {
	if len(validators) > 1 {
		return nil, ErrInvalid
	}
	if authority.ValidateCreditRequest(request) != nil || p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, ErrScope
	}
	if err := s.RequireAgentJournal(ctx, p, request.AgentStateJournalId); err != nil {
		return nil, err
	}
	result := &nodev1.CreditUpdateBatch{
		NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId,
		RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId,
	}
	held := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, err := creditScope(tx, p, request)
		if errors.Is(err, errCreditJournal) {
			held = true
			return holdNodeJournal(tx, p, "CREDIT_RUNTIME_JOURNAL_REPLACED")
		}
		if err != nil {
			return err
		}
		if node.AppliedRevision == 0 || node.AppliedRevision != node.DesiredRevision {
			return nil
		}
		var ready bool
		if err := tx.Model(&model.FleetRuntimeJournal{}).Select("ready").Where("node_id = ? AND runtime_id = ?", p.NodeID, request.RuntimeId).Scan(&ready).Error; err != nil {
			return err
		}
		if !ready {
			return nil
		}
		var updates []model.FleetCreditUpdate
		if err := tx.Where("node_id = ? AND installation_id = ? AND runtime_id = ? AND base_configuration_revision = ? AND revision > acknowledged_revision",
			p.NodeID, p.InstallationID, request.RuntimeId, node.AppliedRevision).Order("grant_id").Limit(32).Find(&updates).Error; err != nil {
			return err
		}
		if len(updates) == 0 {
			return nil
		}
		if len(validators) == 0 || validators[0] == nil {
			return ErrScope
		}
		allowed, err := validators[0](ctx, tx, updates)
		if err != nil {
			return err
		}
		var discarded []string
		for _, update := range updates {
			if !allowed[update.GrantID] {
				discarded = append(discarded, update.GrantID)
				continue
			}
			if update.StateJournalID != request.StateJournalId || update.CreditJournalID != request.CreditJournalId {
				return ErrRecovery
			}
			result.Updates = append(result.Updates, &nodev1.CreditUpdate{
				GrantId: update.GrantID, ClientId: update.ClientID, TermId: update.TermID,
				CreditRevision: uint64(update.Revision), CreditCeiling: uint64(update.CreditCeiling),
			})
		}
		if len(result.Updates) != 0 {
			result.BaseConfigurationRevision = uint64(node.AppliedRevision)
		}
		if len(discarded) != 0 {
			return tx.Delete(&model.FleetCreditUpdate{}, "grant_id IN ?", discarded).Error
		}
		return nil
	})
	if err == nil && held {
		return nil, ErrRecovery
	}
	return result, err
}

func (s *Store) AcknowledgeCredit(ctx context.Context, p identity.Principal, batch *nodev1.CreditUpdateBatch) (*nodev1.CreditUpdateReceipt, error) {
	if authority.ValidateCreditBatch(batch, false) != nil {
		return nil, ErrInvalid
	}
	request := &nodev1.CreditDeliveryRequest{
		NodeId: batch.NodeId, InstallationId: batch.InstallationId, AgentStateJournalId: batch.AgentStateJournalId,
		RuntimeId: batch.RuntimeId, StateJournalId: batch.StateJournalId, CreditJournalId: batch.CreditJournalId,
	}
	if p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, ErrScope
	}
	digest, err := authority.CreditDigest(batch)
	if err != nil {
		return nil, err
	}
	if err := s.RequireAgentJournal(ctx, p, batch.AgentStateJournalId); err != nil {
		return nil, err
	}
	held := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, err := creditScope(tx, p, request)
		if errors.Is(err, errCreditJournal) {
			held = true
			return holdNodeJournal(tx, p, "CREDIT_RUNTIME_JOURNAL_REPLACED")
		}
		if err != nil {
			return err
		}
		if node.AppliedRevision != int64(batch.BaseConfigurationRevision) || node.DesiredRevision != node.AppliedRevision {
			return ErrRevision
		}
		for _, update := range batch.Updates {
			var row model.FleetCreditUpdate
			if err := tx.First(&row, "grant_id = ?", update.GrantId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrRevision
				}
				return err
			}
			if row.NodeID != p.NodeID || row.InstallationID != p.InstallationID || row.RuntimeID != batch.RuntimeId || row.ClientID != update.ClientId || row.TermID != update.TermId ||
				row.StateJournalID != batch.StateJournalId || row.CreditJournalID != batch.CreditJournalId || row.BaseConfigurationRevision != int64(batch.BaseConfigurationRevision) {
				return ErrScope
			}
			if row.AcknowledgedRevision == int64(update.CreditRevision) && row.AcknowledgedCeiling == int64(update.CreditCeiling) {
				continue
			}
			if row.Revision != int64(update.CreditRevision) || row.CreditCeiling != int64(update.CreditCeiling) {
				return ErrRevision
			}
			if err := tx.Model(&row).Updates(map[string]any{"acknowledged_revision": row.Revision, "acknowledged_ceiling": row.CreditCeiling}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrRecovery
	}
	return &nodev1.CreditUpdateReceipt{PayloadSha256: digest}, nil
}
