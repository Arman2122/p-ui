package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type admissionEngine struct {
	handler admission.Handler
	fail    atomic.Bool
}

func (*admissionEngine) Validate([]byte) error { return nil }
func (e *admissionEngine) Apply(ctx context.Context, _ []byte, _ func(string) (*limits.Account, bool)) error {
	if e.fail.Load() {
		return admission.ErrUnavailable
	}
	e.handler = admission.FromContext(ctx)
	return nil
}
func (*admissionEngine) Close() error { return nil }

func TestLocalActivationRequiresAppliedScopeAndCannotMintCredit(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	open := func(name string) *state.Store {
		t.Helper()
		store, err := state.Open(filepath.Join(t.TempDir(), name), id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	agentStore, runtimeStore := open("agent.db"), open("runtime.db")
	credit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = credit.Close() })
	engine := new(admissionEngine)
	process, err := worker.New(t.Context(), "xray", "xray", engine, runtimeStore, credit)
	if err != nil {
		t.Fatal(err)
	}
	boot := uuid.NewString()
	started := time.Now()
	clock, err := trustedtime.New(runtimeStore, func() (trustedtime.Instant, error) {
		return trustedtime.Instant{Boot: boot, Nano: int64(time.Since(started))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := clock.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := clock.Accept(nonce, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := process.RequireClock(clock); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(process.Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
	local := &LocalRuntimes{store: agentStore, workers: []localRuntime{{endpoint: RuntimeEndpoint{ID: "xray", Core: "xray"}, client: client}}}
	descriptor := admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 2, PlacementID: uuid.NewString(), PlacementRevision: 3, InboundInstanceID: uuid.NewString(), InboundTag: "assigned-inbound"}
	payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Admissions: []admission.Descriptor{descriptor}}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := agentStore.Receive(deployment); err != nil {
		t.Fatal(err)
	}
	if err := local.Apply(t.Context(), deployment); err != nil {
		t.Fatal(err)
	}
	if err := agentStore.Applied(1, 1, digest[:]); err != nil {
		t.Fatal(err)
	}
	sent := make(chan *nodev1.ActivationRequest, 4)
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		local.RunActivations(ctx, func(request *nodev1.ActivationRequest) error { sent <- request; return nil })
	}()
	t.Cleanup(func() { cancel(); <-finished })
	done := make(chan error, 1)
	go func() { done <- engine.handler(ctx, descriptor.ClientID, descriptor.InboundTag) }()
	var request *nodev1.ActivationRequest
	select {
	case request = <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("live authenticated attempt not forwarded")
	}
	if request.RuntimeId != "xray" || request.DeploymentRevision != 1 || request.PlacementId != descriptor.PlacementID || request.InboundInstanceId != descriptor.InboundInstanceID {
		t.Fatalf("scope=%+v", request)
	}
	local.ResolveActivation(ctx, &nodev1.ActivationResult{RequestId: request.RequestId, ClientId: descriptor.ClientID, TermId: uuid.NewString(), ExpiresUnixMilli: time.Now().Add(time.Hour).UnixMilli()})
	if err := <-done; !errors.Is(err, admission.ErrUnavailable) {
		t.Fatalf("receipt granted authority=%v", err)
	}
	if account, ok := credit.Lookup(descriptor.ClientID); ok || account != nil {
		t.Fatal("activation receipt created forwarding credit")
	}
	journal, err := local.bindWorkerJournal(ctx, local.workers[0])
	if err != nil {
		t.Fatal(err)
	}
	request.RequestId = uuid.NewString()
	request.RuntimeId = "foreign"
	if local.trackAdmission(local.workers[0], journal, request) {
		t.Fatal("runtime scope accepted")
	}
	request.RuntimeId = "xray"
	request.DeploymentRevision = 2
	if local.trackAdmission(local.workers[0], journal, request) {
		t.Fatal("unapplied revision accepted")
	}
	engine.fail.Store(true)
	deployment.Revision = 2
	if err := local.Apply(t.Context(), deployment); err == nil {
		t.Fatal("rejected backend applied")
	}
	if err := engine.handler(ctx, descriptor.ClientID, descriptor.InboundTag); !errors.Is(err, admission.ErrUnavailable) {
		t.Fatalf("failed apply retained admission=%v", err)
	}
	select {
	case stale := <-sent:
		t.Fatalf("failed apply emitted demand=%+v", stale)
	default:
	}
	cancel()
	<-finished
}
