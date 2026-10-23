package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func fleetUsageReport(t *testing.T, f fleetClientFixture, node int, grant model.FleetGrant, sequence, up, down uint64) {
	t.Helper()
	principal := f.nodes[node]
	_, err := f.store.Ingest(t.Context(), principal, &nodev1.UsageBatch{
		NodeId: principal.NodeID, InstallationId: principal.InstallationID, Sequence: sequence,
		Checkpoints: []*nodev1.UsageCheckpoint{{
			GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.TermID, MeterId: grant.RuntimeID, MeterIncarnation: grant.ID,
			Uploaded: up, Downloaded: down, ObservedUnixMilli: time.Now().UnixMilli(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFleetTrafficViewsUseCurrentLedgerAndKeepLegacySources(t *testing.T) {
	f := fleetFixture(t, false)
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": 50, "down": 70}).Error; err != nil {
		t.Fatal(err)
	}
	first, second := f.approve(t, f.materialize(t, 0)), f.approve(t, f.materialize(t, 1))
	active := f.activate(t, first)
	unlimited := controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
	allocate := func(placement model.FleetPlacement) FleetCredit {
		t.Helper()
		credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
			OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
			ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300, Rates: unlimited,
		})
		if err != nil {
			t.Fatal(err)
		}
		return credit
	}
	a, b := allocate(first), allocate(second)
	fleetUsageReport(t, f, 0, a.Grant, 1, 50, 30)
	fleetUsageReport(t, f, 1, b.Grant, 1, 20, 40)
	legacy := model.ClientRecord{Email: "legacy-view", UUID: uuid.NewString(), Enable: true, TotalGB: 500, ExpiryTime: f.client.ExpiryTime}
	if err := f.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&core.ClientTraffic{Email: legacy.Email, InboundId: f.inbounds[0].Id, Enable: true, Up: 100, Down: 200, Total: 500}).Error; err != nil {
		t.Fatal(err)
	}
	for _, global := range []model.ClientGlobalTraffic{{MasterGuid: "test-old-master", Email: f.client.Email, Up: 99999, Down: 1}, {MasterGuid: "test-old-master", Email: legacy.Email, Up: 500, Down: 100}} {
		if err := f.db.Create(&global).Error; err != nil {
			t.Fatal(err)
		}
	}
	traffic, err := (&InboundService{}).GetClientTrafficByEmail(f.client.Email)
	if err != nil || traffic == nil || traffic.Up != 120 || traffic.Down != 140 {
		t.Fatalf("owned read used stale legacy/global counters: %+v %v", traffic, err)
	}
	meta := traffic.Accounting
	if meta == nil || meta.ClientID != first.ClientID || meta.MeasuredUploaded != 70 || meta.MeasuredDownloaded != 70 || meta.OpeningUploaded != 50 || meta.OpeningDownloaded != 70 ||
		meta.ReservedBytes == nil || *meta.ReservedBytes != 460 || meta.UncertainBytes != nil || meta.HistoryBasis != "reported" || meta.HistoryGranularity != "day" {
		t.Fatalf("accounting sources conflated: %+v", meta)
	}
	old, err := (&InboundService{}).GetClientTrafficByEmail(legacy.Email)
	if err != nil || old.Up != 500 || old.Down != 100 || old.Accounting != nil {
		t.Fatalf("legacy read changed: %+v %v", old, err)
	}
	page, err := (&ClientService{}).ListPaged(&InboundService{}, &SettingService{}, ClientPageParams{UsageFrom: 200, UsageTo: 300})
	if err != nil || page.Filtered != 1 || len(page.Items) != 1 || page.Items[0].Email != f.client.Email || page.Items[0].Traffic.Up != 120 {
		t.Fatalf("paged filter did not use ledger: %+v %v", page, err)
	}
	depleted, err := (&ClientService{}).ListPaged(&InboundService{}, &SettingService{}, ClientPageParams{Filter: "depleted"})
	if err != nil || depleted.Filtered != 1 || depleted.Items[0].Email != legacy.Email {
		t.Fatalf("reserved authority became depleted usage: %+v %v", depleted, err)
	}
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{OperationID: uuid.NewString(), PlacementID: first.ID, Kind: controlplane.TrafficReset, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	fleetAppliedStop(t, f, f.nodes[0], active.Term, a)
	fleetAppliedStop(t, f, f.nodes[1], active.Term, b)
	if _, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, FleetFinishRequest{OperationID: uuid.NewString(), PlacementID: first.ID, PendingTermID: pending.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	fleetUsageReport(t, f, 0, a.Grant, 2, 60, 35)
	traffic, err = (&InboundService{}).GetClientTrafficByEmail(f.client.Email)
	if err != nil || traffic.Up != 0 || traffic.Down != 0 || traffic.Accounting.TermID != pending.Term.ID {
		t.Fatalf("late old-term report leaked into current quota: %+v %v", traffic, err)
	}
	history, err := readClientHistories(f.db, []string{f.client.Email}, time.Now())
	if err != nil || len(history[f.client.Email]) != 1 || history[f.client.Email][0].Up != 80 || history[f.client.Email][0].Down != 75 {
		t.Fatalf("reported history omitted prior term or invented opening spike: %+v %v", history, err)
	}
}
