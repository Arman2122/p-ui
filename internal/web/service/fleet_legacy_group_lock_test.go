package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetLegacyGroupLocksSourceBeforeParentPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 0, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	held := f.db.WithContext(ctx).Begin()
	if held.Error != nil {
		cancel()
		t.Fatal(held.Error)
	}
	var workers sync.WaitGroup
	defer func() { cancel(); _ = held.Rollback().Error; workers.Wait() }()
	if err := held.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, group.Id).Error; err != nil {
		t.Fatal(err)
	}
	entered, done := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	callback := "legacy-group-parent-order-" + uuid.NewString()
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		lock, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if tx.Statement.Table == "client_groups" && ok && lock.Strength == "UPDATE" {
			once.Do(func() { close(entered) })
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	workers.Go(func() {
		_, err := (&ClientService{}).RenameGroup(group.Name, "ordered-rename")
		done <- err
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("legacy group mutation did not reach parent serialization")
	}
	probeErr := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.ClientRecord
		return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).First(&source, f.client.Id).Error
	})
	var state interface{ SQLState() string }
	if !errors.As(probeErr, &state) || state.SQLState() != "55P03" {
		t.Errorf("legacy group parent was reached without source lock: %v", probeErr)
	}
	if err := held.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("legacy group mutation failed after parent release: %v", err)
	}
}
