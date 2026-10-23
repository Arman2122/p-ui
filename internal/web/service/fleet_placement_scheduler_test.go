package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func waitFleetPlacement(t *testing.T, description string, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for !condition() {
		select {
		case <-timer.C:
			t.Fatalf("placement scheduler did not %s", description)
		case <-ticker.C:
		}
	}
}

func TestFleetPlacementSchedulersApplyAndRetireOnlyAfterActualReceiptsPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 2)
	for _, run := range []func(context.Context, *gorm.DB) error{RunFleetPlacementScheduler, RunFleetSourceScheduler} {
		go func() { done <- run(ctx, f.f.db) }()
	}
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("placement/source scheduler exit=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("placement/source scheduler did not cancel")
			}
		}
	})
	var deployment *nodev1.Deployment
	waitFleetPlacement(t, "publish authentication baseline", func() bool {
		var err error
		deployment, err = f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
		if err != nil {
			t.Fatal(err)
		}
		return deployment != nil && deployment.Revision > f.approved
	})
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Admissions) != 1 || len(bundle.Runtimes[0].Grants) != 0 {
		t.Fatalf("scheduled application invented initial authority: %+v %v", bundle, err)
	}
	observe := func(stage nodev1.ApplyStage) {
		t.Helper()
		if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
			Revision: deployment.Revision, AuthorityGeneration: f.generation, PayloadSha256: deployment.PayloadSha256,
			AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: stage,
		}); err != nil {
			t.Fatal(err)
		}
	}
	observe(nodev1.ApplyStage_APPLY_STAGE_RECEIVED)
	if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil || !f.intent.Pending {
		t.Fatalf("received graph prematurely settled assignment: %+v %v", f.intent, err)
	}
	for _, row := range []any{&model.FleetTerm{}, &model.FleetGrant{}} {
		var count int64
		if err := f.f.db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("scheduled baseline started billing before demand: %T %d %v", row, count, err)
		}
	}
	observe(nodev1.ApplyStage_APPLY_STAGE_APPLIED)
	waitFleetPlacement(t, "settle applied baseline", func() bool {
		if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil {
			t.Fatal(err)
		}
		return !f.intent.Pending && f.intent.State == "applied"
	})
	var materialized FleetMaterialization
	if err := f.f.db.First(&materialized.Placement, "id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&materialized.Client, "id = ?", f.intent.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Where("placement_id = ?", materialized.Placement.ID).Find(&materialized.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	grant, authority := f.fundDemand(t, materialized)
	base := deployment.Revision
	f.rule.Enabled, f.rule.ExpectedRevision = false, 1
	if _, err := f.service.PutRule(t.Context(), &f.f.admin, f.rule); err != nil {
		t.Fatal(err)
	}
	waitFleetPlacement(t, "queue frozen authority stop", func() bool {
		batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[1], authority, ValidateFleetAuthorityDeliveryTx)
		if err != nil {
			t.Fatal(err)
		}
		if batch == nil || len(batch.Updates) == 0 || batch.Updates[0].Enabled {
			return false
		}
		var node model.FleetNode
		if err := f.f.db.First(&node, "id = ?", f.f.nodes[1].NodeID).Error; err != nil || node.DesiredRevision != int64(base) {
			t.Fatalf("scheduled retirement omitted graph before stop ACK: %+v %v", node, err)
		}
		if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[1], batch); err != nil {
			t.Fatal(err)
		}
		return true
	})
	waitFleetPlacement(t, "publish stopped identity omission", func() bool {
		var err error
		deployment, err = f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
		if err != nil {
			t.Fatal(err)
		}
		return deployment != nil && deployment.Revision > base
	})
	if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil || !f.intent.Pending {
		t.Fatalf("published omission prematurely settled retirement: %+v %v", f.intent, err)
	}
	observe(nodev1.ApplyStage_APPLY_STAGE_APPLIED)
	waitFleetPlacement(t, "settle applied omission", func() bool {
		if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil {
			t.Fatal(err)
		}
		return !f.intent.Pending && f.intent.State == "retired"
	})
	var retained model.FleetGrant
	if err := f.f.db.First(&retained, "id = ?", grant.ID).Error; err != nil || retained.IssuedBytes != grant.IssuedBytes || retained.TermID != grant.TermID {
		t.Fatalf("scheduled retirement erased quota history: %+v %v", retained, err)
	}
}

func TestFleetPlacementNodeDefersInvalidCurrentIntentAndSkipsOldInstallationsPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	invalid := f.intent
	invalid.ID, invalid.ClientID, invalid.AvailableUnixMilli = uuid.NewString(), uuid.NewString(), -1
	if err := f.f.db.Create(&invalid).Error; err != nil {
		t.Fatal(err)
	}
	for range 9 {
		obsolete := invalid
		obsolete.ID, obsolete.InstallationID = uuid.NewString(), uuid.NewString()
		obsolete.RequiresStop, obsolete.AvailableUnixMilli = true, -2
		if err := f.f.db.Create(&obsolete).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := processFleetPlacementNode(t.Context(), f.f.db, f.intent.NodeID); err != nil {
		t.Fatalf("invalid placement stopped bounded node processing: %v", err)
	}
	var deferred model.FleetPlacementIntent
	if err := f.f.db.First(&deferred, "id = ?", invalid.ID).Error; err != nil || !deferred.Pending || deferred.AvailableUnixMilli <= time.Now().UnixMilli() || deferred.QueuedUnixMilli != invalid.QueuedUnixMilli {
		t.Fatalf("invalid intent did not retain a fair retry: %+v %v", deferred, err)
	}
	if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil || f.intent.MaterializedPlacementID == nil {
		t.Fatalf("obsolete installation work starved current application: %+v %v", f.intent, err)
	}
}

func TestFleetPlacementCanceledAttemptRetainsFairRetryPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProcessFleetPlacementIntent(ctx, f.f.db, f.intent); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled placement attempt error=%v", err)
	}
	var deferred model.FleetPlacementIntent
	if err := f.f.db.First(&deferred, "id = ?", f.intent.ID).Error; err != nil || deferred.AvailableUnixMilli <= time.Now().UnixMilli() || !deferred.Pending || deferred.MaterializedPlacementID != nil || deferred.QueuedUnixMilli != f.intent.QueuedUnixMilli {
		t.Fatalf("canceled attempt lost fair retry or partially applied: %+v %v", deferred, err)
	}
}

func TestFleetAuthoritySchedulersDriveAppliedPlacementBaselinePostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunFleetAuthoritySchedulers(ctx, f.f.db) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("authority scheduler exit=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("authority scheduler group did not cancel")
		}
	})
	var deployment *nodev1.Deployment
	waitFleetPlacement(t, "publish through the owned scheduler group", func() bool {
		var err error
		deployment, err = f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
		if err != nil {
			t.Fatal(err)
		}
		return deployment != nil && deployment.Revision > f.approved
	})
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Admissions) != 1 || len(bundle.Runtimes[0].Grants) != 0 {
		t.Fatalf("scheduler group invented initial forwarding authority: %+v %v", bundle, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation, PayloadSha256: deployment.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	waitFleetPlacement(t, "settle through the owned scheduler group", func() bool {
		if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil {
			t.Fatal(err)
		}
		return !f.intent.Pending && f.intent.State == "applied"
	})
	for _, row := range []any{&model.FleetTerm{}, &model.FleetGrant{}} {
		var count int64
		if err := f.f.db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("scheduler group activated client without demand: %T %d %v", row, count, err)
		}
	}
}
