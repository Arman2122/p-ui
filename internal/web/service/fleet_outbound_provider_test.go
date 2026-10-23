package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func fleetProviderFixture(t *testing.T) (*FleetProviderService, model.User) {
	t.Helper()
	initTestDB(t)
	db := database.GetDB()
	var actor model.User
	if err := db.First(&actor).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewFleetProviderService(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return service, actor
}

func configureProviderFixture(t *testing.T, service *FleetProviderService, actor model.User, address string) FleetProviderRequest {
	t.Helper()
	request := FleetProviderRequest{OperationID: uuid.NewString(), ProviderID: uuid.NewString(), Name: "fixture", CoreID: "xray", Scope: "shared", RefreshEnabled: true, Fetch: providerFetchSpec(address)}
	request.Fetch.AllowPrivate = true
	view, err := service.Configure(t.Context(), &actor, request)
	if err != nil || view.ConfigurationRevision != 1 {
		t.Fatalf("configure provider: revision=%d err=%v", view.ConfigurationRevision, err)
	}
	return request
}

func TestFleetProviderRefreshKeepsImmutableCacheOnFailureAndReplay(t *testing.T) {
	service, actor := fleetProviderFixture(t)
	var body atomic.Value
	body.Store("trojan://fixture@edge.example:443?security=tls#One")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, body.Load().(string))
	}))
	defer server.Close()
	config := configureProviderFixture(t, service, actor, server.URL)
	request := FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: config.ProviderID, ExpectedRevision: 1}
	first, err := service.Refresh(t.Context(), &actor, request)
	if err != nil || !first.Changed || first.CachedRevision != 1 || first.Entries != 1 {
		t.Fatalf("first refresh: %+v err=%v", first, err)
	}
	var original model.FleetOutboundProviderRevision
	if err := service.db.First(&original, "provider_id = ? AND revision = 1", config.ProviderID).Error; err != nil {
		t.Fatal(err)
	}
	body.Store("invalid-feed-secret")
	replay, err := service.Refresh(t.Context(), &actor, request)
	if err != nil || !replay.Replayed || replay.CachedRevision != 1 || hits.Load() != 1 {
		t.Fatalf("lost-response replay fetched again: %+v hits=%d err=%v", replay, hits.Load(), err)
	}
	request.OperationID = uuid.NewString()
	if _, err := service.Refresh(t.Context(), &actor, request); !errors.Is(err, ErrFleetProviderFetch) {
		t.Fatalf("bad feed: %v", err)
	}
	var source model.FleetOutboundProvider
	if err := service.db.First(&source, "id = ?", config.ProviderID).Error; err != nil || source.CachedRevision != 1 || source.Diagnostic != "INVALID_FEED" || source.LeaseID != nil {
		t.Fatalf("failed refresh changed cache or retained claim: revision=%d diagnostic=%s err=%v", source.CachedRevision, source.Diagnostic, err)
	}
	body.Store("trojan://fixture@edge.example:443?security=tls#Renamed")
	unchanged, err := service.Refresh(t.Context(), &actor, request)
	if err != nil || unchanged.Changed || unchanged.CachedRevision != 1 {
		t.Fatalf("failed operation retry: %+v err=%v", unchanged, err)
	}
	body.Store("")
	request.OperationID = uuid.NewString()
	empty, err := service.Refresh(t.Context(), &actor, request)
	if err != nil || !empty.Changed || empty.CachedRevision != 2 || empty.Entries != 0 {
		t.Fatalf("explicit empty revision: %+v err=%v", empty, err)
	}
	var retained model.FleetOutboundProviderRevision
	if err := service.db.First(&retained, "provider_id = ? AND revision = 1", config.ProviderID).Error; err != nil || retained.Outbounds != original.Outbounds || !bytes.Equal(retained.Digest, original.Digest) {
		t.Fatal("old provider revision mutated")
	}
	encoded, err := json.Marshal(empty)
	if err != nil || bytes.Contains(encoded, []byte("fixture")) || bytes.Contains(encoded, []byte(server.URL)) {
		t.Fatal("refresh result exposed feed credentials")
	}
}

func TestFleetProviderRefreshConfigurationCASRejectsLateFetch(t *testing.T) {
	service, actor := fleetProviderFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			_, _ = io.WriteString(w, "trojan://stale@edge.example:443?security=tls")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	config := configureProviderFixture(t, service, actor, server.URL)
	request := FleetProviderRefreshRequest{OperationID: uuid.NewString(), ProviderID: config.ProviderID, ExpectedRevision: 1}
	done := make(chan error, 1)
	go func() { _, err := service.Refresh(t.Context(), &actor, request); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not reach fixture")
	}
	if _, err := service.Refresh(t.Context(), &actor, request); !errors.Is(err, ErrFleetProviderBusy) {
		t.Fatalf("concurrent duplicate refresh: %v", err)
	}
	config.OperationID, config.ExpectedRevision, config.Fetch.URL = uuid.NewString(), 1, server.URL+"/new-configuration"
	updated, err := service.Configure(t.Context(), &actor, config)
	close(release)
	if err != nil || updated.ConfigurationRevision != 2 {
		t.Fatalf("concurrent configuration: revision=%d err=%v", updated.ConfigurationRevision, err)
	}
	if err := <-done; !errors.Is(err, ErrFleetProviderRevision) {
		t.Fatalf("late fetch committed: %v", err)
	}
	var count int64
	if err := service.db.Model(&model.FleetOutboundProviderRevision{}).Where("provider_id = ?", config.ProviderID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("late fetch wrote immutable cache: count=%d err=%v", count, err)
	}
}

func TestFleetProviderConfigurationScopeAndOperationAreImmutable(t *testing.T) {
	service, actor := fleetProviderFixture(t)
	config := configureProviderFixture(t, service, actor, "https://provider.example/fixture")
	replay, err := service.Configure(t.Context(), &actor, config)
	if err != nil || !replay.Replayed || replay.ConfigurationRevision != 1 {
		t.Fatalf("configure replay: %+v err=%v", replay, err)
	}
	config.Name = "changed"
	if _, err := service.Configure(t.Context(), &actor, config); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("operation payload reuse: %v", err)
	}
	config.OperationID, config.ExpectedRevision = uuid.NewString(), 1
	config.Scope, config.NodeID, config.InstallationID = "node", uuid.NewString(), uuid.NewString()
	if _, err := service.Configure(t.Context(), &actor, config); !errors.Is(err, ErrFleetProvider) {
		t.Fatalf("credential sharing scope changed: %v", err)
	}
	stale := actor
	stale.LoginEpoch++
	config.Scope, config.NodeID, config.InstallationID = "shared", "", ""
	if _, err := service.Configure(t.Context(), &stale, config); !errors.Is(err, ErrResellerForbidden) {
		t.Fatalf("stale administrator changed source: %v", err)
	}
}

func TestFleetProviderLifecycleCancellationRollsBackAndStopsHTTP(t *testing.T) {
	service, actor := fleetProviderFixture(t)
	owner, cancel := context.WithCancel(t.Context())
	service.owner = owner
	config := configureProviderFixture(t, service, actor, "https://provider.example/fixture")
	var calls atomic.Int32
	callback := "provider-test-cancel"
	if err := service.db.Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "fleet_operations" {
			cancel()
			calls.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.db.Callback().Create().Remove(callback) })
	config.OperationID, config.ExpectedRevision, config.Name = uuid.NewString(), 1, "cancelled"
	if _, err := service.Configure(t.Context(), &actor, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled source mutation committed: %v", err)
	}
	var source model.FleetOutboundProvider
	if err := service.db.First(&source, "id = ?", config.ProviderID).Error; err != nil || source.Revision != 1 || calls.Load() != 1 {
		t.Fatalf("cancelled transaction was not rolled back: revision=%d calls=%d err=%v", source.Revision, calls.Load(), err)
	}
	if _, err := service.RefreshDue(t.Context(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped lifecycle claimed refresh: %v", err)
	}
}
