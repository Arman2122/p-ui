package controlplane

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func holdNodeJournal(tx *gorm.DB, p identity.Principal, reason string) error {
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}}, DoUpdates: clause.Assignments(map[string]any{"recovery_required": true, "reason": gorm.Expr("CASE WHEN fleet_node_journals.recovery_required THEN fleet_node_journals.reason ELSE EXCLUDED.reason END")})}).Create(&model.FleetNodeJournal{NodeID: p.NodeID, InstallationID: p.InstallationID, RecoveryRequired: true, Reason: reason}).Error
}

func anyRuntimeAuthority(tx *gorm.DB, p identity.Principal, runtime string) (bool, error) {
	query := tx.Model(&model.FleetGrant{}).Where("node_id = ?", p.NodeID)
	if runtime != "" {
		query = query.Where("runtime_id = ?", runtime)
	}
	var grant model.FleetGrant
	err := query.Select("id").Take(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) RegisterJournals(ctx context.Context, p identity.Principal, request *nodev1.JournalRegistration) (*nodev1.JournalRegistrationReceipt, error) {
	request, digest, err := authority.NormalizeRegistration(request)
	if err != nil {
		return nil, ErrInvalid
	}
	if p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, ErrScope
	}
	held := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		var node model.FleetNodeJournal
		result := tx.Find(&node, "node_id = ?", p.NodeID)
		if result.Error != nil {
			return result.Error
		}
		if node.RecoveryRequired {
			held = true
			return nil
		}
		if result.RowsAffected == 0 {
			issued, err := anyRuntimeAuthority(tx, p, "")
			if err != nil {
				return err
			}
			if issued {
				held = true
				return holdNodeJournal(tx, p, "NODE_JOURNAL_HISTORY_MISSING")
			}
			node = model.FleetNodeJournal{NodeID: p.NodeID, InstallationID: p.InstallationID, StateJournalID: &request.AgentStateJournalId}
		} else if node.InstallationID != p.InstallationID || node.StateJournalID == nil || *node.StateJournalID != request.AgentStateJournalId {
			held = true
			return holdNodeJournal(tx, p, "NODE_STATE_JOURNAL_REPLACED")
		}
		var saved []model.FleetRuntimeJournal
		if err := tx.Where("node_id = ?", p.NodeID).Find(&saved).Error; err != nil {
			return err
		}
		byID := make(map[string]model.FleetRuntimeJournal, len(saved))
		for _, runtime := range saved {
			byID[runtime.RuntimeID] = runtime
		}
		var added []model.FleetRuntimeJournal
		for _, runtime := range request.Runtimes {
			previous, exists := byID[runtime.RuntimeId]
			if exists {
				if previous.InstallationID != p.InstallationID || previous.CoreID != runtime.CoreId || previous.StateJournalID != runtime.StateJournalId || previous.CreditJournalID != runtime.CreditJournalId {
					held = true
					return holdNodeJournal(tx, p, "RUNTIME_JOURNAL_REPLACED")
				}
				continue
			}
			if len(saved)+len(added) >= 32 {
				return ErrInvalid
			}
			issued, err := anyRuntimeAuthority(tx, p, runtime.RuntimeId)
			if err != nil {
				return err
			}
			if issued {
				held = true
				return holdNodeJournal(tx, p, "RUNTIME_JOURNAL_HISTORY_MISSING")
			}
			added = append(added, model.FleetRuntimeJournal{NodeID: p.NodeID, InstallationID: p.InstallationID, RuntimeID: runtime.RuntimeId, CoreID: runtime.CoreId, StateJournalID: runtime.StateJournalId, CreditJournalID: runtime.CreditJournalId})
		}
		if len(saved)+len(added) > 32 {
			return ErrInvalid
		}
		if result.RowsAffected == 0 {
			if err := tx.Create(&node).Error; err != nil {
				return err
			}
		}
		if len(added) != 0 {
			return tx.Create(&added).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrRecovery
	}
	return &nodev1.JournalRegistrationReceipt{AgentStateJournalId: request.AgentStateJournalId, RegistrationSha256: digest}, nil
}

func requireNodeJournalTx(tx *gorm.DB, nodeID, installationID, agentJournal string) error {
	var node model.FleetNodeJournal
	if err := tx.First(&node, "node_id = ?", nodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrScope
		}
		return err
	}
	if node.RecoveryRequired || node.InstallationID != installationID || node.StateJournalID == nil {
		return ErrRecovery
	}
	if agentJournal != "" && *node.StateJournalID != agentJournal {
		return ErrRecovery
	}
	return nil
}

// Caller holds the node row lock so a recovery hold cannot race funding.
func RequireRuntimeJournalTx(tx *gorm.DB, nodeID, installationID, runtimeID string) error {
	if err := requireNodeJournalTx(tx, nodeID, installationID, ""); err != nil {
		return err
	}
	var runtime model.FleetRuntimeJournal
	if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", nodeID, runtimeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrScope
		}
		return err
	}
	if runtime.InstallationID != installationID {
		return ErrRecovery
	}
	return nil
}

func (s *Store) RequireAgentJournal(ctx context.Context, p identity.Principal, journalID string) error {
	if !authority.ValidID(journalID) {
		return ErrInvalid
	}
	held := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, journalID)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrRecovery) {
			held = true
			return holdNodeJournal(tx, p, "NODE_STATE_JOURNAL_REPLACED")
		}
		if !errors.Is(err, ErrScope) {
			return err
		}
		issued, err := anyRuntimeAuthority(tx, p, "")
		if err != nil {
			return err
		}
		if !issued {
			return ErrScope
		}
		held = true
		return holdNodeJournal(tx, p, "NODE_JOURNAL_HISTORY_MISSING")
	})
	if err == nil && held {
		return ErrRecovery
	}
	return err
}
