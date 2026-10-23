package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type stopRuntimeScope struct {
	journal     model.FleetRuntimeJournal
	digest      []byte
	descriptors map[string]configuration.DescriptorScope
}

func composeStopAuthority(ctx context.Context, tx *gorm.DB, principal identity.Principal, base model.FleetDeployment, targets []string, validators StopConfigurationValidators) (stopComposition, error) {
	state := stopComposition{grants: map[string]governor.Grant{}, runtimes: map[string]string{}, clients: map[string]string{}, placements: map[string]string{}, targets: map[string]bool{}, ledgers: map[string]model.FleetGrant{}, shares: map[string]model.FleetRateShare{}}
	for _, id := range targets {
		state.targets[id] = true
	}
	var err error
	state.bundle, err = configuration.Decode(base.Payload)
	if err != nil {
		return state, errors.Join(ErrStopComposition, err)
	}
	if err := requireNodeJournalTx(tx, principal.NodeID, principal.InstallationID, ""); err != nil {
		return state, err
	}
	var agent model.FleetNodeJournal
	if err := tx.First(&agent, "node_id = ?", principal.NodeID).Error; err != nil {
		return state, err
	}
	scopes := map[string]stopRuntimeScope{}
	for _, runtime := range state.bundle.Runtimes {
		if err := RequireRuntimeJournalTx(tx, principal.NodeID, principal.InstallationID, runtime.ID); err != nil {
			return state, err
		}
		var scope stopRuntimeScope
		if err := tx.First(&scope.journal, "node_id = ? AND runtime_id = ?", principal.NodeID, runtime.ID).Error; err != nil {
			return state, err
		}
		if scope.journal.CoreID != runtime.Core {
			return state, ErrStopComposition
		}
		projection, err := configuration.ProjectBundle(state.bundle, runtime.ID, runtime.Core)
		if err != nil {
			return state, err
		}
		encoded, err := json.Marshal(projection)
		if err != nil {
			return state, err
		}
		digest := sha256.Sum256(encoded)
		scope.digest = digest[:]
		scope.descriptors, err = configuration.DescriptorScopes(runtime)
		if err != nil {
			return state, err
		}
		var indexed model.FleetPublishedRuntime
		if err := tx.First(&indexed, "node_id = ? AND revision = ? AND runtime_id = ?", principal.NodeID, base.Revision, runtime.ID).Error; err != nil {
			return state, errors.Join(ErrStopComposition, err)
		}
		if indexed.CoreID != runtime.Core || !bytes.Equal(indexed.PayloadDigest, digest[:]) || !bytes.Equal(indexed.DeploymentDigest, base.Digest) {
			return state, ErrStopComposition
		}
		scopes[runtime.ID] = scope
		for _, grant := range runtime.Grants {
			if grant.Policy.Revision != uint64(base.Revision) || grant.Policy.AuthorityRevision != grant.RateRevision {
				return state, ErrStopComposition
			}
			state.grants[grant.ID], state.runtimes[grant.ID] = grant, runtime.ID
			state.clients[runtime.ID+"/"+grant.ClientID] = grant.ID
			state.placements[grant.ID] = scope.descriptors[grant.ClientID].PlacementID
		}
	}
	var authorities []model.FleetAuthorityUpdate
	if err := tx.Where("node_id = ? AND installation_id = ? AND base_configuration_revision = ?", principal.NodeID, principal.InstallationID, base.Revision).Order("runtime_id,grant_id").Find(&authorities).Error; err != nil {
		return state, err
	}
	pendingAuthorities := map[string][]model.FleetAuthorityUpdate{}
	for _, row := range authorities {
		scope, exists := scopes[row.RuntimeID]
		if !exists || agent.StateJournalID == nil || row.AgentStateJournalID != *agent.StateJournalID || row.StateJournalID != scope.journal.StateJournalID || row.CreditJournalID != scope.journal.CreditJournalID || !bytes.Equal(row.BasePayloadSHA256, scope.digest) {
			return state, ErrStopComposition
		}
		update, err := decodeAuthorityUpdate(row)
		if err != nil {
			return state, errors.Join(ErrStopComposition, err)
		}
		descriptor, exists := scope.descriptors[row.ClientID]
		if !exists || descriptor.PlacementID != row.PlacementID || descriptor.CredentialGeneration != update.CredentialGeneration || descriptor.PlacementRevision != update.PlacementRevision || !bytes.Equal(descriptor.SHA256, row.DescriptorSHA256) {
			return state, ErrStopComposition
		}
		if row.AcknowledgedRevision == row.Revision && !bytes.Equal(row.AcknowledgedSHA256, row.PayloadSHA256) || row.DeliveryBlocked && !state.targets[row.GrantID] {
			return state, ErrStopComposition
		}
		policy, err := authority.AuthorityPolicy(uint64(base.Revision), update)
		if err != nil {
			return state, errors.Join(ErrStopComposition, err)
		}
		previous, exists := state.grants[row.GrantID]
		if id := state.clients[row.RuntimeID+"/"+row.ClientID]; id != "" && id != row.GrantID {
			return state, ErrStopComposition
		}
		if exists {
			if previous.ClientID != row.ClientID || state.runtimes[row.GrantID] != row.RuntimeID {
				return state, ErrStopComposition
			}
			policy, err = limits.MergeAuthority(previous.Policy, policy)
			if err != nil {
				return state, errors.Join(ErrStopComposition, err)
			}
		}
		if policy.Enabled && row.AcknowledgedRevision < row.Revision && !state.targets[row.GrantID] && (!exists || policy != previous.Policy) {
			pendingAuthorities[row.RuntimeID] = append(pendingAuthorities[row.RuntimeID], row)
		}
		state.grants[row.GrantID] = governor.Grant{ID: row.GrantID, ClientID: row.ClientID, RateRevision: policy.AuthorityRevision, Policy: policy}
		state.runtimes[row.GrantID], state.clients[row.RuntimeID+"/"+row.ClientID] = row.RuntimeID, row.GrantID
		state.placements[row.GrantID] = row.PlacementID
	}
	if err := state.lockStopSources(tx); err != nil {
		return state, err
	}
	for _, rows := range pendingAuthorities {
		if validators.Authority == nil {
			return state, ErrStopComposition
		}
		for offset := 0; offset < len(rows); offset += 128 {
			chunk := rows[offset:min(offset+128, len(rows))]
			allowed, err := validators.Authority(ctx, tx, chunk)
			if err != nil {
				return state, err
			}
			for _, row := range chunk {
				if !allowed[row.GrantID] {
					return state, ErrStopComposition
				}
			}
		}
	}
	var credits []model.FleetCreditUpdate
	if err := tx.Where("node_id = ? AND installation_id = ? AND base_configuration_revision = ?", principal.NodeID, principal.InstallationID, base.Revision).Order("runtime_id,grant_id").Find(&credits).Error; err != nil {
		return state, err
	}
	pendingCredits := map[string][]model.FleetCreditUpdate{}
	for _, row := range credits {
		grant, exists := state.grants[row.GrantID]
		scope := scopes[row.RuntimeID]
		if !exists || state.runtimes[row.GrantID] != row.RuntimeID || row.ClientID != grant.ClientID || row.TermID != grant.Policy.Term || grant.Policy.QuotaUnlimited || row.StateJournalID != scope.journal.StateJournalID || row.CreditJournalID != scope.journal.CreditJournalID {
			return state, ErrStopComposition
		}
		if row.AcknowledgedRevision == row.Revision && row.AcknowledgedCeiling != row.CreditCeiling || row.Revision <= 0 || row.CreditCeiling < 0 {
			return state, ErrStopComposition
		}
		if uint64(row.Revision) <= grant.Policy.CreditRevision {
			if uint64(row.Revision) == grant.Policy.CreditRevision && uint64(row.CreditCeiling) != grant.Policy.CreditCeiling || uint64(row.CreditCeiling) > grant.Policy.CreditCeiling {
				return state, ErrStopComposition
			}
			continue
		}
		if uint64(row.CreditCeiling) < grant.Policy.CreditCeiling {
			return state, ErrStopComposition
		}
		if grant.Policy.Enabled && row.AcknowledgedRevision < row.Revision && !state.targets[row.GrantID] {
			pendingCredits[row.RuntimeID] = append(pendingCredits[row.RuntimeID], row)
		}
		grant.Policy.CreditRevision, grant.Policy.CreditCeiling = uint64(row.Revision), uint64(row.CreditCeiling)
		state.grants[row.GrantID] = grant
	}
	for _, rows := range pendingCredits {
		if validators.Credit == nil {
			return state, ErrStopComposition
		}
		for offset := 0; offset < len(rows); offset += 32 {
			chunk := rows[offset:min(offset+32, len(rows))]
			allowed, err := validators.Credit(ctx, tx, chunk)
			if err != nil {
				return state, err
			}
			for _, row := range chunk {
				if !allowed[row.GrantID] {
					return state, ErrStopComposition
				}
			}
		}
	}
	if err := state.verifyStopLedgers(tx, principal); err != nil {
		return state, err
	}
	return state, nil
}

func (state *stopComposition) lockStopSources(tx *gorm.DB) error {
	clients := make(map[string]bool, len(state.grants))
	for id, grant := range state.grants {
		clients[grant.ClientID] = clients[grant.ClientID] || !state.targets[id] && grant.Policy.Enabled
	}
	ids := make([]string, 0, len(clients))
	for id := range clients {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var sources []model.FleetClient
	for offset := 0; offset < len(ids); offset += 1024 {
		var rows []model.FleetClient
		chunk := ids[offset:min(offset+1024, len(ids))]
		if err := tx.Select("id,panel_client_id,owned").Where("id IN ?", chunk).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrStopComposition
		}
		sources = append(sources, rows...)
	}
	var panelIDs []int
	for _, source := range sources {
		if !source.Owned || clients[source.ID] && source.PanelClientID == nil {
			return ErrStopComposition
		}
		if source.PanelClientID != nil {
			panelIDs = append(panelIDs, *source.PanelClientID)
		}
	}
	slices.Sort(panelIDs)
	panelIDs = slices.Compact(panelIDs)
	requested := make(map[int]bool, len(panelIDs))
	for _, id := range panelIDs {
		requested[id] = true
	}
	locked := make(map[int]bool, len(panelIDs))
	for offset := 0; offset < len(panelIDs); offset += 1024 {
		var rows []model.ClientRecord
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", panelIDs[offset:min(offset+1024, len(panelIDs))]).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			locked[row.Id] = true
		}
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		var rows []model.FleetClient
		if err := tx.Select("id,panel_client_id,owned").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids[offset:min(offset+1024, len(ids))]).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(ids[offset:min(offset+1024, len(ids))]) {
			return ErrStopComposition
		}
		for _, source := range rows {
			if !source.Owned || source.PanelClientID != nil && !requested[*source.PanelClientID] || clients[source.ID] && (source.PanelClientID == nil || !locked[*source.PanelClientID]) {
				return ErrStopComposition
			}
		}
	}
	return nil
}

func (state *stopComposition) verifyStopLedgers(tx *gorm.DB, principal identity.Principal) error {
	ids := make([]string, 0, len(state.grants))
	for id := range state.grants {
		ids = append(ids, id)
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		chunk := ids[offset:min(offset+1024, len(ids))]
		var ledgers []model.FleetGrant
		var shares []model.FleetRateShare
		if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", chunk, principal.NodeID, principal.InstallationID).Find(&ledgers).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", chunk, principal.NodeID, principal.InstallationID).Find(&shares).Error; err != nil {
			return err
		}
		if len(ledgers) != len(chunk) || len(shares) != len(chunk) {
			return ErrStopComposition
		}
		for _, ledger := range ledgers {
			state.ledgers[ledger.ID] = ledger
		}
		for _, share := range shares {
			state.shares[share.ID] = share
		}
	}
	for id, grant := range state.grants {
		ledger, share := state.ledgers[id], state.shares[id]
		if ledger.ClientID != grant.ClientID || ledger.TermID != grant.Policy.Term || ledger.RuntimeID != state.runtimes[id] || ledger.Unlimited != grant.Policy.QuotaUnlimited || ledger.PlacementID == nil || share.RuntimeID != ledger.RuntimeID || share.BudgetID != ledger.ClientID ||
			uint64(ledger.Revision) < grant.Policy.CreditRevision || uint64(ledger.IssuedBytes) < grant.Policy.CreditCeiling || uint64(share.Revision) < grant.Policy.AuthorityRevision {
			return ErrStopComposition
		}
		if placement := state.placements[id]; placement != "" && placement != *ledger.PlacementID {
			return ErrStopComposition
		}
		if state.targets[id] {
			grant.Policy.CreditRevision, grant.Policy.CreditCeiling = uint64(ledger.Revision), uint64(ledger.IssuedBytes)
			state.grants[id] = grant
			continue
		}
		rates, err := decodeRates(share.Desired)
		if err != nil {
			return err
		}
		if uint64(ledger.Revision) != grant.Policy.CreditRevision || uint64(ledger.IssuedBytes) != grant.Policy.CreditCeiling || uint64(share.Revision) != grant.Policy.AuthorityRevision || uint64(share.Revision) != grant.RateRevision || rates.Upload != grant.Policy.Upload || rates.Download != grant.Policy.Download {
			return ErrStopComposition
		}
	}
	for id := range state.targets {
		if _, exists := state.grants[id]; !exists {
			return ErrStopComposition
		}
	}
	return nil
}
