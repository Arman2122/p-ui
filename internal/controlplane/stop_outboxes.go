package controlplane

import (
	"bytes"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type appliedStopScope struct {
	runtime      string
	descriptor   configuration.DescriptorScope
	grant        governor.Grant
	journal      model.FleetRuntimeJournal
	agentJournal string
}

// A full applied baseline can supersede older deltas; publication alone is never an acknowledgement.
func supersedeAppliedOutboxes(tx *gorm.DB, principal identity.Principal, revision int64, payload []byte) error {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil
	}
	var agent model.FleetNodeJournal
	if err := tx.First(&agent, "node_id = ?", principal.NodeID).Error; err != nil {
		return err
	}
	if agent.StateJournalID == nil {
		return nil
	}
	claims := map[string]appliedStopScope{}
	for _, runtime := range bundle.Runtimes {
		descriptors, err := configuration.DescriptorScopes(runtime)
		if err != nil {
			return err
		}
		var journal model.FleetRuntimeJournal
		if err := tx.Where("node_id = ? AND installation_id = ? AND runtime_id = ? AND core_id = ?", principal.NodeID, principal.InstallationID, runtime.ID, runtime.Core).Find(&journal).Error; err != nil {
			return err
		}
		if journal.StateJournalID == "" {
			continue
		}
		for _, grant := range runtime.Grants {
			if grant.Policy.Revision == uint64(revision) {
				claims[grant.ID] = appliedStopScope{runtime: runtime.ID, descriptor: descriptors[grant.ClientID], grant: grant, journal: journal, agentJournal: *agent.StateJournalID}
			}
		}
	}
	var authorities []model.FleetAuthorityUpdate
	if err := tx.Where("node_id = ? AND installation_id = ? AND base_configuration_revision < ?", principal.NodeID, principal.InstallationID, revision).FindInBatches(&authorities, 128, func(_ *gorm.DB, _ int) error {
		for _, row := range authorities {
			if !appliedAuthorityCovers(claims[row.GrantID], row) {
				continue
			}
			if err := tx.Where("grant_id = ? AND node_id = ? AND installation_id = ? AND base_configuration_revision = ? AND revision = ? AND payload_sha256 = ?", row.GrantID, row.NodeID, row.InstallationID, row.BaseConfigurationRevision, row.Revision, row.PayloadSHA256).Delete(&model.FleetAuthorityUpdate{}).Error; err != nil {
				return err
			}
		}
		return nil
	}).Error; err != nil {
		return err
	}
	var credits []model.FleetCreditUpdate
	return tx.Where("node_id = ? AND installation_id = ? AND base_configuration_revision < ?", principal.NodeID, principal.InstallationID, revision).FindInBatches(&credits, 128, func(_ *gorm.DB, _ int) error {
		for _, row := range credits {
			claim := claims[row.GrantID]
			policy := claim.grant.Policy
			if claim.runtime != row.RuntimeID || claim.grant.ClientID != row.ClientID || policy.Term != row.TermID || policy.QuotaUnlimited || claim.journal.StateJournalID != row.StateJournalID || claim.journal.CreditJournalID != row.CreditJournalID ||
				policy.CreditRevision < uint64(row.Revision) || policy.CreditCeiling < uint64(row.CreditCeiling) || policy.CreditRevision == uint64(row.Revision) && policy.CreditCeiling != uint64(row.CreditCeiling) {
				continue
			}
			if err := tx.Where("grant_id = ? AND node_id = ? AND installation_id = ? AND base_configuration_revision = ? AND revision = ? AND credit_ceiling = ?", row.GrantID, row.NodeID, row.InstallationID, row.BaseConfigurationRevision, row.Revision, row.CreditCeiling).Delete(&model.FleetCreditUpdate{}).Error; err != nil {
				return err
			}
		}
		return nil
	}).Error
}

func appliedAuthorityCovers(claim appliedStopScope, row model.FleetAuthorityUpdate) bool {
	policy, descriptor := claim.grant.Policy, claim.descriptor
	if claim.runtime != row.RuntimeID || claim.grant.ClientID != row.ClientID || policy.Term != row.TermID || claim.agentJournal != row.AgentStateJournalID || claim.journal.StateJournalID != row.StateJournalID || claim.journal.CreditJournalID != row.CreditJournalID ||
		descriptor.PlacementID != row.PlacementID || !bytes.Equal(descriptor.SHA256, row.DescriptorSHA256) {
		return false
	}
	update, err := decodeAuthorityUpdate(row)
	if err != nil || descriptor.CredentialGeneration != update.CredentialGeneration || descriptor.PlacementRevision != update.PlacementRevision || policy.AuthorityRevision < update.AuthorityRevision || policy.CreditRevision < update.CreditRevision || policy.CreditCeiling < update.CreditCeiling ||
		policy.QuotaUnlimited != update.QuotaUnlimited || policy.ExpiresUnixMilli != update.ExpiresUnixMilli || !update.Enabled && policy.Enabled || policy.CreditRevision == update.CreditRevision && policy.CreditCeiling != update.CreditCeiling {
		return false
	}
	rates := authorityRates(update)
	if policy.AuthorityRevision == update.AuthorityRevision && (policy.Enabled != update.Enabled || policy.Upload != rates.Upload || policy.Download != rates.Download) {
		return false
	}
	return policy.Enabled || policy.Upload == (limits.Rate{}) && policy.Download == (limits.Rate{})
}
