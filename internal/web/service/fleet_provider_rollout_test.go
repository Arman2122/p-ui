package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

type providerRolloutFixture struct {
	f              fleetClientFixture
	service        *FleetProviderService
	network        *FleetNetworkService
	provider       FleetProviderRequest
	materialized   FleetMaterialization
	body           *atomic.Value
	clock          time.Time
	generation     uint64
	previewRequest FleetNetworkPreviewRequest
}

func newProviderRolloutFixture(t *testing.T, applied bool) *providerRolloutFixture {
	t.Helper()
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	service, err := NewFleetProviderService(t.Context(), f.db)
	if err != nil {
		t.Fatal(err)
	}
	body := new(atomic.Value)
	body.Store("trojan://fixture-a@edge.example:443?security=tls")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body.Load().(string)) }))
	t.Cleanup(server.Close)
	provider := configureProviderFixture(t, service, f.admin, server.URL)
	if _, err := service.Refresh(t.Context(), &f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: provider.ProviderID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 2, Bases: map[string]FleetRuntimeBase{
		"xray": {Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"protocol":"freedom","tag":"direct","settings":{}}]}`), Providers: []FleetProviderBinding{{ProviderID: provider.ProviderID, Revision: 1, FollowLatest: true}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &providerRolloutFixture{
		f: f, service: service, network: network, provider: provider, materialized: materialized, body: body, clock: time.Now(),
		previewRequest: FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID},
	}
	service.now = func() time.Time { return fixture.clock }
	preview, err := network.Preview(t.Context(), &f.admin, fixture.previewRequest)
	if err != nil || !preview.Ready {
		t.Fatalf("provider preview ready=%v diagnostics=%+v err=%v", preview.Ready, preview.Diagnostics, err)
	}
	publication, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID})
	if err != nil || publication.DeploymentRevision != 1 {
		t.Fatalf("initial publication=%+v err=%v", publication, err)
	}
	fixture.generation, err = f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		fixture.ack(t)
	}
	return fixture
}

func (f *providerRolloutFixture) ack(t *testing.T) {
	t.Helper()
	delivery, err := f.f.store.Delivery(t.Context(), f.f.nodes[0], f.generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[0], f.generation, &nodev1.ApplyResult{
		Revision: delivery.Revision, AuthorityGeneration: f.generation, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *providerRolloutFixture) refresh(t *testing.T) {
	t.Helper()
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls")
	result, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1})
	if err != nil || result.CachedRevision != 2 {
		t.Fatalf("new provider cache=%+v err=%v", result, err)
	}
	if _, err := f.service.FanoutDue(t.Context(), 8); err != nil {
		t.Fatal(err)
	}
}

func (f *providerRolloutFixture) work(t *testing.T) model.FleetProviderRollout {
	t.Helper()
	var work model.FleetProviderRollout
	if err := f.f.db.First(&work, "node_id = ?", f.f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	return work
}

func TestFleetProviderRolloutPublishesThenWaitsForAppliedWithoutFunding(t *testing.T) {
	for _, activate := range []bool{false, true} {
		t.Run(map[bool]string{false: "dormant", true: "first-demand-before-refresh"}[activate], func(t *testing.T) {
			f := newProviderRolloutFixture(t, true)
			if activate {
				demand := fleetDemand(f.materialized)
				if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), f.f.nodes[0], f.generation, f.materialized.Placement.ID, demand); err != nil {
					t.Fatal(err)
				}
			}
			f.refresh(t)
			if count, err := f.service.ProcessRollouts(t.Context(), 1); err != nil || count != 1 {
				t.Fatalf("rollout count=%d err=%v", count, err)
			}
			work := f.work(t)
			if !work.Pending || work.PublishedRevision != 2 || work.PublishedCompilationID == nil || work.Diagnostic != "" {
				t.Fatalf("published work=%+v", work)
			}
			var node model.FleetNode
			if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != 2 || node.AppliedRevision != 1 {
				t.Fatalf("published is not applied: %+v err=%v", node, err)
			}
			f.ack(t)
			f.clock = f.clock.Add(2 * time.Second)
			if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if work = f.work(t); work.Pending || work.PublishedRevision != 2 {
				t.Fatalf("APPLIED work remained pending=%+v", work)
			}
			var grants, terms int64
			if err := f.f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
				t.Fatalf("refresh issued grants=%d err=%v", grants, err)
			}
			wantTerms := int64(0)
			if activate {
				wantTerms = 1
			}
			if err := f.f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != wantTerms {
				t.Fatalf("refresh activated billing=%d err=%v", terms, err)
			}
		})
	}
}

func TestFleetProviderLateAppliedFollowerWakesAfterFanoutCompleted(t *testing.T) {
	f := newProviderRolloutFixture(t, false)
	f.refresh(t)
	var count int64
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("desired-only follower queued=%d err=%v", count, err)
	}
	var source model.FleetOutboundProvider
	if err := f.f.db.First(&source, "id = ?", f.provider.ProviderID).Error; err != nil || source.FanoutPending {
		t.Fatalf("fanout not exhausted: pending=%v err=%v", source.FanoutPending, err)
	}
	f.ack(t)
	if nodes, err := f.service.WakeApplied(t.Context(), 32); err != nil || nodes != 1 {
		t.Fatalf("late APPLIED wake nodes=%d err=%v", nodes, err)
	}
	if work := f.work(t); !work.Pending || work.BaseDeploymentRevision != 1 {
		t.Fatalf("late follower lost refresh=%+v", work)
	}
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.PublishedRevision != 2 {
		t.Fatalf("late follower did not publish=%+v", work)
	}
}

func TestFleetProviderManualDraftDefersUntilCompatibleAppliedBase(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.refresh(t)
	changedBase := FleetRuntimeBase{
		Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"protocol":"freedom","tag":"direct","settings":{}},{"protocol":"blackhole","tag":"manual-block","settings":{}}]}`),
		Providers: []FleetProviderBinding{{ProviderID: f.provider.ProviderID, Revision: 1, FollowLatest: true}},
	}
	node, err := f.network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{
		NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, ExpectedRevision: 1,
		Operations: []FleetNetworkOperation{{Section: "base", Key: "xray", Operation: "replace", Base: &changedBase}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.Diagnostic != "MANUAL_NETWORK_PENDING" || work.PublishedRevision != 0 {
		t.Fatalf("draft published automatically=%+v", work)
	}
	f.previewRequest.ExpectedRevision = node.Revision.Revision
	preview, err := f.network.Preview(t.Context(), &f.f.admin, f.previewRequest)
	if err != nil || !preview.Ready {
		t.Fatalf("manual preview ready=%v diagnostics=%+v err=%v", preview.Ready, preview.Diagnostics, err)
	}
	publication, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.ack(t)
	f.clock = f.clock.Add(2 * time.Second)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.Pending || work.Revision != 2 || work.BaseCompilationID != preview.ID || work.PublishedRevision != int64(publication.DeploymentRevision) {
		t.Fatalf("compatible APPLIED rebase=%+v", work)
	}
}

func TestFleetProviderUnchangedPublicationRefreshesDerivedPins(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	preview, err := f.network.Preview(t.Context(), &f.f.admin, f.previewRequest)
	if err != nil || !preview.Ready {
		t.Fatalf("unchanged preview ready=%v err=%v", preview.Ready, err)
	}
	publication, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID})
	if err != nil || publication.DeploymentRevision != 1 {
		t.Fatalf("unchanged publication revision=%d err=%v", publication.DeploymentRevision, err)
	}
	f.refresh(t)
	if _, err := f.service.ProcessRollouts(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if work := f.work(t); work.PublishedRevision != 2 {
		t.Fatalf("unchanged publication lost future refresh=%+v", work)
	}
}
