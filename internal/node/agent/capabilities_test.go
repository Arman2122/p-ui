package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/inventory"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type inventoryEngine struct {
	journalEngine
	value  atomic.Pointer[nodev1.CoreCapability]
	failed atomic.Bool
}

func (e *inventoryEngine) Capability(ctx context.Context) (*nodev1.CoreCapability, error) {
	if e.failed.Load() {
		return nil, inventory.ErrInventory
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := e.value.Load()
	if value == nil {
		return nil, nil
	}
	return proto.Clone(value).(*nodev1.CoreCapability), nil
}

func inventoryCapability(core string, protocols ...string) *nodev1.CoreCapability {
	return &nodev1.CoreCapability{
		CoreId: core, Version: "sha256:" + strings.Repeat("a", 64), Protocols: protocols,
		ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true,
	}
}

func inventoryLocal(t *testing.T) *LocalRuntimes {
	t.Helper()
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &LocalRuntimes{store: store}
}

func addInventoryWorker(t *testing.T, local *LocalRuntimes, id, core string, value *nodev1.CoreCapability, mutate func(*httptest.ResponseRecorder)) *inventoryEngine {
	t.Helper()
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state.db"), local.store.Identity())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credit, err := governor.Open(filepath.Join(dir, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = credit.Close() })
	engine := new(inventoryEngine)
	engine.value.Store(value)
	process, err := worker.New(t.Context(), id, core, engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	handler := process.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/capabilities" || mutate == nil {
			handler.ServeHTTP(w, r)
			return
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		mutate(response)
		for key, values := range response.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
	local.workers = append(local.workers, localRuntime{endpoint: RuntimeEndpoint{ID: id, Core: core}, client: client})
	return engine
}

func TestHelloCarriesAuthenticatedUnconfiguredInventoryAndReconnectsOnChange(t *testing.T) {
	local := inventoryLocal(t)
	xray := addInventoryWorker(t, local, "edge-xray", "xray", inventoryCapability("xray", "vless", "http"), nil)
	addInventoryWorker(t, local, "edge-ssh", "ssh", inventoryCapability("ssh", "ssh"), nil)
	agent := &Agent{store: local.store, applier: local}
	hello, err := agent.hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := local.store.Identity()
	if hello.NodeId != id.NodeID || hello.InstallationId != id.InstallationID || hello.AgentStateJournalId != local.store.JournalID() || hello.ProtocolVersion != compatibility.Protocol || len(hello.Capabilities) != 2 {
		t.Fatalf("hello lost installed inventory or identity: %+v", hello)
	}
	want := inventoryCapability("xray", "http", "vless")
	if !proto.Equal(hello.Capabilities[1], want) || agent.inventoryChanged(t.Context(), hello.Capabilities) {
		t.Fatalf("stable canonical inventory changed: %+v", hello.Capabilities)
	}
	changed := proto.Clone(want).(*nodev1.CoreCapability)
	changed.Version = "sha256:" + strings.Repeat("b", 64)
	xray.value.Store(changed)
	if !agent.inventoryChanged(t.Context(), hello.Capabilities) {
		t.Fatal("changed running build did not request a reconnect")
	}
	xray.failed.Store(true)
	current, err := agent.hello(t.Context())
	if err != nil || len(current.Capabilities) != 1 || current.Capabilities[0].CoreId != "ssh" || !agent.inventoryChanged(t.Context(), hello.Capabilities) {
		t.Fatalf("unavailable runtime inherited configured capabilities: %+v %v", current, err)
	}
	xray.failed.Store(false)
	if !agent.inventoryChanged(t.Context(), current.Capabilities) {
		t.Fatal("runtime recovery did not refresh inventory")
	}
}

func TestInventoryRequiresEverySameFamilyWorkerToAgree(t *testing.T) {
	for _, scenario := range []string{"equal", "version", "protocol", "rate", "quota", "admission", "unavailable", "missing provider"} {
		t.Run(scenario, func(t *testing.T) {
			local := inventoryLocal(t)
			value := inventoryCapability("xray", "http", "vless")
			addInventoryWorker(t, local, "edge-a", "xray", value, nil)
			other := proto.Clone(value).(*nodev1.CoreCapability)
			switch scenario {
			case "version":
				other.Version = "sha256:" + strings.Repeat("b", 64)
			case "protocol":
				other.Protocols = []string{"vless"}
			case "rate":
				other.ClientRateEnforcement = false
			case "quota":
				other.ClientQuotaEnforcement = false
			case "admission":
				other.ActivationAdmission = false
			case "missing provider":
				other = nil
			}
			second := addInventoryWorker(t, local, "edge-b", "xray", other, nil)
			second.failed.Store(scenario == "unavailable")
			got := local.Capabilities(t.Context())
			if scenario == "equal" {
				if len(got) != 1 || !proto.Equal(got[0], value) {
					t.Fatalf("equal actual workers did not aggregate: %+v", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("ambiguous family was advertised: %+v", got)
			}
		})
	}
}

func TestInventoryRejectsForeignMalformedAndOversizedSnapshots(t *testing.T) {
	for _, scenario := range []string{"node", "installation", "control plane", "runtime", "state journal", "credit journal", "core", "unknown field", "trailing", "oversize", "missing version", "duplicate protocols"} {
		t.Run(scenario, func(t *testing.T) {
			local := inventoryLocal(t)
			addInventoryWorker(t, local, "edge-xray", "xray", inventoryCapability("xray", "http"), func(response *httptest.ResponseRecorder) {
				var snapshot inventory.Snapshot
				if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
					t.Error(err)
					return
				}
				switch scenario {
				case "node":
					snapshot.NodeID = uuid.NewString()
				case "installation":
					snapshot.InstallationID = uuid.NewString()
				case "control plane":
					snapshot.ControlPlaneID = uuid.NewString()
				case "runtime":
					snapshot.Journal.RuntimeId = "other"
				case "state journal":
					snapshot.Journal.StateJournalId = uuid.NewString()
				case "credit journal":
					snapshot.Journal.CreditJournalId = uuid.NewString()
				case "core":
					snapshot.Capability.CoreId = "ssh"
				case "missing version":
					snapshot.Capability.Version = ""
				case "duplicate protocols":
					snapshot.Capability.Protocols = []string{"http", "http"}
				}
				encoded, err := json.Marshal(snapshot)
				if err != nil {
					t.Error(err)
					return
				}
				switch scenario {
				case "unknown field":
					encoded = append([]byte(`{"foreign":true,`), encoded[1:]...)
				case "trailing":
					encoded = append(encoded, []byte(`{}`)...)
				case "oversize":
					encoded = append(encoded, []byte(strings.Repeat(" ", inventory.MaxResponseBytes))...)
				}
				response.Body.Reset()
				_, _ = response.Body.Write(encoded)
			})
			if got := local.Capabilities(t.Context()); len(got) != 0 {
				t.Fatalf("unverified snapshot advertised: %+v", got)
			}
		})
	}
}

func TestNativeInventoryStaysUnknownDespitePositiveStaticProvider(t *testing.T) {
	local := inventoryLocal(t)
	addInventoryWorker(t, local, "native", "native", inventoryCapability("native", "wgkernel"), nil)
	if got := local.Capabilities(t.Context()); len(got) != 0 {
		t.Fatalf("closed native gate advertised serving capability: %+v", got)
	}
}

func TestInventoryCancellationStopsHelloBeforePublication(t *testing.T) {
	local := inventoryLocal(t)
	addInventoryWorker(t, local, "xray", "xray", inventoryCapability("xray", "vless"), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	agent := &Agent{store: local.store, applier: local}
	if hello, err := agent.hello(ctx); !errors.Is(err, context.Canceled) || hello != nil || time.Since(start) > time.Second {
		t.Fatalf("canceled inventory created hello: %+v %v", hello, err)
	}
}
