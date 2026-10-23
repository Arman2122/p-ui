//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type (
	projectedConfiguration struct {
		Secret string `json:"secret"`
		File   string `json:"file"`
	}
	projectionEngine struct {
		applied, closed atomic.Int32
		invalid         atomic.Bool
	}
)

func (e *projectionEngine) Validate(data []byte) error {
	if e.invalid.Load() {
		return configuration.ErrConfiguration
	}
	var value projectedConfiguration
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	material, err := os.ReadFile(value.File)
	if err != nil {
		return err
	}
	if string(material) != value.Secret {
		return configuration.ErrConfiguration
	}
	return nil
}

func (*projectionEngine) ResolveFileAssets(data []byte, resolve configuration.FileAssetResolver) ([]byte, error) {
	var value projectedConfiguration
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	file, err := resolve(value.File)
	if err != nil {
		return nil, err
	}
	value.File = file
	return json.Marshal(value)
}

func (e *projectionEngine) Apply(context.Context, []byte, func(string) (*limits.Account, bool)) error {
	e.applied.Add(1)
	return nil
}
func (e *projectionEngine) Close() error { e.closed.Add(1); return nil }

func TestRuntimeProjectionKeepsOtherCoreSecretsOutOfPrivateJournals(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	agentStore, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer agentStore.Close()
	local := &LocalRuntimes{store: agentStore}
	var stores []*state.Store
	var engines []*projectionEngine
	var processes []*worker.Worker
	var directories []string
	bundle := configuration.Bundle{Version: 1}
	for _, name := range []string{"ssh", "xray"} {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		store, err := state.Open(filepath.Join(directory, "state.db"), id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		credit, err := governor.Open(filepath.Join(directory, "credit.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = credit.Close() })
		engine := new(projectionEngine)
		ctx, cancel := context.WithCancel(t.Context())
		process, err := worker.New(ctx, name, name, engine, store, credit)
		if err != nil {
			t.Fatal(err)
		}
		process.SetAssetDirectory(directory)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- process.Run(ctx, listener) }()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(3 * time.Second):
				t.Error("projected worker did not stop")
			}
		})
		client := &http.Client{Timeout: 3 * time.Second, Transport: runtimeTestTransport{target: "http://" + listener.Addr().String(), base: &http.Transport{}}}
		local.workers = append(local.workers, localRuntime{endpoint: RuntimeEndpoint{ID: name, Core: name}, client: client})
		stores, engines, processes, directories = append(stores, store), append(engines, engine), append(processes, process), append(directories, directory)
		secret := name + "-private-material"
		digest := sha256.Sum256([]byte(secret))
		asset := configuration.FileAsset{ID: "private-key", SHA256: hex.EncodeToString(digest[:]), Data: []byte(secret)}
		data, _ := json.Marshal(projectedConfiguration{Secret: secret, File: configuration.AssetReference(asset.ID, asset.SHA256)})
		bundle.Runtimes = append(bundle.Runtimes, configuration.Runtime{ID: name, Core: name, Configuration: data, FileAssets: []configuration.FileAsset{asset}})
	}
	defer local.Close()
	makeDeployment := func(revision uint64, bundle configuration.Bundle) *nodev1.Deployment {
		t.Helper()
		payload, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		return &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 7, Revision: revision, Payload: payload, PayloadSha256: digest[:]}
	}
	master := makeDeployment(1, bundle)
	before := proto.Clone(master)
	bad := proto.Clone(master).(*nodev1.Deployment)
	bad.PayloadSha256 = bytes.Repeat([]byte{1}, 32)
	if err := local.Apply(t.Context(), bad); !errors.Is(err, state.ErrDigest) {
		t.Fatalf("projection accepted an invalid master digest: %v", err)
	}
	if err := agentStore.Receive(master); err != nil {
		t.Fatal(err)
	}
	if err := local.Apply(t.Context(), master); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(master, before) {
		t.Fatal("worker projection changed master deployment")
	}
	saved, err := agentStore.Desired()
	if err != nil || !proto.Equal(saved, master) {
		t.Fatal("master journal lost original payload or digest")
	}
	for i, store := range stores {
		own := bundle.Runtimes[i]
		saved, err := store.Desired()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := configuration.Decode(saved.Payload)
		if err != nil || len(decoded.Runtimes) != 1 || decoded.Runtimes[0].ID != own.ID || bytes.Contains(saved.Payload, []byte(bundle.Runtimes[1-i].ID+"-private-material")) {
			t.Fatalf("private worker journal contains another runtime: %s %v", own.ID, err)
		}
		digest := sha256.Sum256(saved.Payload)
		if !bytes.Equal(saved.PayloadSha256, digest[:]) || bytes.Equal(saved.PayloadSha256, master.PayloadSha256) || saved.Revision != master.Revision || saved.AuthorityGeneration != master.AuthorityGeneration || saved.NodeId != master.NodeId || saved.ControlPlaneId != master.ControlPlaneId {
			t.Fatal("projected hash or authority scope changed")
		}
		if err := processes[i].Apply(t.Context(), saved); err != nil {
			t.Fatalf("private projection could not restore: %v", err)
		}
		otherPath := filepath.Join(directories[i], "assets", bundle.Runtimes[1-i].FileAssets[0].SHA256)
		if _, err := os.Stat(otherPath); !os.IsNotExist(err) {
			t.Fatalf("foreign runtime material reached worker files: %v", err)
		}
	}
	engines[1].invalid.Store(true)
	next := makeDeployment(2, bundle)
	if err := local.Apply(t.Context(), next); err == nil || !strings.Contains(err.Error(), "runtime xray validate: local runtime returned HTTP 409") {
		t.Fatalf("invalid projected configuration: %v", err)
	}
	for _, engine := range engines {
		if engine.applied.Load() != 2 {
			t.Fatal("validation failure partially applied another runtime")
		}
	}
	engines[1].invalid.Store(false)
	if err := local.Apply(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err := local.Apply(t.Context(), makeDeployment(3, configuration.Bundle{Version: 1})); err != nil {
		t.Fatal(err)
	}
	for i, store := range stores {
		stopped, err := store.Desired()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := configuration.Decode(stopped.Payload)
		if err != nil || len(decoded.Runtimes) != 0 || engines[i].closed.Load() != 1 {
			t.Fatalf("omitted runtime was not stopped: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(directories[i], "assets"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("stopped projection retained assets: %v", err)
		}
	}
}
