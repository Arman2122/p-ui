package mtproto

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
the accounting reads, the same PUT /secrets a hot apply uses, and the same kick
a cut uses. Faking the manager's internals instead would have proven only that
the fake agrees with itself: the api port, the bearer token and the counter
epoch all have to survive a round trip through the generated TOML to mean
anything.
*/

func TestMain(m *testing.M) {
	if os.Getenv("MTG_FAKE_CHILD") == "1" {
		serveFakeMtg()
		return
	}
	os.Exit(m.Run())
}

var tomlValue = regexp.MustCompile(`(?m)^(\S+)\s*=\s*"([^"]*)"`)

// fakeStats is the file the rig writes and the stand-in answers /stats from.
// Generation moves on every feed, and a new generation forgets every kick: a
// fed session is a fresh connection, the way a client that reconnects is.
type fakeStats struct {
	StartedAt  string               `json:"started_at"`
	Generation int                  `json:"generation"`
	Features   []string             `json:"features"`
	Users      map[string]*fakeUser `json:"users"`
}

// fakeLimits is what the last PUT /secrets set for one user, bytes per second.
type fakeLimits struct {
	up, down int64
}

// serveFakeMtg impersonates mtg-proxy: it binds the api address from its own
// generated config, answers /stats from a file the test rewrites, reflects the
// speeds the last PUT pushed, and ends kicked places until the next feed.
func serveFakeMtg() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("9.9.9 (go1.26 fake)")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "web-relay" {
		serveFakeRelay()
		return
	}
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
	served := secretNamesFromTOML(string(cfg))
	recordServed(served)

	var (
		mu         sync.Mutex
		limits     = map[string]fakeLimits{}
		kicked     = map[string]map[string]bool{}
		generation = -1
	)
	current := func() fakeStats {
		var stats fakeStats
		if body, err := os.ReadFile(statsFile); err != nil || json.Unmarshal(body, &stats) != nil {
			stats = fakeStats{Users: map[string]*fakeUser{}}
		}
		if stats.Generation != generation {
			generation = stats.Generation
			kicked = map[string]map[string]bool{}
		}
		// Every served secret is a user, as PreRegister makes it in the real daemon.
		for _, name := range served {
			if stats.Users[name] == nil {
				stats.Users[name] = &fakeUser{}
			}
		}
		for email, u := range stats.Users {
			if l, ok := limits[email]; ok {
				u.SpeedLimitUp, u.SpeedLimitDown = l.up, l.down
			}
			gone := kicked[email]
			u.ConnectedIPs = slices.DeleteFunc(u.ConnectedIPs, func(ip string) bool { return gone[ip] })
			for ip := range u.ConnectedIPsLastSeen {
				if gone[ip] {
					delete(u.ConnectedIPsLastSeen, ip)
				}
			}
		}
		return stats
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/secrets":
			var body struct {
				Secrets map[string]struct {
					SpeedUp   string `json:"speed_up"`
					SpeedDown string `json:"speed_down"`
				} `json:"secrets"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil {
				names := make([]string, 0, len(body.Secrets))
				for name, entry := range body.Secrets {
					names = append(names, name)
					up, _ := strconv.ParseInt(entry.SpeedUp, 10, 64)
					down, _ := strconv.ParseInt(entry.SpeedDown, 10, 64)
					limits[name] = fakeLimits{up: up, down: down}
				}
				served = names
				recordServed(names)
			}
			if puts := os.Getenv("MTG_FAKE_PUTS"); puts != "" {
				if f, err := os.OpenFile(puts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
					_, _ = f.WriteString("put\n")
					_ = f.Close()
				}
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/stats":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(current())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/kick"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/secrets/"), "/kick")
			if !slices.Contains(served, name) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var body struct {
				IPs []string `json:"ips"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IPs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stats := current()
			closed := 0
			if u := stats.Users[name]; u != nil {
				for _, ip := range body.IPs {
					if slices.Contains(u.ConnectedIPs, ip) {
						if kicked[name] == nil {
							kicked[name] = map[string]bool{}
						}
						kicked[name][ip] = true
						closed++
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"closed":%d}`, closed)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	if ready := os.Getenv("MTG_FAKE_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte(keys["api-bind-to"]), 0o600)
	}
	_ = http.Serve(ln, mux)
	os.Exit(0)
}

// serveFakeRelay is the WEB relay as the manager drives it: --check validates
// and exits, a run binds the stats socket the config names and answers /stats,
// and its pid goes to its own file so a test can tell the two processes apart.
func serveFakeRelay() {
	if slices.Contains(os.Args, "--check") {
		fmt.Println("web-relay: configuration is valid")
		return
	}
	// The real relay re-reads its file on SIGHUP; the stand-in has nothing to
	// re-read, and must not die of the signal the manager sends after an edit.
	signal.Ignore(syscall.SIGHUP)
	if f, err := os.OpenFile(os.Getenv("MTG_FAKE_RELAY_PIDFILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
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
	ln, err := net.Listen("tcp", keys["stats-bind-to"])
	if err != nil {
		os.Exit(1)
	}
	profiles := secretNamesFromTOML(string(cfg))
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"profiles": profiles, "sessions": map[string]int{"active": 0}})
	})
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

// fakeUser is one /stats user as the real payload shapes it: the counters, the
// addresses with their activity, and the caps the sidecar holds.
type fakeUser struct {
	Connections          int64             `json:"connections"`
	BytesIn              int64             `json:"bytes_in"`
	BytesOut             int64             `json:"bytes_out"`
	ConnectedIPs         []string          `json:"connected_ips,omitempty"`
	ConnectedIPsLastSeen map[string]string `json:"connected_ips_last_seen,omitempty"`
	SpeedLimitUp         int64             `json:"speed_limit_up,omitempty"`
	SpeedLimitDown       int64             `json:"speed_limit_down,omitempty"`
}

// fakeFeatures is what the stand-in advertises: everything the panel drives.
var fakeFeatures = []string{
	"client-ip-activity", "client-ips", "faketls", "ip-limits", "kick", "listeners", "quota-ceiling",
	"quota-reset", "secret-modes", "secret-read", "secrets-api", "speed-directions", "speed-limits",
}

type rig struct {
	t         *testing.T
	pidFile   string
	ready     string
	stats     string
	servedF   string
	putsF     string
	relayPids string

	mu         sync.Mutex
	startedAt  string
	generation int
	users      map[string]*fakeUser
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
		putsF:     filepath.Join(binDir, "mtg-puts.txt"),
		relayPids: filepath.Join(binDir, "mtg-relay-pids.txt"),
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
	t.Setenv("MTG_FAKE_PUTS", r.putsF)
	t.Setenv("MTG_FAKE_RELAY_PIDFILE", r.relayPids)
	// Drop the fixture's rate cache too; StopAll only stops its processes.
	t.Cleanup(func() { engine.GetManager().Remove(r.instance(0).ID) })
	return r
}

func (r *rig) writeStats() {
	r.t.Helper()
	body, err := json.Marshal(fakeStats{StartedAt: r.startedAt, Generation: r.generation, Features: fakeFeatures, Users: r.users})
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
	u := r.userLocked(email)
	u.BytesIn, u.BytesOut = up, down
	r.mu.Unlock()
	r.writeStats()
	r.waitReady()
}

// feedSession is one connected place for a client, seen at the given instant:
// a new generation, so a kick from before it is forgotten the way a reconnect is.
func (r *rig) feedSession(email, source string, lastSeenUnixMilli int64) {
	r.mu.Lock()
	u := r.userLocked(email)
	if !slices.Contains(u.ConnectedIPs, source) {
		u.ConnectedIPs = append(u.ConnectedIPs, source)
	}
	if u.ConnectedIPsLastSeen == nil {
		u.ConnectedIPsLastSeen = map[string]string{}
	}
	u.ConnectedIPsLastSeen[source] = time.UnixMilli(lastSeenUnixMilli).UTC().Format(time.RFC3339)
	r.generation++
	r.mu.Unlock()
	r.writeStats()
	r.waitReady()
}

func (r *rig) userLocked(email string) *fakeUser {
	u := r.users[email]
	if u == nil {
		u = &fakeUser{Connections: 1}
		r.users[email] = u
	}
	return u
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

// puts counts the PUT /secrets the stand-in has taken: the churn a rate pass
// must not add.
func (r *rig) puts() int {
	r.t.Helper()
	data, err := os.ReadFile(r.putsF)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		r.t.Fatalf("read puts file: %v", err)
	}
	return len(strings.Fields(string(data)))
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
		NewCore:       func() (core.Core, error) { return New(Deps{}), nil },
		Instance:      r.instance,
		Starts:        r.starts,
		Alive:         r.alive,
		FeedTraffic:   r.feed,
		FeedSession:   r.feedSession,
		RestartSource: r.restart,
		ServedUsers:   r.served,
	}
}

// TestMtprotoConformsToTheContract is the acceptance test for the port. A failure
// here means the contract is wrong, not that mtproto is special.
func TestMtprotoConformsToTheContract(t *testing.T) {
	requirePosixHost(t)
	coretest.RunAdapterSuite(t, newRig(t).asRig())
}

func TestPreflightRejectsAMissingBinary(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	err := New(Deps{}).Preflight(t.Context())
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
	requirePosixHost(t)
	r := newRig(t)
	c := New(Deps{})
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

/*
The rate pass runs every ten seconds with the same ladder. A push per pass would
be a whole-set PUT per pass, which hands every client a fresh bucket each time
(Q8 of the brief); only a ladder that moved may reach the daemon, and what the
daemon then holds is read back from the daemon, in the contract's bits per second.
*/
func TestUnchangedRatesAreNotPushedAgain(t *testing.T) {
	requirePosixHost(t)
	r := newRig(t)
	c := New(Deps{})
	inst := r.instance(1)
	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	r.waitReady()
	before := r.puts()

	want := map[string]core.RateLimit{inst.Users[0].Email: {UpBps: 1_000_000, DownBps: 2_000_000}}
	if err := c.EnforceRates(t.Context(), inst, want); err != nil {
		t.Fatalf("EnforceRates: %v", err)
	}
	if got := r.puts(); got != before+1 {
		t.Fatalf("a changed ladder must reach the daemon in one PUT, got %d (was %d)", got, before)
	}
	if got := r.starts(); got != 1 {
		t.Fatalf("a rate is a secrets-only change and must not restart the daemon (%d spawns)", got)
	}
	if err := c.EnforceRates(t.Context(), inst, want); err != nil {
		t.Fatalf("EnforceRates again: %v", err)
	}
	if got := r.puts(); got != before+1 {
		t.Fatalf("an unchanged ladder must not be pushed again, got %d PUTs", got)
	}
	held, err := c.EnforcedRates(t.Context(), inst)
	if err != nil {
		t.Fatalf("EnforcedRates: %v", err)
	}
	if held[inst.Users[0].Email] != want[inst.Users[0].Email] {
		t.Fatalf("the daemon holds %+v, want %+v: bytes must come back as the bits they were pushed as", held, want)
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
	got, ok, _ := toEngine(inst)
	if !ok {
		t.Fatal("an enabled instance with one keyed client must be serveable")
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "real@example.com" {
		t.Fatalf("secrets must come from Users alone, got %+v", got.Secrets)
	}
}

// The limit mapping had shipped for months with no test naming it. The address
// limit joins quota and expiry on the way to the engine, and a client without
// one carries zero, which the render reads as no key.
func TestLimitsReachTheEngine(t *testing.T) {
	inst := core.Instance{
		ID: 1, Tag: "in-1", Port: 443, Enable: true,
		Users: []core.User{
			{
				Email: "capped@example.com", Enable: true, QuotaBytes: 1 << 30, ExpiryUnixMilli: 1_893_456_000_000, LimitIP: 3,
				Credentials: map[string]any{CredSecret: "ee01"},
			},
			{Email: "free@example.com", Enable: true, Credentials: map[string]any{CredSecret: "ee02"}},
		},
	}
	got, ok, err := toEngine(inst)
	if err != nil || !ok {
		t.Fatalf("toEngine: ok=%v err=%v", ok, err)
	}
	capped, free := got.Secrets[0], got.Secrets[1]
	if capped.QuotaBytes != 1<<30 || capped.ExpiresUnix != 1_893_456_000 || capped.IPLimit != 3 {
		t.Fatalf("the capped client lost a limit on the way: %+v", capped)
	}
	if free.QuotaBytes != 0 || free.ExpiresUnix != 0 || free.IPLimit != 0 {
		t.Fatalf("the free client gained a limit it never had: %+v", free)
	}
}

func TestPlanChangeSeparatesReloadFromRestart(t *testing.T) {
	c := New(Deps{})
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

/*
Settings that will not parse are not a request to stop serving.

The three reasons an instance is not served used to collapse into one boolean,
so a settings blob the panel could not read looked exactly like "this inbound is
gone" — and Reconcile stopped the sidecar, dropping every connected client, on
what is most likely a transient or hand-edited row.
*/
func TestUnreadableSettingsDoNotStopALiveSidecar(t *testing.T) {
	keyed := []core.User{{Email: "a@example.com", Enable: true, Credentials: map[string]any{CredSecret: "ee01"}}}

	for _, tc := range []struct {
		name      string
		inst      core.Instance
		wantServe bool
		wantErr   bool
	}{
		{
			name:      "unreadable settings report an error and no verdict",
			inst:      core.Instance{ID: 1, Tag: "in-1", Port: 443, Enable: true, Settings: `{not json`, Users: keyed},
			wantServe: false,
			wantErr:   true,
		},
		{
			name:      "a disabled inbound is a verdict, not an error",
			inst:      core.Instance{ID: 1, Tag: "in-1", Port: 443, Enable: false, Users: keyed},
			wantServe: false,
			wantErr:   false,
		},
		{
			name:      "no keyed client is a verdict, not an error",
			inst:      core.Instance{ID: 1, Tag: "in-1", Port: 443, Enable: true},
			wantServe: false,
			wantErr:   false,
		},
		{
			name:      "an ordinary instance serves",
			inst:      core.Instance{ID: 1, Tag: "in-1", Port: 443, Enable: true, Users: keyed},
			wantServe: true,
			wantErr:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, serve, err := toEngine(tc.inst)
			if serve != tc.wantServe {
				t.Errorf("serve = %v, want %v", serve, tc.wantServe)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want error: %v", err, tc.wantErr)
			}
		})
	}
}

// alive asks the kernel about the last stand-in mtg, which is what the daemon is
// here, so Health is checked against a process and not against the adapter.
func (r *rig) alive() bool {
	data, err := os.ReadFile(r.pidFile)
	if err != nil {
		return false
	}
	pids := strings.Fields(string(data))
	if len(pids) == 0 {
		return false
	}
	pid, err := strconv.Atoi(pids[len(pids)-1])
	if err != nil {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
