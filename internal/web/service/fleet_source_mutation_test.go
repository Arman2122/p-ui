package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func sourceEnableRequest(t *testing.T, f fleetClientFixture, clientID string, enabled bool) FleetClientEnableRequest {
	t.Helper()
	var client model.FleetClient
	if err := f.db.First(&client, "id = ?", clientID).Error; err != nil {
		t.Fatal(err)
	}
	return FleetClientEnableRequest{OperationID: uuid.NewString(), ClientID: clientID, ExpectedRevision: uint64(client.Revision), Enabled: enabled}
}

func TestFleetSourceEnableMutationRollsBackWithDesiredWorkPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	request := sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)
	for _, statement := range []string{
		`CREATE FUNCTION reject_source_work() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'source work fault'; END $$`,
		`CREATE TRIGGER reject_source_work BEFORE INSERT ON fleet_source_works FOR EACH ROW EXECUTE FUNCTION reject_source_work()`,
	} {
		if err := f.f.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, request); err == nil || !strings.Contains(err.Error(), "source work fault") {
		t.Fatalf("source/outbox fault was not reached=%v", err)
	}
	var source model.ClientRecord
	var client model.FleetClient
	var placement model.FleetPlacement
	if err := f.f.db.First(&source, f.f.client.Id).Error; err != nil || !source.Enable {
		t.Fatalf("failed desired work changed source=%+v err=%v", source, err)
	}
	if err := f.f.db.First(&client, "id = ?", request.ClientID).Error; err != nil || uint64(client.Revision) != request.ExpectedRevision {
		t.Fatalf("failed desired work advanced client=%+v err=%v", client, err)
	}
	if err := f.f.db.First(&placement, "id = ?", f.materialized.Placement.ID).Error; err != nil || !placement.Enabled || placement.Revision != f.materialized.Placement.Revision {
		t.Fatalf("failed desired work invalidated placement=%+v err=%v", placement, err)
	}
	var count int64
	if err := f.f.db.Model(&model.FleetOperation{}).Where("id = ?", request.OperationID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed desired work committed receipt=%d err=%v", count, err)
	}
}

func TestFleetSourceMutationPreservesAuthorityAndStopAcrossSupersessionPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	f.queue(t, true)
	request := sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)
	disabled, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, request)
	if err != nil || !disabled.Pending || disabled.Targets != 1 || disabled.Enabled || disabled.Revision != request.ExpectedRevision+1 {
		t.Fatalf("source disable was not pending convergence=%+v err=%v", disabled, err)
	}
	if replay, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, request); err != nil || !replay.Replayed || replay.Revision != disabled.Revision {
		t.Fatalf("source replay changed revision=%+v err=%v", replay, err)
	}
	conflict := request
	conflict.Enabled = true
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, conflict); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("conflicting source operation replay=%v", err)
	}
	work, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 || !work[0].RequiresStop || work[0].Revision != 1 {
		t.Fatalf("durable disable work=%+v err=%v", work, err)
	}
	called := 0
	action := func(context.Context, *gorm.DB, model.FleetSourceWork) error { called++; return nil }
	if pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], action); err != nil || !pending.Pending || !pending.RequiresStop || called != 1 {
		t.Fatalf("callback success fabricated stop convergence=%+v called=%d err=%v", pending, called, err)
	}
	enable := sourceEnableRequest(t, f.f, request.ClientID, true)
	enabled, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, enable)
	if err != nil || !enabled.Pending || !enabled.Enabled {
		t.Fatalf("re-enable did not queue fresh reconciliation=%+v err=%v", enabled, err)
	}
	called = 0
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], action); !errors.Is(err, controlplane.ErrRevision) || called != 0 {
		t.Fatalf("old work overtook newer source intent=%v called=%d", err, called)
	}
	work, err = PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 || !work[0].RequiresStop || work[0].Revision != 2 || !work[0].Enabled {
		t.Fatalf("enable erased unfinished stop requirement=%+v err=%v", work, err)
	}
	var grant model.FleetGrant
	var share model.FleetRateShare
	var placement model.FleetPlacement
	var outbox model.FleetAuthorityUpdate
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision || grant.TermID != f.active.Term.ID {
		t.Fatalf("source flag changed quota or term=%+v err=%v", grant, err)
	}
	if err := f.f.db.First(&share, "id = ?", grant.ID).Error; err != nil || share.Revision != f.credit.Rates.Revision {
		t.Fatalf("source flag released reserved rate=%+v err=%v", share, err)
	}
	var before, after controlplane.Rates
	if err := json.Unmarshal([]byte(f.credit.Rates.Outstanding), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(share.Outstanding), &after); err != nil || before != after {
		t.Fatalf("source flag changed outstanding rate=%+v want=%+v err=%v", after, before, err)
	}
	if err := f.f.db.First(&placement, "id = ?", f.materialized.Placement.ID).Error; err != nil || placement.Enabled || len(placement.GraphDigest) != 0 || placement.CurrentGrantID == nil || *placement.CurrentGrantID != grant.ID {
		t.Fatalf("source change reused approved placement or lost old grant=%+v err=%v", placement, err)
	}
	if err := f.f.db.First(&outbox, "grant_id = ?", grant.ID).Error; err != nil || !outbox.DeliveryBlocked || outbox.Revision != f.credit.Rates.Revision {
		t.Fatalf("source change left old enabled authority deliverable=%+v err=%v", outbox, err)
	}
}

func TestFleetSourceWriterDoesNotAcquireNodeOrTargetForeignKeyLocksPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	request := sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)
	err := f.f.db.Transaction(func(tx *gorm.DB) error {
		var node model.FleetNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		result, err := SetFleetClientEnabledTx(ctx, f.f.db, &f.f.admin, request)
		if err != nil || !result.Pending {
			t.Fatalf("source writer waited for a held node lock=%+v err=%v", result, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFleetSourceStopRequiresAcknowledgmentAndEnableRequiresNewBasePostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	f.queue(t, true)
	disabled, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.f.store.Transition(t.Context(), controlplane.TransitionRequest{
		OperationID: uuid.NewString(), ClientID: f.materialized.Client.ID, ExpectedRevision: disabled.Revision, ExpectedCredentialGeneration: uint64(f.materialized.Client.CredentialGeneration),
		ExpectedTermID: f.active.Term.ID, SourcePolicyDigest: f.active.Term.SourcePolicyDigest, Kind: controlplane.TrafficReset, Spec: controlplane.TermSpec{QuotaBytes: 1000},
	})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("independent lifecycle revision=%+v err=%v", pending, err)
	}
	if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID,
		NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, RuntimeID: "xray",
	}); err != nil {
		t.Fatal(err)
	}
	stop := f.queue(t, false)
	work, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 {
		t.Fatalf("source work=%+v err=%v", work, err)
	}
	action := func(context.Context, *gorm.DB, model.FleetSourceWork) error { return nil }
	if pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], action); err != nil || !pending.Pending || !pending.RequiresStop {
		t.Fatalf("queued stop was mistaken for applied proof=%+v err=%v", pending, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], stop); err != nil {
		t.Fatal(err)
	}
	completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], action)
	if err != nil || completed.Pending || completed.RequiresStop || completed.AppliedStopRevision != 1 {
		t.Fatalf("applied stop did not settle despite unrelated client revision=%+v err=%v", completed, err)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, true)); err != nil {
		t.Fatal(err)
	}
	work, err = PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 || work[0].AppliedStopRevision != 1 || !work[0].RequiresStop {
		t.Fatalf("enable lost applied-stop floor=%+v err=%v", work, err)
	}
	ready, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], action)
	if err != nil || !ready.Pending || ready.RequiresStop || ready.AppliedStopRevision != 1 {
		t.Fatalf("enable skipped fresh compilation phase=%+v err=%v", ready, err)
	}
	if pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, ready, action); err != nil || !pending.Pending {
		t.Fatalf("same-base callback reopened disabled authority=%+v err=%v", pending, err)
	}
}

func TestFleetSourceConcurrentExpectedRevisionAllowsOneMutationPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	first := sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)
	second := first
	second.OperationID = uuid.NewString()
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, request := range []FleetClientEnableRequest{first, second} {
		workers.Go(func() {
			_, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, request)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, controlplane.ErrRevision) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("source compare-and-swap successes=%d conflicts=%d", successes, conflicts)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil || work.Revision != 1 || work.SourceRevision != int64(first.ExpectedRevision)+1 {
		t.Fatalf("competing source intents multiplied revisions=%+v err=%v", work, err)
	}
}

func TestFleetSourceMutationDoesNotInvertAuthorityDeliveryLocksPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	f.queue(t, true)
	entered, resume := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type fetchResult struct {
		updates int
		err     error
	}
	done := make(chan fetchResult, 1)
	go func() {
		batch, err := f.f.store.FetchAuthorityUpdates(ctx, f.f.nodes[0], f.request, func(ctx context.Context, tx *gorm.DB, updates []model.FleetAuthorityUpdate) (map[string]bool, error) {
			close(entered)
			select {
			case <-resume:
				return ValidateFleetAuthorityDeliveryTx(ctx, tx, updates)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		result := fetchResult{err: err}
		if batch != nil {
			result.updates = len(batch.Updates)
		}
		done <- result
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("authority fetch did not reach source validator")
	}
	write, stop := context.WithTimeout(t.Context(), 2*time.Second)
	result, err := SetFleetClientEnabledTx(write, f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false))
	stop()
	close(resume)
	if err != nil || !result.Pending {
		t.Fatalf("source writer waited for delivery node/queued row=%+v err=%v", result, err)
	}
	select {
	case fetched := <-done:
		if fetched.err != nil || fetched.updates != 0 {
			t.Fatalf("preloaded authority escaped source invalidation=%+v", fetched)
		}
	case <-ctx.Done():
		t.Fatal("authority source validator did not join")
	}
}

func TestFleetSourceEnableConvergesOnlyAfterFreshAppliedPublicationPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	f.queue(t, true)
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID,
		NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, RuntimeID: "xray",
	}); err != nil {
		t.Fatal(err)
	}
	queuedStop := f.queue(t, false)
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, true)); err != nil {
		t.Fatal(err)
	}
	stop, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(stop.Updates) != 1 || stop.Updates[0].Enabled || stop.Updates[0].AuthorityRevision != queuedStop.Updates[0].AuthorityRevision {
		t.Fatalf("enable hid the unfinished disabled delivery=%+v err=%v", stop, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], stop); err != nil {
		t.Fatal(err)
	}
	queued, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(queued) != 1 {
		t.Fatalf("source enable work=%+v err=%v", queued, err)
	}
	action := func(context.Context, *gorm.DB, model.FleetSourceWork) error { return nil }
	work, err := ProcessFleetSourceWork(t.Context(), f.f.db, queued[0], action)
	if err != nil || work.RequiresStop || !work.Pending || work.AppliedStopRevision != 1 {
		t.Fatalf("stopped enable work=%+v err=%v", work, err)
	}
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID})
	if err != nil || !preview.Ready {
		t.Fatalf("fresh enable preview=%+v err=%v", preview, err)
	}
	publication, err := network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID})
	if err != nil || publication.DeploymentRevision <= uint64(work.AppliedStopRevision) {
		t.Fatalf("fresh enable publication=%+v err=%v", publication, err)
	}
	if pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, action); err != nil || !pending.Pending {
		t.Fatalf("unapplied publication claimed convergence=%+v err=%v", pending, err)
	}
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.f.store.Delivery(t.Context(), f.f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[0], generation, &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: delivery.Revision, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("restore published membership")
	if err := f.f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetPublishedClientAdmission{}).Where("node_id = ? AND revision = ? AND client_id = ?", work.NodeID, publication.DeploymentRevision, work.ClientID).Update("placement_revision", 1).Error; err != nil {
			return err
		}
		if published, err := fleetSourceWorkPublished(tx, work); err != nil || published {
			t.Fatalf("stale published membership claimed convergence=%v err=%v", published, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	publicationAttempts := 0
	completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, func(ctx context.Context, tx *gorm.DB, _ model.FleetSourceWork) error {
		publicationAttempts++
		reconcile, err := NewFleetNetworkService(tx)
		if err != nil {
			return err
		}
		next, err := reconcile.Preview(ctx, &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID})
		if err != nil {
			return err
		}
		_, err = reconcile.Publish(ctx, &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: next.ID})
		return err
	})
	if err != nil || completed.Pending || completed.RequiresStop {
		t.Fatalf("fresh applied publication did not settle enable=%+v err=%v", completed, err)
	}
	if publicationAttempts != 0 {
		t.Fatalf("already applied source work republished before checking convergence=%d", publicationAttempts)
	}
	assertFleetSourceOldTermBaselinePending(t, f, completed, publication.DeploymentRevision)
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision {
		t.Fatalf("source enable minted replacement quota=%+v err=%v", grant, err)
	}
}

func TestFleetSourcePendingStopCannotBeBypassedByNormalPublicationPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, enabled)); err != nil {
			t.Fatal(err)
		}
	}
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID})
	if err != nil || !preview.Ready {
		t.Fatalf("source enable preview=%+v err=%v", preview, err)
	}
	request := FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}
	if _, err := network.Publish(t.Context(), &f.f.admin, request); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("normal publication bypassed pending stop=%v", err)
	}
	var placement model.FleetPlacement
	var grant model.FleetGrant
	if err := f.f.db.First(&placement, "id = ?", f.materialized.Placement.ID).Error; err != nil || placement.Enabled || len(placement.GraphDigest) != 0 {
		t.Fatalf("failed stop gate implicitly approved source=%+v err=%v", placement, err)
	}
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision {
		t.Fatalf("failed stop gate changed quota=%+v err=%v", grant, err)
	}
	if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: grant.ID, BudgetID: f.materialized.Client.ID,
		NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, RuntimeID: "xray",
	}); err != nil {
		t.Fatal(err)
	}
	stop := f.queue(t, false)
	queued, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(queued) != 1 {
		t.Fatalf("source work=%+v err=%v", queued, err)
	}
	action := func(context.Context, *gorm.DB, model.FleetSourceWork) error { return nil }
	if pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, queued[0], action); err != nil || !pending.RequiresStop {
		t.Fatalf("unapplied stop cleared publication gate=%+v err=%v", pending, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], stop); err != nil {
		t.Fatal(err)
	}
	if _, err := network.Publish(t.Context(), &f.f.admin, request); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("publication skipped stop-work settlement=%v", err)
	}
	if ready, err := ProcessFleetSourceWork(t.Context(), f.f.db, queued[0], action); err != nil || ready.RequiresStop || !ready.Pending {
		t.Fatalf("applied stop did not permit fresh reconciliation=%+v err=%v", ready, err)
	}
	if published, err := network.Publish(t.Context(), &f.f.admin, request); err != nil || published.DeploymentRevision != 2 {
		t.Fatalf("proved stop did not release publication gate=%+v err=%v", published, err)
	}
}
