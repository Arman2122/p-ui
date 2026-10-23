package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func fleetScheduledCredit(t *testing.T, f fleetClientFixture, placement model.FleetPlacement, term model.FleetTerm) (FleetCredit, *nodev1.GrantAuthority) {
	t.Helper()
	credit, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := fleetApplyCredit(t, f, term, credit)
	return credit, &nodev1.GrantAuthority{
		GrantId: credit.Grant.ID, ClientId: credit.Grant.ClientID, TermId: credit.Grant.TermID, PolicyRevision: base,
		CreditCeiling: 100, ReservedHighWater: 50, AdmissionHighWater: 50, AdmissionKnown: true, SpendableKnown: true, SpendableBytes: 50, AuthorizationActive: true,
	}
}

func TestFleetCreditQueueLeasesCoalesceAndBackOff(t *testing.T) {
	f := fleetFixture(t, false)
	placements := []model.FleetPlacement{f.approve(t, f.materialize(t, 0)), f.approve(t, f.materialize(t, 1))}
	active := f.activate(t, placements[0])
	var credits []FleetCredit
	var observations []*nodev1.GrantAuthority
	for i, placement := range placements {
		credit, report := fleetScheduledCredit(t, f, placement, active.Term)
		credits, observations = append(credits, credit), append(observations, report)
		fleetAuthorityReportAtNode(t, f, i, 1, time.Now(), report)
	}
	var wg sync.WaitGroup
	leases := make(chan model.FleetCreditNodeQueue, 4)
	for range 4 {
		wg.Go(func() {
			claimed, err := f.store.ClaimCreditNodes(t.Context(), 1)
			if err != nil {
				t.Error(err)
				return
			}
			for _, lease := range claimed {
				leases <- lease
			}
		})
	}
	wg.Wait()
	close(leases)
	byNode := make(map[string]model.FleetCreditNodeQueue)
	for lease := range leases {
		if _, duplicate := byNode[lease.NodeID]; duplicate {
			t.Fatal("concurrent schedulers leased one node twice")
		}
		byNode[lease.NodeID] = lease
	}
	if len(byNode) != 2 {
		t.Fatalf("fair node leases=%d", len(byNode))
	}
	lease := byNode[f.nodes[0].NodeID]
	work, err := f.store.DueCreditCandidates(t.Context(), lease)
	if err != nil || len(work) != 1 || work[0].GrantID != credits[0].Grant.ID {
		t.Fatalf("leased candidates=%+v err=%v", work, err)
	}
	if err := f.store.SettleCreditCandidate(t.Context(), lease, work[0], true); err != nil {
		t.Fatal(err)
	}
	var backedOff model.FleetCreditCandidate
	if err := f.db.First(&backedOff, "grant_id = ?", work[0].GrantID).Error; err != nil || backedOff.Attempts != 1 || backedOff.AvailableUnixMilli <= time.Now().UnixMilli() {
		t.Fatalf("candidate retry did not back off=%+v err=%v", backedOff, err)
	}
	fleetAuthorityReportAtNode(t, f, 0, 2, time.Now(), observations[0])
	if err := f.store.SettleCreditCandidate(t.Context(), lease, work[0], false); err != nil {
		t.Fatal(err)
	}
	var coalesced model.FleetCreditCandidate
	if err := f.db.First(&coalesced, "grant_id = ?", work[0].GrantID).Error; err != nil || coalesced.AuthoritySequence != 2 || coalesced.AvailableUnixMilli != backedOff.AvailableUnixMilli || coalesced.Attempts != 1 {
		t.Fatalf("new report erased backoff or old worker erased new demand=%+v err=%v", coalesced, err)
	}
	if err := f.store.ReleaseCreditNode(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if more, err := f.store.ClaimCreditNodes(t.Context(), 4); err != nil || len(more) != 0 {
		t.Fatalf("backoff/leased node hot-looped=%+v err=%v", more, err)
	}
	other := byNode[f.nodes[1].NodeID]
	if err := f.db.Model(&model.FleetCreditNodeQueue{}).Where("node_id = ?", other.NodeID).Update("lease_until_unix_milli", 0).Error; err != nil {
		t.Fatal(err)
	}
	reclaimed, err := f.store.ClaimCreditNodes(t.Context(), 1)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].NodeID != other.NodeID || *reclaimed[0].ClaimID == *other.ClaimID {
		t.Fatalf("crashed scheduler lease did not recover=%+v err=%v", reclaimed, err)
	}
	if err := f.store.ReleaseCreditNode(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	var retained model.FleetCreditNodeQueue
	if err := f.db.First(&retained, "node_id = ?", other.NodeID).Error; err != nil || retained.ClaimID == nil || *retained.ClaimID != *reclaimed[0].ClaimID {
		t.Fatalf("old owner released replacement lease=%+v err=%v", retained, err)
	}
}

func TestFleetCreditSchedulerFundsOnceAndDoesNotSpinOnExhaustion(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	credit, report := fleetScheduledCredit(t, f, placement, active.Term)
	fleetAuthorityReport(t, f, 1, time.Now(), report)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunFleetCreditScheduler(ctx, f.db) }()
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	defer cancel()
	for {
		var grant model.FleetGrant
		if err := f.db.First(&grant, "id = ?", credit.Grant.ID).Error; err != nil {
			t.Fatal(err)
		}
		if grant.IssuedBytes == 1000 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("scheduler exited before funding=%v", err)
		case <-deadline.C:
			t.Fatal("scheduler did not fund bounded candidate")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler shutdown=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler leaked workers after host cancellation")
	}
	var queued model.FleetCreditUpdate
	if err := f.db.First(&queued, "grant_id = ?", credit.Grant.ID).Error; err != nil || queued.CreditCeiling != 1000 || queued.Revision != 2 {
		t.Fatalf("scheduler did not commit one outbox update=%+v err=%v", queued, err)
	}
	var budget model.FleetBudget
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 1000 || budget.UsedBytes != 0 {
		t.Fatalf("scheduler exceeded finite budget=%+v err=%v", budget, err)
	}
	report.CreditCeiling, report.SpendableBytes = 1000, 950
	fleetAuthorityReport(t, f, 2, time.Now(), report)
	leases, err := f.store.ClaimCreditNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("post-delivery candidate=%+v err=%v", leases, err)
	}
	if err := processFleetCreditNode(t.Context(), f.db, f.store, leases[0]); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.FleetCreditCandidate{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("exhausted finite quota kept retrying=%d err=%v", count, err)
	}
	fleetAuthorityReport(t, f, 3, time.Now(), report)
	if err := f.db.Model(&model.FleetCreditCandidate{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unchanged low remaining authority requeued exhaustion=%d err=%v", count, err)
	}
	next, err := f.store.RequeueNodeCredit(t.Context(), f.nodes[0].NodeID, "", 1)
	if err != nil || next != credit.Grant.ID {
		t.Fatalf("explicit configuration reconciliation cursor=%q err=%v", next, err)
	}
	if err := f.db.Model(&model.FleetCreditCandidate{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("unchanged graph could not wake reconciled authority=%d err=%v", count, err)
	}
	if next, err = f.store.RequeueNodeCredit(t.Context(), f.nodes[0].NodeID, next, 1); err != nil || next != "" {
		t.Fatalf("bounded configuration reconciliation did not terminate=%q err=%v", next, err)
	}
}

func TestFleetCreditCandidateDatabaseConflictBacksOffWithoutFunding(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	credit, report := fleetScheduledCredit(t, f, placement, active.Term)
	fleetAuthorityReport(t, f, 1, time.Now(), report)
	for _, statement := range []string{
		`CREATE SEQUENCE fleet_refill_failure_sequence`,
		`CREATE FUNCTION fleet_refill_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('fleet_refill_failure_sequence') = 1 THEN RAISE EXCEPTION 'injected scheduler transaction conflict' USING ERRCODE='40P01'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER fleet_refill_failure BEFORE INSERT ON fleet_operations FOR EACH ROW EXECUTE FUNCTION fleet_refill_failure()`,
	} {
		if err := f.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	leases, err := f.store.ClaimCreditNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("conflict candidate lease=%+v err=%v", leases, err)
	}
	if err := processFleetCreditNode(t.Context(), f.db, f.store, leases[0]); err != nil {
		t.Fatalf("one candidate database conflict stopped scheduler=%v", err)
	}
	var candidate model.FleetCreditCandidate
	if err := f.db.First(&candidate, "grant_id = ?", credit.Grant.ID).Error; err != nil || candidate.Attempts != 1 || candidate.AvailableUnixMilli <= time.Now().UnixMilli() {
		t.Fatalf("database conflict lacked durable backoff=%+v err=%v", candidate, err)
	}
	var grant model.FleetGrant
	if err := f.db.First(&grant, "id = ?", credit.Grant.ID).Error; err != nil || grant.IssuedBytes != 100 || grant.Revision != 1 {
		t.Fatalf("failed candidate leaked funding=%+v err=%v", grant, err)
	}
	var pending int64
	if err := f.db.Model(&model.FleetCreditUpdate{}).Count(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("failed candidate leaked delivery=%d err=%v", pending, err)
	}
}
