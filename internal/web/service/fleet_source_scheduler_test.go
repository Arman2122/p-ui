package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetSourceSchedulerPublishesStopAndCompletesOnlyAfterAppliedReceipt(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunFleetSourceScheduler(ctx, f.f.db) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("source scheduler exit=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("source scheduler did not cancel")
		}
	})
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	acknowledged := false
	for {
		var work model.FleetSourceWork
		if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
			t.Fatal(err)
		}
		if !work.Pending {
			if !acknowledged || work.RequiresStop || work.AppliedStopRevision != 1 {
				t.Fatalf("source scheduler completed without exact applied stop=%+v acknowledged=%v", work, acknowledged)
			}
			break
		}
		if !acknowledged {
			batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
			if err != nil {
				t.Fatal(err)
			}
			if batch != nil && len(batch.Updates) == 1 && !batch.Updates[0].Enabled {
				if !work.RequiresStop || batch.Updates[0].CreditCeiling != uint64(f.credit.Grant.IssuedBytes) {
					t.Fatal("scheduled stop settled early or changed credit")
				}
				if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
					t.Fatal(err)
				}
				acknowledged = true
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("source scheduler failed to converge; acknowledged=%v work=%+v", acknowledged, work)
		case <-ticker.C:
		}
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.ConsumedBytes != 0 || grant.IssuedBytes != f.credit.Grant.IssuedBytes {
		t.Fatalf("scheduled disable changed billing=%+v err=%v", grant, err)
	}
}

func TestFleetSourceSchedulerDefersInvalidTargetWithoutStarvingRealStop(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	var valid model.FleetSourceWork
	if err := f.f.db.First(&valid, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.PlacementID, invalid.OperationID, invalid.AvailableUnixMilli = uuid.NewString(), uuid.NewString(), 0
	if err := f.f.db.Create(&invalid).Error; err != nil {
		t.Fatal(err)
	}
	for range 9 {
		orphan := valid
		orphan.PlacementID, orphan.OperationID, orphan.InstallationID = uuid.NewString(), uuid.NewString(), uuid.NewString()
		orphan.AvailableUnixMilli = -1
		if err := f.f.db.Create(&orphan).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := processFleetSourceNode(t.Context(), f.f.db, valid.NodeID); err != nil {
		t.Fatalf("invalid source target stopped other work=%v", err)
	}
	var deferred model.FleetSourceWork
	if err := f.f.db.First(&deferred, "placement_id = ?", invalid.PlacementID).Error; err != nil || !deferred.Pending || !deferred.RequiresStop || deferred.AvailableUnixMilli <= time.Now().UnixMilli() || deferred.QueuedUnixMilli != invalid.QueuedUnixMilli {
		t.Fatalf("invalid source target lost intent or did not back off=%+v err=%v", deferred, err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || batch == nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
		t.Fatalf("invalid source target starved real stop=%+v err=%v", batch, err)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Where("node_id = ?", valid.NodeID).Update("available_unix_milli", time.Now().Add(time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	nodes, err := dueFleetSourceNodes(t.Context(), f.f.db, "")
	if err != nil || len(nodes) != 0 {
		t.Fatalf("scheduler selected backoff-only node=%v err=%v", nodes, err)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Where("placement_id = ?", valid.PlacementID).Update("available_unix_milli", 0).Error; err != nil {
		t.Fatal(err)
	}
	nodes, err = dueFleetSourceNodes(t.Context(), f.f.db, "")
	if err != nil || len(nodes) != 1 || nodes[0] != valid.NodeID {
		t.Fatalf("due source node missing=%v err=%v", nodes, err)
	}
	if nodes, err = dueFleetSourceNodes(t.Context(), f.f.db, valid.NodeID); err != nil || len(nodes) != 0 {
		t.Fatalf("node cursor revisited busy node=%v err=%v", nodes, err)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Where("placement_id = ?", valid.PlacementID).Update("installation_id", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	if nodes, err = dueFleetSourceNodes(t.Context(), f.f.db, ""); err != nil || len(nodes) != 0 {
		t.Fatalf("orphan installation scheduled=%v err=%v", nodes, err)
	}
}

func TestFleetSourceSchedulerRefusesTransactionOwnership(t *testing.T) {
	f := fleetFixture(t, false)
	tx := f.db.Begin()
	defer tx.Rollback()
	if err := RunFleetSourceScheduler(t.Context(), tx); !errors.Is(err, controlplane.ErrInvalid) {
		t.Fatalf("source scheduler accepted transaction=%v", err)
	}
}

func TestFleetSourceSchedulerReenablesThroughNewAppliedScope(t *testing.T) {
	f, work := sourceEnableFixture(t)
	if err := processFleetSourceNode(t.Context(), f.f.db, work.NodeID); err != nil {
		t.Fatal(err)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(f.request.BaseConfigurationRevision)+1 || node.AppliedRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("scheduler did not publish enabled source=%+v err=%v", node, err)
	}
	var pending model.FleetSourceWork
	if err := f.f.db.First(&pending, "placement_id = ?", work.PlacementID).Error; err != nil || !pending.Pending || pending.RequiresStop {
		t.Fatalf("scheduler completed enabled source before applied receipt=%+v err=%v", pending, err)
	}
	if base := applyFleetSourcePublication(t, f.f); base != f.request.BaseConfigurationRevision+1 {
		t.Fatalf("applied source base=%d", base)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Where("placement_id = ?", work.PlacementID).Update("available_unix_milli", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := processFleetSourceNode(t.Context(), f.f.db, work.NodeID); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&pending, "placement_id = ?", work.PlacementID).Error; err != nil || pending.Pending || pending.RequiresStop || pending.AppliedStopRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("scheduler did not finish applied enabled source=%+v err=%v", pending, err)
	}
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(f.request.BaseConfigurationRevision)+1 {
		t.Fatalf("scheduler republished already applied source=%+v err=%v", node, err)
	}
}
