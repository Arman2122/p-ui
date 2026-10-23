package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type cancelBackend struct {
	started  chan struct{}
	shutdown int
	finalErr error
}

type refusedRestoreBackend struct {
	journal  string
	attempts int
	stops    int
}

func (b *refusedRestoreBackend) AuthorityJournalID() string { return b.journal }
func (*refusedRestoreBackend) Authority(context.Context, string, int) (authority.Page, error) {
	return authority.Page{}, nil
}

func (*refusedRestoreBackend) Validate(*configuration.Runtime) error { return nil }
func (b *refusedRestoreBackend) Apply(context.Context, *configuration.Runtime) error {
	b.attempts++
	if b.attempts == 1 {
		return errors.New("listener temporarily unavailable")
	}
	return nil
}
func (*refusedRestoreBackend) Collect(context.Context) error    { return nil }
func (b *refusedRestoreBackend) Shutdown(context.Context) error { b.stops++; return nil }

func TestRejectedRestoreKeepsControlAvailable(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(t.TempDir(), "worker.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "test", Core: "test", Configuration: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := store.Receive(deployment); err != nil {
		t.Fatal(err)
	}
	if err := store.Applied(1, 1, digest[:]); err != nil {
		t.Fatal(err)
	}
	backend := &refusedRestoreBackend{journal: uuid.NewString()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	process, err := NewWithBackend(ctx, "test", "test", store, backend, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Run(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	statusRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String()+"/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Do(statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	statusBytes, err := io.ReadAll(status.Body)
	_ = status.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	condition := new(nodev1.RuntimeCondition)
	if err := proto.Unmarshal(statusBytes, condition); err != nil {
		t.Fatal(err)
	}
	if condition.Ready || condition.Condition != "RESTORE_FAILED" {
		t.Fatalf("failed restore advertised ready: %+v", condition)
	}
	deployment.Revision = 2
	encoded, err := proto.Marshal(deployment)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+listener.Addr().String()+"/v1/apply", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Pui-State-Journal", store.JournalID())
	request.Header.Set("X-Pui-Credit-Journal", backend.journal)
	request.Header.Set("X-Pui-Agent-Journal", uuid.NewString())
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("master cannot repair refused restore: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("repair status=%d", response.StatusCode)
	}
	cursors, err := store.Cursors()
	if err != nil || cursors.Applied != 2 {
		t.Fatalf("repair was not applied: %+v %v", cursors, err)
	}
}

func (*cancelBackend) Validate(*configuration.Runtime) error               { return nil }
func (*cancelBackend) Apply(context.Context, *configuration.Runtime) error { return nil }
func (b *cancelBackend) Collect(ctx context.Context) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (b *cancelBackend) Shutdown(ctx context.Context) error {
	b.shutdown++
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return b.finalErr
}

func TestShutdownUsesLiveContextAndPreservesFinalFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
	}{{"normal", nil}, {"storage failure", errors.New("final checkpoint unavailable")}} {
		t.Run(test.name, func(t *testing.T) {
			store, err := state.Open(filepath.Join(t.TempDir(), "worker.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			backend := &cancelBackend{started: make(chan struct{}), finalErr: test.failure}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			process, err := NewWithBackend(ctx, "native", "native", store, backend, 10*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- process.Run(ctx, listener) }()
			<-backend.started
			cancel()
			err = <-done
			if !errors.Is(err, test.failure) || backend.shutdown != 1 {
				t.Fatalf("shutdown result = %v, calls = %d", err, backend.shutdown)
			}
		})
	}
}
