package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestLocalApplyRequiresExactAssetReceiptIncludingEmptyManifest(t *testing.T) {
	for _, files := range []bool{false, true} {
		for _, mode := range []string{"missing", "wrong", "exact"} {
			t.Run(mode+map[bool]string{false: "-empty", true: "-files"}[files], func(t *testing.T) {
				id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
				store, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), id)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				runtime := configuration.Runtime{ID: "ssh", Core: "ssh", Configuration: json.RawMessage(`{}`)}
				if files {
					data := []byte("exact file content")
					digest := sha256.Sum256(data)
					runtime.FileAssets = []configuration.FileAsset{{ID: "key", SHA256: hex.EncodeToString(digest[:]), Data: data}}
				}
				payload, _ := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{runtime}})
				digest := sha256.Sum256(payload)
				journal := &nodev1.RuntimeJournalIdentity{RuntimeId: "ssh", CoreId: "ssh", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}
				var applications atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/journals" {
						encoded, _ := proto.Marshal(journal)
						_, _ = w.Write(encoded)
						return
					}
					if r.Header.Get("X-Pui-State-Journal") != journal.StateJournalId || r.Header.Get("X-Pui-Credit-Journal") != journal.CreditJournalId {
						t.Error("apply lost journal identity")
					}
					if r.URL.Path == "/v1/apply" {
						applications.Add(1)
						switch mode {
						case "wrong":
							w.Header().Set(configuration.AssetManifestHeader, hex.EncodeToString(bytes.Repeat([]byte{1}, 32)))
						case "exact":
							w.Header().Set(configuration.AssetManifestHeader, hex.EncodeToString(configuration.RuntimeAssetManifest(runtime.ID, runtime.FileAssets)))
						}
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer server.Close()
				client := server.Client()
				client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
				local := &LocalRuntimes{store: store, workers: []localRuntime{{endpoint: RuntimeEndpoint{ID: "ssh", Core: "ssh"}, client: client}}}
				err = local.Apply(t.Context(), &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]})
				if applications.Load() != 1 {
					t.Fatalf("fixture did not reach apply receipt: %v", err)
				}
				if mode == "exact" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("unproven worker files were acknowledged")
				}
			})
		}
	}
}
