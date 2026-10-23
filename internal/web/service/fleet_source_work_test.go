package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetSourceNodeWideCallbacksDoNotHoldTargetClientLocksPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	second := f
	second.client = model.ClientRecord{Email: "second-source", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: f.client.ExpiryTime, Reset: 1}
	if err := f.db.Create(&second.client).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: second.client.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: second.client.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: second.client.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	var selected []model.FleetSourceWork
	for clientIndex, fixture := range []fleetClientFixture{f, second} {
		first := fixture.approve(t, fixture.materialize(t, 0))
		other := fixture.approve(t, fixture.materialize(t, 1))
		term := fixture.activate(t, first)
		for _, placement := range []model.FleetPlacement{first, other} {
			if _, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: term.Term.ID, ExpectedPlacementRevision: uint64(placement.Revision),
				ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), Bytes: 100,
				Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, first.ClientID, false)); err != nil {
			t.Fatal(err)
		}
		var work model.FleetSourceWork
		if err := f.db.First(&work, "client_id = ? AND node_id = ?", first.ClientID, f.nodes[clientIndex].NodeID).Error; err != nil {
			t.Fatal(err)
		}
		selected = append(selected, work)
	}
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1}); err != nil {
		t.Fatal(err)
	}
	for _, node := range f.nodes {
		if _, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: node.NodeID, InstallationID: node.InstallationID}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}, 2), make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	for _, work := range selected {
		workers.Go(func() {
			_, err := ProcessFleetSourceWork(ctx, f.db, work, func(ctx context.Context, tx *gorm.DB, work model.FleetSourceWork) error {
				state, err := loadFleetNetworkState(tx, work.NodeID, work.InstallationID)
				if err != nil {
					return err
				}
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				_, _, _, err = prepareFleetNetworkSources(tx, state)
				return err
			})
			results <- err
		})
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("source callbacks failed to enter independently")
		}
	}
	close(release)
	workers.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("full-node source callbacks inverted client locks=%v", err)
		}
	}
}

func assertFleetSourceOldTermBaselinePending(t *testing.T, f authorityFixture, work model.FleetSourceWork, revision uint64) {
	t.Helper()
	var client model.FleetClient
	if err := f.f.db.First(&client, "id = ?", work.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	pending, err := f.f.store.Transition(t.Context(), controlplane.TransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(client.Revision), ExpectedCredentialGeneration: uint64(client.CredentialGeneration),
		ExpectedTermID: f.active.Term.ID, SourcePolicyDigest: f.active.Term.SourcePolicyDigest, Kind: controlplane.TrafficReset,
		Spec: controlplane.TermSpec{QuotaBytes: 1000, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}},
	})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("independent term reset=%+v err=%v", pending, err)
	}
	if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: client.ID,
		NodeID: work.NodeID, InstallationID: work.InstallationID, RuntimeID: work.RuntimeID,
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, work.NodeID, revision, work.RuntimeID, work.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = revision, scope.PayloadSHA256
	var runtime model.FleetRuntimeJournal
	if err := f.f.db.First(&runtime, "node_id = ? AND runtime_id = ?", work.NodeID, work.RuntimeID).Error; err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, uint64(runtime.Sequence+1), time.Now())
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, false)); err != nil {
		t.Fatal(err)
	}
	completed, err := f.f.store.CompleteTransition(t.Context(), controlplane.CompleteTransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, PendingTermID: pending.Term.ID, ExpectedRevision: uint64(pending.Client.Revision),
		ExpectedCredentialGeneration: uint64(client.CredentialGeneration), SourcePolicyDigest: pending.Term.SourcePolicyDigest,
	})
	if err != nil || completed.PendingDrain {
		t.Fatalf("independent term completion=%+v err=%v", completed, err)
	}
	if err := f.f.db.Model(&work).Update("pending", true).Error; err != nil {
		t.Fatal(err)
	}
	result, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, func(context.Context, *gorm.DB, model.FleetSourceWork) error { return nil })
	if err != nil || !result.Pending {
		t.Fatalf("previous-term baseline settled fresh enable work=%+v err=%v", result, err)
	}
}

func TestFleetSourceSupersededActionRollsBackPublicationWritesPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	queued, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(queued) != 1 {
		t.Fatalf("source work=%+v err=%v", queued, err)
	}
	called := false
	if err := f.f.db.Transaction(func(tx *gorm.DB) error {
		_, err := ProcessFleetSourceWork(t.Context(), tx, queued[0], func(context.Context, *gorm.DB, model.FleetSourceWork) error { called = true; return nil })
		if !errors.Is(err, controlplane.ErrInvalid) || called {
			t.Fatalf("savepoint retained preflight locks across callback=%v called=%v", err, called)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	marker := uuid.NewString()
	_, err = ProcessFleetSourceWork(t.Context(), f.f.db, queued[0], func(ctx context.Context, tx *gorm.DB, _ model.FleetSourceWork) error {
		if err := tx.Create(&model.FleetOperation{ID: marker, Digest: make([]byte, 32), Result: []byte("must roll back")}).Error; err != nil {
			return err
		}
		write, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := SetFleetClientEnabledTx(write, f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, true))
		return err
	})
	if !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("superseded source action was not rejected=%v", err)
	}
	var count int64
	if err := f.f.db.Model(&model.FleetOperation{}).Where("id = ?", marker).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("superseded action committed publication effects=%d err=%v", count, err)
	}
	var current model.FleetSourceWork
	if err := f.f.db.First(&current, "placement_id = ?", queued[0].PlacementID).Error; err != nil || !current.Pending || !current.Enabled || !current.RequiresStop || current.Revision != queued[0].Revision+1 {
		t.Fatalf("old action erased newer source work=%+v err=%v", current, err)
	}
	if err := DeferFleetSourceWork(t.Context(), f.f.db, queued[0], 30*time.Second); err != nil {
		t.Fatal(err)
	}
	var after model.FleetSourceWork
	if err := f.f.db.First(&after, "placement_id = ?", current.PlacementID).Error; err != nil || after.AvailableUnixMilli != current.AvailableUnixMilli || after.QueuedUnixMilli != current.QueuedUnixMilli {
		t.Fatalf("old action deferred superseding work=%+v err=%v", after, err)
	}
}

func TestFleetSourceQueueRotatesBlockedWorkAndPrioritizesStopsPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	var clients []model.ClientRecord
	for i := range 129 {
		clients = append(clients, model.ClientRecord{Email: "queued-source-" + strconv.Itoa(i), UUID: uuid.NewString(), Enable: true, TotalGB: 1000})
	}
	if err := f.db.CreateInBatches(&clients, 128).Error; err != nil {
		t.Fatal(err)
	}
	var owners []model.FleetClient
	var placements []model.FleetPlacement
	var work []model.FleetSourceWork
	queuedAt := time.Now().Add(-time.Second).UnixMilli()
	for i := range clients {
		owner := model.FleetClient{ID: uuid.NewString(), PanelClientID: &clients[i].Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: make([]byte, 32), Owned: true}
		placement := model.FleetPlacement{ID: uuid.NewString(), ClientID: owner.ID, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, RuntimeID: "xray", CoreID: "xray", Revision: 1, CredentialGeneration: 1, SourcePolicyDigest: make([]byte, 32), DefinitionDigest: make([]byte, 32)}
		owners, placements = append(owners, owner), append(placements, placement)
		work = append(work, model.FleetSourceWork{
			PlacementID: placement.ID, ClientID: owner.ID, NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID,
			OperationID: uuid.NewString(), Revision: 1, SourceRevision: 1, Enabled: true, RequiresStop: true, Pending: true, QueuedUnixMilli: queuedAt, AvailableUnixMilli: queuedAt,
		})
	}
	for _, rows := range []any{&owners, &placements, &work} {
		if err := f.db.CreateInBatches(rows, 128).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], configuration.Bundle{Version: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := PendingFleetSourceWork(t.Context(), f.db, f.nodes[0].NodeID, 128)
	if err != nil || len(first) != 128 {
		t.Fatalf("bounded source queue=%d err=%v", len(first), err)
	}
	seen := map[string]bool{}
	for index, item := range first {
		seen[item.PlacementID] = true
		var failure error
		if index%2 == 1 {
			failure = ErrFleetNetworkFunding
		}
		result, err := ProcessFleetSourceWork(t.Context(), f.db, item, func(context.Context, *gorm.DB, model.FleetSourceWork) error { return failure })
		if !errors.Is(err, failure) {
			t.Fatalf("source action error was not retained=%v want=%v", err, failure)
		}
		if err := f.db.First(&result, "placement_id = ?", item.PlacementID).Error; err != nil {
			t.Fatal(err)
		}
		if failure != nil && result.AvailableUnixMilli <= queuedAt {
			t.Fatalf("failed source action did not rotate=%+v", result)
		}
		if !result.RequiresStop || !result.Pending || result.QueuedUnixMilli != queuedAt || result.AvailableUnixMilli <= queuedAt {
			t.Fatalf("blocked work did not rotate fairly=%+v err=%v", result, err)
		}
	}
	next, err := PendingFleetSourceWork(t.Context(), f.db, f.nodes[0].NodeID, 1)
	if err != nil || len(next) != 1 || seen[next[0].PlacementID] {
		t.Fatalf("oldest 128 entries starved remaining stop=%+v err=%v", next, err)
	}
	if err := f.db.Model(&model.FleetSourceWork{}).Where("placement_id IN ?", []string{first[0].PlacementID, first[1].PlacementID}).Updates(map[string]any{"requires_stop": false, "available_unix_milli": queuedAt - 1}).Error; err != nil {
		t.Fatal(err)
	}
	prioritized, err := PendingFleetSourceWork(t.Context(), f.db, f.nodes[0].NodeID, 1)
	if err != nil || len(prioritized) != 1 || !prioritized[0].RequiresStop {
		t.Fatalf("enable compilation ran ahead of remaining stop=%+v err=%v", prioritized, err)
	}
}
