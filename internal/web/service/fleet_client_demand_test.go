package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestFleetAuthOnlyPublicationQueuesFreshDemandWithoutFunding(t *testing.T) {
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: 1, ExpectedDefaultsID: defaults.ID})
	if err != nil || !preview.Ready || len(preview.Identities) != 1 || preview.Identities[0].State != "dormant" || len(preview.Identities[0].InboundTags) != 2 {
		t.Fatalf("qualified auth-only preview=%+v err=%v", preview, err)
	}
	if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	var terms int64
	if err := f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != 0 {
		t.Fatalf("auth-only publication started billing=%d err=%v", terms, err)
	}
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configuration.Decode(delivery.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Admissions) != 2 || len(bundle.Runtimes[0].Grants) != 0 {
		t.Fatalf("auth-only deployment invented forwarding authority=%+v err=%v", bundle, err)
	}
	if err := f.store.Observe(t.Context(), f.nodes[0], generation, &nodev1.ApplyResult{
		Revision: delivery.Revision, AuthorityGeneration: generation, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	request := fleetDemand(materialized)
	request.DeploymentRevision = delivery.Revision
	request.DeadlineUnixMilli = time.Now().Add(14 * time.Second).UnixMilli()
	activated, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, request)
	if err != nil || activated.TermId == "" {
		t.Fatalf("fresh authenticated activation=%+v err=%v", activated, err)
	}
	var first model.FleetAdmissionDemand
	if err := f.db.First(&first, "placement_id = ?", materialized.Placement.ID).Error; err != nil || first.TermID != activated.TermId || first.Revision != 1 || !first.Pending || !bytes.Equal(first.DeploymentDigest, delivery.PayloadSha256) {
		t.Fatalf("activation and demand were not atomic=%+v err=%v", first, err)
	}
	if _, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, request); err != nil {
		t.Fatal(err)
	}
	var replay model.FleetAdmissionDemand
	if err := f.db.First(&replay, "placement_id = ?", first.PlacementID).Error; err != nil || replay.Revision != first.Revision || replay.ReceivedUnixMilli != first.ReceivedUnixMilli || replay.DeadlineUnixMilli != first.DeadlineUnixMilli {
		t.Fatalf("duplicate demand refreshed live intent=%+v err=%v", replay, err)
	}
	leases, err := f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("admission lease=%+v err=%v", leases, err)
	}
	lease := leases[0]
	if duplicate, err := f.store.ClaimAdmissionNodes(t.Context(), 1); err != nil || len(duplicate) != 0 {
		t.Fatalf("live admission lease claimed twice=%+v err=%v", duplicate, err)
	}
	queued, err := f.store.DueAdmissionDemands(t.Context(), lease)
	if err != nil || len(queued) != 1 || queued[0].RequestID != request.RequestId {
		t.Fatalf("fresh queue=%+v err=%v", queued, err)
	}
	failure := errors.New("authority action rolls back")
	marker := uuid.NewString()
	if err := WithFleetAdmissionDemand(t.Context(), f.db, lease, first, func(_ context.Context, tx *gorm.DB, authorized FleetAdmissionAuthorization) error {
		if authorized.Term.ID != activated.TermId || authorized.Descriptor.InboundInstanceID != request.InboundInstanceId {
			t.Fatal("demand action lost authoritative term/descriptor")
		}
		if err := tx.Create(&model.FleetOperation{ID: marker, Digest: make([]byte, 32), Result: []byte("rollback")}).Error; err != nil {
			return err
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("action rollback=%v", err)
	}
	var markers int64
	if err := f.db.Model(&model.FleetOperation{}).Where("id = ?", marker).Count(&markers).Error; err != nil || markers != 0 {
		t.Fatalf("failed demand action committed effects=%d err=%v", markers, err)
	}
	secondRequest := proto.Clone(request).(*nodev1.ActivationRequest)
	secondRequest.RequestId, secondRequest.DeadlineUnixMilli = uuid.NewString(), time.Now().Add(14*time.Second).UnixMilli()
	if result, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, secondRequest); err != nil || result.TermId != activated.TermId || result.ExpiresUnixMilli != activated.ExpiresUnixMilli {
		t.Fatalf("new demand renewed an active term=%+v err=%v", result, err)
	}
	if err := f.store.SettleAdmissionDemand(t.Context(), lease, first, false); err != nil {
		t.Fatal(err)
	}
	var second model.FleetAdmissionDemand
	if err := f.db.First(&second, "placement_id = ?", first.PlacementID).Error; err != nil || !second.Pending || second.Revision != 2 || second.RequestID != secondRequest.RequestId {
		t.Fatalf("old completion erased fresh authentication=%+v err=%v", second, err)
	}
	called := false
	action := func(_ context.Context, _ *gorm.DB, _ FleetAdmissionAuthorization) error { called = true; return nil }
	if err := WithFleetAdmissionDemand(t.Context(), f.db, lease, first, action); !errors.Is(err, ErrFleetSource) || called {
		t.Fatalf("superseded authentication reached allocator=%v called=%v", err, called)
	}
	for _, tc := range []struct {
		name string
		edit func(*gorm.DB) error
		want error
	}{
		{"source", func(tx *gorm.DB) error {
			return tx.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update("security", "changed-after-auth").Error
		}, ErrFleetSource},
		{"session", func(tx *gorm.DB) error {
			return tx.Model(&model.FleetNode{}).Where("id = ?", node.NodeID).Update("authority_generation", int64(generation+1)).Error
		}, ErrFleetSource},
		{"journal", func(tx *gorm.DB) error {
			return tx.Model(&model.FleetRuntimeJournal{}).Where("node_id = ? AND runtime_id = ?", node.NodeID, "xray").Update("state_journal_id", uuid.NewString()).Error
		}, controlplane.ErrRecovery},
	} {
		if err := f.db.Transaction(func(tx *gorm.DB) error {
			if err := tc.edit(tx); err != nil {
				return err
			}
			called = false
			if err := WithFleetAdmissionDemand(t.Context(), tx, lease, second, action); !errors.Is(err, tc.want) || called {
				t.Errorf("changed %s reached demand allocator=%v called=%v", tc.name, err, called)
			}
			return failure
		}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if err := WithFleetAdmissionDemand(t.Context(), f.db, lease, second, action); err != nil || !called {
		t.Fatalf("fresh bounded action=%v called=%v", err, called)
	}
	if _, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, secondRequest); err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&second, "placement_id = ?", first.PlacementID).Error; err != nil || second.Pending || second.Revision != 2 {
		t.Fatalf("completed request was requeued=%+v err=%v", second, err)
	}
	if err := f.store.ReleaseAdmissionNode(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	thirdRequest := proto.Clone(secondRequest).(*nodev1.ActivationRequest)
	thirdRequest.RequestId, thirdRequest.DeadlineUnixMilli = uuid.NewString(), time.Now().Add(14*time.Second).UnixMilli()
	if _, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, thirdRequest); err != nil {
		t.Fatal(err)
	}
	leases, err = f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("replacement admission lease=%+v err=%v", leases, err)
	}
	if err := f.store.ReleaseAdmissionNode(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	var owner model.FleetAdmissionNodeQueue
	if err := f.db.First(&owner, "node_id = ?", lease.NodeID).Error; err != nil || owner.ClaimID == nil || *owner.ClaimID != *leases[0].ClaimID {
		t.Fatalf("stale worker released new admission owner=%+v err=%v", owner, err)
	}
	var third model.FleetAdmissionDemand
	if err := f.db.First(&third, "placement_id = ?", first.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if err := f.db.Model(&model.FleetAdmissionDemand{}).Where("placement_id = ?", first.PlacementID).Updates(map[string]any{"received_unix_milli": now - 100, "deadline_unix_milli": now - 1}).Error; err != nil {
		t.Fatal(err)
	}
	called = false
	if err := WithFleetAdmissionDemand(t.Context(), f.db, leases[0], third, action); !errors.Is(err, ErrFleetSource) || called {
		t.Fatalf("expired intent reached demand allocator=%v called=%v", err, called)
	}
	if expired, err := f.store.DueAdmissionDemands(t.Context(), leases[0]); err != nil || len(expired) != 0 {
		t.Fatalf("expired authentication was replayed=%+v err=%v", expired, err)
	}
	if err := f.store.ReleaseAdmissionNode(t.Context(), leases[0]); err != nil {
		t.Fatal(err)
	}
	var grants int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("demand queue invented quota/rate authority=%d err=%v", grants, err)
	}
	if err := f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != 1 {
		t.Fatalf("demand processing replayed activation=%d err=%v", terms, err)
	}
}
