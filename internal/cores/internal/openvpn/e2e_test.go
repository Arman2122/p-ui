//go:build linux

package openvpn

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/radius"
)

// The e2e half needs root, the daemon, the plugin and a network namespace for
// the client: the VM. It is gated on OPENVPN_E2E=1 and skips elsewhere.
func e2eGate(t *testing.T) string {
	t.Helper()
	if os.Getenv("OPENVPN_E2E") != "1" {
		t.Skip("OPENVPN_E2E not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	bin, err := exec.LookPath("openvpn")
	if err != nil {
		t.Skip("openvpn not installed")
	}
	if _, err := os.Stat(PluginPath); err != nil {
		t.Skip("openvpn-auth-radius not installed")
	}
	return bin
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
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

// newE2ECore: the panel's own RADIUS server on its default ports, the exec backend as root.
func newE2ECore(t *testing.T, bin string) *Core {
	t.Helper()
	srv, err := radius.New(radius.NewSecret(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return New(Deps{Radius: srv, Binary: bin, StateDir: t.TempDir()})
}

// mintedSettings is an inbound as the panel would have saved it.
func mintedSettings(t *testing.T, c *Core, subnet string) string {
	t.Helper()
	out, err := c.MintInstance(Kind, `{"proto":"udp","subnet":"`+subnet+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// withClientKeys mints every user's wrapped key against the inbound, as the
// client save path does through MintClientCredentials.
func withClientKeys(t *testing.T, c *Core, inst core.Instance) core.Instance {
	t.Helper()
	for i := range inst.Users {
		have := map[string]string{
			core.CredUsername: core.CredString(inst.Users[i].Credentials, core.CredUsername),
			core.CredPassword: core.CredString(inst.Users[i].Credentials, core.CredPassword),
		}
		minted, err := c.MintClientCredentials(Kind, inst.Settings, have)
		if err != nil {
			t.Fatal(err)
		}
		inst.Users[i].Credentials[core.CredTLSCryptV2] = minted[core.CredTLSCryptV2]
	}
	return inst
}

/*
servedByDaemon asks the way a login would: an Access-Request through the real
RADIUS server for each candidate with its known password. Accepted means
served. There is no user list in the daemon to read; this is the honest one.
*/
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
		ok, err := radiusLogin(srv, nas, core.CredString(u.Credentials, core.CredUsername), core.CredString(u.Credentials, core.CredPassword))
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
	bin := e2eGate(t)
	var current *Core
	port := freeUDPPort(t)
	var settings string
	candidates := []core.User{e2eUser(0), e2eUser(1), e2eUser(2)}
	restarted := map[string]bool{}
	generation := map[string]int{}
	fedBytes := map[string]int64{}
	sessionOf := func(email string) string { return fmt.Sprintf("fed-%s-%d", email, generation[email]) }
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := newE2ECore(t, bin)
			if current == nil {
				current = c
				settings = mintedSettings(t, c, "10.213.0.0/24")
				for i := range candidates {
					have := map[string]string{core.CredUsername: core.CredString(candidates[i].Credentials, core.CredUsername), core.CredPassword: core.CredString(candidates[i].Credentials, core.CredPassword)}
					minted, err := c.MintClientCredentials(Kind, settings, have)
					if err != nil {
						t.Fatal(err)
					}
					candidates[i].Credentials[core.CredTLSCryptV2] = minted[core.CredTLSCryptV2]
				}
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: Kind, Tag: "ovpn-1", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settings}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, candidates[i])
			}
			return inst
		},
		Starts: func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
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
		ServedUsers:  func() []string { return current.servedByDaemon(t, candidates) },
		HostSubjects: func() map[string][]string { return current.ccdSubjects(t, candidates) },
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

// ccdSubjects reads the tunnel address the plugin landed for each client off
// the host: the ccd file OpenVPN reads at connect, which is the identity the
// shaping keys must agree with.
func (c *Core) ccdSubjects(t *testing.T, candidates []core.User) map[string][]string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]string{}
	for _, cur := range c.instances {
		for _, u := range candidates {
			name := core.CredString(u.Credentials, core.CredUsername)
			body, err := os.ReadFile(filepath.Join(cur.paths.ccd, name))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(body), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "ifconfig-push" {
					out[u.Email] = append(out[u.Email], fields[1]+"/32")
				}
			}
		}
	}
	return out
}

// radiusLogin is one Access-Request as the plugin would send it.
func radiusLogin(srv *radius.Server, nas, username, password string) (bool, error) {
	return radius.ProbeLogin(context.Background(), srv.AuthAddr(), srv.Secret(), nas, username, password)
}

/*
Real bytes through a real tunnel: a client in its own namespace logs in through
the plugin, gets the sink's address, fetches through the tunnel, and the core
bills the client and reports its session with its source; removing the client
ends the session.
*/
func TestRealTrafficIsBilledAndSeen(t *testing.T) {
	bin := e2eGate(t)
	c := newE2ECore(t, bin)
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	port := freeUDPPort(t)
	inst := withClientKeys(t, c, core.Instance{ID: 2, Kind: Kind, Tag: "ovpn-2", Listen: "", Port: port, Enable: true, Settings: mintedSettings(t, c, "10.214.0.0/24"), Users: []core.User{e2eUser(1)}})
	alice := inst.Users[0]
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	ns := "pui-ovpn-e2e"
	shellOut("ip netns del " + ns + " 2>/dev/null; ip link del ve2e0 2>/dev/null; true")
	for _, step := range [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", "ve2e0", "type", "veth", "peer", "name", "ve2e1"},
		{"ip", "link", "set", "ve2e1", "netns", ns},
		{"ip", "addr", "add", "10.250.1.1/30", "dev", "ve2e0"},
		{"ip", "link", "set", "ve2e0", "up"},
		{"ip", "netns", "exec", ns, "ip", "addr", "add", "10.250.1.2/30", "dev", "ve2e1"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "ve2e1", "up"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"},
	} {
		if out, err := exec.Command(step[0], step[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	shellOut("ip addr add 10.215.0.1/32 dev lo 2>/dev/null; true")
	t.Cleanup(func() {
		shellOut("ip netns del " + ns + " 2>/dev/null; ip link del ve2e0 2>/dev/null; ip addr del 10.215.0.1/32 dev lo 2>/dev/null; true")
	})

	// The client profile the panel would serve, pointed at the veth side and
	// told the extra route the server config does not push.
	share, err := c.RenderClient(inst, alice, "10.250.1.1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	profile := filepath.Join(dir, "client.ovpn")
	body := share.Body + "route 10.215.0.0 255.255.255.0\ndev povclient\ndev-type tun\nlog-append " + filepath.Join(dir, "client.log") + "\n"
	body = strings.Replace(body, "dev tun\n", "", 1)
	if err := os.WriteFile(profile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	const payload = 300_000
	ln, err := net.Listen("tcp", "10.215.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, payload)) })}
	go func() { _ = origin.Serve(ln) }()
	t.Cleanup(func() { _ = origin.Close() })

	client := exec.Command("ip", "netns", "exec", ns, bin, "--config", profile)
	clientErr, _ := os.Create(filepath.Join(dir, "client.err"))
	client.Stdout, client.Stderr = clientErr, clientErr
	client.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-client.Process.Pid, syscall.SIGTERM); _ = client.Wait() })
	addr := func() string { return shellOut("ip netns exec " + ns + " ip -4 -o addr show povclient 2>/dev/null") }
	waitFor(t, 30*time.Second, func() bool { return strings.Contains(addr(), "10.214.0.") }, func() string {
		return "the client never came up: " + addr() + "\n--- client log:\n" + tailOf(filepath.Join(dir, "client.log")) + "\n--- client stderr:\n" + tailOf(filepath.Join(dir, "client.err")) +
			"\n--- server log:\n" + tailOf(c.instances[2].paths.log) + "\n--- server stderr:\n" + tailOf(filepath.Join(c.instances[2].paths.dir, "openvpn.err")) + "\n--- server.conf:\n" + tailOf(c.instances[2].paths.conf)
	})

	out := shellOut(fmt.Sprintf("ip netns exec %s curl -s -m 20 -o /dev/null -w '%%{http_code} %%{size_download}' http://10.215.0.1:%d/", ns, ln.Addr().(*net.TCPAddr).Port))
	if !strings.HasPrefix(out, "200 ") {
		t.Fatalf("GET through the tunnel: %q", out)
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
	sessions, _ := c.Sessions(context.Background())
	found := false
	for _, s := range sessions {
		if s.Email == alice.Email && s.Source.String() == "10.250.1.2" && len(s.Local) == 1 && strings.HasPrefix(s.Local[0].String(), "10.214.0.") {
			found = true
		}
	}
	if !found {
		t.Fatalf("alice's session with source 10.250.1.2 and a tunnel address is not reported: %+v", sessions)
	}
	if online, _ := c.OnlineEmails(context.Background()); !slices.Contains(online, alice.Email) {
		t.Fatalf("online = %v", online)
	}

	// Removing the client ends the session through the management socket.
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
		return fmt.Sprintf("alice still has a session after removal: %+v\n--- server log:\n%s\n--- client log:\n%s", s, tailOf(c.instances[2].paths.log), tailOf(filepath.Join(dir, "client.log")))
	})
	if served := c.servedByDaemon(t, []core.User{alice}); len(served) != 0 {
		t.Fatalf("a removed client still logs in: %v", served)
	}
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

var _ = io.Discard
