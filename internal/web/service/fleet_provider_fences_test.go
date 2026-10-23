package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func TestFleetProviderSupersededTargetSurvivesEarlierApplied(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.refresh(t)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	old := f.work(t)
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls\ntrojan://fixture-c@third.example:443?security=tls")
	result, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1})
	if err != nil || result.CachedRevision != 3 {
		t.Fatalf("superseding refresh=%+v err=%v", result, err)
	}
	if _, err := f.service.FanoutDue(t.Context(), 8); err != nil {
		t.Fatal(err)
	}
	newer := f.work(t)
	if newer.Revision <= old.Revision || newer.OperationID == old.OperationID {
		t.Fatal("new target reused old rollout CAS")
	}
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.Diagnostic != "AWAITING_APPLIED" || !work.Pending {
		t.Fatalf("pending deployment was superseded without APPLIED=%+v", work)
	}
	f.ack(t)
	f.clock = f.clock.Add(2 * time.Second)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); !work.Pending || work.PublishedRevision != 3 {
		t.Fatalf("earlier APPLIED erased newer provider target=%+v", work)
	}
	f.ack(t)
	f.clock = f.clock.Add(2 * time.Second)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.Pending || work.PublishedRevision != 3 {
		t.Fatalf("latest target did not converge=%+v", work)
	}
}

func TestFleetProviderEmptyAndChangedSourceKeepAppliedGraph(t *testing.T) {
	for _, changeSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-cache", true: "source-credential-drift"}[changeSource], func(t *testing.T) {
			f := newProviderRolloutFixture(t, true)
			want := "PROVIDER_EMPTY"
			if changeSource {
				f.refresh(t)
				if err := f.f.db.Model(&model.ClientRecord{}).Where("id = ?", f.f.client.Id).Update("uuid", uuid.NewString()).Error; err != nil {
					t.Fatal(err)
				}
				want = "PLACEMENT_SOURCE_CHANGED"
			} else {
				f.body.Store("")
				if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.FanoutDue(t.Context(), 8); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if work := f.work(t); work.Diagnostic != want || !work.Pending || work.PublishedRevision != 0 {
				t.Fatalf("unsafe dependency changed graph=%+v want=%s", work, want)
			}
			var node model.FleetNode
			if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != 1 || node.AppliedRevision != 1 {
				t.Fatalf("failed rollout changed applied graph=%+v err=%v", node, err)
			}
		})
	}
}

func TestFleetProviderPinnedDefaultsDoNotFollowSharedRefresh(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.f.approve(t, f.f.materialize(t, 1))
	follow := false
	node, err := f.network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: f.f.nodes[1].NodeID, InstallationID: f.f.nodes[1].InstallationID, FollowDefaults: &follow, PinnedDefaultsID: f.previewRequest.ExpectedDefaultsID})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := f.network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: node.Defaults.ID})
	if err != nil || !preview.Ready {
		t.Fatalf("pinned preview=%+v err=%v", preview.Diagnostics, err)
	}
	if _, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	providerAckNode(t, f.f, 1)
	f.refresh(t)
	if _, err := f.service.WakeApplied(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", f.f.nodes[1].NodeID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("pinned default received rollout=%d err=%v", count, err)
	}
	var binding model.FleetPublishedProviderBinding
	if err := f.f.db.First(&binding, "node_id = ?", f.f.nodes[1].NodeID).Error; err != nil || binding.FollowLatest || binding.ProviderRevision != 1 {
		t.Fatalf("pinned publication changed=%+v err=%v", binding, err)
	}
}

func TestFleetProviderDisabledFollowerRetainsRefreshUntilEnabled(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	if _, err := f.service.WakeApplied(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Model(&model.FleetNode{}).Where("id = ?", f.f.nodes[0].NodeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	var count int64
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", f.f.nodes[0].NodeID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("disabled follower lost durable refresh: count=%d err=%v", count, err)
	}
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); !work.Pending || work.PublishedRevision != 0 {
		t.Fatalf("disabled follower published=%+v", work)
	}
	if err := f.f.db.Model(&model.FleetNode{}).Where("id = ?", f.f.nodes[0].NodeID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Second)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.PublishedRevision != 2 {
		t.Fatalf("reenabled follower did not apply retained refresh=%+v", work)
	}
}

func TestFleetProviderRematerializedSourceRequiresNewApprovedBase(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.refresh(t)
	if err := f.f.db.Model(&model.ClientRecord{}).Where("id = ?", f.f.client.Id).Update("uuid", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	f.f.approve(t, f.f.materialize(t, 0))
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.Diagnostic != "SOURCE_SCOPE_CHANGED" || !work.Pending || work.PublishedRevision != 0 {
		t.Fatalf("rematerialized source bypassed applied baseline=%+v", work)
	}
}

func TestFleetProviderStaleClaimCannotPublishSupersedingWork(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.refresh(t)
	lease := uuid.NewString()
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", f.f.nodes[0].NodeID).
		Updates(map[string]any{"lease_id": lease, "lease_until_unix_milli": f.clock.Add(time.Minute).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	old := f.work(t)
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls\ntrojan://fixture-c@third.example:443?security=tls")
	if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.FanoutDue(t.Context(), 8); err != nil {
		t.Fatal(err)
	}
	newer := f.work(t)
	var hold fleetProviderHold
	if err := f.service.processRollout(t.Context(), old); !errors.As(err, &hold) || hold.Code != "WORK_SUPERSEDED" {
		t.Fatalf("stale lease published superseding work: %v", err)
	}
	if current := f.work(t); current.Revision != newer.Revision || current.OperationID != newer.OperationID || current.PublishedRevision != 0 || !current.Pending {
		t.Fatalf("old callback changed superseding CAS=%+v", current)
	}
}

func providerAckNode(t *testing.T, f fleetClientFixture, index int) {
	t.Helper()
	node := f.nodes[index]
	generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{
		Revision: delivery.Revision, AuthorityGeneration: generation, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetProviderFanoutPagesRealAppliedPublications(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	for range 33 {
		node := identity.Principal{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
		if err := f.f.db.Create(&model.FleetNode{ID: node.NodeID, InstallationID: node.InstallationID, Name: "fanout-fixture", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.f.store.RegisterJournals(t.Context(), node, &nodev1.JournalRegistration{
			NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: uuid.NewString(),
			Runtimes: []*nodev1.RuntimeJournalIdentity{{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}},
		}); err != nil {
			t.Fatal(err)
		}
		f.f.nodes = append(f.f.nodes, node)
		index := len(f.f.nodes) - 1
		f.f.approve(t, f.f.materialize(t, index))
		view, err := f.network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: node.NodeID, InstallationID: node.InstallationID})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := f.network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: view.Revision.Revision, ExpectedDefaultsID: view.Defaults.ID})
		if err != nil || !preview.Ready {
			t.Fatalf("fanout preview ready=%v err=%v", preview.Ready, err)
		}
		if _, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
			t.Fatal(err)
		}
		providerAckNode(t, f.f, index)
	}
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls")
	if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{32, 34} {
		if pages, err := f.service.FanoutDue(t.Context(), 1); err != nil || pages != 1 {
			t.Fatalf("fanout pages=%d err=%v", pages, err)
		}
		var count int64
		if err := f.f.db.Model(&model.FleetProviderRollout{}).Count(&count).Error; err != nil || count != want {
			t.Fatalf("bounded fanout count=%d want=%d err=%v", count, want, err)
		}
	}
	var source model.FleetOutboundProvider
	if err := f.f.db.First(&source, "id = ?", f.provider.ProviderID).Error; err != nil || source.FanoutPending {
		t.Fatalf("fanout cursor did not finish: pending=%v err=%v", source.FanoutPending, err)
	}
	var grants int64
	if err := f.f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("fanout allocated authority=%d err=%v", grants, err)
	}
	var work model.FleetProviderRollout
	if err := f.f.db.First(&work).Error; err != nil {
		t.Fatal(err)
	}
	var targets map[string]FleetNetworkProviderPin
	if err := json.Unmarshal([]byte(work.Targets), &targets); err != nil || len(targets) != 1 {
		t.Fatalf("fanout target vector len=%d err=%v", len(targets), err)
	}
}
