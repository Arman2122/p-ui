package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetStatsDoesNotPresentStaleOrFutureObservationsAsReady(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, state, runtime string
		change               func(*model.FleetNode, *model.FleetNodeObservation, *model.FleetRuntimeJournal)
	}{
		{"fresh", "ready", "ready", func(*model.FleetNode, *model.FleetNodeObservation, *model.FleetRuntimeJournal) {}},
		{"disconnected", "disconnected", "stale", func(n *model.FleetNode, _ *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			n.LastSeenUnixMilli = now.Add(-time.Hour).UnixMilli()
		}},
		{"disabled", "disabled", "stale", func(n *model.FleetNode, _ *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			n.Enabled = false
		}},
		{"stale report", "stale", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.ReceivedUnixMilli = now.Add(-time.Hour).UnixMilli()
		}},
		{"stale observation", "stale", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.ObservedUnixMilli = now.Add(-time.Hour).UnixMilli()
		}},
		{"future observation", "stale", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.ObservedUnixMilli = now.Add(time.Hour).UnixMilli()
		}},
		{"old session", "stale", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.RuntimeGeneration--
		}},
		{"no conditions", "unknown", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.ConditionsJSON = `[]`
		}},
		{"wrong applied graph", "unknown", "ready", func(_ *model.FleetNode, o *model.FleetNodeObservation, _ *model.FleetRuntimeJournal) {
			o.ReportedAppliedRevision--
		}},
		{"runtime stale", "ready", "stale", func(_ *model.FleetNode, _ *model.FleetNodeObservation, r *model.FleetRuntimeJournal) {
			r.ObservedUnixMilli = now.Add(-time.Hour).UnixMilli()
		}},
		{"runtime future", "ready", "stale", func(_ *model.FleetNode, _ *model.FleetNodeObservation, r *model.FleetRuntimeJournal) {
			r.ObservedUnixMilli = now.Add(time.Hour).UnixMilli()
		}},
		{"runtime wrong installation", "ready", "stale", func(_ *model.FleetNode, _ *model.FleetNodeObservation, r *model.FleetRuntimeJournal) {
			r.InstallationID = "other"
		}},
		{"runtime unready", "ready", "degraded", func(_ *model.FleetNode, _ *model.FleetNodeObservation, r *model.FleetRuntimeJournal) { r.Ready = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := model.FleetNode{ID: "node", InstallationID: "installation", Name: "edge", Enabled: true, LastSeenUnixMilli: now.UnixMilli(), AuthorityGeneration: 2, AppliedRevision: 7, DesiredRevision: 8}
			observation := model.FleetNodeObservation{NodeID: node.ID, RuntimeGeneration: 2, ReceivedUnixMilli: now.UnixMilli(), ObservedUnixMilli: now.UnixMilli(), ReportedAppliedRevision: 7, ConditionsJSON: `[{"ready":true}]`}
			runtime := model.FleetRuntimeJournal{RuntimeID: "xray", InstallationID: node.InstallationID, Ready: true, ReceivedUnixMilli: now.UnixMilli(), ObservedUnixMilli: now.UnixMilli()}
			test.change(&node, &observation, &runtime)
			view, err := fleetStatsNode(node, observation, []model.FleetRuntimeJournal{runtime}, now)
			if err != nil || view.State != test.state || len(view.Runtimes) != 1 || view.Runtimes[0].State != test.runtime || view.AppliedRevision != 7 || view.DesiredRevision != 8 || view.ObservedUnixMilli != observation.ObservedUnixMilli {
				t.Fatalf("fleet observation presentation=%+v %v", view, err)
			}
		})
	}
}

func TestFleetStatsSnapshotUsesLedgerAndBatchesRecipientReadsPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	if err := (&SettingService{}).setString("trafficDiff", "1"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": 50, "down": 70}).Error; err != nil {
		t.Fatal(err)
	}
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fleetUsageReport(t, f, 0, credit.Grant, 1, 50, 30)
	if err := f.db.Model(&model.Inbound{}).Where("id IN ?", []int{f.inbounds[0].Id, f.inbounds[1].Id}).Update("enable", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Updates(map[string]any{"tg_id": 33, "total_gb": 1, "expiry_time": 1}).Error; err != nil {
		t.Fatal(err)
	}
	for i := range 129 {
		client := model.ClientRecord{Email: fmt.Sprintf("awaiting-%03d", i), TgID: 44, Enable: true}
		if err := f.db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.FleetClient{ID: uuid.NewString(), PanelClientID: &client.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: make([]byte, 32), Owned: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&core.ClientTraffic{Email: client.Email, Up: 3, Down: 7}).Error; err != nil {
			t.Fatal(err)
		}
	}
	legacy := model.ClientRecord{Email: "legacy-ignored", TgID: 33, Enable: true}
	if err := f.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&core.ClientTraffic{Email: legacy.Email, Up: 999999}).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	callback := "test:fleet_stats_query_count"
	count := func(*gorm.DB) { queries++ }
	if err := f.db.Callback().Query().After("gorm:query").Register(callback, count); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Callback().Row().After("gorm:row").Register(callback, count); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback); _ = f.db.Callback().Row().Remove(callback) })
	snapshot, err := ReadFleetStats(t.Context(), f.db, time.Now())
	if err != nil || snapshot.ClientCount != 130 || len(snapshot.Clients) != 1 || len(snapshot.Nodes) != 2 || queries > 16 || snapshot.MeasuredBytes != 80 || snapshot.OpeningBytes != 1410 {
		t.Fatalf("fleet snapshot cardinality/query count: clients=%d nodes=%d queries=%d err=%v", len(snapshot.Clients), len(snapshot.Nodes), queries, err)
	}
	client := snapshot.Clients[0]
	meta := client.Traffic.Accounting
	if client.Traffic.Email != f.client.Email || client.TelegramID != 33 || !client.Attached || !client.EnabledInbound || client.Traffic.Total != 1000 || client.Traffic.ExpiryTime != active.Term.ExpiresUnixMilli ||
		meta == nil || meta.MeasuredUploaded != 50 || meta.MeasuredDownloaded != 30 || meta.OpeningUploaded != 50 || meta.OpeningDownloaded != 70 || meta.ReservedBytes == nil || *meta.ReservedBytes != 220 || meta.UncertainBytes != nil {
		t.Fatalf("fleet snapshot used source edits, reservations or stale counters: %+v accounting=%+v", client, meta)
	}
}

func TestFleetStatsSnapshotDoesNotReadPartialTermStatePostgres(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	callback := "test:fleet_stats_read_snapshot"
	changed := false
	if err := f.db.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
		if changed || tx.Statement.Table != "c" {
			return
		}
		changed = true
		if err := f.db.Model(&model.FleetTerm{}).Where("id = ?", active.Term.ID).Update("opening_uploaded", 100).Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Row().Remove(callback) })
	snapshot, err := ReadFleetStats(t.Context(), f.db, time.Now())
	if err != nil || !changed || snapshot.ClientCount != 1 || snapshot.OpeningBytes != 0 {
		t.Fatalf("read-only snapshot mixed transactions: changed=%v snapshot=%+v error=%v", changed, snapshot, err)
	}
}

func TestFleetStatsKeepsAggregateTotalsWithoutRetainingEveryClient(t *testing.T) {
	snapshot := FleetStatsSnapshot{GeneratedAt: time.Now(), TrafficThreshold: 2}
	client := FleetStatsClient{Traffic: core.ClientTraffic{Enable: true, Up: 2, Total: 100, Accounting: &core.TrafficAccounting{Source: "fleet", TermID: "term", MeasuredUploaded: 2, OpeningDownloaded: 1}}}
	for range 10000 {
		if err := foldFleetStatsClient(&snapshot, client); err != nil {
			t.Fatal(err)
		}
	}
	if len(snapshot.Clients) != 0 || snapshot.ClientCount != 10000 || snapshot.MeasuredBytes != 20000 || snapshot.OpeningBytes != 10000 {
		t.Fatalf("unbounded retention or incomplete totals: %+v", snapshot)
	}
	client.EnabledInbound, client.Traffic.Total = true, 3
	for range fleetStatsCandidateLimit {
		if err := foldFleetStatsClient(&snapshot, client); err != nil {
			t.Fatal(err)
		}
	}
	if err := foldFleetStatsClient(&snapshot, client); !errors.Is(err, ErrFleetStatsCapacity) || len(snapshot.Clients) != fleetStatsCandidateLimit {
		t.Fatalf("report silently dropped or retained unbounded recipients: count=%d error=%v", len(snapshot.Clients), err)
	}
}

func TestFleetStatsRejectsNestedAndReplacedDatabaseHandlesPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := ReadFleetStats(t.Context(), tx, time.Now())
		if !errors.Is(err, ErrFleetStatsTx) {
			t.Fatalf("nested transaction lost snapshot options: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadFleetStats(ctx, db, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lifecycle began a snapshot: %v", err)
	}
	if err := database.InitDB(); err != nil {
		t.Fatal(err)
	}
	if database.GetDB() == db {
		t.Fatal("database replacement test did not replace the handle")
	}
	if _, err := ReadFleetStats(t.Context(), db, time.Now()); !errors.Is(err, ErrFleetStatsDB) {
		t.Fatalf("old database handle produced a report after replacement: %v", err)
	}
}
