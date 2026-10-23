package service

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetSourceStreamStopActionQueuesOnceAndWaitsForAppliedReceipt(t *testing.T) {
	f := newAuthorityFixture(t)
	active := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], active); err != nil {
		t.Fatal(err)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	work, err := PendingFleetSourceWork(t.Context(), f.f.db, f.f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 {
		t.Fatalf("source stop work=%+v %v", work, err)
	}
	if err := StopFleetSourceWorkTx(t.Context(), f.f.db, work[0]); !errors.Is(err, controlplane.ErrInvalid) {
		t.Fatalf("stop escaped source transaction=%v", err)
	}
	queued, err := ProcessFleetSourceWork(t.Context(), f.f.db, work[0], StopFleetSourceWorkTx)
	if err != nil || !queued.Pending || !queued.RequiresStop {
		t.Fatalf("stream stop action=%+v %v", queued, err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || batch == nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled || batch.Updates[0].CreditCeiling != uint64(f.credit.Grant.IssuedBytes) || batch.Updates[0].AuthorityRevision != active.Updates[0].AuthorityRevision+1 {
		t.Fatalf("disabled stream authority=%+v %v", batch, err)
	}
	retried, err := ProcessFleetSourceWork(t.Context(), f.f.db, queued, StopFleetSourceWorkTx)
	if err != nil || !retried.RequiresStop {
		t.Fatalf("stop retry fabricated applied receipt=%+v %v", retried, err)
	}
	replay, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || replay == nil || len(replay.Updates) != 1 || replay.Updates[0].AuthorityRevision != batch.Updates[0].AuthorityRevision {
		t.Fatalf("retry minted new stop authority=%+v %v", replay, err)
	}
	var node model.FleetNode
	var grant model.FleetGrant
	var share model.FleetRateShare
	if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != 1 {
		t.Fatalf("stream stop republished node graph=%+v %v", node, err)
	}
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.Revision != f.credit.Grant.Revision || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.ConsumedBytes != 0 {
		t.Fatalf("stop changed traffic credit=%+v %v", grant, err)
	}
	if err := f.f.db.First(&share, "id = ?", grant.ID).Error; err != nil || share.AppliedRevision != int64(active.Updates[0].AuthorityRevision) {
		t.Fatalf("stop send became a rate ACK=%+v %v", share, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, retried, StopFleetSourceWorkTx)
	if err != nil || completed.Pending || completed.RequiresStop || completed.AppliedStopRevision != 1 {
		t.Fatalf("applied stream stop did not settle=%+v %v", completed, err)
	}
}

func TestFleetSourceNativeStopSupersessionRollsBackRealPublication(t *testing.T) {
	f, service, preview := fleetNativePublicationFixture(t, model.OCServ)
	funding := []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2000000, BurstBytes: 65536}}}}
	if _, err := service.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, funding); err != nil {
		t.Fatal(err)
	}
	original := nativeObserveDeployment(t, f)
	clientID := preview.NativeBindings[0].ClientID
	if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, clientID, false)); err != nil {
		t.Fatal(err)
	}
	work, err := PendingFleetSourceWork(t.Context(), f.db, f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 {
		t.Fatalf("native supersession work=%+v %v", work, err)
	}
	var changed atomic.Bool
	var changeErr error
	callback := "test:source_stop_supersession"
	if err := f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "fleet_clients" || len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "id,panel_client_id,owned" || !changed.CompareAndSwap(false, true) {
			return
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, changeErr = SetFleetClientEnabledTx(ctx, f.db, &f.admin, sourceEnableRequest(t, f, clientID, true))
	}); err != nil {
		t.Fatal(err)
	}
	_, stopErr := ProcessFleetSourceWork(t.Context(), f.db, work[0], StopFleetSourceWorkTx)
	_ = f.db.Callback().Query().Remove(callback)
	if !changed.Load() || changeErr != nil || !errors.Is(stopErr, controlplane.ErrRevision) {
		t.Fatalf("real stop supersession=%v changed=%v source=%v", stopErr, changed.Load(), changeErr)
	}
	var node model.FleetNode
	var share model.FleetRateShare
	var source model.ClientRecord
	if err := f.db.First(&node, "id = ?", f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != 1 || node.AppliedRevision != 1 {
		t.Fatalf("superseded stop published graph=%+v %v", node, err)
	}
	if err := f.db.First(&share, "id = ?", original.Runtimes[0].Grants[0].ID).Error; err != nil || share.Revision != 1 || share.AppliedRevision != 1 {
		t.Fatalf("superseded stop zeroed rates=%+v %v", share, err)
	}
	if err := f.db.First(&source, f.client.Id).Error; err != nil || !source.Enable {
		t.Fatalf("newer source change was lost=%+v %v", source, err)
	}
}

func TestFleetSourceNativeStopActionPublishesFrozenGraphBeforeSettlement(t *testing.T) {
	f, service, preview := fleetNativePublicationFixture(t, model.OCServ)
	funding := []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2000000, BurstBytes: 65536}}}}
	if _, err := service.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, funding); err != nil {
		t.Fatal(err)
	}
	original := nativeObserveDeployment(t, f)
	clientID := preview.NativeBindings[0].ClientID
	if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, clientID, false)); err != nil {
		t.Fatal(err)
	}
	work, err := PendingFleetSourceWork(t.Context(), f.db, f.nodes[0].NodeID, 128)
	if err != nil || len(work) != 1 {
		t.Fatalf("native stop work=%+v %v", work, err)
	}
	queued, err := ProcessFleetSourceWork(t.Context(), f.db, work[0], StopFleetSourceWorkTx)
	if err != nil || !queued.Pending || !queued.RequiresStop {
		t.Fatalf("native stop action=%+v %v", queued, err)
	}
	var node model.FleetNode
	if err := f.db.First(&node, "id = ?", f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != 2 || node.AppliedRevision != 1 {
		t.Fatalf("native stop send became an applied graph=%+v %v", node, err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.db, queued, StopFleetSourceWorkTx); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("pending graph was overwritten=%v", err)
	}
	applied := nativeObserveDeployment(t, f)
	if len(applied.Runtimes) != 1 || !bytes.Equal(applied.Runtimes[0].Configuration, original.Runtimes[0].Configuration) || len(applied.Runtimes[0].Grants) != 1 || applied.Runtimes[0].Grants[0].Policy.Enabled || applied.Runtimes[0].Grants[0].Policy.CreditCeiling != 100 {
		t.Fatal("native stop changed credentials/networking or increased authority")
	}
	completed, err := ProcessFleetSourceWork(t.Context(), f.db, queued, StopFleetSourceWorkTx)
	if err != nil || completed.Pending || completed.RequiresStop || completed.AppliedStopRevision != 2 {
		t.Fatalf("applied native stop did not settle=%+v %v", completed, err)
	}
}
