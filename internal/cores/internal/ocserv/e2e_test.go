//go:build linux

package ocserv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/radius"
	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// The e2e half needs root, the daemon, its client and a network namespace for
// that client: the VM. It is gated on OCSERV_E2E=1 and skips elsewhere.
func e2eGate(t *testing.T) (string, string, string) {
	t.Helper()
	if os.Getenv("OCSERV_E2E") != "1" {
		t.Skip("OCSERV_E2E not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	daemon, err := exec.LookPath("ocserv")
	if err != nil {
		t.Skip("ocserv not installed")
	}
	ctl, err := exec.LookPath("occtl")
	if err != nil {
		t.Skip("occtl not installed")
	}
	client, err := exec.LookPath("openconnect")
	if err != nil {
		t.Skip("openconnect not installed")
	}
	return daemon, ctl, client
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func e2eUser(n int) core.User {
	email := coretest.Subject
	if n > 0 {
		email = fmt.Sprintf("user%d@example.com", n)
	}
	return core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUsername: fmt.Sprintf("user%d", n), core.CredPassword: fmt.Sprintf("secret-password-%d", n),
	}}
}

/*
newE2ECore runs the daemon as root under the exec backend, which is what a
panel without systemd does; the state directory is world-traversable because
ocserv drops privileges and then opens its sockets there.
*/
func newE2ECore(t *testing.T, daemon, ctl string) *Core {
	t.Helper()
	srv, err := radius.New(radius.NewSecret(), freeUDPAddr(t), freeUDPAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	dir, err := os.MkdirTemp("", "pui-ocserv-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	_ = os.Chmod(dir, 0o755)
	return New(Deps{Radius: srv, Binary: daemon, Occtl: ctl, StateDir: dir})
}

func mintedSettings(t *testing.T, c *Core, subnet string) string {
	t.Helper()
	// Everything through the tunnel: the origin sits outside the pool.
	out, err := c.MintInstance(Kind, `{"subnet":"`+subnet+`","redirectGateway":true}`, "")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

/*
servedByDaemon asks the way a login would: an Access-Request through the real
RADIUS server for each candidate with its known password. Accepted means
served. The daemon holds no user list to read; this is the honest one.
*/
// hostSubjects is who the daemon holds on which tunnel address, as it lists
// them itself; the only source a shaping target can be checked against.
func (c *Core) hostSubjects(t *testing.T, id int) map[string][]string {
	t.Helper()
	c.mu.Lock()
	cur, ok := c.instances[id]
	var byName map[string]core.User
	var socket string
	if ok {
		byName, socket = cur.userByName, cur.paths.ctl
	}
	c.mu.Unlock()
	out := map[string][]string{}
	if !ok {
		return out
	}
	rows, err := listUsers(context.Background(), c.occtlPath(), socket)
	if err != nil && !errors.Is(err, errNoDaemon) {
		t.Fatalf("occtl show users: %v", err)
	}
	for _, row := range rows {
		u, known := byName[row.Username]
		if !known {
			continue
		}
		if addr, err := netip.ParseAddr(row.IPv4); err == nil {
			out[u.Email] = append(out[u.Email], netip.PrefixFrom(addr, 32).String())
		}
	}
	return out
}

func (c *Core) servedByDaemon(t *testing.T, candidates []core.User) []string {
	t.Helper()
	c.mu.Lock()
	var nas string
	for id := range c.instances {
		nas = radius.NASIdentifier(string(ID), id)
	}
	srv := c.deps.Radius
	c.mu.Unlock()
	var out []string
	for _, u := range candidates {
		ok, err := radius.ProbeLogin(context.Background(), srv.AuthAddr(), srv.Secret(), nas,
			core.CredString(u.Credentials, core.CredUsername), core.CredString(u.Credentials, core.CredPassword))
		if err != nil {
			t.Logf("login %s: %v", u.Email, err)
		}
		if ok {
			out = append(out, u.Email)
		}
	}
	return out
}

func TestConformance(t *testing.T) {
	daemon, ctl, _ := e2eGate(t)
	var current *Core
	port := freeTCPPort(t)
	var settings string
	candidates := []core.User{e2eUser(0), e2eUser(1), e2eUser(2)}
	restarted := map[string]bool{}
	generation := map[string]int{}
	fedBytes := map[string]int64{}
	sessionOf := func(email string) string { return fmt.Sprintf("fed-%s-%d", email, generation[email]) }
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := newE2ECore(t, daemon, ctl)
			if current == nil {
				current = c
				settings = mintedSettings(t, c, "10.219.0.0/24")
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: Kind, Tag: "ocs-1", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settings}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, candidates[i])
			}
			return inst
		},
		Starts: func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
		Alive:  func() bool { return unitAlive(current.backend, 1) },
		// The suite's first reading is a session the panel found already running
		// (an interim record, baselined); after a daemon restart every client
		// reconnects, so the next record of each opens a new session with a Start.
		FeedTraffic: func(email string, up, down int64) {
			current.mu.Lock()
			for _, cur := range current.instances {
				if restarted[email] {
					cur.ledger.Apply(radius.Event{Status: radius.Start, SessionID: sessionOf(email), Username: email})
					delete(restarted, email)
				}
				cur.ledger.Apply(radius.Event{Status: radius.Interim, SessionID: sessionOf(email), Username: email, In: up, Out: down})
			}
			current.mu.Unlock()
		},
		RestartSource: func() {
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.ledger = radius.NewLedger(cur.inst.Tag)
			}
			current.mu.Unlock()
			for _, u := range candidates {
				restarted[u.Email] = true
				generation[u.Email]++
			}
		},
		ServedUsers: func() []string { return current.servedByDaemon(t, candidates) },
		// Read off the daemon, never the adapter: with no client connected the
		// daemon lists nobody, which is the truth a target must agree with.
		HostSubjects: func() map[string][]string { return current.hostSubjects(t, 1) },
		// A session fed with a stamp is one whose record arrived then: the ledger's
		// clock is that stamp while the record lands, and the record moves bytes.
		FeedSession: func(email, source string, lastSeen int64) {
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.ledger.SetClock(func() time.Time { return time.UnixMilli(lastSeen) })
				fedBytes[email]++
				cur.ledger.Apply(radius.Event{Status: radius.Interim, SessionID: sessionOf(email), Username: email, Peer: radius.Peer{Source: netip.MustParseAddr(source)}, In: fedBytes[email]})
				cur.ledger.SetClock(nil)
			}
			current.mu.Unlock()
		},
	}
	for _, failure := range coretest.Check(rig) {
		t.Error(failure)
	}
	if current != nil {
		_ = current.StopAll(context.Background())
	}
}

/*
Real bytes through a real tunnel, driven by the core rather than by a
hand-written config: a client built from the core's own rendered profile logs
in, gets the address the sink chose, fetches through the tunnel, and the core
bills it and reports the session from its source with its tunnel address.
Removing the client ends it, and the daemon's per-session device carries the
inbound's prefix, which is what keeps two inbounds' interfaces apart.
*/
func TestRealTrafficIsBilledAndSeen(t *testing.T) {
	daemon, ctl, openconnect := e2eGate(t)
	realTraffic(t, openconnect, newE2ECore(t, daemon, ctl))
}

/*
The same tunnel the way production runs it: a transient systemd unit, and the
daemon dropping to p-ui-core from the config the core wrote. Under the exec
backend it stays root and never drops, so this is the only run that proves
the state directory is one the daemon user can actually open its sockets in.
*/
func TestRealTrafficUnderSystemd(t *testing.T) {
	daemon, ctl, openconnect := e2eGate(t)
	skipWhenAPanelWouldSweepTheUnit(t)
	backend, err := supervisor.NewSystemd(context.Background())
	if err != nil {
		t.Skip("no systemd: ", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	who, err := osuser.Lookup("p-ui-core")
	if err != nil {
		t.Skip("no p-ui-core user: ", err)
	}
	gid, _ := strconv.Atoi(who.Gid)
	srv, err := radius.New(radius.NewSecret(), freeUDPAddr(t), freeUDPAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	dir, err := os.MkdirTemp("", "pui-ocserv-systemd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The core's own root under the traversable parent: what a stock install has.
	c := New(Deps{Radius: srv, Binary: daemon, Occtl: ctl, StateDir: filepath.Join(dir, "ocserv"), Backend: backend, User: who.Username, Group: gid})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(shellOut("journalctl -u " + supervisor.UnitName(string(ID), 2) + " --no-pager -n 40 2>&1"))
		}
	})
	realTraffic(t, openconnect, c)
}

func realTraffic(t *testing.T, openconnect string, c *Core) {
	t.Helper()
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	port := freeTCPPort(t)
	inst := core.Instance{
		ID: 2, Kind: Kind, Tag: "ocs-2", Listen: "10.251.2.1", Port: port, Enable: true,
		Settings: mintedSettings(t, c, "10.218.0.0/24"), Users: []core.User{e2eUser(1)},
	}
	alice := inst.Users[0]

	ns := "pui-ocserv-e2e"
	shellOut("ip netns del " + ns + " 2>/dev/null; ip link del voc0 2>/dev/null; true")
	for _, step := range [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", "voc0", "type", "veth", "peer", "name", "voc1"},
		{"ip", "link", "set", "voc1", "netns", ns},
		{"ip", "addr", "add", "10.251.2.1/30", "dev", "voc0"},
		{"ip", "link", "set", "voc0", "up"},
		{"ip", "netns", "exec", ns, "ip", "addr", "add", "10.251.2.2/30", "dev", "voc1"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "voc1", "up"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"},
	} {
		if out, err := exec.Command(step[0], step[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	shellOut("ip addr add 10.217.0.1/32 dev lo 2>/dev/null; true")
	t.Cleanup(func() {
		shellOut("ip netns del " + ns + " 2>/dev/null; ip link del voc0 2>/dev/null; ip addr del 10.217.0.1/32 dev lo 2>/dev/null; true")
	})

	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c.mu.Lock()
	p := c.instances[2].paths
	c.mu.Unlock()
	waitFor(t, 20*time.Second, func() bool { _, err := os.Stat(p.ctl); return err == nil },
		func() string {
			return "the daemon never opened its control socket:\n" + tailOf(filepath.Join(p.dir, "ocserv.err"))
		})

	// The profile the panel would serve, written as the client reads it, plus
	// the one route the server config does not push.
	share, err := c.RenderClient(inst, alice, "10.251.2.1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	profile := filepath.Join(dir, "client.conf")
	if err := os.WriteFile(profile, []byte(share.Body), 0o600); err != nil {
		t.Fatal(err)
	}

	const payload = 300_000
	const bigPayload = 2 << 20
	ln, err := net.Listen("tcp", "10.217.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := payload
		if r.URL.Path == "/big" {
			n = bigPayload
		}
		_, _ = w.Write(make([]byte, n))
	})}
	go func() { _ = origin.Serve(ln) }()
	t.Cleanup(func() { _ = origin.Close() })

	password := core.CredString(alice.Credentials, core.CredPassword)
	client := exec.Command("ip", "netns", "exec", ns, openconnect, "--config", profile,
		"--passwd-on-stdin", "--interface", "ocsclient", "--script", "/usr/share/vpnc-scripts/vpnc-script",
		"--non-inter", "--disable-ipv6", fmt.Sprintf("https://10.251.2.1:%d", port))
	client.Stdin = strings.NewReader(password + "\n")
	clientLog, _ := os.Create(filepath.Join(dir, "client.log"))
	client.Stdout, client.Stderr = clientLog, clientLog
	client.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-client.Process.Pid, syscall.SIGTERM); _ = client.Wait() })

	addr := func() string { return shellOut("ip netns exec " + ns + " ip -4 -o addr show ocsclient 2>/dev/null") }
	waitFor(t, 40*time.Second, func() bool { return strings.Contains(addr(), "10.218.0.") }, func() string {
		return "the client never came up: " + addr() + "\n--- client log:\n" + tailOf(filepath.Join(dir, "client.log")) +
			"\n--- daemon stderr:\n" + tailOf(filepath.Join(p.dir, "ocserv.err")) + "\n--- config:\n" + tailOf(p.conf)
	})

	// The device the daemon made for this session carries the inbound's own
	// prefix, which is what stops two inbounds sharing an interface name.
	if devices := shellOut("ip -o link show | grep -o 'poc2-[0-9]*'"); !strings.Contains(devices, "poc2-") {
		t.Fatalf("no device with this inbound's prefix exists:\n%s", shellOut("ip -o link show | cut -d: -f2"))
	}

	out := shellOut(fmt.Sprintf("ip netns exec %s curl -s -m 20 -o /dev/null -w '%%{http_code} %%{size_download}' http://10.217.0.1:%d/", ns, ln.Addr().(*net.TCPAddr).Port))
	if !strings.HasPrefix(out, "200 ") {
		t.Fatalf("GET through the tunnel: %q\n--- client log:\n%s", out, tailOf(filepath.Join(dir, "client.log")))
	}
	var down int64
	waitFor(t, 60*time.Second, func() bool {
		deltas, _ := c.CollectTraffic(context.Background())
		for _, d := range deltas {
			if d.Email == alice.Email {
				down += d.Down
			}
		}
		return down >= payload
	}, func() string { return fmt.Sprintf("billed %d down bytes to %s, want %d", down, alice.Email, payload) })

	// The session's identity, read off the daemon: the device it made and the
	// address the client holds, which is what a limit is attached to.
	clientAddr := tunnelAddr(t, addr())
	targets, err := c.ShapingTargets(context.Background(), inst)
	if err != nil {
		t.Fatalf("ShapingTargets: %v", err)
	}
	if len(targets) != 1 || !strings.HasPrefix(targets[0].Device, devicePrefixFor(2)) {
		t.Fatalf("targets = %+v, want the one session device %sN", targets, devicePrefixFor(2))
	}
	if got, want := targets[0].Keys[alice.Email].Prefixes, []netip.Prefix{netip.PrefixFrom(clientAddr, 32)}; !slices.Equal(got, want) {
		t.Fatalf("alice is keyed by %v on %s, want %v", got, targets[0].Device, want)
	}
	shapedDownload(t, targets[0], alice.Email, ns, fmt.Sprintf("http://10.217.0.1:%d/big", ln.Addr().(*net.TCPAddr).Port))

	sessions, _ := c.Sessions(context.Background())
	found := false
	for _, s := range sessions {
		if s.Email == alice.Email && s.Source.String() == "10.251.2.2" && len(s.Local) == 1 && strings.HasPrefix(s.Local[0].String(), "10.218.0.") {
			found = true
		}
	}
	if !found {
		t.Fatalf("alice's session with source 10.251.2.2 and a tunnel address is not reported: %+v", sessions)
	}
	if online, _ := c.OnlineEmails(context.Background()); !slices.Contains(online, alice.Email) {
		t.Fatalf("online = %v", online)
	}

	// A cut by source reaches the real daemon: occtl's JSON names the device's
	// session id and remote address, and the disconnect ends that session. Read
	// back through the daemon's own Accounting-Stop — the session leaves the
	// ledger, and a reconnect the client makes is a NEW session with a later start.
	before := startedFrom(c, alice.Email, "10.251.2.2")
	if before == 0 {
		t.Fatal("the ledger reports no start for the live session; the cut could not be told from a reconnect")
	}
	if cut, err := c.CutSessions(context.Background(), alice.Email, netip.MustParseAddr("10.251.2.2")); err != nil || cut != 1 {
		t.Fatalf("CutSessions = (%d, %v), want (1, nil)\n--- daemon stderr:\n%s", cut, err, tailOf(filepath.Join(p.dir, "ocserv.err")))
	}
	waitFor(t, 30*time.Second, func() bool {
		now := startedFrom(c, alice.Email, "10.251.2.2")
		return now == 0 || now > before
	}, func() string {
		s, _ := c.Sessions(context.Background())
		return fmt.Sprintf("the cut session is still up: %+v\n--- client log:\n%s", s, tailOf(filepath.Join(dir, "client.log")))
	})

	// Removing the client refuses its next login and ends the session it holds.
	if err := c.RemoveUser(context.Background(), inst, alice.Email); err != nil {
		t.Fatalf("remove: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool {
		sessions, _ := c.Sessions(context.Background())
		for _, s := range sessions {
			if s.Email == alice.Email {
				return false
			}
		}
		return true
	}, func() string {
		s, _ := c.Sessions(context.Background())
		return fmt.Sprintf("alice still has a session after removal: %+v\n--- client log:\n%s", s, tailOf(filepath.Join(dir, "client.log")))
	})
	if served := c.servedByDaemon(t, []core.User{alice}); len(served) != 0 {
		t.Fatalf("a removed client still logs in: %v", served)
	}
}

// startedFrom is when the client's session from one source opened, per the
// ledger, or 0 when it holds none: a cut is told from a reconnect by the start.
func startedFrom(c *Core, email, source string) int64 {
	sessions, _ := c.Sessions(context.Background())
	for _, s := range sessions {
		if s.Email == email && s.Source.String() == source {
			return s.StartedUnixMilli
		}
	}
	return 0
}

// tunnelAddr picks the client's tunnel address out of `ip -o addr show`.
func tunnelAddr(t *testing.T, line string) netip.Addr {
	t.Helper()
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == "inet" && i+1 < len(fields) {
			if prefix, err := netip.ParsePrefix(fields[i+1]); err == nil {
				return prefix.Addr()
			}
		}
	}
	t.Fatalf("no tunnel address in %q", line)
	return netip.Addr{}
}

/*
shapedDownload is the measured half: the panel's own shaping manager, driven
against the session device the core named, holds a real download to the limit
and lets it go when the want is withdrawn. Two mebibytes at two megabits is
eight seconds; unshaped, off loopback, it is a fraction of one. Only real
bytes through the real device prove this — a readback that agrees with the
push cannot.
*/
func shapedDownload(t *testing.T, target core.ShapingTarget, email, ns, url string) {
	t.Helper()
	owned := shaping.DefaultNamespaces()
	if err := owned.RegisterFamily(DevicePrefix); err != nil {
		t.Fatal(err)
	}
	m := shaping.NewManager(shaping.HostPlane(), owned)
	ctx := context.Background()
	bare := []shaping.DeviceWant{{Device: target.Device}}
	t.Cleanup(func() { _ = m.Converge(ctx, bare) })
	want := []shaping.DeviceWant{{Device: target.Device, Subjects: []shaping.Subject{{
		ID:     email,
		Keys:   []shaping.Key{{Prefix: target.Keys[email].Prefixes[0]}},
		Limits: shaping.Limits{DownBps: 2_000_000},
	}}}}
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("Converge on %s: %v", target.Device, err)
	}
	get := func() time.Duration {
		start := time.Now()
		out := shellOut(fmt.Sprintf("ip netns exec %s curl -s -m 60 -o /dev/null -w '%%{http_code} %%{size_download}' %s", ns, url))
		if !strings.HasPrefix(out, "200 ") {
			t.Fatalf("GET %s: %q", url, out)
		}
		return time.Since(start)
	}
	shaped := get()
	if shaped < 5*time.Second {
		t.Fatalf("2 MiB arrived in %s under a 2 Mbit limit on %s; the session device is not shaped", shaped, target.Device)
	}
	if err := m.Converge(ctx, bare); err != nil {
		t.Fatalf("teardown on %s: %v", target.Device, err)
	}
	free := get()
	if free > 2*time.Second {
		t.Fatalf("2 MiB took %s after the limit was withdrawn; the tree on %s was not torn down", free, target.Device)
	}
	t.Logf("2 MiB through %s: %s at 2 Mbit, %s unshaped", target.Device, shaped.Round(time.Millisecond), free.Round(time.Millisecond))
}

func shellOut(script string) string {
	out, _ := exec.Command("sh", "-c", script).CombinedOutput()
	return string(out)
}

func tailOf(path string) string {
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, why func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal(why())
}

/*
A live panel on this host reconciles the same unit names and stops any it does
not recognise -- which is exactly what this test's transient unit looks like to
it. It gets stopped a few seconds in and the failure reads as a billing bug, so
the test says so instead of spending a minute proving nothing.
*/
func skipWhenAPanelWouldSweepTheUnit(t *testing.T) {
	t.Helper()
	out, _ := exec.Command("systemctl", "is-active", "p-ui.service").Output()
	if strings.TrimSpace(string(out)) == "active" {
		t.Skip("p-ui.service is running and sweeps unit names this test uses; stop it for this run")
	}
}

// Its own RADIUS ports, not the defaults: `go test ./...` runs packages in
// parallel, so two cores' end-to-end tests would otherwise race for 1812/1813.
func freeUDPAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()
	return addr
}

// "Redirect off" renders a route for the pool alone; the daemon, not the
// renderer, is what says whether that file is one it runs.
func TestRealDaemonAcceptsTheSplitRouteConfig(t *testing.T) {
	daemon, ctl, _ := e2eGate(t)
	c := newE2ECore(t, daemon, ctl)
	raw, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24","redirectGateway":false}`, "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := parseSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	cur := &instance{
		inst:     core.Instance{ID: 7, Kind: Kind, Tag: "ocserv-7", Port: freeTCPPort(t), Settings: raw, Users: []core.User{e2eUser(1)}},
		settings: settings,
		ledger:   radius.NewLedger("ocserv-7"),
		paths:    pathsIn(filepath.Join(c.stateDir(), "7")),
	}
	cur.userByName = usersByName(cur.inst.Users)
	c.mu.Lock()
	err = c.writeFilesLocked(cur)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(cur.paths.conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "route = 10.11.0.0/24\n") || strings.Contains(string(conf), "route = default") {
		t.Fatalf("rendered routes:\n%s", conf)
	}
	if err := c.testConfig(context.Background(), cur.paths.conf); err != nil {
		t.Fatalf("ocserv --test-config: %v", err)
	}
}
