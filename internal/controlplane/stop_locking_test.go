package controlplane

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func mirrorStopSubject(t *testing.T, f stopFixture, original stopSubject, node identity.Principal, grantID string) stopSubject {
	t.Helper()
	subject := original
	subject.placement = original.placement
	subject.placement.ID, subject.placement.NodeID, subject.placement.InstallationID = uuid.NewString(), node.NodeID, node.InstallationID
	subject.placement.CurrentGrantID, subject.placement.CurrentGrantTermID = nil, nil
	subject.instance = uuid.NewString()
	if err := f.db.Create(&subject.placement).Error; err != nil {
		t.Fatal(err)
	}
	panelInbound := 11
	if err := f.db.Create(&model.FleetPlacementInbound{PlacementID: subject.placement.ID, InstanceID: subject.instance, PanelInbound: &panelInbound}).Error; err != nil {
		t.Fatal(err)
	}
	var err error
	subject.grant, err = f.store.reserve(t.Context(), ReserveRequest{
		OperationID: uuid.NewString(), GrantID: grantID, NodeID: node.NodeID, InstallationID: node.InstallationID,
		RuntimeID: subject.placement.RuntimeID, ClientID: subject.client.ID, TermID: subject.term.ID, BudgetIDs: []string{subject.term.BudgetID}, Bytes: 100,
	}, subject.placement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&subject.placement).Updates(map[string]any{"current_grant_id": grantID, "current_grant_term_id": subject.term.ID}).Error; err != nil {
		t.Fatal(err)
	}
	subject.share, err = f.store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: grantID, BudgetID: subject.client.ID, NodeID: node.NodeID,
		InstallationID: node.InstallationID, RuntimeID: subject.placement.RuntimeID, Desired: limitedRates(10000, 1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

type stopLockNodeKey struct{}

func TestStopConfigurationOrdersOrphanClientsAcrossNodesPostgres(t *testing.T) {
	f := newStopFixture(t, true)
	var other model.FleetNode
	if err := f.db.Where("id <> ?", f.node.NodeID).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	node := identity.Principal{NodeID: other.ID, InstallationID: other.InstallationID}
	bootstrapTestRuntime(t, f.store, node, "native")
	bootstrapTestRuntime(t, f.store, node, "xray")
	nativeID, streamID := "00000000-0000-0000-0000-000000000001", "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if f.native.grant.ID < f.stream.grant.ID {
		nativeID, streamID = streamID, nativeID
	}
	native := mirrorStopSubject(t, f, f.native, node, nativeID)
	stream := mirrorStopSubject(t, f, f.stream, node, streamID)
	applyBundle(t, f.store, node, configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{
		{ID: "native", Core: "native", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{native.authority()}},
		{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{stream.authority()}, Admissions: []admission.Descriptor{stream.descriptor()}},
	}})
	var base model.FleetDeployment
	if err := f.db.First(&base, "node_id = ? AND revision = 1", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	for _, client := range []model.FleetClient{f.native.client, f.stream.client} {
		if err := f.db.Model(&client).Update("panel_client_id", nil).Error; err != nil {
			t.Fatal(err)
		}
	}
	callback := "test:orphan_stop_barrier"
	var entered sync.Map
	var arrivals atomic.Int64
	both := make(chan struct{})
	if err := f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		owner, _ := tx.Statement.Context.Value(stopLockNodeKey{}).(string)
		if tx.Statement.Table != "fleet_rate_budgets" || !ok || locking.Strength != "UPDATE" || owner == "" {
			return
		}
		if _, known := entered.LoadOrStore(owner, true); known {
			return
		}
		if arrivals.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(250 * time.Millisecond):
		case <-t.Context().Done():
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	requests := []struct {
		node    identity.Principal
		request StopConfigurationRequest
	}{
		{f.node, f.request},
		{node, StopConfigurationRequest{OperationID: uuid.NewString(), ExpectedBaseRevision: 1, ExpectedBaseSHA256: base.Digest, GrantIDs: []string{nativeID, streamID}}},
	}
	var wg sync.WaitGroup
	for _, request := range requests {
		wg.Go(func() {
			ctx := context.WithValue(t.Context(), stopLockNodeKey{}, request.node.NodeID)
			result, err := f.store.StopConfiguration(ctx, request.node, request.request, stopValidators())
			if err != nil || result.Revision != 2 {
				t.Errorf("opposite per-node grant order deadlocked orphan stop: %+v %v", result, err)
			}
		})
	}
	wg.Wait()
	if arrivals.Load() != 2 {
		t.Fatalf("both node stop transactions did not reach their pools: %d", arrivals.Load())
	}
}

func TestStopConfigurationOrdersPoolsAgainstAppliedAcknowledgementPostgres(t *testing.T) {
	f := newStopFixture(t, true)
	var other model.FleetNode
	if err := f.db.Where("id <> ?", f.node.NodeID).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	node := identity.Principal{NodeID: other.ID, InstallationID: other.InstallationID}
	bootstrapTestRuntime(t, f.store, node, "native")
	bootstrapTestRuntime(t, f.store, node, "xray")
	nativeID, streamID := "00000000-0000-0000-0000-000000000001", "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if f.native.client.ID < f.stream.client.ID {
		nativeID, streamID = streamID, nativeID
	}
	native := mirrorStopSubject(t, f, f.native, node, nativeID)
	stream := mirrorStopSubject(t, f, f.stream, node, streamID)
	applyBundle(t, f.store, node, configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{
		{ID: "native", Core: "native", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{native.authority()}},
		{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{stream.authority()}, Admissions: []admission.Descriptor{stream.descriptor()}},
	}})
	var base model.FleetDeployment
	if err := f.db.First(&base, "node_id = ? AND revision = 1", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	generation, err := f.store.BeginSession(t.Context(), f.node, journalHello(t, f.store, f.node))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.store.Delivery(t.Context(), f.node, generation)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stopLocked, resumeStop := make(chan struct{}), make(chan struct{})
	ackPID := make(chan int, 1)
	var firstStop, firstAck, release sync.Once
	defer release.Do(func() { close(resumeStop) })
	before, after := "test:stop_ack_before_pool", "test:stop_ack_after_pool"
	if err := f.db.Callback().Query().Before("gorm:query").Register(before, func(tx *gorm.DB) {
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if tx.Statement.Table != "fleet_rate_budgets" || !ok || locking.Strength != "UPDATE" || tx.Statement.Context.Value(stopLockNodeKey{}) != "ack" {
			return
		}
		firstAck.Do(func() {
			var pid int
			fresh := tx.Session(&gorm.Session{NewDB: true, Initialized: true})
			if err := fresh.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
				_ = tx.AddError(err)
				return
			}
			ackPID <- pid
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Callback().Query().After("gorm:query").Register(after, func(tx *gorm.DB) {
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if tx.Statement.Table != "fleet_rate_budgets" || !ok || locking.Strength != "UPDATE" || tx.Statement.Context.Value(stopLockNodeKey{}) != "stop" {
			return
		}
		firstStop.Do(func() {
			close(stopLocked)
			select {
			case <-resumeStop:
			case <-ctx.Done():
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Callback().Query().Remove(before)
		_ = f.db.Callback().Query().Remove(after)
	})
	stopResult, ackResult := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := f.store.StopConfiguration(context.WithValue(ctx, stopLockNodeKey{}, "stop"), node,
			StopConfigurationRequest{OperationID: uuid.NewString(), ExpectedBaseRevision: 1, ExpectedBaseSHA256: base.Digest, GrantIDs: []string{nativeID, streamID}}, stopValidators())
		stopResult <- err
	}()
	select {
	case <-stopLocked:
	case err := <-stopResult:
		t.Fatalf("stop did not acquire its first pool: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		ackResult <- f.store.Observe(context.WithValue(ctx, stopLockNodeKey{}, "ack"), f.node, generation, appliedStop(generation, deployment))
	}()
	var pid int
	select {
	case pid = <-ackPID:
	case err := <-ackResult:
		t.Fatalf("acknowledgement did not enter its pool locks: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for {
		var waiting bool
		if err := f.db.WithContext(ctx).Raw("SELECT COALESCE((SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = ?), false)", pid).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	release.Do(func() { close(resumeStop) })
	stopErr, ackErr := <-stopResult, <-ackResult
	if stopErr != nil || ackErr != nil {
		t.Fatalf("stop and applied acknowledgement lock order diverged: stop=%v ack=%v", stopErr, ackErr)
	}
}
