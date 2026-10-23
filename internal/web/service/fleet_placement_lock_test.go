package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type placementFenceProbe struct{}

func TestFleetPlacementRemovalLocksAuthorityFenceBeforeNodePostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	f.applyBaseline(t)
	f.rule.Enabled, f.rule.ExpectedRevision = false, 1
	if _, err := f.service.PutRule(t.Context(), &f.f.admin, f.rule); err != nil {
		t.Fatal(err)
	}
	f.derive(t)
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	failure := "placement_first_compile_failure"
	if err := f.f.db.Callback().Create().Before("gorm:create").Register(failure, func(tx *gorm.DB) {
		if tx.Statement.Table == "fleet_network_compilations" {
			tx.AddError(ErrFleetNetworkDependency)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if removeErr := f.f.db.Callback().Create().Remove(failure); removeErr != nil {
		t.Fatal(removeErr)
	}
	if !errors.Is(err, ErrFleetNetworkDependency) {
		t.Fatalf("first removal attempt did not retain retry: %v", err)
	}
	if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil || f.intent.RequiresStop || !f.intent.Pending {
		t.Fatalf("actual stop proof was not retained before retry: %+v %v", f.intent, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	held := f.f.db.WithContext(ctx).Begin()
	if held.Error != nil {
		cancel()
		t.Fatal(held.Error)
	}
	var workers sync.WaitGroup
	defer func() { cancel(); _ = held.Rollback().Error; workers.Wait() }()
	var fence model.FleetFence
	if err := held.Clauses(clause.Locking{Strength: "UPDATE"}).First(&fence, "id = 1").Error; err != nil {
		t.Fatal(err)
	}
	entered, done := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	callback := "placement_removal_fence_order"
	if err := f.f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		probe, _ := tx.Statement.Context.Value(placementFenceProbe{}).(bool)
		lock, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if probe && tx.Statement.Table == "fleet_fences" && ok && lock.Strength == "SHARE" {
			once.Do(func() { close(entered) })
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.f.db.Callback().Query().Remove(callback) })
	workers.Go(func() {
		_, err := publishFleetPlacementRemoval(context.WithValue(ctx, placementFenceProbe{}, true), f.f.db, f.intent)
		done <- err
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("placement removal did not reach authority fence")
	}
	probeErr := f.f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node model.FleetNode
		return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).First(&node, "id = ?", f.intent.NodeID).Error
	})
	if probeErr != nil {
		t.Errorf("placement removal acquired node before authority fence: %v", probeErr)
	}
	if err := held.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("placement removal did not resume after fence release: %v", err)
	}
}
