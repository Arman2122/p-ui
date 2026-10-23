package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetSourceConcurrentEnableCompilesWholeNodesInSourceOrderPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1}); err != nil {
		t.Fatal(err)
	}
	for index, principal := range f.nodes {
		node, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: principal.NodeID, InstallationID: principal.InstallationID})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: node.Defaults.ID})
		if err != nil || !preview.Ready {
			t.Fatalf("initial network approval=%+v err=%v", preview.Diagnostics, err)
		}
		if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
			t.Fatal(err)
		}
		providerAckNode(t, f, index)
	}
	other := f
	other.client = model.ClientRecord{Email: "other-enable", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: f.client.ExpiryTime, Reset: 1}
	if err := f.db.Create(&other.client).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: other.client.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: other.client.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: other.client.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	var selected []model.FleetSourceWork
	for index, fixture := range []fleetClientFixture{f, other} {
		first := fixture.approve(t, fixture.materialize(t, 0))
		fixture.approve(t, fixture.materialize(t, 1))
		for _, enabled := range []bool{false, true} {
			if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, first.ClientID, enabled)); err != nil {
				t.Fatal(err)
			}
		}
		var works []model.FleetSourceWork
		if err := f.db.Where("client_id = ?", first.ClientID).Find(&works).Error; err != nil {
			t.Fatal(err)
		}
		for _, work := range works {
			advanced, err := ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
			if err != nil || advanced.RequiresStop || !advanced.Pending {
				t.Fatalf("empty authority stop=%+v err=%v", advanced, err)
			}
			if advanced.NodeID == f.nodes[index].NodeID {
				selected = append(selected, advanced)
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release, results := make(chan struct{}, 2), make(chan struct{}), make(chan error, 2)
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	for _, work := range selected {
		workers.Go(func() {
			_, err := ProcessFleetSourceWork(ctx, f.db, work, func(ctx context.Context, tx *gorm.DB, work model.FleetSourceWork) error {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				return ReconcileFleetSourceWorkTx(ctx, tx, work)
			})
			results <- err
		})
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("enable callbacks did not enter independently")
		}
	}
	close(release)
	workers.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("whole-node enable inverted source locks=%v", err)
		}
	}
	for _, node := range f.nodes {
		var saved model.FleetNode
		if err := f.db.First(&saved, "id = ?", node.NodeID).Error; err != nil || saved.DesiredRevision != 2 || saved.AppliedRevision != 1 {
			t.Fatalf("enable missed node publication=%+v err=%v", saved, err)
		}
	}
}

func sourceEnableFixture(t *testing.T) (authorityFixture, model.FleetSourceWork) {
	t.Helper()
	return sourceEnableFixtureWithNetwork(t, true, nil)
}

func sourceEnableFixtureWithNetwork(t *testing.T, publish bool, beforeStop func(authorityFixture, *FleetNetworkService, FleetNodeNetworkView)) (authorityFixture, model.FleetSourceWork) {
	t.Helper()
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := network.ApproveDefaults(t.Context(), &f.f.admin, 0, FleetNetworkSpec{Version: 1}); err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: node.Defaults.ID})
		if err != nil || !preview.Ready {
			t.Fatalf("source fixture network approval=%+v err=%v", preview.Diagnostics, err)
		}
		if _, err := network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
			t.Fatal(err)
		}
		base := applyFleetSourcePublication(t, f.f)
		scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, node.NodeID, base, "xray", f.materialized.Client.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = base, scope.PayloadSHA256
	}
	if beforeStop != nil {
		beforeStop(f, network, node)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, enabled)); err != nil {
			t.Fatal(err)
		}
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
		t.Fatalf("source enable fixture stop=%+v err=%v", batch, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx)
	if err != nil || !work.Pending || work.RequiresStop {
		t.Fatalf("source enable fixture stop proof=%+v err=%v", work, err)
	}
	return f, work
}

func TestFleetSourceStreamEnablePublishesFreshScopeAndDemandReusesCreditPostgres(t *testing.T) {
	f, work := sourceEnableFixture(t)
	if err := ReconcileFleetSourceWorkTx(t.Context(), f.f.db, work); !errors.Is(err, controlplane.ErrInvalid) {
		t.Fatalf("enable escaped transaction=%v", err)
	}
	pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !pending.Pending || pending.RequiresStop {
		t.Fatalf("source enable publication=%+v err=%v", pending, err)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(f.request.BaseConfigurationRevision)+1 || node.AppliedRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("enable publication applied prematurely=%+v err=%v", node, err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, pending, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	var compilations []model.FleetNetworkCompilation
	if err := f.f.db.Where("actor_id = 0").Find(&compilations).Error; err != nil || len(compilations) != 1 || compilations[0].ActorID != 0 {
		t.Fatalf("enable retry republished or invented an admin=%+v err=%v", compilations, err)
	}
	base := applyFleetSourcePublication(t, f.f)
	completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, pending, ReconcileFleetSourceWorkTx)
	if err != nil || completed.Pending {
		t.Fatalf("applied fresh source did not converge=%+v err=%v", completed, err)
	}
	var materialized FleetMaterialization
	if err := f.f.db.First(&materialized.Placement, "id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&materialized.Client, "id = ?", work.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Where("placement_id = ?", work.PlacementID).Find(&materialized.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, 2, time.Now())
	request := fleetDemand(materialized)
	request.DeploymentRevision = base
	if _, err := HandleFleetActivation(t.Context(), f.f.db, f.f.nodes[0], generation, request); err != nil {
		t.Fatal(err)
	}
	var demand model.FleetAdmissionDemand
	if err := f.f.db.First(&demand, "placement_id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	leases, err := f.f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("fresh enable demand lease=%+v err=%v", leases, err)
	}
	result, err := ProcessFleetAdmissionDemand(t.Context(), f.f.db, leases[0], demand)
	if err != nil || result.Outcome != FleetAdmissionSatisfied {
		t.Fatalf("fresh source demand=%+v err=%v", result, err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, work.NodeID, base, work.RuntimeID, work.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = base, scope.PayloadSHA256
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || !batch.Updates[0].Enabled || batch.Updates[0].Upload.Mode == nodev1.AuthorityRateMode_AUTHORITY_RATE_BLOCKED || batch.Updates[0].Download.Mode == nodev1.AuthorityRateMode_AUTHORITY_RATE_BLOCKED {
		t.Fatalf("fresh source did not restore usable authority=%+v err=%v", batch, err)
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != 300 || grant.Revision != f.credit.Grant.Revision || grant.TermID != f.active.Term.ID {
		t.Fatalf("source enable duplicated quota or term=%+v err=%v", grant, err)
	}
}

func TestFleetSourceEnableDoesNotReconcileChangedCredentialsPostgres(t *testing.T) {
	f, work := sourceEnableFixture(t)
	if err := f.f.db.Model(&model.ClientRecord{}).Where("id = ?", f.f.client.Id).Update("uuid", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("enable action adopted new credential authority=%v", err)
	}
	var client model.FleetClient
	var node model.FleetNode
	var count int64
	if err := f.f.db.First(&client, "id = ?", work.ClientID).Error; err != nil || client.CredentialGeneration != f.materialized.Client.CredentialGeneration {
		t.Fatalf("rejected enable changed credentials=%+v err=%v", client, err)
	}
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("rejected enable published=%+v err=%v", node, err)
	}
	if err := f.f.db.Model(&model.FleetNetworkCompilation{}).Where("actor_id = 0").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected enable leaked compilation=%d err=%v", count, err)
	}
}

func TestFleetSourceEnableAppliedBetweenPhasesDoesNotRepublishPostgres(t *testing.T) {
	f, work := sourceEnableFixture(t)
	work, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !work.Pending {
		t.Fatalf("initial enable=%+v err=%v", work, err)
	}
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.f.store.Delivery(t.Context(), f.f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, func(ctx context.Context, tx *gorm.DB, work model.FleetSourceWork) error {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		if err := store.Observe(ctx, f.f.nodes[0], generation, &nodev1.ApplyResult{
			AuthorityGeneration: generation, Revision: delivery.Revision, PayloadSha256: delivery.PayloadSha256,
			AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
		}); err != nil {
			return err
		}
		return ReconcileFleetSourceWorkTx(ctx, tx, work)
	})
	if err != nil || completed.Pending {
		t.Fatalf("applied between phases caused fresh publication=%+v err=%v", completed, err)
	}
	var node model.FleetNode
	var count int64
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(delivery.Revision) || node.AppliedRevision != int64(delivery.Revision) {
		t.Fatalf("applied enable republished node=%+v err=%v", node, err)
	}
	if err := f.f.db.Model(&model.FleetNetworkCompilation{}).Where("actor_id = 0").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("applied enable compiled again=%d err=%v", count, err)
	}
}

func TestFleetSourceNativeConvergenceRequiresUsableObservedAuthorityPostgres(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "usable", true: "blocked"}[blocked], func(t *testing.T) {
			f, network, preview := fleetNativePublicationFixture(t, model.OCServ)
			rates := controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2_000_000, BurstBytes: 65536}}
			if blocked {
				rates = controlplane.Rates{}
			}
			if _, err := network.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: rates}}); err != nil {
				t.Fatal(err)
			}
			base := applyFleetSourcePublication(t, f)
			work := model.FleetSourceWork{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, RuntimeID: "native", PlacementID: preview.NativeBindings[0].PlacementID, ClientID: preview.NativeBindings[0].ClientID}
			if done, err := fleetSourceWorkPublished(f.db, work); err != nil || done {
				t.Fatalf("unknown native spendable authority completed source work=%v err=%v", done, err)
			}
			var grant model.FleetGrant
			if err := f.db.First(&grant, "placement_id = ?", work.PlacementID).Error; err != nil {
				t.Fatal(err)
			}
			reportFleetSourceNativeCredit(t, f, grant, base, 1)
			if done, err := fleetSourceWorkPublished(f.db, work); err != nil || done == blocked {
				t.Fatalf("native source usability done=%v blocked=%v err=%v", done, blocked, err)
			}
			if blocked {
				return
			}
			for _, enabled := range []bool{false, true} {
				if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, work.ClientID, enabled)); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.db.First(&work, "placement_id = ?", work.PlacementID).Error; err != nil {
				t.Fatal(err)
			}
			work, err := ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
			if err != nil {
				t.Fatal(err)
			}
			applyFleetSourcePublication(t, f)
			work, err = ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
			if err != nil || work.RequiresStop || !work.Pending {
				t.Fatalf("native applied stop before enable=%+v err=%v", work, err)
			}
			if _, err := ProcessFleetSourceWork(t.Context(), f.db, work, ReconcileFleetSourceWorkTx); !errors.Is(err, ErrFleetNetworkFunding) {
				t.Fatalf("native enable declared service restored without safe funding=%v", err)
			}
		})
	}
}
