package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetProviderConcurrentFirstFanoutMergesTargets(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	second := configureProviderFixture(t, f.service, f.f.admin, f.provider.Fetch.URL)
	if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: second.ProviderID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	baseConfig := FleetRuntimeBase{Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"protocol":"freedom","tag":"direct","settings":{}}]}`), Providers: []FleetProviderBinding{
		{ProviderID: f.provider.ProviderID, Revision: 1, FollowLatest: true}, {ProviderID: second.ProviderID, Revision: 1, FollowLatest: true},
	}}
	view, err := f.network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{
		NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, ExpectedRevision: 1,
		Operations: []FleetNetworkOperation{{Section: "base", Key: "xray", Operation: "replace", Base: &baseConfig}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.previewRequest.ExpectedRevision = view.Revision.Revision
	preview, err := f.network.Preview(t.Context(), &f.f.admin, f.previewRequest)
	if err != nil || !preview.Ready {
		t.Fatalf("two-provider preview ready=%v err=%v", preview.Ready, err)
	}
	if _, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	f.ack(t)
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls")
	for _, id := range []string{f.provider.ProviderID, second.ProviderID} {
		if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: id, ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	base, err := loadFleetProviderBase(f.f.db, node)
	if err != nil {
		t.Fatal(err)
	}
	var updates []FleetNetworkProviderPin
	for _, pin := range base.Manifest.Providers {
		var source model.FleetOutboundProvider
		if err := f.f.db.First(&source, "id = ?", pin.ProviderID).Error; err != nil {
			t.Fatal(err)
		}
		cached, err := loadProviderCache(f.f.db, source.ID, 2)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := providerTargets(base, source, cached)
		if err != nil || len(targets) != 1 {
			t.Fatalf("concurrent target count=%d err=%v", len(targets), err)
		}
		updates = append(updates, targets[0])
	}
	pool, err := f.f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(5)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	held := f.f.db.WithContext(ctx).Begin()
	if held.Error != nil {
		cancel()
		t.Fatal(held.Error)
	}
	var workers sync.WaitGroup
	defer func() { cancel(); _ = held.Rollback().Error; workers.Wait() }()
	if err := queueFleetProviderRollout(held, node, base, updates[:1]); err != nil {
		t.Fatal(err)
	}
	pids, results := make(chan int, 2), make(chan error, 2)
	for _, pin := range updates {
		workers.Go(func() {
			results <- f.f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var source model.FleetOutboundProvider
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, "id = ?", pin.ProviderID).Error; err != nil {
					return err
				}
				var pid int
				if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					return err
				}
				pids <- pid
				return queueFleetProviderRollout(tx, node, base, []FleetNetworkProviderPin{pin})
			})
		})
	}
	var backends []int
	for range 2 {
		select {
		case pid := <-pids:
			backends = append(backends, pid)
		case <-ctx.Done():
			t.Fatal("fanout transactions did not start")
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int64
		if err := f.f.db.WithContext(ctx).Raw("SELECT count(*) FROM pg_stat_activity WHERE pid IN ? AND wait_event_type = 'Lock'", backends).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("fanout transactions did not wait behind first-row holder")
		}
	}
	if err := held.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	work := f.work(t)
	var targets map[string]FleetNetworkProviderPin
	if err := json.Unmarshal([]byte(work.Targets), &targets); err != nil || len(targets) != 2 || work.Revision != 2 {
		t.Fatalf("concurrent first fanout lost target: targets=%d revision=%d err=%v", len(targets), work.Revision, err)
	}
}
