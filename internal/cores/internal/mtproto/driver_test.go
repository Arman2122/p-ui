package mtproto

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

/*
The conformance rig for mtproto.

The stand-in mtg is the test binary re-executed, and it serves the real
management API off the config the manager generated for it — the same /stats
the accounting reads and the same PUT /secrets a hot apply uses. Faking the
manager's internals instead would have proven only that the fake agrees with
itself: the api port, the bearer token and the counter epoch all have to survive
a round trip through the generated TOML to mean anything.
*/

func TestMain(m *testing.M) {
	if os.Getenv("MTG_FAKE_CHILD") == "1" {
		serveFakeMtg()
		return
	}
	os.Exit(m.Run())
}

var tomlValue = regexp.MustCompile(`(?m)^(\S+)\s*=\s*"([^"]*)"`)

// serveFakeMtg impersonates mtg-multi: it binds the api address from its own
// generated config and answers /stats from a file the test rewrites.
func serveFakeMtg() {
	if f, err := os.OpenFile(os.Getenv("MTG_FAKE_PIDFILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
		_ = f.Close()
	}
	cfg, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(1)
	}
	keys := map[string]string{}
	for _, m := range tomlValue.FindAllStringSubmatch(string(cfg), -1) {
		keys[m[1]] = m[2]
	}
	ln, err := net.Listen("tcp", keys["api-bind-to"])
	if err != nil {
		os.Exit(1)
	}
	token := keys["api-token"]
	statsFile := os.Getenv("MTG_FAKE_STATS")
	recordServed(secretNamesFromTOML(string(cfg)))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/secrets" {
			var body struct {
				Secrets map[string]json.RawMessage `json:"secrets"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil {
				names := make([]string, 0, len(body.Secrets))
				for name := range body.Secrets {
					names = append(names, name)
				}
				recordServed(names)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/stats" {
			body, err := os.ReadFile(statsFile)
			if err != nil {
				body = []byte(`{"users":{}}`)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if ready := os.Getenv("MTG_FAKE_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte(keys["api-bind-to"]), 0o600)
	}
	_ = http.Serve(ln, mux)
	os.Exit(0)
}

var tomlSecret = regexp.MustCompile(`(?m)^"([^"]+)"\s*=\s*"ee`)

// secretNamesFromTOML reads the generated [secrets] section. Only secret values
// carry the FakeTLS "ee" marker, so no other quoted pair in the file matches.
func secretNamesFromTOML(cfg string) []string {
	var names []string
	for _, m := range tomlSecret.FindAllStringSubmatch(cfg, -1) {
		names = append(names, m[1])
	}
	return names
}

// recordServed publishes the secret set this stand-in is serving, so the rig can
// check provisioning against the daemon rather than against the adapter.
func recordServed(names []string) {
	path := os.Getenv("MTG_FAKE_SERVED")
	if path == "" {
		return
	}
	sort.Strings(names)
	_ = os.WriteFile(path, []byte(strings.Join(names, "\n")), 0o600)
}

type fakeUser struct {
	Connections int64 `json:"connections"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
}

type rig struct {
	t       *testing.T
	pidFile string
	ready   string
	stats   string
	servedF string

	mu        sync.Mutex
	startedAt string
	users     map[string]*fakeUser
}

func newRig(t *testing.T) *rig {
	t.Helper()
	binDir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	payload, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, engine.GetBinaryName()), payload, 0o755); err != nil {
		t.Fatalf("install fake mtg: %v", err)
	}
	r := &rig{
		t:         t,
		pidFile:   filepath.Join(binDir, "mtg-pids.txt"),
		ready:     filepath.Join(binDir, "mtg-ready.txt"),
		stats:     filepath.Join(binDir, "mtg-stats.json"),
		servedF:   filepath.Join(binDir, "mtg-served.txt"),
		startedAt: "2026-01-01T00:00:00Z",
		users:     map[string]*fakeUser{},
	}
	r.writeStats()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	t.Setenv("MTG_FAKE_CHILD", "1")
	t.Setenv("MTG_FAKE_PIDFILE", r.pidFile)
	t.Setenv("MTG_FAKE_READY", r.ready)
	t.Setenv("MTG_FAKE_STATS", r.stats)
	t.Setenv("MTG_FAKE_SERVED", r.servedF)
	t.Cleanup(func() { engine.GetManager().StopAll() })
	return r
}

func (r *rig) writeStats() {
	r.t.Helper()
	body, err := json.Marshal(struct {
		StartedAt string               `json:"started_at"`
		Users     map[string]*fakeUser `json:"users"`
	}{StartedAt: r.startedAt, Users: r.users})
	if err != nil {
		r.t.Fatalf("encode stats: %v", err)
	}
	if err := os.WriteFile(r.stats, body, 0o600); err != nil {
		r.t.Fatalf("write stats: %v", err)
	}
}

// waitReady blocks until a stand-in mtg has bound its api port, so a scrape
// that races process startup is never mistaken for a client that moved no bytes.
func (r *rig) waitReady() {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(r.ready); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the stand-in mtg never bound its api port")
}

func (r *rig) feed(email string, up, down int64) {
	r.mu.Lock()
	r.users[email] = &fakeUser{Connections: 1, BytesIn: up, BytesOut: down}
	r.mu.Unlock()
	r.writeStats()
	r.waitReady()
}

// restart models mtg being replaced under the manager: a new incarnation, with
// every cumulative counter back at zero.
func (r *rig) restart() {
	r.mu.Lock()
	r.startedAt = "2026-01-02T00:00:00Z"
	for _, u := range r.users {
		u.BytesIn, u.BytesOut = 0, 0
	}
	r.mu.Unlock()
	r.writeStats()
}

// served reads back the secret set the stand-in mtg is actually serving.
func (r *rig) served() []string {
	r.t.Helper()
	r.waitReady()
	data, err := os.ReadFile(r.servedF)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatalf("read served file: %v", err)
	}
	return strings.Fields(string(data))
}

func (r *rig) spawns() int {
	r.t.Helper()
	data, err := os.ReadFile(r.pidFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		r.t.Fatalf("read pid file: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// starts waits for the daemon, then lets any extra spawn land before counting:
// counting too early reads a restart as an idempotent reconcile and passes it.
func (r *rig) starts() int {
	r.t.Helper()
	r.waitReady()
	time.Sleep(250 * time.Millisecond)
	return r.spawns()
}

func (r *rig) instance(users int) core.Instance {
	inst := core.Instance{
		ID:     7,
		Kind:   Kind,
		Tag:    "inbound-7",
		Listen: "127.0.0.1",
		Port:   24700,
		Enable: true,
		Settings: `{"throttleMaxConnections":128,` +
			`"clients":[{"email":"ignored@example.com","secret":"ee99","enable":true}]}`,
	}
	for i := range users {
		email := fmt.Sprintf("%c@example.com", 'a'+i)
		inst.Users = append(inst.Users, core.User{
			Email:       email,
			Enable:      true,
			Credentials: map[string]any{CredSecret: fmt.Sprintf("ee%02d", i)},
		})
	}
	return inst
}

func (r *rig) asRig() coretest.Rig {
	return coretest.Rig{
		NewCore:       func() (core.Core, error) { return New(), nil },
		Instance:      r.instance,
		Starts:        r.starts,
		FeedTraffic:   r.feed,
		RestartSource: r.restart,
		ServedUsers:   r.served,
	}
}

// TestMtprotoConformsToTheContract is the acceptance test for the port. A failure
// here means the contract is wrong, not that mtproto is special.
func TestMtprotoConformsToTheContract(t *testing.T) {
	coretest.RunAdapterSuite(t, newRig(t).asRig())
}

func TestPreflightRejectsAMissingBinary(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	err := New().Preflight(t.Context())
	if err == nil {
		t.Fatal("preflight must fail without an mtg binary, or every inbound fails one at a time instead")
	}
	if !strings.Contains(err.Error(), engine.GetBinaryName()) {
		t.Fatalf("preflight error must name the binary it looked for, got %v", err)
	}
}

// TestAddUserIsAppliedWithoutRestarting is what UserHotAdd claims. A restart drops
// every other client, so the claim is checked against a running daemon.
func TestAddUserIsAppliedWithoutRestarting(t *testing.T) {
	r := newRig(t)
	c := New()
	inst := r.instance(1)

	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	r.waitReady()
	if got := r.starts(); got != 1 {
		t.Fatalf("expected one daemon, got %d", got)
	}

	added := core.User{Email: "z@example.com", Enable: true, Credentials: map[string]any{CredSecret: "eeff"}}
	if err := c.AddUser(t.Context(), inst, added); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if got := r.starts(); got != 1 {
		t.Fatalf("adding a user restarted the daemon (%d spawns); every other client would have been dropped", got)
	}
	served := r.served()
	if !slices.Contains(served, added.Email) {
		t.Fatalf("the daemon serves %v, without the added client; it cannot connect and nothing reported an error", served)
	}
	if !slices.Contains(served, inst.Users[0].Email) {
		t.Fatalf("the daemon serves %v; adding a client must not drop the ones already there", served)
	}
}

// TestUsersComeFromTheContractNotTheSettingsBlob pins where a client lives: the
// settings JSON still has a clients array, and reading it resurrects dead users.
func TestUsersComeFromTheContractNotTheSettingsBlob(t *testing.T) {
	inst := core.Instance{
		ID: 1, Tag: "in-1", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"ghost@example.com","secret":"eedead","enable":true}]}`,
		Users: []core.User{
			{Email: "real@example.com", Enable: true, Credentials: map[string]any{CredSecret: "ee01"}},
		},
	}
	got, ok := toEngine(inst)
	if !ok {
		t.Fatal("an enabled instance with one keyed client must be serveable")
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "real@example.com" {
		t.Fatalf("secrets must come from Users alone, got %+v", got.Secrets)
	}
}

func TestPlanChangeSeparatesReloadFromRestart(t *testing.T) {
	c := New()
	base := core.Instance{
		ID: 1, Tag: "in-1", Listen: "127.0.0.1", Port: 443, Enable: true,
		Users: []core.User{{Email: "a@example.com", Enable: true, Credentials: map[string]any{CredSecret: "ee01"}}},
	}

	rekeyed := base
	rekeyed.Users = []core.User{{Email: "a@example.com", Enable: true, Credentials: map[string]any{CredSecret: "ee02"}}}
	moved := base
	moved.Port = 8443
	disabled := base
	disabled.Enable = false

	for _, tc := range []struct {
		name string
		next core.Instance
		want core.Action
	}{
		{"identical", base, core.ActionNoop},
		{"rekeyed client", rekeyed, core.ActionHotApply},
		{"moved port", moved, core.ActionRestart},
		{"disabled inbound", disabled, core.ActionRestart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.PlanChange(base, tc.next); got != tc.want {
				t.Fatalf("PlanChange = %s, want %s", got, tc.want)
			}
		})
	}
}
