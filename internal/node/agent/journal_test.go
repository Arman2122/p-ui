package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type journalEngine struct{ applications atomic.Int32 }

func (*journalEngine) Validate([]byte) error { return nil }
func (e *journalEngine) Apply(context.Context, []byte, func(string) (*limits.Account, bool)) error {
	e.applications.Add(1)
	return nil
}
func (*journalEngine) Close() error { return nil }

func TestWorkerJournalReplacementCannotReceiveDeployment(t *testing.T) {
	for _, mode := range []string{"unchanged", "before identity read", "between identity and apply"} {
		t.Run(mode, func(t *testing.T) {
			id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
			agentStore, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), id)
			if err != nil {
				t.Fatal(err)
			}
			defer agentStore.Close()
			makeWorker := func() (*worker.Worker, *journalEngine) {
				t.Helper()
				dir := t.TempDir()
				store, err := state.Open(filepath.Join(dir, "state.db"), id)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				credit, err := governor.Open(filepath.Join(dir, "credit.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = credit.Close() })
				engine := new(journalEngine)
				process, err := worker.New(t.Context(), "xray", "xray", engine, store, credit)
				if err != nil {
					t.Fatal(err)
				}
				return process, engine
			}
			original, oldEngine := makeWorker()
			replacement, newEngine := makeWorker()
			var swapped, armed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := original
				if swapped.Load() {
					current = replacement
				}
				current.Handler().ServeHTTP(w, r)
				if mode == "between identity and apply" && armed.Load() && r.URL.Path == "/v1/journals" {
					swapped.Store(true)
				}
			}))
			defer server.Close()
			client := server.Client()
			client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
			local := &LocalRuntimes{store: agentStore, workers: []localRuntime{{endpoint: RuntimeEndpoint{ID: "xray", Core: "xray"}, client: client}}}
			if _, err := local.Journals(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "before identity read" {
				swapped.Store(true)
			}
			armed.Store(true)
			payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`)}}})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(payload)
			err = local.Apply(t.Context(), &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]})
			if mode == "unchanged" {
				if err != nil || oldEngine.applications.Load() != 1 {
					t.Fatalf("valid deployment failed: %v", err)
				}
			} else if err == nil || oldEngine.applications.Load() != 0 || newEngine.applications.Load() != 0 {
				t.Fatalf("replacement received deployment: err=%v old=%d new=%d", err, oldEngine.applications.Load(), newEngine.applications.Load())
			}
		})
	}
}
