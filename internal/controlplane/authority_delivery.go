package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type AuthorityDeliveryValidator func(context.Context, *gorm.DB, []model.FleetAuthorityUpdate) (map[string]bool, error)

var ErrCreditDeliveryRequired = errors.New("quota-only changes require credit delivery")

func authorityRate(value limits.Rate) *nodev1.AuthorityRate {
	return &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode(value.Mode), BitsPerSecond: value.BitsPerSecond, BurstBytes: uint32(value.BurstBytes)}
}

func authorityRates(update *nodev1.AuthorityUpdate) Rates {
	return Rates{
		Upload:   limits.Rate{Mode: limits.Mode(update.Upload.Mode), BitsPerSecond: update.Upload.BitsPerSecond, BurstBytes: int(update.Upload.BurstBytes)},
		Download: limits.Rate{Mode: limits.Mode(update.Download.Mode), BitsPerSecond: update.Download.BitsPerSecond, BurstBytes: int(update.Download.BurstBytes)},
	}
}

func decodeAuthorityUpdate(row model.FleetAuthorityUpdate) (*nodev1.AuthorityUpdate, error) {
	digest := sha256.Sum256(row.Payload)
	if len(row.Payload) == 0 || len(row.Payload) > 4096 || !bytes.Equal(digest[:], row.PayloadSHA256) {
		return nil, ErrRecovery
	}
	update := new(nodev1.AuthorityUpdate)
	if err := proto.Unmarshal(row.Payload, update); err != nil {
		return nil, ErrRecovery
	}
	if update.GrantId != row.GrantID || update.ClientId != row.ClientID || update.TermId != row.TermID || update.PlacementId != row.PlacementID ||
		update.AuthorityRevision != uint64(row.Revision) || !bytes.Equal(update.DescriptorSha256, row.DescriptorSHA256) {
		return nil, ErrRecovery
	}
	batch := authorityBatch(row)
	batch.Updates = []*nodev1.AuthorityUpdate{update}
	if authority.ValidateAuthorityBatch(batch, false) != nil {
		return nil, ErrRecovery
	}
	return update, nil
}

func authorityBatch(row model.FleetAuthorityUpdate) *nodev1.AuthorityUpdateBatch {
	return &nodev1.AuthorityUpdateBatch{
		NodeId: row.NodeID, InstallationId: row.InstallationID, AgentStateJournalId: row.AgentStateJournalID,
		RuntimeId: row.RuntimeID, StateJournalId: row.StateJournalID, CreditJournalId: row.CreditJournalID,
		BaseConfigurationRevision: uint64(row.BaseConfigurationRevision), BasePayloadSha256: bytes.Clone(row.BasePayloadSHA256),
	}
}

func sameAuthorityScope(row model.FleetAuthorityUpdate, batch *nodev1.AuthorityUpdateBatch) bool {
	return row.NodeID == batch.NodeId && row.InstallationID == batch.InstallationId && row.AgentStateJournalID == batch.AgentStateJournalId &&
		row.RuntimeID == batch.RuntimeId && row.StateJournalID == batch.StateJournalId && row.CreditJournalID == batch.CreditJournalId &&
		row.BaseConfigurationRevision == int64(batch.BaseConfigurationRevision) && bytes.Equal(row.BasePayloadSHA256, batch.BasePayloadSha256)
}

// The caller reserves quota and rate authority in the same transaction before queuing its delivery.
func (s *Store) QueueAuthorityUpdate(ctx context.Context, grantID string, baseRevision uint64, enabled bool) error {
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
		if grant.PlacementID == nil {
			return ErrScope
		}
		if err := RequireRuntimeJournalTx(tx, grant.NodeID, grant.InstallationID, grant.RuntimeID); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", grant.NodeID).Error; err != nil {
			return err
		}
		if node.AppliedRevision != int64(baseRevision) || node.DesiredRevision != node.AppliedRevision {
			return ErrRevision
		}
		term, err := managedAuthority(tx, grant.ClientID, grant.TermID, enabled)
		if err != nil {
			return err
		}
		if term == nil || enabled && term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= time.Now().UnixMilli() {
			return ErrScope
		}
		scope, err := publishedAuthorityMetadataTx(tx, grant.NodeID, baseRevision, grant.RuntimeID, grant.ClientID)
		if err != nil {
			return err
		}
		if !admission.SupportsRuntime(scope.CoreID) || scope.Descriptor.PlacementID != *grant.PlacementID {
			return ErrScope
		}
		var pool model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pool, "id = ? AND client_id = ?", grant.ClientID, grant.ClientID).Error; err != nil {
			return err
		}
		var share model.FleetRateShare
		if err := tx.First(&share, "id = ?", grant.ID).Error; err != nil {
			return err
		}
		if share.NodeID != grant.NodeID || share.InstallationID != grant.InstallationID || share.RuntimeID != grant.RuntimeID || share.BudgetID != pool.ID {
			return ErrScope
		}
		rates, err := decodeRates(share.Desired)
		if err != nil {
			return err
		}
		if !enabled && rates != (Rates{}) {
			return ErrScope
		}
		var agent model.FleetNodeJournal
		var runtime model.FleetRuntimeJournal
		if err := tx.First(&agent, "node_id = ?", grant.NodeID).Error; err != nil {
			return err
		}
		if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", grant.NodeID, grant.RuntimeID).Error; err != nil {
			return err
		}
		if agent.StateJournalID == nil || runtime.CoreID != scope.CoreID {
			return ErrScope
		}
		update := &nodev1.AuthorityUpdate{
			GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.TermID, AuthorityRevision: uint64(share.Revision),
			CreditRevision: uint64(grant.Revision), CreditCeiling: uint64(grant.IssuedBytes), QuotaUnlimited: grant.Unlimited, Enabled: enabled, ExpiresUnixMilli: term.ExpiresUnixMilli,
			Upload: authorityRate(rates.Upload), Download: authorityRate(rates.Download), CredentialGeneration: scope.Descriptor.CredentialGeneration,
			PlacementId: scope.Descriptor.PlacementID, PlacementRevision: scope.Descriptor.PlacementRevision, DescriptorSha256: scope.Descriptor.SHA256,
		}
		if base := scope.BaseGrant; base != nil {
			if base.ID != grant.ID || base.ClientID != grant.ClientID || base.Policy.Term != grant.TermID || base.Policy.QuotaUnlimited != grant.Unlimited ||
				base.Policy.ExpiresUnixMilli != update.ExpiresUnixMilli || enabled && !base.Policy.Enabled {
				return ErrScope
			}
			if update.AuthorityRevision < base.Policy.AuthorityRevision || update.CreditRevision < base.Policy.CreditRevision || update.CreditCeiling < base.Policy.CreditCeiling {
				return ErrRecovery
			}
			if update.AuthorityRevision == base.Policy.AuthorityRevision {
				if rates.Upload != base.Policy.Upload || rates.Download != base.Policy.Download || enabled != base.Policy.Enabled {
					return ErrRevision
				}
				if update.CreditRevision != base.Policy.CreditRevision || update.CreditCeiling != base.Policy.CreditCeiling {
					return ErrCreditDeliveryRequired
				}
				update.CreditRevision, update.CreditCeiling = base.Policy.CreditRevision, base.Policy.CreditCeiling
			}
		}
		var conflict model.FleetAuthorityUpdate
		foundOther := tx.Select("grant_id").Where("node_id = ? AND runtime_id = ? AND base_configuration_revision = ? AND client_id = ? AND grant_id <> ?", grant.NodeID, grant.RuntimeID, baseRevision, grant.ClientID, grant.ID).Find(&conflict)
		if foundOther.Error != nil {
			return foundOther.Error
		}
		if foundOther.RowsAffected != 0 {
			return ErrScope
		}
		incoming := model.FleetAuthorityUpdate{
			GrantID: grant.ID, NodeID: grant.NodeID, InstallationID: grant.InstallationID, RuntimeID: grant.RuntimeID,
			AgentStateJournalID: *agent.StateJournalID, StateJournalID: runtime.StateJournalID, CreditJournalID: runtime.CreditJournalID, ClientID: grant.ClientID, TermID: grant.TermID,
			PlacementID: *grant.PlacementID, BaseConfigurationRevision: int64(baseRevision), BasePayloadSHA256: scope.PayloadSHA256, DescriptorSHA256: scope.Descriptor.SHA256, Revision: share.Revision,
		}
		batch := authorityBatch(incoming)
		batch.Updates = []*nodev1.AuthorityUpdate{update}
		if authority.ValidateAuthorityBatch(batch, false) != nil {
			return ErrInvalid
		}
		incoming.Payload, err = proto.MarshalOptions{Deterministic: true}.Marshal(update)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(incoming.Payload)
		incoming.PayloadSHA256 = digest[:]
		var previous model.FleetAuthorityUpdate
		found := tx.Find(&previous, "grant_id = ?", grant.ID)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected != 0 {
			if previous.BaseConfigurationRevision > incoming.BaseConfigurationRevision || previous.Revision > incoming.Revision {
				return ErrRecovery
			}
			if sameAuthorityScope(previous, batch) && enabled {
				old, err := decodeAuthorityUpdate(previous)
				if err != nil {
					return err
				}
				if !old.Enabled {
					return ErrScope
				}
			}
			if sameAuthorityScope(previous, batch) && previous.Revision == incoming.Revision {
				old, err := decodeAuthorityUpdate(previous)
				if err != nil {
					return err
				}
				if old.CreditRevision > update.CreditRevision || old.CreditCeiling > update.CreditCeiling {
					return ErrRecovery
				}
				if old.CreditRevision != update.CreditRevision || old.CreditCeiling != update.CreditCeiling {
					return ErrCreditDeliveryRequired
				}
				update.CreditRevision, update.CreditCeiling = old.CreditRevision, old.CreditCeiling
				if !proto.Equal(old, update) {
					return ErrConflict
				}
				return tx.Model(&previous).Update("delivery_blocked", false).Error
			}
			if sameAuthorityScope(previous, batch) {
				incoming.AcknowledgedRevision, incoming.AcknowledgedSHA256 = previous.AcknowledgedRevision, previous.AcknowledgedSHA256
			}
		}
		if enabled {
			if err := tx.Where("grant_id = ?", grant.ID).Delete(&model.FleetGrantStop{}).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&incoming).Error
	})
}

func authorityDeliveryScope(tx *gorm.DB, p identity.Principal, request *nodev1.AuthorityDeliveryRequest) (model.FleetRuntimeJournal, error) {
	credit := &nodev1.CreditDeliveryRequest{
		NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId,
		RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId,
	}
	node, err := creditScope(tx, p, credit)
	if err != nil {
		return model.FleetRuntimeJournal{}, err
	}
	if node.AppliedRevision != int64(request.BaseConfigurationRevision) || node.AppliedRevision != node.DesiredRevision {
		return model.FleetRuntimeJournal{}, ErrRevision
	}
	scope, err := publishedAuthorityMetadataTx(tx, p.NodeID, request.BaseConfigurationRevision, request.RuntimeId, "")
	if err != nil {
		return model.FleetRuntimeJournal{}, err
	}
	if !admission.SupportsRuntime(scope.CoreID) || !bytes.Equal(scope.PayloadSHA256, request.BasePayloadSha256) {
		return model.FleetRuntimeJournal{}, ErrScope
	}
	var runtime model.FleetRuntimeJournal
	err = tx.First(&runtime, "node_id = ? AND runtime_id = ?", p.NodeID, request.RuntimeId).Error
	return runtime, err
}

func (s *Store) FetchAuthorityUpdates(ctx context.Context, p identity.Principal, request *nodev1.AuthorityDeliveryRequest, authorize AuthorityDeliveryValidator) (*nodev1.AuthorityUpdateBatch, error) {
	if authority.ValidateAuthorityRequest(request) != nil || p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, ErrScope
	}
	if err := s.RequireAgentJournal(ctx, p, request.AgentStateJournalId); err != nil {
		return nil, err
	}
	result := &nodev1.AuthorityUpdateBatch{
		NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId,
		RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId, BaseConfigurationRevision: request.BaseConfigurationRevision, BasePayloadSha256: bytes.Clone(request.BasePayloadSha256),
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		runtime, err := authorityDeliveryScope(tx, p, request)
		if err != nil {
			return err
		}
		if !runtime.Ready || runtime.ReceivedUnixMilli <= 0 || runtime.ReceivedUnixMilli < time.Now().Add(-30*time.Second).UnixMilli() {
			return nil
		}
		var rows []model.FleetAuthorityUpdate
		if err := tx.Where("node_id = ? AND installation_id = ? AND runtime_id = ? AND base_configuration_revision = ? AND revision > acknowledged_revision AND NOT delivery_blocked", p.NodeID, p.InstallationID, request.RuntimeId, request.BaseConfigurationRevision).
			Order("grant_id").Limit(128).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if authorize == nil {
			return ErrScope
		}
		allowed, err := authorize(ctx, tx, rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !sameAuthorityScope(row, result) {
				return ErrRecovery
			}
			update, err := decodeAuthorityUpdate(row)
			if err != nil {
				return err
			}
			if !allowed[row.GrantID] {
				if err := tx.Model(&row).Update("delivery_blocked", true).Error; err != nil {
					return err
				}
				continue
			}
			if err := validateAuthorityEntryTx(tx, row, update, true); err != nil {
				if errors.Is(err, ErrRevision) || errors.Is(err, ErrScope) {
					if err := tx.Model(&row).Update("delivery_blocked", true).Error; err != nil {
						return err
					}
					continue
				}
				return err
			}
			result.Updates = append(result.Updates, update)
		}
		return authority.ValidateAuthorityBatch(result, true)
	})
	return result, err
}

func validateAuthorityEntryTx(tx *gorm.DB, row model.FleetAuthorityUpdate, update *nodev1.AuthorityUpdate, requireActive bool) error {
	scope, err := publishedAuthorityMetadataTx(tx, row.NodeID, uint64(row.BaseConfigurationRevision), row.RuntimeID, row.ClientID)
	if err != nil {
		return err
	}
	if !bytes.Equal(scope.PayloadSHA256, row.BasePayloadSHA256) || !bytes.Equal(scope.Descriptor.SHA256, update.DescriptorSha256) ||
		scope.Descriptor.CredentialGeneration != update.CredentialGeneration || scope.Descriptor.PlacementID != update.PlacementId || scope.Descriptor.PlacementRevision != update.PlacementRevision {
		return ErrScope
	}
	var grant model.FleetGrant
	var share model.FleetRateShare
	if err := tx.First(&grant, "id = ?", row.GrantID).Error; err != nil {
		return err
	}
	if err := tx.First(&share, "id = ?", row.GrantID).Error; err != nil {
		return err
	}
	if grant.NodeID != row.NodeID || grant.InstallationID != row.InstallationID || grant.RuntimeID != row.RuntimeID || grant.ClientID != row.ClientID || grant.TermID != row.TermID ||
		grant.PlacementID == nil || *grant.PlacementID != row.PlacementID || grant.Unlimited != update.QuotaUnlimited || grant.Revision < int64(update.CreditRevision) || grant.IssuedBytes < int64(update.CreditCeiling) ||
		share.NodeID != row.NodeID || share.InstallationID != row.InstallationID || share.RuntimeID != row.RuntimeID || share.BudgetID != row.ClientID || share.Revision != row.Revision {
		return ErrRevision
	}
	rates, err := decodeRates(share.Desired)
	if err != nil {
		return err
	}
	if rates != authorityRates(update) || !update.Enabled && rates != (Rates{}) {
		return ErrScope
	}
	var term *model.FleetTerm
	if requireActive && update.Enabled {
		term, err = managedAuthority(tx, row.ClientID, row.TermID, true)
	} else {
		term = new(model.FleetTerm)
		err = tx.Where("id = ? AND client_id = ?", row.TermID, row.ClientID).First(term).Error
	}
	if err != nil {
		return err
	}
	if term == nil || term.ExpiresUnixMilli != update.ExpiresUnixMilli || requireActive && update.Enabled && term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= time.Now().UnixMilli() {
		return ErrScope
	}
	return nil
}

func (s *Store) AcknowledgeAuthorityUpdates(ctx context.Context, p identity.Principal, batch *nodev1.AuthorityUpdateBatch) (*nodev1.AuthorityUpdateReceipt, error) {
	if authority.ValidateAuthorityBatch(batch, false) != nil || p.Require(batch.NodeId, batch.InstallationId) != nil {
		return nil, ErrInvalid
	}
	request := &nodev1.AuthorityDeliveryRequest{
		NodeId: batch.NodeId, InstallationId: batch.InstallationId, AgentStateJournalId: batch.AgentStateJournalId,
		RuntimeId: batch.RuntimeId, StateJournalId: batch.StateJournalId, CreditJournalId: batch.CreditJournalId, BaseConfigurationRevision: batch.BaseConfigurationRevision, BasePayloadSha256: batch.BasePayloadSha256,
	}
	digest, err := authority.AuthorityDigest(batch)
	if err != nil {
		return nil, err
	}
	if err := s.RequireAgentJournal(ctx, p, batch.AgentStateJournalId); err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := authorityDeliveryScope(tx, p, request); err != nil {
			return err
		}
		rows := make(map[string]model.FleetAuthorityUpdate, len(batch.Updates))
		var budgets []string
		for _, update := range batch.Updates {
			var row model.FleetAuthorityUpdate
			if err := tx.First(&row, "grant_id = ?", update.GrantId).Error; err != nil {
				return err
			}
			if !sameAuthorityScope(row, batch) {
				return ErrScope
			}
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(update)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(encoded)
			if row.AcknowledgedRevision == int64(update.AuthorityRevision) && bytes.Equal(row.AcknowledgedSHA256, hash[:]) {
				continue
			}
			if row.Revision != int64(update.AuthorityRevision) || !bytes.Equal(row.PayloadSHA256, hash[:]) || !bytes.Equal(row.Payload, encoded) {
				return ErrRevision
			}
			if err := validateAuthorityEntryTx(tx, row, update, false); err != nil {
				return err
			}
			rows[row.GrantID] = row
			budgets = append(budgets, row.ClientID)
		}
		slices.Sort(budgets)
		budgets = slices.Compact(budgets)
		if len(budgets) != 0 {
			var held []model.FleetRateBudget
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id IN ?", budgets).Order("id").Find(&held).Error; err != nil {
				return err
			}
			if len(held) != len(budgets) {
				return ErrScope
			}
		}
		for _, update := range batch.Updates {
			row, changed := rows[update.GrantId]
			if !changed {
				continue
			}
			var share model.FleetRateShare
			if err := tx.First(&share, "id = ?", row.GrantID).Error; err != nil {
				return err
			}
			desired, err := decodeRates(share.Desired)
			if err != nil {
				return err
			}
			if share.Revision != row.Revision || desired != authorityRates(update) {
				return ErrRevision
			}
			if err := tx.Model(&share).Updates(map[string]any{"applied_revision": row.Revision, "outstanding": share.Desired}).Error; err != nil {
				return err
			}
			if !update.Enabled {
				stop := model.FleetGrantStop{GrantID: row.GrantID, DeploymentRevision: row.BaseConfigurationRevision, RateRevision: row.Revision}
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&stop).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Where("grant_id = ?", row.GrantID).Delete(&model.FleetGrantStop{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&row).Updates(map[string]any{"acknowledged_revision": row.Revision, "acknowledged_sha256": row.PayloadSHA256}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.AuthorityUpdateReceipt{PayloadSha256: digest}, nil
}
