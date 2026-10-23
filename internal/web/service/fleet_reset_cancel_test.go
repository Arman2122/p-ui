package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestFleetScheduledResetCancellationKeepsOriginalAllowanceAndMandatoryStopsPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	schedule := dueFleetResetFixture(t, f)
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil {
		t.Fatal(err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	request := webruntime.ResetScheduleCancelRequest{OperationID: uuid.NewString(), ClientID: schedule.ClientID, PendingOperationID: *schedule.PendingOperationID, ExpectedRevision: uint64(schedule.Revision)}
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	webruntime.SetManager(nil)
	if _, err := CancelFleetResetSchedule(t.Context(), &f.f.admin, request); !errors.Is(err, webruntime.ErrFleetDispatcher) {
		t.Fatalf("cancellation escaped owned dispatcher=%v", err)
	}
	useFleetEnableAPIDispatcher(t, f.f)
	wrong := request
	wrong.PendingOperationID = uuid.NewString()
	if _, err := CancelFleetResetSchedule(t.Context(), &f.f.admin, wrong); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("cancelled a different pending reset=%v", err)
	}
	injected := errors.New("injected cancellation queue failure")
	callback := "test:cancel_reset_queue"
	if err := f.f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "fleet_source_works" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, cancelErr := CancelFleetResetSchedule(t.Context(), &f.f.admin, request)
	_ = f.f.db.Callback().Create().Remove(callback)
	if !errors.Is(cancelErr, injected) {
		t.Fatalf("cancellation ignored failed stop persistence=%v", cancelErr)
	}
	var client model.FleetClient
	if err := f.f.db.First(&client, "id = ?", schedule.ClientID).Error; err != nil || client.PendingTermID == nil {
		t.Fatalf("failed cancellation erased pending term=%+v err=%v", client, err)
	}
	pendingTermID := *client.PendingTermID
	if err := f.f.db.Model(&f.f.client).Update("uuid", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	cancelled, err := CancelFleetResetSchedule(t.Context(), &f.f.admin, request)
	if err != nil || cancelled.PendingOperationID != "" || cancelled.Revision != request.ExpectedRevision+1 || cancelled.Diagnostic != "RESET_CANCELLED" || cancelled.NextDueUnixMilli <= schedule.NextDueUnixMilli {
		t.Fatalf("administrator could not cancel source-drifted reset=%+v err=%v", cancelled, err)
	}
	if err := f.f.db.First(&client, "id = ?", schedule.ClientID).Error; err != nil || client.PendingTermID != nil || client.CurrentTermID == nil || *client.CurrentTermID != f.active.Term.ID {
		t.Fatalf("cancellation replaced original term=%+v err=%v", client, err)
	}
	var old, abandoned model.FleetTerm
	if err := f.f.db.First(&old, "id = ?", f.active.Term.ID).Error; err != nil || old.State != "active" {
		t.Fatalf("cancellation did not restore original term=%+v err=%v", old, err)
	}
	if err := f.f.db.First(&abandoned, "id = ?", pendingTermID).Error; err != nil || abandoned.State != "closed" {
		t.Fatalf("cancelled allowance stayed pending=%+v err=%v", abandoned, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", old.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 300 || budget.LimitBytes != 1000 {
		t.Fatalf("cancellation replenished or billed old allowance=%+v err=%v", budget, err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil || !work.Pending || !work.RequiresStop || !work.Enabled {
		t.Fatalf("cancellation removed mandatory stop=%+v err=%v", work, err)
	}
	var placement model.FleetPlacement
	if err := f.f.db.First(&placement, "id = ?", work.PlacementID).Error; err != nil || placement.CurrentGrantID == nil || *placement.CurrentGrantID != f.credit.Grant.ID {
		t.Fatalf("cancellation discarded retained grant accounting=%+v err=%v", placement, err)
	}
	replayed, err := CancelFleetResetSchedule(t.Context(), &f.f.admin, request)
	if err != nil || !replayed.Replayed || replayed.Revision != cancelled.Revision || replayed.NextDueUnixMilli != cancelled.NextDueUnixMilli {
		t.Fatalf("lost cancellation response was not idempotent=%+v err=%v", replayed, err)
	}
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("old scheduled work survived cancellation=%v", err)
	}
	var original model.FleetOperation
	if err := f.f.db.First(&original, "id = ?", request.PendingOperationID).Error; err != nil {
		t.Fatal(err)
	}
	var record fleetScheduledResetRecord
	if err := json.Unmarshal(original.Result, &record); err != nil || record.CancelledByOperationID != request.OperationID {
		t.Fatalf("original reset receipt lost its cancellation link=%+v err=%v", record, err)
	}
}
