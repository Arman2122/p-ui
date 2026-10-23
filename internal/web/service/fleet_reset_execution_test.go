package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func dueFleetResetFixture(t *testing.T, f authorityFixture) model.FleetResetSchedule {
	t.Helper()
	now := time.Now()
	if err := f.f.db.Model(&model.FleetTerm{}).Where("id = ?", f.active.Term.ID).Update("started_unix_milli", now.Add(-26*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	previousClock := fleetResetNow
	fleetResetNow = func() time.Time { return now.Add(-24*time.Hour - 30*time.Minute) }
	_, err := configureFleetResetSchedule(t.Context(), f.f.db, &f.f.admin, FleetResetScheduleRequest{
		OperationID: uuid.NewString(), ClientID: f.materialized.Client.ID, Override: true, Cycle: "hourly",
	})
	fleetResetNow = previousClock
	if err != nil {
		t.Fatal(err)
	}
	return readFleetResetSchedule(t, f.f.db, f.materialized.Client.ID)
}

func readFleetResetSchedule(t *testing.T, db *gorm.DB, clientID string) model.FleetResetSchedule {
	t.Helper()
	var row model.FleetResetSchedule
	if err := db.First(&row, "client_id = ?", clientID).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func resetAuthorityRequest(t *testing.T, f fleetClientFixture, principal identity.Principal, revision uint64, clientID string) *nodev1.AuthorityDeliveryRequest {
	t.Helper()
	var agent model.FleetNodeJournal
	var runtime model.FleetRuntimeJournal
	if err := f.db.First(&agent, "node_id = ?", principal.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&runtime, "node_id = ? AND runtime_id = ?", principal.NodeID, "xray").Error; err != nil {
		t.Fatal(err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.db, principal.NodeID, revision, "xray", clientID)
	if err != nil {
		t.Fatal(err)
	}
	return &nodev1.AuthorityDeliveryRequest{
		NodeId: principal.NodeID, InstallationId: principal.InstallationID, AgentStateJournalId: *agent.StateJournalID,
		RuntimeId: "xray", StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID,
		BaseConfigurationRevision: revision, BasePayloadSha256: scope.PayloadSHA256,
	}
}

func TestFleetScheduledResetResumesAcrossMissedCyclesAndRequiresEveryAppliedStopPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	active := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], active); err != nil {
		t.Fatal(err)
	}
	second := f.f.approve(t, f.f.materialize(t, 1))
	credit, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: second.ID, TermID: f.active.Term.ID,
		ExpectedPlacementRevision: uint64(second.Revision), ExpectedCredentialGeneration: uint64(second.CredentialGeneration), Bytes: 300,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 100_000_000, BurstBytes: 1 << 20}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 100_000_000, BurstBytes: 1 << 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyCredit(t, f.f, f.active.Term, credit)
	fleetAuthorityReportAtNode(t, f.f, 1, 1, time.Now())
	requests := []*nodev1.AuthorityDeliveryRequest{f.request, resetAuthorityRequest(t, f.f, f.f.nodes[1], revision, second.ClientID)}
	fleetUsageReport(t, f.f, 0, f.credit.Grant, 1, 10, 20)
	schedule := dueFleetResetFixture(t, f)
	started, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule)
	if err != nil || started.PendingOperationID == "" {
		t.Fatalf("scheduled reset did not persist pending operation=%+v err=%v", started, err)
	}
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("stale due selection created another reset=%v", err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	var client model.FleetClient
	if err := f.f.db.First(&client, "id = ?", schedule.ClientID).Error; err != nil || client.PendingTermID == nil {
		t.Fatalf("reset has no pending term=%+v err=%v", client, err)
	}
	pendingID := *client.PendingTermID
	for index, request := range requests {
		if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, controlplane.ErrDrain) {
			t.Fatalf("reset completed before every actual applied stop=%v index=%d", err, index)
		}
		work, err := PendingFleetSourceWork(t.Context(), f.f.db, request.NodeId, 128)
		if err != nil || len(work) != 1 || !work[0].RequiresStop {
			t.Fatalf("reset did not persist source stop work=%+v err=%v", work, err)
		}
		queued, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], StopFleetSourceWorkTx)
		if err != nil || !queued.RequiresStop {
			t.Fatalf("reset source stop publication=%+v err=%v", queued, err)
		}
		batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[index], request, ValidateFleetAuthorityDeliveryTx)
		if err != nil || batch == nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
			t.Fatalf("reset disabled authority=%+v err=%v index=%d", batch, err, index)
		}
		if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, controlplane.ErrDrain) {
			t.Fatalf("sending stop substituted for applied receipt=%v", err)
		}
		if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[index], batch); err != nil {
			t.Fatal(err)
		}
		if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, queued, StopFleetSourceWorkTx); err != nil {
			t.Fatal(err)
		}
	}
	storageFailure := errors.New("injected reset completion storage failure")
	callback := "test:scheduled_reset_projection"
	if err := f.f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "client_traffics" {
			tx.AddError(storageFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, finishErr := advanceFleetResetSchedule(t.Context(), f.f.db, schedule)
	_ = f.f.db.Callback().Update().Remove(callback)
	if !errors.Is(finishErr, storageFailure) {
		t.Fatalf("completion ignored failed projection=%v", finishErr)
	}
	var pending model.FleetTerm
	if err := f.f.db.First(&pending, "id = ?", pendingID).Error; err != nil || pending.State != "pending" {
		t.Fatalf("failed projection committed new allowance=%+v err=%v", pending, err)
	}
	completed, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule)
	if err != nil || completed.PendingOperationID != "" || completed.AnchorUnixMilli != schedule.AnchorUnixMilli || completed.NextDueUnixMilli <= time.Now().UnixMilli() {
		t.Fatalf("reset did not resume original anchored operation=%+v err=%v", completed, err)
	}
	current := readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	if current.BoundTermID == nil || *current.BoundTermID != pendingID || current.LastCompletedOperationID == nil || *current.LastCompletedOperationID != started.PendingOperationID {
		t.Fatalf("completion lost original receipt=%+v", current)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", f.active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 30 || budget.ReservedBytes != 570 {
		t.Fatalf("reset fabricated usage or reclaimed unknown old credit=%+v err=%v", budget, err)
	}
	var placements []model.FleetPlacement
	if err := f.f.db.Where("client_id = ?", schedule.ClientID).Find(&placements).Error; err != nil || len(placements) != 2 {
		t.Fatalf("reset placements=%+v err=%v", placements, err)
	}
	for _, placement := range placements {
		if placement.CurrentGrantID != nil || placement.CurrentGrantTermID != nil || placement.Enabled || len(placement.GraphDigest) != 0 {
			t.Fatalf("reset left old authority attached=%+v", placement)
		}
		var work model.FleetSourceWork
		if err := f.f.db.First(&work, "placement_id = ?", placement.ID).Error; err != nil || !work.Pending || work.RequiresStop || !work.Enabled {
			t.Fatalf("reset did not queue source reconciliation=%+v err=%v", work, err)
		}
	}
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, current); err != nil {
		t.Fatal(err)
	}
	var terms int64
	if err := f.f.db.Model(&model.FleetTerm{}).Where("client_id = ?", current.ClientID).Count(&terms).Error; err != nil || terms != 2 {
		t.Fatalf("missed periods minted multiple allowances=%d err=%v", terms, err)
	}
}

func TestFleetScheduledResetRollbackAndSourceDriftPreservePendingWorkPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	schedule := dueFleetResetFixture(t, f)
	failure := errors.New("injected scheduled stop queue failure")
	callback := "test:scheduled_stop_queue"
	if err := f.f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "fleet_source_works" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule)
	_ = f.f.db.Callback().Create().Remove(callback)
	if !errors.Is(err, failure) {
		t.Fatalf("stop queue failure=%v", err)
	}
	var term model.FleetTerm
	if err := f.f.db.First(&term, "id = ?", f.active.Term.ID).Error; err != nil || term.State != "active" {
		t.Fatalf("failed stop queue left previous term draining=%+v err=%v", term, err)
	}
	if got := readFleetResetSchedule(t, f.f.db, schedule.ClientID); got.PendingOperationID != nil {
		t.Fatalf("failed stop queue left pending receipt=%+v", got)
	}
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil {
		t.Fatal(err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	if err := f.f.db.Model(&f.f.client).Update("uuid", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("changed credentials completed pending reset=%v", err)
	}
	if got := readFleetResetSchedule(t, f.f.db, schedule.ClientID); !sameFleetResetID(got.PendingOperationID, schedule.PendingOperationID) {
		t.Fatalf("source drift discarded pending reset=%+v", got)
	}
}

func TestFleetResetSchedulePublicMutationRequiresOwnedDispatcherPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	placement := f.approve(t, materialized)
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	webruntime.SetManager(nil)
	request := FleetResetScheduleRequest{OperationID: uuid.NewString(), ClientID: materialized.Client.ID, Override: true, Cycle: "monthly"}
	if _, err := ConfigureFleetResetSchedule(t.Context(), &f.admin, request); !errors.Is(err, webruntime.ErrFleetDispatcher) {
		t.Fatalf("schedule mutation escaped runtime dispatcher=%v", err)
	}
	useFleetEnableAPIDispatcher(t, f)
	view, err := ConfigureFleetResetSchedule(t.Context(), &f.admin, request)
	if err != nil || view.Revision != 1 || view.NextDueUnixMilli != 0 || view.AnchorUnixMilli != 0 {
		t.Fatalf("dormant schedule prematurely started=%+v err=%v", view, err)
	}
	active := f.activate(t, placement)
	schedule := readFleetResetSchedule(t, f.db, placement.ClientID)
	if schedule.BoundTermID == nil || *schedule.BoundTermID != active.Term.ID || schedule.AnchorUnixMilli != active.Term.StartedUnixMilli || schedule.NextDueUnixMilli <= active.Term.StartedUnixMilli {
		t.Fatalf("activation did not initialize owned schedule=%+v", schedule)
	}
}
