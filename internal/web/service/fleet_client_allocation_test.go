package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type admissionAllocationFixture struct {
	f            fleetClientFixture
	materialized []FleetMaterialization
	generations  []uint64
}

func newAdmissionAllocationFixture(t *testing.T, tiers string) admissionAllocationFixture {
	t.Helper()
	f := fleetFixture(t, false)
	if err := f.db.Model(&f.client).Update("total_gb", 1<<30).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.Policy{Name: "shared", Rev: 1, OnDepletion: "disable", ResetCycle: "none", Tiers: tiers}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id, AppliedRev: 1, AppliedOnDepletion: "disable", AppliedResetCycle: "none"}).Error; err != nil {
		t.Fatal(err)
	}
	out := admissionAllocationFixture{f: f}
	for index, node := range f.nodes {
		materialized := f.materialize(t, index)
		materialized.Placement = f.approve(t, materialized)
		generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
		if err != nil {
			t.Fatal(err)
		}
		fleetApplyProbe(t, f, node, generation, fleetAdmissionBundle(materialized))
		fleetAuthorityReportAtNode(t, f, index, 1, time.Now())
		out.materialized, out.generations = append(out.materialized, materialized), append(out.generations, generation)
	}
	return out
}

func (f admissionAllocationFixture) demand(t *testing.T, index int) model.FleetAdmissionDemand {
	t.Helper()
	request := fleetDemand(f.materialized[index])
	request.DeadlineUnixMilli = time.Now().Add(14 * time.Second).UnixMilli()
	if _, err := HandleFleetActivation(t.Context(), f.f.db, f.f.nodes[index], f.generations[index], request); err != nil {
		t.Fatal(err)
	}
	var demand model.FleetAdmissionDemand
	if err := f.f.db.First(&demand, "placement_id = ?", request.PlacementId).Error; err != nil {
		t.Fatal(err)
	}
	return demand
}

func (f admissionAllocationFixture) claim(t *testing.T, index int) model.FleetAdmissionNodeQueue {
	t.Helper()
	leases, err := f.f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 || leases[0].NodeID != f.f.nodes[index].NodeID {
		t.Fatalf("node %d lease=%+v err=%v", index, leases, err)
	}
	return leases[0]
}

func (f admissionAllocationFixture) delivery(t *testing.T, index int) *nodev1.AuthorityUpdateBatch {
	t.Helper()
	node := f.f.nodes[index]
	var agent model.FleetNodeJournal
	var runtime model.FleetRuntimeJournal
	if err := f.f.db.First(&agent, "node_id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&runtime, "node_id = ? AND runtime_id = ?", node.NodeID, "xray").Error; err != nil {
		t.Fatal(err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, node.NodeID, 1, "xray", f.materialized[index].Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), node, &nodev1.AuthorityDeliveryRequest{
		NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: *agent.StateJournalID, RuntimeId: "xray",
		StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID, BaseConfigurationRevision: 1, BasePayloadSha256: scope.PayloadSHA256,
	}, ValidateFleetAuthorityDeliveryTx)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func (f admissionAllocationFixture) process(t *testing.T, lease model.FleetAdmissionNodeQueue, demand model.FleetAdmissionDemand, want FleetAdmissionOutcome) FleetAdmissionResult {
	t.Helper()
	result, err := ProcessFleetAdmissionDemand(t.Context(), f.f.db, lease, demand)
	if err != nil || result.Outcome != want {
		t.Fatalf("demand result=%+v want=%s err=%v", result, want, err)
	}
	return result
}

func TestFleetAdmissionAllocatesSharedRateOnlyAfterAcknowledgedReductionPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	assertFleetSharedRateAdmission(t, f)
}

func assertFleetSharedRateAdmission(t *testing.T, f admissionAllocationFixture) {
	t.Helper()
	first := f.demand(t, 0)
	leaseA := f.claim(t, 0)
	f.process(t, leaseA, first, FleetAdmissionSatisfied)
	initial := f.delivery(t, 0)
	if len(initial.Updates) != 1 || initial.Updates[0].Upload.BitsPerSecond != 100_000_000 || initial.Updates[0].CreditCeiling != 64<<20 {
		t.Fatalf("first authority=%+v", initial)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], initial); err != nil {
		t.Fatal(err)
	}
	f.process(t, leaseA, f.demand(t, 0), FleetAdmissionSatisfied)
	var grantA model.FleetGrant
	if err := f.f.db.First(&grantA, "id = ?", initial.Updates[0].GrantId).Error; err != nil || grantA.IssuedBytes != 64<<20 || grantA.Revision != 1 {
		t.Fatalf("existing-authority demand reserved more quota=%+v err=%v", grantA, err)
	}
	if err := f.f.store.ReleaseAdmissionNode(t.Context(), leaseA); err != nil {
		t.Fatal(err)
	}
	second := f.demand(t, 1)
	leaseB := f.claim(t, 1)
	f.process(t, leaseB, second, FleetAdmissionAwaitingRate)
	if batch := f.delivery(t, 1); len(batch.Updates) != 0 {
		t.Fatalf("new node received unreserved rate authority=%+v", batch)
	}
	if err := f.f.store.WakeRateReconciliationNodes(t.Context()); err != nil {
		t.Fatal(err)
	}
	leaseA = f.claim(t, 0)
	jobs, err := f.f.store.DueRateReconciliations(t.Context(), leaseA)
	if err != nil || len(jobs) != 1 || jobs[0].GrantID != grantA.ID {
		t.Fatalf("separate holder reconciliation=%+v err=%v", jobs, err)
	}
	if err := reconcileFleetAdmissionRate(t.Context(), f.f.db, leaseA, jobs[0]); err != nil {
		t.Fatal(err)
	}
	reduction := f.delivery(t, 0)
	if len(reduction.Updates) != 1 || reduction.Updates[0].Upload.BitsPerSecond != 50_000_000 || reduction.Updates[0].Download.BitsPerSecond != 50_000_000 || reduction.Updates[0].CreditCeiling != 64<<20 {
		t.Fatalf("holder reduction changed quota or failed fair split=%+v", reduction)
	}
	f.process(t, leaseB, second, FleetAdmissionAwaitingRate)
	var before []model.FleetGrant
	if err := f.f.db.Find(&before).Error; err != nil || len(before) != 1 {
		t.Fatalf("unacknowledged reduction funded another node=%+v err=%v", before, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], reduction); err != nil {
		t.Fatal(err)
	}
	f.process(t, leaseB, second, FleetAdmissionSatisfied)
	allocated := f.delivery(t, 1)
	if len(allocated.Updates) != 1 || allocated.Updates[0].Upload.BitsPerSecond != 50_000_000 || allocated.Updates[0].Download.BitsPerSecond != 50_000_000 || allocated.Updates[0].CreditCeiling != 64<<20 {
		t.Fatalf("safe next authority=%+v", allocated)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = (SELECT budget_id FROM fleet_terms WHERE id = ?)", first.TermID).Error; err != nil || budget.ReservedBytes != 128<<20 {
		t.Fatalf("shared quota conservation=%+v err=%v", budget, err)
	}
	var queued model.FleetAdmissionDemand
	if err := f.f.db.First(&queued, "placement_id = ?", second.PlacementID).Error; err != nil || queued.Pending || queued.Outcome != string(FleetAdmissionSatisfied) {
		t.Fatalf("authority and intent settlement diverged=%+v err=%v", queued, err)
	}
}

func TestFleetAdmissionCapacityAndOfflineAuthorityRemainReservedPostgres(t *testing.T) {
	for _, tc := range []struct {
		name, tiers string
		offline     bool
		retired     bool
	}{
		{"capacity", `[{"fromBytes":0,"upBps":1000000,"downBps":1000000}]`, false, false},
		{"offline", `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`, true, false},
		{"retired-grant", `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdmissionAllocationFixture(t, tc.tiers)
			first, leaseA := f.demand(t, 0), f.claim(t, 0)
			f.process(t, leaseA, first, FleetAdmissionSatisfied)
			if err := f.f.store.ReleaseAdmissionNode(t.Context(), leaseA); err != nil {
				t.Fatal(err)
			}
			if tc.offline {
				if err := f.f.db.Model(&model.FleetNode{}).Where("id = ?", f.f.nodes[0].NodeID).Update("last_seen_unix_milli", time.Now().Add(-time.Hour).UnixMilli()).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.retired {
				if err := f.f.db.Model(&model.FleetPlacement{}).Where("id = ?", first.PlacementID).Updates(map[string]any{"current_grant_id": nil, "current_grant_term_id": nil}).Error; err != nil {
					t.Fatal(err)
				}
			}
			second, leaseB := f.demand(t, 1), f.claim(t, 1)
			result := f.process(t, leaseB, second, FleetAdmissionAwaitingRate)
			if !tc.offline && !tc.retired && !strings.Contains(result.Diagnostic, "minimum burst") {
				t.Fatalf("capacity failure was hidden=%+v", result)
			}
			var grants []model.FleetGrant
			if err := f.f.db.Find(&grants).Error; err != nil || len(grants) != 1 || grants[0].IssuedBytes != 64<<20 {
				t.Fatalf("unavailable authority was reused=%+v err=%v", grants, err)
			}
			var shares []model.FleetRateShare
			if err := f.f.db.Find(&shares).Error; err != nil || len(shares) != 1 {
				t.Fatalf("rate reservations=%+v err=%v", shares, err)
			}
			var outstanding controlplane.Rates
			bits := uint64(1_000_000)
			if tc.offline || tc.retired {
				bits = 100_000_000
			}
			if err := json.Unmarshal([]byte(shares[0].Outstanding), &outstanding); err != nil || outstanding.Upload.BitsPerSecond != bits || outstanding.Download.BitsPerSecond != bits || shares[0].Revision != 1 {
				t.Fatalf("outstanding rate discarded=%+v err=%v", outstanding, err)
			}
		})
	}
}

func TestFleetAdmissionFailedOrExpiredIntentCannotCommitAuthorityPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	demand, lease := f.demand(t, 0), f.claim(t, 0)
	failure := errors.New("publication failed")
	_, err := WithFleetAdmissionDemandResult(t.Context(), f.f.db, lease, demand, func(ctx context.Context, tx *gorm.DB, authorized FleetAdmissionAuthorization) (FleetAdmissionResult, error) {
		result, err := allocateFleetAdmission(ctx, tx, authorized)
		if err != nil {
			return result, err
		}
		return result, failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("allocation failure=%v", err)
	}
	for _, table := range []any{&model.FleetGrant{}, &model.FleetRateShare{}, &model.FleetAuthorityUpdate{}} {
		var count int64
		if err := f.f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("failed authority committed %T count=%d err=%v", table, count, err)
		}
	}
	now := time.Now().UnixMilli()
	if err := f.f.db.Model(&model.FleetAdmissionDemand{}).Where("placement_id = ?", demand.PlacementID).Updates(map[string]any{"received_unix_milli": now - 100, "deadline_unix_milli": now - 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := WithFleetAdmissionDemandResult(t.Context(), f.f.db, lease, demand, allocateFleetAdmission); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("expired demand allocated=%v", err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = (SELECT budget_id FROM fleet_terms WHERE id = ?)", demand.TermID).Error; err != nil || budget.ReservedBytes != 0 {
		t.Fatalf("failed or expired intent leaked quota=%+v err=%v", budget, err)
	}
}

func TestFleetAdmissionExistingCreditRefillDoesNotRepublishAuthorityPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	demand, lease := f.demand(t, 0), f.claim(t, 0)
	f.process(t, lease, demand, FleetAdmissionSatisfied)
	initial := f.delivery(t, 0)
	if len(initial.Updates) != 1 {
		t.Fatalf("initial authority=%+v", initial)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], initial); err != nil {
		t.Fatal(err)
	}
	credit, err := allocateFleetCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: demand.PlacementID, TermID: demand.TermID, ExpectedPlacementRevision: uint64(demand.PlacementRevision),
		ExpectedCredentialGeneration: uint64(demand.CredentialGeneration), Bytes: 100,
	}, true)
	if err != nil || credit.Grant.IssuedBytes != 64<<20+100 || credit.Rates.Revision != 1 {
		t.Fatalf("credit-only refill=%+v err=%v", credit, err)
	}
	f.process(t, lease, f.demand(t, 0), FleetAdmissionSatisfied)
	if batch := f.delivery(t, 0); len(batch.Updates) != 0 {
		t.Fatalf("unchanged rates republished credit-only update=%+v", batch)
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", credit.Grant.ID).Error; err != nil || grant.IssuedBytes != credit.Grant.IssuedBytes || grant.Revision != credit.Grant.Revision {
		t.Fatalf("existing authority reserved again after refill=%+v err=%v", grant, err)
	}
}

func TestFleetAdmissionExpiredRateIntentCannotChangeHolderPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	first, leaseA := f.demand(t, 0), f.claim(t, 0)
	f.process(t, leaseA, first, FleetAdmissionSatisfied)
	if err := f.f.store.ReleaseAdmissionNode(t.Context(), leaseA); err != nil {
		t.Fatal(err)
	}
	second, leaseB := f.demand(t, 1), f.claim(t, 1)
	f.process(t, leaseB, second, FleetAdmissionAwaitingRate)
	if err := f.f.store.WakeRateReconciliationNodes(t.Context()); err != nil {
		t.Fatal(err)
	}
	leaseA = f.claim(t, 0)
	jobs, err := f.f.store.DueRateReconciliations(t.Context(), leaseA)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("pending holder reconciliation=%+v err=%v", jobs, err)
	}
	now := time.Now().UnixMilli()
	if err := f.f.db.Model(&model.FleetAdmissionDemand{}).Where("placement_id = ?", second.PlacementID).Updates(map[string]any{"received_unix_milli": now - 100, "deadline_unix_milli": now - 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := reconcileFleetAdmissionRate(t.Context(), f.f.db, leaseA, jobs[0]); err != nil {
		t.Fatal(err)
	}
	var share model.FleetRateShare
	if err := f.f.db.First(&share, "id = ?", jobs[0].GrantID).Error; err != nil || share.Revision != 1 {
		t.Fatalf("expired demand changed another node's authority=%+v err=%v", share, err)
	}
	var remaining model.FleetRateReconciliation
	if err := f.f.db.First(&remaining, "grant_id = ?", jobs[0].GrantID).Error; err != nil || remaining.Pending {
		t.Fatalf("expired rate intent was not settled=%+v err=%v", remaining, err)
	}
}

func TestFleetAdmissionRateParticipantLimitRejectsCompleteSnapshotPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	first, leaseA := f.demand(t, 0), f.claim(t, 0)
	f.process(t, leaseA, first, FleetAdmissionSatisfied)
	if err := f.f.store.ReleaseAdmissionNode(t.Context(), leaseA); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Exec(`INSERT INTO fleet_grants (id,placement_id,node_id,installation_id,runtime_id,client_id,term_id,unlimited,issued_bytes,consumed_bytes,revision)
SELECT md5(? || n::text)::uuid,?,?,?,?,?,?,FALSE,0,0,1 FROM generate_series(1,?) AS n`, uuid.NewString(), first.PlacementID, first.NodeID, first.InstallationID, first.RuntimeID, first.ClientID, first.TermID, fleetRateParticipantLimit).Error; err != nil {
		t.Fatal(err)
	}
	zero, err := json.Marshal(controlplane.Rates{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Exec(`INSERT INTO fleet_rate_shares (id,budget_id,node_id,installation_id,runtime_id,revision,applied_revision,desired,outstanding)
SELECT g.id,g.client_id,g.node_id,g.installation_id,g.runtime_id,1,1,?::jsonb,?::jsonb FROM fleet_grants g
WHERE NOT EXISTS (SELECT 1 FROM fleet_rate_shares s WHERE s.id = g.id)`, string(zero), string(zero)).Error; err != nil {
		t.Fatal(err)
	}
	second, lease := f.demand(t, 0), f.claim(t, 0)
	_, err = WithFleetAdmissionDemandResult(t.Context(), f.f.db, lease, second, allocateFleetAdmission)
	if !errors.Is(err, controlplane.ErrInvalid) {
		t.Fatalf("truncated outstanding snapshot was allocated: %v", err)
	}
	var grants, updates int64
	if err := f.f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != fleetRateParticipantLimit+1 {
		t.Fatalf("participant overflow issued quota=%d err=%v", grants, err)
	}
	if err := f.f.db.Model(&model.FleetAuthorityUpdate{}).Count(&updates).Error; err != nil || updates != 1 {
		t.Fatalf("participant overflow published authority=%d err=%v", updates, err)
	}
}

func TestFleetAdmissionSchedulerProcessesFreshDemandAndJoinsCancellationPostgres(t *testing.T) {
	f := newAdmissionAllocationFixture(t, `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`)
	demand := f.demand(t, 0)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- RunFleetAdmissionScheduler(ctx, f.f.db) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var current model.FleetAdmissionDemand
		if err := f.f.db.First(&current, "placement_id = ?", demand.PlacementID).Error; err != nil {
			t.Fatal(err)
		}
		if !current.Pending && current.Outcome == string(FleetAdmissionSatisfied) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduler did not consume fresh demand=%+v", current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler cancellation=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler failed to join workers")
	}
	var queue model.FleetAdmissionNodeQueue
	if err := f.f.db.First(&queue, "node_id = ?", demand.NodeID).Error; err != nil || queue.ClaimID != nil || queue.LeaseUntilUnixMilli != 0 {
		t.Fatalf("cancelled scheduler retained lease=%+v err=%v", queue, err)
	}
}
