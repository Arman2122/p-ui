package service

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

func fleetSourceNativeUsable(tx *gorm.DB, node model.FleetNode, placement model.FleetPlacement, baseline governor.Grant) (bool, error) {
	var grant model.FleetGrant
	var share model.FleetRateShare
	var observed model.FleetGrantAuthority
	var runtime model.FleetRuntimeJournal
	for _, read := range []struct {
		value any
		query string
		args  []any
	}{
		{&grant, "id = ?", []any{baseline.ID}},
		{&share, "id = ?", []any{baseline.ID}},
		{&observed, "grant_id = ?", []any{baseline.ID}},
		{&runtime, "node_id = ? AND runtime_id = ?", []any{node.ID, placement.RuntimeID}},
	} {
		if err := tx.Where(read.query, read.args...).First(read.value).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
	}
	if err := controlplane.RequireRuntimeJournalTx(tx, node.ID, node.InstallationID, placement.RuntimeID); err != nil {
		return false, err
	}
	var desired, outstanding controlplane.Rates
	if json.Unmarshal([]byte(share.Desired), &desired) != nil || json.Unmarshal([]byte(share.Outstanding), &outstanding) != nil {
		return false, controlplane.ErrRecovery
	}
	creditApplied, err := fleetSourceNativeCreditApplied(tx, node, placement, runtime, grant, baseline)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixMilli()
	return usableFleetRates(desired) && desired == outstanding && desired.Upload == baseline.Policy.Upload && desired.Download == baseline.Policy.Download &&
		share.NodeID == node.ID && share.InstallationID == node.InstallationID && share.RuntimeID == placement.RuntimeID && share.BudgetID == placement.ClientID &&
		share.Revision == share.AppliedRevision && uint64(share.Revision) == baseline.RateRevision && uint64(share.Revision) == baseline.Policy.AuthorityRevision &&
		grant.PlacementID != nil && *grant.PlacementID == placement.ID && grant.NodeID == node.ID && grant.InstallationID == node.InstallationID && grant.RuntimeID == placement.RuntimeID &&
		grant.ClientID == placement.ClientID && grant.TermID == baseline.Policy.Term && !grant.Unlimited && !baseline.Policy.QuotaUnlimited &&
		creditApplied &&
		runtime.InstallationID == node.InstallationID && runtime.CoreID == placement.CoreID && runtime.Ready && runtime.Sequence >= observed.ReportSequence &&
		runtime.ReceivedUnixMilli >= now-30_000 && runtime.ObservedUnixMilli >= now-30_000 && runtime.ObservedUnixMilli <= now+5_000 &&
		observed.NodeID == node.ID && observed.RuntimeID == placement.RuntimeID && observed.ReceivedUnixMilli >= now-30_000 && observed.PolicyRevision == node.AppliedRevision &&
		observed.AuthorityRevision == share.Revision && observed.CreditCeiling == grant.IssuedBytes && !observed.QuotaUnlimited && observed.AuthorizationActive && observed.SpendableKnown && observed.SpendableBytes > 0, nil
}

func fleetSourceNativeCreditApplied(tx *gorm.DB, node model.FleetNode, placement model.FleetPlacement, runtime model.FleetRuntimeJournal, grant model.FleetGrant, baseline governor.Grant) (bool, error) {
	if uint64(grant.Revision) < baseline.Policy.CreditRevision || uint64(grant.IssuedBytes) < baseline.Policy.CreditCeiling {
		return false, nil
	}
	if uint64(grant.Revision) == baseline.Policy.CreditRevision && uint64(grant.IssuedBytes) == baseline.Policy.CreditCeiling {
		return true, nil
	}
	var credit model.FleetCreditUpdate
	if err := tx.First(&credit, "grant_id = ?", grant.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return credit.NodeID == node.ID && credit.InstallationID == node.InstallationID && credit.RuntimeID == placement.RuntimeID &&
		credit.ClientID == grant.ClientID && credit.TermID == grant.TermID && credit.BaseConfigurationRevision == node.AppliedRevision &&
		credit.StateJournalID == runtime.StateJournalID && credit.CreditJournalID == runtime.CreditJournalID &&
		credit.Revision == grant.Revision && credit.CreditCeiling == grant.IssuedBytes && credit.AcknowledgedRevision == grant.Revision && credit.AcknowledgedCeiling == grant.IssuedBytes, nil
}
