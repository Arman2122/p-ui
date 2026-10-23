package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type placementRuleInsertionContext struct{}

func TestFleetPlacementRuleInsertionSerializesWithRetirementPublicationPostgres(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), placementRuleInsertionContext{}, true), 10*time.Second)
	defer cancel()
	application := "placement-insert-" + uuid.NewString()
	started := make(chan struct{})
	completed := make(chan error, 1)
	var parentEntered, publicationEntered, blocked atomic.Bool
	parentCallback := "placement_rule_parent_lock"
	publicationCallback := "placement_rule_publication_lock"
	if err := f.f.db.Callback().Query().Before("gorm:query").Register(parentCallback, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "policies" || tx.Statement.Context.Value(placementRuleInsertionContext{}) != true || !parentEntered.CompareAndSwap(false, true) {
			return
		}
		if err := tx.Session(&gorm.Session{NewDB: true}).Exec("SELECT set_config('application_name', ?, true)", application).Error; err != nil {
			tx.AddError(err)
		}
		close(started)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.f.db.Callback().Query().Remove(parentCallback) })
	if err := f.f.db.Callback().Create().Before("gorm:create").Register(publicationCallback, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "fleet_network_publications" || !publicationEntered.CompareAndSwap(false, true) {
			return
		}
		go func() {
			request := f.rule
			request.ID, request.ExpectedRevision, request.Enabled = uuid.NewString(), 0, true
			_, err := f.service.PutRule(ctx, &f.f.admin, request)
			completed <- err
		}()
		select {
		case <-started:
		case <-ctx.Done():
			tx.AddError(ctx.Err())
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case err := <-completed:
				tx.AddError(fmt.Errorf("matching rule insertion crossed retirement publication: %w", err))
				return
			default:
			}
			var waiting int64
			if err := f.f.db.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock'", application).Scan(&waiting).Error; err != nil {
				tx.AddError(err)
				return
			}
			if waiting == 1 {
				blocked.Store(true)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		tx.AddError(errors.New("matching rule insertion did not wait for retirement publication"))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.f.db.Callback().Create().Remove(publicationCallback) })
	current, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || !publicationEntered.Load() || !blocked.Load() || current.State != "awaiting-apply" {
		t.Fatalf("retirement/new-rule ordering failed: state=%+v publication=%v blocked=%v err=%v", current, publicationEntered.Load(), blocked.Load(), err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.derive(t)
	if !f.intent.Assigned || !f.intent.Pending || f.intent.Revision <= current.Revision {
		t.Fatalf("new matching rule was lost after earlier removal publication: %+v", f.intent)
	}
}
