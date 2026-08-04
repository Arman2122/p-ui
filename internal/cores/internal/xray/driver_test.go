package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/util/json_util"
	engine "github.com/Arman2122/p-ui/internal/xray"

	command "github.com/xtls/xray-core/app/proxyman/command"
	statsService "github.com/xtls/xray-core/app/stats/command"
)

/*
The conformance rig for Xray.

The stand-in core is the test binary re-executed, and it serves the real gRPC
API off the config the adapter generated for it: the api inbound's port, the
stats service the accounting reads, the handler service AddUser talks to. Faking
the adapter's internals instead would have proven only that the fake agrees with
itself - the generated config has to survive a round trip through the process for
any of this to mean anything.

Traffic and served users are exchanged through files, because the stand-in is a
separate process.
*/

func TestMain(m *testing.M) {
	if os.Getenv("XRAY_FAKE_CHILD") == "1" {
		serveFakeXray()
		return
	}
	os.Exit(m.Run())
}

// serveFakeXray impersonates xray-core: -version answers the probe the process
// runs at startup, -c binds the api port its own config names.
func serveFakeXray() {
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		fmt.Println("Xray 25.0.0 (fake)")
		return
	}
	if f, err := os.OpenFile(os.Getenv("XRAY_FAKE_PIDFILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
		_ = f.Close()
	}
	cfg, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(1)
	}
	var parsed struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Settings struct {
				Clients []struct {
					Email string `json:"email"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		os.Exit(1)
	}
	apiPort := 0
	var served []string
	for _, ib := range parsed.Inbounds {
		if ib.Tag == "api" {
			apiPort = ib.Port
			continue
		}
		for _, c := range ib.Settings.Clients {
			served = append(served, c.Email)
		}
	}
	if apiPort == 0 {
		os.Exit(1)
	}
	writeServed(served)

	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", apiPort))
	if err != nil {
		os.Exit(1)
	}
	srv := grpc.NewServer()
	statsService.RegisterStatsServiceServer(srv, &fakeStats{})
	command.RegisterHandlerServiceServer(srv, &fakeHandler{served: served})
	if ready := os.Getenv("XRAY_FAKE_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte(lis.Addr().String()), 0o600)
	}
	_ = srv.Serve(lis)
}

func writeServed(emails []string) {
	path := os.Getenv("XRAY_FAKE_SERVED")
	if path == "" {
		return
	}
	sorted := slices.Clone(emails)
	slices.Sort(sorted)
	_ = os.WriteFile(path, []byte(strings.Join(sorted, "\n")), 0o600)
}

// fakeStats answers QueryStats from a file the test rewrites, so the adapter's
// counter sees cumulative readings it did not produce itself.
type fakeStats struct {
	statsService.UnimplementedStatsServiceServer
}

// GetUsersStats reports whoever the stats file names as having moved bytes,
// which is what the core treats as connected right now.
func (f *fakeStats) GetUsersStats(context.Context, *statsService.GetUsersStatsRequest) (*statsService.GetUsersStatsResponse, error) {
	resp := &statsService.GetUsersStatsResponse{}
	for _, email := range emailsInStats() {
		resp.Users = append(resp.Users, &statsService.UserStat{
			Email: email,
			Ips:   []*statsService.OnlineIPEntry{{Ip: "127.0.0.1", LastSeen: 1}},
		})
	}
	return resp, nil
}

func emailsInStats() []string {
	data, err := os.ReadFile(os.Getenv("XRAY_FAKE_STATS"))
	if err != nil {
		return nil
	}
	var counters map[string]int64
	if err := json.Unmarshal(data, &counters); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for name := range counters {
		rest, ok := strings.CutPrefix(name, "user>>>")
		if !ok {
			continue
		}
		email, _, _ := strings.Cut(rest, ">>>")
		if email != "" && !seen[email] {
			seen[email] = true
			out = append(out, email)
		}
	}
	slices.Sort(out)
	return out
}

func (f *fakeStats) QueryStats(context.Context, *statsService.QueryStatsRequest) (*statsService.QueryStatsResponse, error) {
	data, err := os.ReadFile(os.Getenv("XRAY_FAKE_STATS"))
	if err != nil {
		return &statsService.QueryStatsResponse{}, nil
	}
	var counters map[string]int64
	if err := json.Unmarshal(data, &counters); err != nil {
		return &statsService.QueryStatsResponse{}, nil
	}
	resp := &statsService.QueryStatsResponse{}
	for name, value := range counters {
		resp.Stat = append(resp.Stat, &statsService.Stat{Name: name, Value: value})
	}
	return resp, nil
}

// fakeHandler accepts AlterInbound so AddUser and RemoveUser are observable:
// the served set is republished on every change.
type fakeHandler struct {
	command.UnimplementedHandlerServiceServer
	mu     sync.Mutex
	served []string
}

func (f *fakeHandler) AlterInbound(_ context.Context, req *command.AlterInboundRequest) (*command.AlterInboundResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch op := operationOf(req).(type) {
	case *command.RemoveUserOperation:
		f.served = slices.DeleteFunc(f.served, func(e string) bool { return e == op.GetEmail() })
	case *command.AddUserOperation:
		if email := op.GetUser().GetEmail(); email != "" && !slices.Contains(f.served, email) {
			f.served = append(f.served, email)
		}
	}
	writeServed(f.served)
	return &command.AlterInboundResponse{}, nil
}

// RemoveInbound drops the handler and everyone it served. The rig runs a single
// user inbound, so clearing the set models it exactly.
func (f *fakeHandler) RemoveInbound(_ context.Context, _ *command.RemoveInboundRequest) (*command.RemoveInboundResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = nil
	writeServed(f.served)
	return &command.RemoveInboundResponse{}, nil
}

// AddInbound is accepted but does not republish a served set: the request
// carries a built proto rather than the JSON, and nothing in the suite reads
// users back through this path - they arrive as AlterInbound user operations.
func (f *fakeHandler) AddInbound(context.Context, *command.AddInboundRequest) (*command.AddInboundResponse, error) {
	return &command.AddInboundResponse{}, nil
}

// operationOf unmarshals the operation rather than reading its printed form.
// That form escapes the proto length prefix, so a byte in front of an email
// parses as part of the address and provisions a client nobody asked for.
func operationOf(req *command.AlterInboundRequest) proto.Message {
	op := req.GetOperation()
	if op == nil {
		return nil
	}
	msg, err := op.GetInstance()
	if err != nil {
		return nil
	}
	return msg
}

type rig struct {
	t       *testing.T
	pidFile string
	ready   string
	stats   string
	servedF string
	apiPort int
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
		t.Fatalf("install fake xray: %v", err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatalf("reserve api port: %v", err)
	}
	r := &rig{
		t:       t,
		pidFile: filepath.Join(binDir, "xray-pids.txt"),
		ready:   filepath.Join(binDir, "xray-ready.txt"),
		stats:   filepath.Join(binDir, "xray-stats.json"),
		servedF: filepath.Join(binDir, "xray-served.txt"),
		apiPort: port,
	}
	r.writeStats(map[string]int64{})
	t.Setenv("PUI_BIN_FOLDER", binDir)
	t.Setenv("XRAY_FAKE_CHILD", "1")
	t.Setenv("XRAY_FAKE_PIDFILE", r.pidFile)
	t.Setenv("XRAY_FAKE_READY", r.ready)
	t.Setenv("XRAY_FAKE_STATS", r.stats)
	t.Setenv("XRAY_FAKE_SERVED", r.servedF)
	t.Cleanup(func() {
		if p := engine.GetManager().Current(); p != nil && p.IsRunning() {
			_ = p.Stop()
		}
	})
	return r
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// baseConfig is what the panel keeps owning: the api listener the core talks to,
// and nothing about user inbounds.
func (r *rig) baseConfig() (*engine.Config, error) {
	return &engine.Config{
		API:   json_util.RawMessage(`{"tag":"api","services":["HandlerService","StatsService"]}`),
		Stats: json_util.RawMessage(`{}`),
		InboundConfigs: []core.InboundConfig{{
			Listen:   json_util.RawMessage(`"127.0.0.1"`),
			Port:     r.apiPort,
			Protocol: "dokodemo-door",
			Settings: json_util.RawMessage(`{"address":"127.0.0.1"}`),
			Tag:      "api",
		}},
	}, nil
}

func (r *rig) writeStats(counters map[string]int64) {
	r.t.Helper()
	body, err := json.Marshal(counters)
	if err != nil {
		r.t.Fatalf("encode stats: %v", err)
	}
	if err := os.WriteFile(r.stats, body, 0o600); err != nil {
		r.t.Fatalf("write stats: %v", err)
	}
}

func (r *rig) waitReady() {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(r.ready); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the stand-in xray never bound its api port")
}

func (r *rig) feed(email string, up, down int64) {
	r.writeStats(map[string]int64{
		"user>>>" + email + ">>>traffic>>>uplink":   up,
		"user>>>" + email + ">>>traffic>>>downlink": down,
	})
	r.waitReady()
}

func (r *rig) restart() { r.writeStats(map[string]int64{}) }

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
	if ops, err := os.ReadFile(filepath.Join(filepath.Dir(r.servedF), "oplog.txt")); err == nil {
		r.t.Logf("gRPC operations so far:\n%s", ops)
	} else {
		r.t.Log("no gRPC operations reached the stand-in core")
	}
	return strings.Fields(string(data))
}

func (r *rig) starts() int {
	r.t.Helper()
	r.waitReady()
	time.Sleep(300 * time.Millisecond)
	data, err := os.ReadFile(r.pidFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		r.t.Fatalf("read pid file: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// instance mirrors what the runtime layer builds: the clients live in the
// settings blob and Users is the projection of them. A rig that put users in
// only one of the two would certify a renderer that reads the other.
func (r *rig) instance(users int) core.Instance {
	inst := core.Instance{
		ID:             9,
		Kind:           "vless",
		Tag:            "inbound-9",
		Listen:         "127.0.0.1",
		Port:           29900,
		Enable:         true,
		StreamSettings: `{"network":"tcp"}`,
	}
	clients := make([]string, 0, users)
	for i := range users {
		email := fmt.Sprintf("%c@example.com", 'a'+i)
		id := fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i)
		clients = append(clients, fmt.Sprintf(`{"email":%q,"id":%q,"enable":true}`, email, id))
		inst.Users = append(inst.Users, core.User{
			Email:       email,
			Enable:      true,
			Credentials: map[string]any{"id": id},
		})
	}
	inst.Settings = fmt.Sprintf(`{"clients":[%s],"decryption":"none"}`, strings.Join(clients, ","))
	return inst
}

func (r *rig) asRig() coretest.Rig {
	return coretest.Rig{
		NewCore:       func() (core.Core, error) { return New(Deps{BaseConfig: r.baseConfig}), nil },
		Instance:      r.instance,
		Starts:        r.starts,
		FeedTraffic:   r.feed,
		RestartSource: r.restart,
		ServedUsers:   r.served,
	}
}

// TestXrayConformsToTheContract is the acceptance test for the port. The suite
// is the same one mtproto passes; a failure here is the contract's problem.
func TestXrayConformsToTheContract(t *testing.T) {
	coretest.RunAdapterSuite(t, newRig(t).asRig())
}

func TestPreflightRejectsAMissingBinary(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	err := New(Deps{}).Preflight(t.Context())
	if err == nil {
		t.Fatal("preflight must fail without an xray binary")
	}
	if !strings.Contains(err.Error(), engine.GetBinaryName()) {
		t.Fatalf("preflight error must name the binary it looked for, got %v", err)
	}
}

/*
The regression for a config that restarts Xray without changing.

The full-config generator passes the stored sections through untouched, so this
core must too. Rebuilding clients from Instance.Users sorts the keys and drops
every non-scalar credential, and even a re-marshal that changes nothing else
compacts what a healer indented — and InboundConfig.Equals compares bytes.
*/
func TestStoredSettingsAreRenderedVerbatim(t *testing.T) {
	stored := `{"clients":[{"id":"beef","email":"real@example.com","allowedIPs":["10.0.0.2/32"],"keepAlive":25}],"decryption":"none"}`
	indented := "{\n  \"network\": \"tcp\"\n}"
	inst := core.Instance{
		ID: 1, Kind: "vless", Tag: "in-1", Port: 443, Enable: true,
		Settings:       stored,
		StreamSettings: indented,
		Users: []core.User{
			{Email: "real@example.com", Enable: true, Credentials: map[string]any{"id": "beef"}},
		},
	}
	got, ok := toInbound(inst)
	if !ok {
		t.Fatal("an enabled instance must be serveable")
	}
	if string(got.Settings) != stored {
		t.Fatalf("settings were rewritten\n got: %s\nwant: %s", got.Settings, stored)
	}
	if string(got.StreamSettings) != indented {
		t.Fatalf("streamSettings were reformatted\n got: %q\nwant: %q", got.StreamSettings, indented)
	}
}

func TestPlanChangeSeparatesHotApplyFromRestart(t *testing.T) {
	c := New(Deps{})
	base := core.Instance{
		ID: 1, Kind: "vless", Tag: "in-1", Listen: "127.0.0.1", Port: 443, Enable: true,
		Settings:       `{"clients":[{"email":"a@example.com","id":"aaa"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp"}`,
		Users: []core.User{
			{Email: "a@example.com", Enable: true, Credentials: map[string]any{"id": "aaa"}},
		},
	}
	// The blob moves with the user list, as the runtime layer builds it: a
	// client added to Users alone is not a change this core can see.
	added := base
	added.Settings = `{"clients":[{"email":"a@example.com","id":"aaa"},{"email":"b@example.com","id":"bbb"}],"decryption":"none"}`
	added.Users = append(slices.Clone(base.Users), core.User{
		Email: "b@example.com", Enable: true, Credentials: map[string]any{"id": "bbb"},
	})
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
		{"added client", added, core.ActionHotApply},
		// Unlike a sidecar core, Xray replaces the listener through its own API,
		// so moving a port keeps every other inbound's connections.
		{"moved port", moved, core.ActionHotApply},
		{"disabled inbound", disabled, core.ActionRestart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.PlanChange(base, tc.next); got != tc.want {
				t.Fatalf("PlanChange = %s, want %s", got, tc.want)
			}
		})
	}
}
