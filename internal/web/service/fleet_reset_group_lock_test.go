package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type resetGroupClient struct {
	fixture  fleetClientFixture
	schedule model.FleetResetSchedule
	used     int64
}

func groupedResetClients(t *testing.T, existing bool) (*gorm.DB, model.ClientGroup, []resetGroupClient) {
	t.Helper()
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "scheduled-reset-group"}
	if existing {
		group.ResetUp, group.ResetDown = 1000, 2000
		if err := f.db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	f.client.Group = group.Name
	other := f.client
	other.Id, other.Email, other.UUID = 0, "scheduled-reset-other", uuid.NewString()
	if err := f.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: other.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: other.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: other.TotalGB, ExpiryTime: other.ExpiryTime, Reset: other.Reset}).Error; err != nil {
		t.Fatal(err)
	}
	fixtures := []fleetClientFixture{f, f}
	fixtures[1].client = other
	var clients []resetGroupClient
	for index, fixture := range fixtures {
		up, down := int64(11+20*index), int64(17+20*index)
		if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", fixture.client.Email).Updates(map[string]any{"up": up, "down": down}).Error; err != nil {
			t.Fatal(err)
		}
		materialized := fixture.materialize(t, index)
		materialized.Placement = fixture.approve(t, materialized)
		active := fixture.activate(t, materialized.Placement)
		schedule := dueFleetResetFixture(t, authorityFixture{f: fixture, materialized: materialized, active: active})
		started, err := advanceFleetResetSchedule(t.Context(), f.db, schedule)
		if err != nil || started.PendingOperationID == "" {
			t.Fatalf("prepare grouped reset: %+v %v", started, err)
		}
		clients = append(clients, resetGroupClient{fixture: fixture, schedule: readFleetResetSchedule(t, f.db, schedule.ClientID), used: up + down})
	}
	return f.db, group, clients
}

type resetGroupCompletionKey struct{}

func TestFleetScheduledResetGroupCompletionsSerializePostgres(t *testing.T) {
	db, group, clients := groupedResetClients(t, true)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(5)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	held := db.WithContext(ctx).Begin()
	if held.Error != nil {
		cancel()
		t.Fatal(held.Error)
	}
	var locked model.ClientGroup
	if err := held.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "name = ?", group.Name).Error; err != nil {
		_ = held.Rollback().Error
		cancel()
		t.Fatal(err)
	}
	var tasks sync.WaitGroup
	defer func() { cancel(); _ = held.Rollback().Error; tasks.Wait() }()
	pids := make(chan int, 2)
	var entered sync.Map
	callback := "test:reset_group_before_lock"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		owner, _ := tx.Statement.Context.Value(resetGroupCompletionKey{}).(string)
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if owner == "" || tx.Statement.Table != "client_groups" || !ok || locking.Strength != "SHARE" && locking.Strength != "UPDATE" {
			return
		}
		if _, loaded := entered.LoadOrStore(owner, true); loaded {
			return
		}
		var pid int
		if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
			tx.AddError(err)
			return
		}
		pids <- pid
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	type completed struct {
		view FleetResetScheduleView
		err  error
	}
	results := make(chan completed, 2)
	for _, client := range clients {
		tasks.Go(func() {
			work := context.WithValue(ctx, resetGroupCompletionKey{}, client.schedule.ClientID)
			view, err := advanceFleetResetSchedule(work, db, client.schedule)
			results <- completed{view, err}
		})
	}
	var backends []int
	for range clients {
		select {
		case pid := <-pids:
			backends = append(backends, pid)
		case result := <-results:
			t.Fatalf("reset ended before group lock: %+v %v", result.view, result.err)
		case <-ctx.Done():
			t.Fatal("both completions did not reach group lock")
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int64
		if err := db.WithContext(ctx).Raw("SELECT count(*) FROM pg_stat_activity WHERE pid IN ? AND wait_event_type = 'Lock'", backends).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("both group queries did not wait behind holder")
		}
	}
	if err := held.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	for range clients {
		result := <-results
		if result.err != nil || result.view.PendingOperationID != "" {
			t.Errorf("grouped reset completion failed: %+v %v", result.view, result.err)
		}
	}
	tasks.Wait()
	if t.Failed() {
		return
	}
	assertGroupedResetAccounting(t, db, group, clients)
}

func assertGroupedResetAccounting(t *testing.T, db *gorm.DB, before model.ClientGroup, clients []resetGroupClient) {
	t.Helper()
	var group model.ClientGroup
	if err := db.First(&group, "name = ?", before.Name).Error; err != nil || group.ResetUp != before.ResetUp || group.ResetDown != before.ResetDown {
		t.Fatalf("group reset baseline was reset or multiplied: %+v %v", group, err)
	}
	for _, client := range clients {
		schedule := readFleetResetSchedule(t, db, client.schedule.ClientID)
		if schedule.PendingOperationID != nil || !sameFleetResetID(schedule.LastCompletedOperationID, client.schedule.PendingOperationID) {
			t.Fatalf("completion lost original reset receipt: %+v", schedule)
		}
		var provenance model.FleetTermGroup
		if schedule.BoundTermID == nil {
			t.Fatal("scheduled reset lost its completed term")
		}
		if err := db.First(&provenance, "term_id = ?", *schedule.BoundTermID).Error; err != nil || provenance.GroupID == nil || *provenance.GroupID != group.Id {
			t.Fatalf("scheduled reset lost immutable group provenance: %+v %v", provenance, err)
		}
		if traffic := readFleetTraffic(t, client.fixture); traffic.Up != 0 || traffic.Down != 0 {
			t.Fatalf("completed reset retained current counters: %+v", traffic)
		}
		var previous model.FleetTerm
		if err := db.First(&previous, "id = ?", *client.schedule.BoundTermID).Error; err != nil || previous.State != "closed" {
			t.Fatalf("old term remained active: %+v %v", previous, err)
		}
		var budget model.FleetBudget
		if err := db.First(&budget, "id = ?", previous.BudgetID).Error; err != nil || budget.UsedBytes != client.used || budget.ReservedBytes != 0 {
			t.Fatalf("reset rewrote old accounting: %+v %v", budget, err)
		}
	}
}

func TestFleetScheduledResetCreatesMissingGroupWithoutChangingScopePostgres(t *testing.T) {
	db, group, clients := groupedResetClients(t, false)
	for _, client := range clients {
		if _, err := advanceFleetResetSchedule(t.Context(), db, client.schedule); err != nil {
			t.Fatal(err)
		}
	}
	assertGroupedResetAccounting(t, db, group, clients)
	for name, relation := range map[string]any{"quota": &model.GroupQuota{}, "inbounds": &model.GroupInbound{}} {
		var count int64
		if err := db.Model(relation).Where("name = ?", group.Name).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("missing group lock changed %s: count=%d %v", name, count, err)
		}
	}
	for _, client := range clients {
		var source model.ClientRecord
		if err := db.First(&source, client.fixture.client.Id).Error; err != nil || source.Group != group.Name || source.TotalGB != client.fixture.client.TotalGB {
			t.Fatalf("missing group lock changed membership or quota: %+v %v", source, err)
		}
		var attached int64
		if err := db.Model(&model.ClientInbound{}).Where("client_id = ?", source.Id).Count(&attached).Error; err != nil || attached != 2 {
			t.Fatalf("missing group lock changed attachments: count=%d error=%v", attached, err)
		}
	}
}
