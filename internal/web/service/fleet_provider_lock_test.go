package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type providerFenceProbe struct{}

func TestFleetProviderRolloutLocksAuthorityFenceBeforeNode(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.refresh(t)
	lease := uuid.NewString()
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", f.f.nodes[0].NodeID).
		Updates(map[string]any{"lease_id": lease, "lease_until_unix_milli": f.clock.Add(time.Minute).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	work := f.work(t)
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
	callback := "provider-fence-order"
	if err := f.f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		probe, _ := tx.Statement.Context.Value(providerFenceProbe{}).(bool)
		lock, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if probe && tx.Statement.Table == "fleet_fences" && ok && lock.Strength == "SHARE" {
			once.Do(func() { close(entered) })
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.f.db.Callback().Query().Remove(callback) })
	workers.Go(func() { done <- f.service.processRollout(context.WithValue(ctx, providerFenceProbe{}, true), work) })
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("provider rollout did not reach authority fence")
	}
	probeErr := f.f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node model.FleetNode
		return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).First(&node, "id = ?", f.f.nodes[0].NodeID).Error
	})
	if probeErr != nil {
		t.Errorf("provider rollout acquired node before authority fence: %v", probeErr)
	}
	if err := held.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("provider rollout did not resume after fence release: %v", err)
	}
}
