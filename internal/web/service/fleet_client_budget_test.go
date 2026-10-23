package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetSharedGroupPoolRejectsMixedAuthorityAndPreservesReservations(t *testing.T) {
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "fleet-family"}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.GroupQuota{Name: group.Name, QuotaBytes: 700}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	other := model.ClientRecord{Email: "fleet-other", UUID: uuid.NewString(), Group: group.Name, Enable: true, TotalGB: 1000, ExpiryTime: f.client.ExpiryTime, Reset: 1}
	if err := f.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: other.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: other.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: other.ExpiryTime, Reset: 1}).Error; err != nil {
		t.Fatal(err)
	}
	first := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, first)
	unlimited := controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
	request := FleetCreditRequest{OperationID: uuid.NewString(), PlacementID: first.ID, TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 600, Rates: unlimited}
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, ErrFleetPool) {
		t.Fatalf("mixed legacy group spent fleet balance=%v", err)
	}
	secondFixture := f
	secondFixture.client = other
	second := secondFixture.approve(t, secondFixture.materialize(t, 1))
	otherActive := secondFixture.activate(t, second)
	firstCredit, err := (FleetClientService{}).AllocateCredit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var scope model.FleetBudgetScope
	if err := f.db.Where("kind = ? AND source_id = ?", "group", group.Id).First(&scope).Error; err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.PlacementID, request.TermID, request.Bytes = uuid.NewString(), second.ID, otherActive.Term.ID, 101
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, controlplane.ErrBudget) {
		t.Fatalf("group reservations were multiplied=%v", err)
	}
	request.Bytes = 100
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	var shared model.FleetBudget
	if err := f.db.First(&shared, "id = ?", scope.BudgetID).Error; err != nil || shared.UsedBytes != 0 || shared.ReservedBytes != 700 {
		t.Fatalf("group conservation=%+v err=%v", shared, err)
	}
	if err := (&ClientService{}).ResetGroupTraffic(group.Name); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("legacy group reset bypassed outstanding authority=%v", err)
	}
	var links []model.FleetGrantBudget
	if err := f.db.Where("grant_id = ?", firstCredit.Grant.ID).Find(&links).Error; err != nil || len(links) != 2 {
		t.Fatalf("master-derived budget links=%+v err=%v", links, err)
	}
}

func TestFleetUnlimitedQuotaStillBoundsFastTierAuthority(t *testing.T) {
	f := fleetFixture(t, false)
	if err := f.db.Model(&f.client).Update("total_gb", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Update("total", 0).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.Policy{Name: "tiered", Rev: 1, OnDepletion: "disable", ResetCycle: "none", Tiers: `[{"fromBytes":0,"upBps":1000000,"downBps":1000000},{"fromBytes":200,"upBps":100000,"downBps":100000}]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id, AppliedRev: 1, AppliedOnDepletion: "disable", AppliedResetCycle: "none"}).Error; err != nil {
		t.Fatal(err)
	}
	a, b := f.approve(t, f.materialize(t, 0)), f.approve(t, f.materialize(t, 1))
	active := f.activate(t, a)
	rate := func(bits uint64, burst int) controlplane.Rates {
		return controlplane.Rates{
			Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: bits, BurstBytes: burst}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: bits, BurstBytes: burst},
		}
	}
	request := FleetCreditRequest{OperationID: uuid.NewString(), PlacementID: a.ID, TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 200, Rates: rate(600000, 40000)}
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	other := request
	other.OperationID, other.PlacementID, other.Bytes, other.Rates = uuid.NewString(), b.ID, 1, rate(400000, 25536)
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), other); !errors.Is(err, controlplane.ErrBudget) {
		t.Fatalf("unlimited budget duplicated fast-phase credit=%v", err)
	}
	if _, err := f.store.Ingest(t.Context(), f.nodes[0], &nodev1.UsageBatch{
		NodeId: f.nodes[0].NodeID, InstallationId: f.nodes[0].InstallationID, Sequence: 1,
		Checkpoints: []*nodev1.UsageCheckpoint{{GrantId: credit.Grant.ID, ClientId: credit.Grant.ClientID, TermId: active.Term.ID, MeterId: "xray", MeterIncarnation: uuid.NewString(), Uploaded: 200, ObservedUnixMilli: time.Now().UnixMilli()}},
	}); err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.Bytes = uuid.NewString(), 1
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, ErrFleetPhase) {
		t.Fatalf("unsettled tier advanced on consumption=%v", err)
	}
	if err := f.store.SetRateBudget(t.Context(), credit.Grant.ClientID, credit.Grant.ClientID, active.Term.ID, 2, rate(100000, 65536)); err != nil {
		t.Fatal(err)
	}
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, ErrFleetPhase) {
		t.Fatalf("new quota funded retained fast-rate authority=%v", err)
	}
}

func TestFleetActivationUsesProvidedTransactionAndRollsBackProjection(t *testing.T) {
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	node := f.nodes[0]
	demand := fleetDemand(materialized)
	rollback := errors.New("business transaction rolls back")
	err := f.db.Transaction(func(tx *gorm.DB) error {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		generation, err := store.BeginSession(t.Context(), node, f.hello(t, node))
		if err != nil {
			return err
		}
		owned := f
		owned.db, owned.store = tx, store
		demand.DeploymentRevision = fleetApplyProbe(t, owned, node, generation, fleetAdmissionBundle(materialized))
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		result, err := HandleFleetActivation(ctx, tx, node, generation, demand)
		if err != nil || result == nil || result.TermId == "" {
			t.Fatalf("callback ignored owning transaction: %+v %v", result, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var terms int64
	if err := f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != 0 {
		t.Fatalf("rolled-back term persisted: %d %v", terms, err)
	}
	if got := readFleetTraffic(t, f); got.ExpiryTime != f.client.ExpiryTime {
		t.Fatalf("rolled-back expiry persisted: %+v", got)
	}
}
