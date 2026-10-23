package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetProviderSchedulersCancelPendingFetchAndJoin(t *testing.T) {
	service, actor := fleetProviderFixture(t)
	entered, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(stopped) }))
	defer server.Close()
	config := configureProviderFixture(t, service, actor, server.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunFleetProviderSchedulers(ctx, service.db) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider scheduler did not start due fetch")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler cancellation=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider schedulers did not join after cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("provider HTTP request outlived lifecycle")
	}
	var source model.FleetOutboundProvider
	if err := service.db.First(&source, "id = ?", config.ProviderID).Error; err != nil || source.CachedRevision != 0 {
		t.Fatalf("cancelled fetch committed cache=%d err=%v", source.CachedRevision, err)
	}
}

func TestFleetProviderUnknownPublicationBackoffDoesNotStarveNeighbor(t *testing.T) {
	f := newProviderRolloutFixture(t, true)
	f.f.approve(t, f.f.materialize(t, 1))
	node, err := f.network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{NodeID: f.f.nodes[1].NodeID, InstallationID: f.f.nodes[1].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := f.network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: node.Defaults.ID})
	if err != nil || !preview.Ready {
		t.Fatalf("neighbor preview ready=%v err=%v", preview.Ready, err)
	}
	if _, err := f.network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	providerAckNode(t, f.f, 1)
	if err := f.f.db.Model(&model.FleetPublishedProviderBinding{}).Where("node_id = ?", f.f.nodes[0].NodeID).Update("scope_known", false).Error; err != nil {
		t.Fatal(err)
	}
	f.body.Store("trojan://fixture-a@edge.example:443?security=tls\ntrojan://fixture-b@other.example:443?security=tls")
	if _, err := f.service.Refresh(t.Context(), &f.f.admin, FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: f.provider.ProviderID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if nodes, err := f.service.WakeApplied(t.Context(), 32); err != nil || nodes != 2 {
		t.Fatalf("unknown publication starved independent follower: nodes=%d err=%v", nodes, err)
	}
	var unknown model.FleetPublishedProviderBinding
	if err := f.f.db.First(&unknown, "node_id = ?", f.f.nodes[0].NodeID).Error; err != nil || !unknown.Pending || unknown.ReadyUnixMilli <= f.clock.UnixMilli() || unknown.Diagnostic != "PUBLICATION_SCOPE_UNAVAILABLE" {
		t.Fatalf("unknown publication lost visible backoff: pending=%v ready=%d diagnostic=%s err=%v", unknown.Pending, unknown.ReadyUnixMilli, unknown.Diagnostic, err)
	}
	var count int64
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", node.NodeID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("neighbor not queued: count=%d err=%v", count, err)
	}
	if err := f.f.db.Model(&model.FleetProviderRollout{}).Where("node_id = ?", f.f.nodes[0].NodeID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unknown projection authorized rollout: count=%d err=%v", count, err)
	}
}
