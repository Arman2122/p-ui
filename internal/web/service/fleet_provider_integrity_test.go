package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetProviderCorruptAppliedPayloadCannotAuthorizeRollout(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	if _, err := f.service.WakeApplied(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Model(&model.FleetDeployment{}).Where("node_id = ? AND revision = 1", f.f.nodes[0].NodeID).
		Update("payload", []byte(`{"version":1,"runtimes":[]}`)).Error; err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	var count int64
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("corrupt applied payload authorized provider rollout: count=%d err=%v", count, err)
	}
	var binding model.FleetPublishedProviderBinding
	if err := f.f.db.First(&binding, "node_id = ?", f.f.nodes[0].NodeID).Error; err != nil || !binding.Pending || binding.Diagnostic != "PUBLICATION_SCOPE_UNAVAILABLE" {
		t.Fatalf("corrupt applied payload lost pending diagnostic: pending=%v diagnostic=%s err=%v", binding.Pending, binding.Diagnostic, err)
	}
}

func TestFleetProviderFundedDemandRefreshKeepsGrantAndReservation(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	active, err := (FleetClientService{}).ActivateFromDemand(t.Context(), f.f.nodes[0], f.generation, f.materialized.Placement.ID, fleetDemand(f.materialized))
	if err != nil {
		t.Fatal(err)
	}
	rates := controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
	credit, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300, Rates: rates,
	})
	if err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, 1, time.Now())
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, f.f.nodes[0].NodeID, 1, "xray", f.materialized.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal := fleetCreditRequest(t, f.f)
	request := &nodev1.AuthorityDeliveryRequest{
		NodeId: journal.NodeId, InstallationId: journal.InstallationId, AgentStateJournalId: journal.AgentStateJournalId,
		RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId, BaseConfigurationRevision: 1, BasePayloadSha256: scope.PayloadSHA256,
	}
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), credit.Grant.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.GetUpdates()) != 1 {
		t.Fatalf("funded initial authority: count=%d err=%v", len(batch.GetUpdates()), err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, 2, time.Now(), &nodev1.GrantAuthority{
		GrantId: credit.Grant.ID, ClientId: credit.Grant.ClientID, TermId: credit.Grant.TermID,
		PolicyRevision: 1, AuthorityRevision: 1, CreditCeiling: 300, AdmissionKnown: true, SpendableKnown: true, SpendableBytes: 300, AuthorizationActive: true,
	})
	f.refresh(t)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.PublishedRevision != 2 {
		t.Fatalf("funded demand blocked refresh=%+v", work)
	}
	delivery, err := f.f.store.Delivery(t.Context(), f.f.nodes[0], f.generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configuration.Decode(delivery.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Grants) != 1 {
		t.Fatalf("funded refresh bundle invalid: %v", err)
	}
	grant := bundle.Runtimes[0].Grants[0]
	if grant.ID != credit.Grant.ID || grant.Policy.Term != active.Term.ID || grant.Policy.CreditCeiling != 300 || grant.Policy.CreditRevision != uint64(credit.Grant.Revision) || grant.Policy.ExpiresUnixMilli != active.Term.ExpiresUnixMilli {
		t.Fatalf("provider refresh replaced original funded authority=%+v", grant.Policy)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 300 {
		t.Fatalf("provider refresh changed original reservation=%+v err=%v", budget, err)
	}
	var grants int64
	if err := f.f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 1 {
		t.Fatalf("provider refresh minted authority: count=%d err=%v", grants, err)
	}
}
