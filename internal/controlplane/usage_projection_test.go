package controlplane

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetUsageProjectionConcurrentReplayAndRollbackPostgres(t *testing.T) {
	s, db, nodes, _, _, _ := setupLedger(t)
	client := managedClient(t, s, db)
	request := activation(client)
	request.Spec.OpeningUsedBytes, request.Spec.OpeningUploaded, request.Spec.OpeningDownloaded = 100, 40, 60
	active, err := s.Transition(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	a := reserve(t, s, nodes[0], client.ID, active.Term.ID, []string{active.Term.BudgetID}, 400)
	b := reserve(t, s, nodes[1], client.ID, active.Term.ID, []string{active.Term.BudgetID}, 400)
	first, second := batch(nodes[0], a, 1, 50, 30), batch(nodes[1], b, 1, 20, 60)
	first.Checkpoints[0].ObservedUnixMilli = time.Now().Add(time.Hour).UnixMilli()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			principal, report := nodes[0], first
			if i%2 != 0 {
				principal, report = nodes[1], second
			}
			if _, err := s.Ingest(t.Context(), principal, report); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var usage model.FleetTermUsage
	if err := db.First(&usage, "term_id = ?", active.Term.ID).Error; err != nil || usage.Uploaded != 70 || usage.Downloaded != 90 || usage.ObservedUnixMilli > usage.ReceivedUnixMilli || usage.ReceivedUnixMilli == 0 {
		t.Fatalf("replayed projection=%+v err=%v", usage, err)
	}
	var history []model.FleetTermUsageDay
	if err := db.Where("term_id = ?", active.Term.ID).Find(&history).Error; err != nil || len(history) != 1 || history[0].Uploaded != 70 || history[0].Downloaded != 90 {
		t.Fatalf("receipt-day history includes duplicate or opening traffic=%+v err=%v", history, err)
	}
	budget(t, db, active.Term.BudgetID, 260, 640)
	if _, err := s.Ingest(t.Context(), nodes[0], batch(nodes[0], a, 2, 400, 100)); !errors.Is(err, ErrBudget) {
		t.Fatalf("over-credit report=%v", err)
	}
	var unchanged model.FleetTermUsage
	if err := db.First(&unchanged, "term_id = ?", active.Term.ID).Error; err != nil || unchanged != usage {
		t.Fatalf("rejected report changed projection=%+v err=%v", unchanged, err)
	}
	budget(t, db, active.Term.BudgetID, 260, 640)
	if _, err := s.Ingest(t.Context(), nodes[0], batch(nodes[0], a, 2, 60, 40)); err != nil {
		t.Fatalf("failed report advanced the receipt cursor=%v", err)
	}
	if err := db.First(&usage, "term_id = ?", active.Term.ID).Error; err != nil || usage.Uploaded != 80 || usage.Downloaded != 100 {
		t.Fatalf("retry projection=%+v err=%v", usage, err)
	}
}
