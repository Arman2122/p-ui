package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func settleFleetSourceStreamWork(t *testing.T, f fleetClientFixture, clientID string) model.FleetSourceWork {
	t.Helper()
	var work model.FleetSourceWork
	if err := f.db.First(&work, "client_id = ? AND node_id = ?", clientID, f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	work, err := ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil {
		t.Fatal(err)
	}
	var node model.FleetNode
	if err := f.db.First(&node, "id = ?", work.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.db, work.NodeID, uint64(node.AppliedRevision), work.RuntimeID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	journal := fleetCreditRequest(t, f)
	batch, err := f.store.FetchAuthorityUpdates(t.Context(), f.nodes[0], &nodev1.AuthorityDeliveryRequest{
		NodeId: journal.NodeId, InstallationId: journal.InstallationId, AgentStateJournalId: journal.AgentStateJournalId,
		RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId,
		BaseConfigurationRevision: uint64(node.AppliedRevision), BasePayloadSha256: scope.PayloadSHA256,
	}, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
		t.Fatalf("source retirement disabled authority=%+v err=%v", batch, err)
	}
	if _, err := f.store.AcknowledgeAuthorityUpdates(t.Context(), f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil || work.RequiresStop || work.Pending != work.Enabled {
		t.Fatalf("source retirement applied stop=%+v err=%v", work, err)
	}
	return work
}

func TestFleetSourceCompletedStreamDisableDoesNotBlockOtherEnablePostgres(t *testing.T) {
	f, originalWork := sourceEnableFixture(t)
	other := f.f
	other.client = model.ClientRecord{Email: "disabled-neighbor", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: f.f.client.ExpiryTime, Reset: 1}
	if err := f.f.db.Create(&other.client).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.f.inbounds {
		if err := f.f.db.Create(&model.ClientInbound{ClientId: other.client.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.f.db.Create(&core.ClientTraffic{Email: other.client.Email, InboundId: f.f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: other.client.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	materialized := other.materialize(t, 0)
	materialized.Placement = other.approve(t, materialized)
	other.activate(t, materialized.Placement)
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	var head model.FleetNodeNetworkHead
	if err := f.f.db.First(&head, "node_id = ?", originalWork.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: head.NodeID, InstallationID: head.InstallationID, ExpectedRevision: uint64(head.Revision), ExpectedDefaultsID: head.DesiredDefaultsID})
	if err != nil || !preview.Ready {
		t.Fatalf("two-client source preview=%+v err=%v", preview, err)
	}
	if _, err := network.PublishFunded(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, []FleetNetworkFunding{{PlacementID: materialized.Placement.ID, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}}); err != nil {
		t.Fatal(err)
	}
	applyFleetSourcePublication(t, f.f)
	if completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, originalWork, ReconcileFleetSourceWorkTx); err != nil || completed.Pending {
		t.Fatalf("initial B publication did not converge=%+v err=%v", completed, err)
	}
	fleetAuthorityReport(t, f.f, 2, time.Now())
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, materialized.Client.ID, false)); err != nil {
		t.Fatal(err)
	}
	settleFleetSourceStreamWork(t, f.f, materialized.Client.ID)
	for _, enabled := range []bool{false, true} {
		if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, originalWork.ClientID, enabled)); err != nil {
			t.Fatal(err)
		}
	}
	work := settleFleetSourceStreamWork(t, f.f, originalWork.ClientID)
	pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !pending.Pending {
		t.Fatalf("completed disabled stream blocked another enable=%+v err=%v", pending, err)
	}
	bundle := nativeObserveDeployment(t, f.f)
	if len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Grants) != 1 || bundle.Runtimes[0].Grants[0].ClientID != originalWork.ClientID {
		t.Fatalf("disabled source remained in fresh graph=%+v", bundle)
	}
	if completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, pending, ReconcileFleetSourceWorkTx); err != nil || completed.Pending {
		t.Fatalf("enabled neighbor did not converge=%+v err=%v", completed, err)
	}
	var disabled model.ClientRecord
	var grant model.FleetGrant
	if err := f.f.db.First(&disabled, other.client.Id).Error; err != nil || disabled.Enable {
		t.Fatalf("retirement changed disabled source=%+v err=%v", disabled, err)
	}
	if err := f.f.db.First(&grant, "placement_id = ?", materialized.Placement.ID).Error; err != nil || grant.IssuedBytes != 100 || grant.ConsumedBytes != 0 {
		t.Fatalf("retirement released or charged old quota=%+v err=%v", grant, err)
	}
}

func TestFleetSourceNativeDisableReclaimsAddressOnlyAfterAppliedOmissionPostgres(t *testing.T) {
	f, network, preview := fleetNativePublicationFixture(t, model.OCServ)
	rates := controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2_000_000, BurstBytes: 65536}}
	if _, err := network.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: rates}}); err != nil {
		t.Fatal(err)
	}
	applyFleetSourcePublication(t, f)
	if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, preview.NativeBindings[0].ClientID, false)); err != nil {
		t.Fatal(err)
	}
	var work model.FleetSourceWork
	if err := f.db.First(&work, "placement_id = ?", preview.NativeBindings[0].PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	work, err := ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil {
		t.Fatal(err)
	}
	applyFleetSourcePublication(t, f)
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil || work.Pending || work.RequiresStop {
		t.Fatalf("native source disable did not settle=%+v err=%v", work, err)
	}
	for _, test := range []struct {
		name   string
		change func(*gorm.DB) error
	}{
		{"missing-stop-proof", func(tx *gorm.DB) error {
			return tx.Where("grant_id IN (?)", tx.Model(&model.FleetGrant{}).Select("id").Where("placement_id = ?", work.PlacementID)).Delete(&model.FleetGrantStop{}).Error
		}},
		{"work-still-pending", func(tx *gorm.DB) error { return tx.Model(&work).Update("pending", true).Error }},
		{"source-reenabled", func(tx *gorm.DB) error {
			return tx.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update("enable", true).Error
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rollback := errors.New("restore source retirement proof")
			if err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := test.change(tx); err != nil {
					return err
				}
				service, err := NewFleetNetworkService(tx)
				if err != nil {
					return err
				}
				unsafe, err := service.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: preview.NodeID, InstallationID: preview.InstallationID, ExpectedRevision: preview.NodeRevision.Revision, ExpectedDefaultsID: preview.Defaults.ID})
				if err != nil || unsafe.Ready && len(unsafe.NativeBindings) == 0 {
					t.Fatalf("unproven source disable omitted native identity=%+v err=%v", unsafe, err)
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	removal, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: preview.NodeID, InstallationID: preview.InstallationID, ExpectedRevision: preview.NodeRevision.Revision, ExpectedDefaultsID: preview.Defaults.ID})
	if err != nil || !removal.Ready || len(removal.NativeBindings) != 0 || len(removal.Runtimes) != 0 {
		t.Fatalf("completed native source disable blocked removal=%+v err=%v", removal, err)
	}
	if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: removal.ID}); err != nil {
		t.Fatal(err)
	}
	var node model.FleetNode
	if err := f.db.First(&node, "id = ?", work.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if active, retired, err := loadFleetNativeBindings(f.db, node); err != nil || len(active) != 1 || len(retired) != 0 {
		t.Fatalf("unapplied source removal released native address=%v %v err=%v", active, retired, err)
	}
	applyFleetSourcePublication(t, f)
	if err := f.db.First(&node, "id = ?", work.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if active, retired, err := loadFleetNativeBindings(f.db, node); err != nil || len(active) != 0 || len(retired) != 1 {
		t.Fatalf("applied source removal retained native address=%v %v err=%v", active, retired, err)
	}
}
