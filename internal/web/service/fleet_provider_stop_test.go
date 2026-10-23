package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetProviderStoppedAppliedBaseRetainsRefreshWake(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	active, err := (FleetClientService{}).ActivateFromDemand(t.Context(), f.f.nodes[0], f.generation, f.materialized.Placement.ID, fleetDemand(f.materialized))
	if err != nil {
		t.Fatal(err)
	}
	credit, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), credit.Grant.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	var base model.FleetDeployment
	if err := f.f.db.First(&base, "node_id = ? AND revision = 1", f.f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	stopped, err := f.f.store.StopConfiguration(t.Context(), f.f.nodes[0], controlplane.StopConfigurationRequest{
		OperationID: uuid.NewString(), ExpectedBaseRevision: 1,
		ExpectedBaseSHA256: base.Digest, GrantIDs: []string{credit.Grant.ID},
	}, controlplane.StopConfigurationValidators{Authority: ValidateFleetAuthorityDeliveryTx, Credit: ValidateFleetCreditDeliveryTx})
	if err != nil || stopped.Revision != 2 {
		t.Fatalf("provider stop revision=%d err=%v", stopped.Revision, err)
	}
	f.ack(t)
	f.refresh(t)
	if _, err := f.service.WakeApplied(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	work := f.work(t)
	if !work.Pending || work.BaseDeploymentRevision != 2 || work.PublishedRevision != 0 {
		t.Fatalf("stopped APPLIED provider base lost refresh wake=%+v", work)
	}
	var binding model.FleetPublishedProviderBinding
	if err := f.f.db.First(&binding, "node_id = ? AND deployment_revision = 2", f.f.nodes[0].NodeID).Error; err != nil || !binding.ScopeKnown || binding.ProviderRevision != 1 || binding.Diagnostic != "" {
		t.Fatalf("frozen provider binding was rematerialized or unknown: revision=%d known=%v err=%v", binding.ProviderRevision, binding.ScopeKnown, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 300 {
		t.Fatalf("stop/refresh altered original reservation=%+v err=%v", budget, err)
	}
}
