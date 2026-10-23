package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetResetSchedulerResumesPersistedDrainAndCancelsWithLifecyclePostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	schedule := dueFleetResetFixture(t, f)
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil {
		t.Fatal(err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 2)
	go func() { done <- RunFleetResetScheduler(ctx, f.f.db) }()
	go func() { done <- RunFleetSourceScheduler(ctx, f.f.db) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("owned reset/source scheduler exit=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("owned reset/source scheduler did not cancel")
			}
		}
	})
	deadline := time.NewTimer(15 * time.Second)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	acknowledged := false
	for {
		current := readFleetResetSchedule(t, f.f.db, schedule.ClientID)
		if current.PendingOperationID == nil {
			if !acknowledged || !sameFleetResetID(current.LastCompletedOperationID, schedule.PendingOperationID) || current.Diagnostic != "" {
				t.Fatalf("scheduler completion lost stop or original operation=%+v ack=%v", current, acknowledged)
			}
			return
		}
		if !acknowledged {
			batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
			if err != nil {
				t.Fatal(err)
			}
			if batch != nil && len(batch.Updates) == 1 && !batch.Updates[0].Enabled {
				if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
					t.Fatal(err)
				}
				acknowledged = true
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("persisted reset did not converge=%+v ack=%v", current, acknowledged)
		case <-ticker.C:
		}
	}
}

func TestFleetResetRetryCannotOverwriteNewPolicyAndDisablePreservesCadencePostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	expected := dueFleetResetFixture(t, f)
	request := FleetResetScheduleRequest{OperationID: uuid.NewString(), ClientID: expected.ClientID, ExpectedRevision: uint64(expected.Revision), Override: true, Cycle: "weekly"}
	changed, err := configureFleetResetSchedule(t.Context(), f.f.db, &f.f.admin, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := deferFleetResetSchedule(t.Context(), f.f.db, expected, controlplane.ErrDrain); err != nil {
		t.Fatal(err)
	}
	row := readFleetResetSchedule(t, f.f.db, expected.ClientID)
	if row.Revision != int64(changed.Revision) || row.NextDueUnixMilli != changed.NextDueUnixMilli || row.ReadyUnixMilli != changed.NextAttemptUnixMilli || row.Diagnostic != "" {
		t.Fatalf("stale reset retry overwrote new policy=%+v", row)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, row.ClientID, false)); err != nil {
		t.Fatal(err)
	}
	disabled := readFleetResetSchedule(t, f.f.db, row.ClientID)
	if disabled.ReadyUnixMilli != 0 || disabled.AnchorUnixMilli != row.AnchorUnixMilli || disabled.NextDueUnixMilli != row.NextDueUnixMilli {
		t.Fatalf("disable discarded cadence=%+v", disabled)
	}
	var client model.FleetClient
	if err := f.f.db.First(&client, "id = ?", row.ClientID).Error; err != nil || client.PendingTermID != nil {
		t.Fatalf("schedule policy minted pending allowance=%+v err=%v", client, err)
	}
}
