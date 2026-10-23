package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetScheduledResetUsesClientPolicyAfterAllInboundAttachmentsAreRemovedPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	denied := []int{}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, FleetPlacementPreferenceRequest{
		OperationID: uuid.NewString(), ClientID: f.materialized.Client.ID, ExpectedRevision: 1, Action: "restrict", InboundIDs: &denied,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Where("client_id = ?", f.f.client.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: f.active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 1,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	}); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("billing snapshot relaxed actual attachment authorization=%v", err)
	}
	schedule := dueFleetResetFixture(t, f)
	if started, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil || started.PendingOperationID == "" {
		t.Fatalf("detached placement prevented client-owned cadence=%+v err=%v", started, err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("detachment erased outstanding authority=%v", err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	work, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || batch == nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
		t.Fatalf("detached-client stop delivery=%+v err=%v", batch, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx)
	if err != nil {
		t.Fatal(err)
	}
	if work.Pending || work.RequiresStop {
		t.Fatalf("unassigned placement remained enabled after actual stop=%+v", work)
	}
	if completed, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil || completed.PendingOperationID != "" {
		t.Fatalf("detached client could not complete its authorized reset=%+v err=%v", completed, err)
	}
	if err := f.f.db.First(&work, "placement_id = ?", work.PlacementID).Error; err != nil || work.Pending || work.RequiresStop {
		t.Fatalf("reset reattached a restricted target=%+v err=%v", work, err)
	}
	var grants int64
	if err := f.f.db.Model(&model.FleetGrant{}).Where("client_id = ?", schedule.ClientID).Count(&grants).Error; err != nil || grants != 1 {
		t.Fatalf("unplaced reset created forwarding credit=%d err=%v", grants, err)
	}
}
