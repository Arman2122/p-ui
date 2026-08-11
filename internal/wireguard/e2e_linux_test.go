//go:build linux

package wireguard

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
Drives the real engine against a real kernel device. Everything else in this
package tests against wgtest, and a fake can be wrong in exactly the ways the
kernel is surprising -- allowed-IP ownership, counter lifetime, ifindex reuse.

Run it inside a private network namespace -- `ip netns exec <ns> ./wireguard.test`
-- because a stranded-device pass deletes every pwg<N> link it can see, which on
a live panel host is every wgkernel inbound. The gate below enforces that.
*/

func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("PUI_WG_E2E") != "1" {
		t.Skip("set PUI_WG_E2E=1 to run against the real kernel (needs root + the wireguard module)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if !confinedToOwnNetns("/proc/self/ns/net", "/proc/1/ns/net") {
		t.Skip("run inside a private network namespace: these tests delete every pwg<N> device they can see")
	}
}

// confinedToOwnNetns reports whether this process left init's network namespace.
// Unreadable is not confined: the only safe answer is the one that skips.
func confinedToOwnNetns(selfPath, initPath string) bool {
	self, err := os.Readlink(selfPath)
	if err != nil {
		return false
	}
	init, err := os.Readlink(initPath)
	if err != nil {
		return false
	}
	return self != init
}

// The gate is the only thing between a live panel host and a Reconcile that
// deletes every wgkernel inbound's device, so every unknown must skip.
func TestTheLiveGateRefusesTheHostNamespace(t *testing.T) {
	dir := t.TempDir()
	link := func(name, target string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
		return path
	}
	host := link("host", "net:[4026531840]")
	private := link("private", "net:[4026532567]")
	absent := filepath.Join(dir, "gone")

	cases := []struct {
		name string
		self string
		init string
		want bool
	}{
		{"init's own namespace is not confined", host, host, false},
		{"a namespace init is not in is confined", private, host, true},
		{"an unreadable self is not confined", absent, host, false},
		{"an unreadable init is not confined", private, absent, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := confinedToOwnNetns(tc.self, tc.init); got != tc.want {
				t.Fatalf("confinedToOwnNetns(%s, %s) = %v, want %v", tc.self, tc.init, got, tc.want)
			}
		})
	}
}

func liveManager(t *testing.T) (*Manager, string) {
	t.Helper()
	e2e(t)
	m := NewManager(hostPlane())
	id := 9000 + (os.Getpid() % 500)
	name := InterfaceName(id)
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "del", name).Run()
	})
	_ = exec.Command("ip", "link", "del", name).Run()
	return m, name
}

func genKey(t *testing.T) (priv, pub string) {
	t.Helper()
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	return k.String(), k.PublicKey().String()
}

func liveDevice(t *testing.T, name string) *wgtypes.Device {
	t.Helper()
	c, err := wgctrl.New()
	if err != nil {
		t.Fatalf("wgctrl.New: %v", err)
	}
	defer c.Close()
	d, err := c.Device(name)
	if err != nil {
		t.Fatalf("Device(%s): %v", name, err)
	}
	return d
}

func ifindexOf(t *testing.T, name string) int {
	t.Helper()
	l, err := net.InterfaceByName(name)
	if err != nil {
		return -1
	}
	return l.Index
}

func inst(id int, priv string, peers ...Peer) Instance {
	return Instance{
		ID: id, Tag: "e2e", Port: 51820, PrivateKey: priv,
		Address: []string{"10.123.0.1/24"}, MTU: 1420, Peers: peers,
	}
}

// peerBytes is the kernel's own cumulative total for one peer, read outside the
// engine so a test can compare what moved against what the panel billed.
func peerBytes(t *testing.T, name, pub string) int64 {
	t.Helper()
	for _, p := range liveDevice(t, name).Peers {
		if p.PublicKey.String() == pub {
			return p.ReceiveBytes + p.TransmitBytes
		}
	}
	return 0
}

func billedTo(deltas []Traffic, email string) int64 {
	var sum int64
	for _, d := range deltas {
		if d.Email == email {
			sum += d.Up + d.Down
		}
	}
	return sum
}

// liveTunnel puts a real WireGuard client in its own namespace, reached over a
// veth pair, and returns a function that drives real traffic through it.
func liveTunnel(t *testing.T, tag, name, srvPub, cliPriv string) func() {
	t.Helper()
	ns := "puiwg" + tag
	host, client := "pwg"+tag+"-h", "pwg"+tag+"-c"
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	_ = exec.Command("ip", "netns", "del", ns).Run()
	_ = exec.Command("ip", "link", "del", host).Run()
	t.Cleanup(func() {
		_ = exec.Command("ip", "netns", "del", ns).Run()
		_ = exec.Command("ip", "link", "del", host).Run()
	})

	sh("ip", "link", "set", name, "up")
	sh("ip", "netns", "add", ns)
	sh("ip", "link", "add", host, "type", "veth", "peer", "name", client)
	sh("ip", "link", "set", client, "netns", ns)
	sh("ip", "addr", "add", "192.168.66.1/24", "dev", host)
	sh("ip", "link", "set", host, "up")
	sh("ip", "netns", "exec", ns, "ip", "addr", "add", "192.168.66.2/24", "dev", client)
	sh("ip", "netns", "exec", ns, "ip", "link", "set", client, "up")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")

	kf := t.TempDir() + "/cli.key"
	if err := os.WriteFile(kf, []byte(cliPriv), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	sh("ip", "netns", "exec", ns, "ip", "link", "add", "wgc", "type", "wireguard")
	sh("ip", "netns", "exec", ns, "ip", "addr", "add", "10.123.0.2/24", "dev", "wgc")
	sh("ip", "netns", "exec", ns, "wg", "set", "wgc", "private-key", kf)
	sh("ip", "netns", "exec", ns, "wg", "set", "wgc", "peer", srvPub,
		"allowed-ips", "10.123.0.0/24", "endpoint", "192.168.66.1:51820", "persistent-keepalive", "5")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "wgc", "up")

	return func() {
		t.Helper()
		_ = exec.Command("ip", "netns", "exec", ns, "ping", "-c", "20", "-i", "0.1", "-s", "1200", "-W", "2", "10.123.0.1").Run()
		time.Sleep(600 * time.Millisecond)
	}
}

// awaitTraffic drives the tunnel until the peer's counters move. A device the
// panel rebuilt breaks the client's session, and WireGuard backs off for several
// seconds before it tries a new handshake.
func awaitTraffic(t *testing.T, name, pub string, drive func()) int64 {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		drive()
		if got := peerBytes(t, name, pub); got > 0 {
			return got
		}
		if time.Now().After(deadline) {
			return 0
		}
	}
}

// Ensure must build the whole device from nothing, and a second Ensure must be
// a no-op that still leaves it correct.
func TestLiveEnsureBuildsAndIsIdempotent(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	in := inst(9000+(os.Getpid()%500), srv, Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}})

	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	d := liveDevice(t, name)
	if got := d.PrivateKey.String(); got != srv {
		t.Errorf("private key = %q, want the configured one", got)
	}
	if d.ListenPort != 51820 {
		t.Errorf("listen port = %d, want 51820", d.ListenPort)
	}
	if len(d.Peers) != 1 {
		t.Fatalf("peers = %d, want 1", len(d.Peers))
	}
	if got := d.Peers[0].AllowedIPs[0].String(); got != "10.123.0.2/32" {
		t.Errorf("allowed-ip = %q, want 10.123.0.2/32", got)
	}

	idx := ifindexOf(t, name)
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if got := ifindexOf(t, name); got != idx {
		t.Errorf("ifindex changed %d -> %d; the second Ensure recreated the link", idx, got)
	}
}

// The property the whole core is built around: no client edit takes the link
// down, and no edit disturbs a peer it did not name.
func TestLiveClientEditsNeverTakeTheLinkDown(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	_, pubB := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	base := inst(id, srv,
		Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}},
		Peer{Email: "b@x", PublicKey: pubB, AllowedIPs: []string{"10.123.0.3/32"}},
	)
	if err := m.Ensure(context.Background(), base); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	idx := ifindexOf(t, name)

	_, pubC := genKey(t)
	steps := []struct {
		name string
		run  func() error
	}{
		{"add a peer", func() error {
			return m.AddPeer(context.Background(), base, Peer{Email: "c@x", PublicKey: pubC, AllowedIPs: []string{"10.123.0.4/32"}})
		}},
		{"move an allowed-IP", func() error {
			next := base
			next.Peers = []Peer{
				{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.9/32"}},
				{Email: "b@x", PublicKey: pubB, AllowedIPs: []string{"10.123.0.3/32"}},
				{Email: "c@x", PublicKey: pubC, AllowedIPs: []string{"10.123.0.4/32"}},
			}
			return m.Ensure(context.Background(), next)
		}},
		{"change keepalive", func() error {
			next := base
			next.Peers = []Peer{
				{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.9/32"}, KeepAlive: 25},
				{Email: "b@x", PublicKey: pubB, AllowedIPs: []string{"10.123.0.3/32"}},
				{Email: "c@x", PublicKey: pubC, AllowedIPs: []string{"10.123.0.4/32"}},
			}
			return m.Ensure(context.Background(), next)
		}},
		{"remove a peer", func() error {
			return m.RemovePeer(context.Background(), base, "c@x", pubC)
		}},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := ifindexOf(t, name); got != idx {
			t.Fatalf("%s: ifindex changed %d -> %d, the link went down", s.name, idx, got)
		}
	}
	d := liveDevice(t, name)
	if len(d.Peers) != 2 {
		t.Fatalf("peers = %d, want 2 after add+remove", len(d.Peers))
	}
	for _, p := range d.Peers {
		if p.PublicKey.String() == pubA {
			if got := p.AllowedIPs[0].String(); got != "10.123.0.9/32" {
				t.Errorf("moved allowed-ip = %q, want 10.123.0.9/32", got)
			}
			if p.PersistentKeepaliveInterval != 25*time.Second {
				t.Errorf("keepalive = %v, want 25s", p.PersistentKeepaliveInterval)
			}
		}
	}
}

// The reason Ensure snapshots instead of caching fingerprints: the panel is not
// the only thing that can touch the device.
func TestLiveEnsureRepairsOutOfBandDamage(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// an operator flushes the peer behind the panel's back
	if out, err := exec.Command("wg", "set", name, "peer", pubA, "remove").CombinedOutput(); err != nil {
		t.Fatalf("out-of-band remove: %v: %s", err, out)
	}
	if got := len(liveDevice(t, name).Peers); got != 0 {
		t.Fatalf("setup: peers = %d, want 0 after the out-of-band flush", got)
	}

	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("repairing Ensure: %v", err)
	}
	if got := len(liveDevice(t, name).Peers); got != 1 {
		t.Errorf("peers = %d after repair, want 1 -- Ensure did not reconverge", got)
	}
}

// A link destroyed and rebuilt zeroes every kernel counter. The epoch exists so
// that reset is observed and the whole new reading billed, not measured against
// a baseline the kernel no longer holds.
func TestLiveLinkRecreateIsObservedAsAReset(t *testing.T) {
	m, name := liveManager(t)
	srv, srvPub := genKey(t)
	cliPriv, cliPub := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: cliPub, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	drive := liveTunnel(t, "reset", name, srvPub, cliPriv)
	m.CollectTraffic(context.Background())
	drive()
	if billed := billedTo(collect(m), "a@x"); billed <= 0 {
		t.Fatalf("nothing was billed before the recreate; the rig is broken, not the engine")
	}

	if out, err := exec.Command("ip", "link", "del", name).CombinedOutput(); err != nil {
		t.Fatalf("link del: %v: %s", err, out)
	}
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure after delete: %v", err)
	}
	if got := len(liveDevice(t, name).Peers); got != 1 {
		t.Fatalf("peers = %d after recreate, want 1", got)
	}

	onDevice := awaitTraffic(t, name, cliPub, drive)
	if onDevice <= 0 {
		t.Fatalf("the rebuilt device counted nothing; the client never re-handshook, so this proves nothing about the epoch")
	}
	billed := billedTo(collect(m), "a@x")
	if billed < onDevice {
		t.Errorf("the rebuilt device holds %d bytes and the panel billed %d; a recreate restarts the counters at zero, so the whole reading is unbilled traffic", onDevice, billed)
	}
}

func collect(m *Manager) []Traffic {
	deltas, _ := m.CollectTraffic(context.Background())
	return deltas
}

// Real traffic through a real tunnel, attributed to the right peer. Note a peer
// accrues handshake bytes before any user traffic, so this asserts growth, not
// an exact figure.
func TestLiveTrafficIsAttributedPerPeer(t *testing.T) {
	m, name := liveManager(t)
	srv, srvPub := genKey(t)
	cliPriv, cliPub := genKey(t)
	idlePriv, idlePub := genKey(t)
	_ = idlePriv
	id := 9000 + (os.Getpid() % 500)

	in := inst(id, srv,
		Peer{Email: "busy@x", PublicKey: cliPub, AllowedIPs: []string{"10.123.0.2/32"}},
		Peer{Email: "idle@x", PublicKey: idlePub, AllowedIPs: []string{"10.123.0.3/32"}},
	)
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	drive := liveTunnel(t, "attr", name, srvPub, cliPriv)

	m.CollectTraffic(context.Background())
	drive()

	deltas, online := m.CollectTraffic(context.Background())
	byEmail := map[string]int64{}
	for _, d := range deltas {
		byEmail[d.Email] += d.Up + d.Down
	}
	if byEmail["busy@x"] <= 0 {
		t.Errorf("busy@x billed %d bytes, want > 0 -- real traffic was not attributed", byEmail["busy@x"])
	}
	if len(online) == 0 || online[0] != "busy@x" {
		t.Errorf("online = %v, want [busy@x] -- handshake not reported", online)
	}
	if byEmail["idle@x"] != 0 {
		t.Errorf("idle@x billed %d bytes, want 0 -- traffic leaked across peers", byEmail["idle@x"])
	}
	t.Logf("attributed: busy=%d idle=%d", byEmail["busy@x"], byEmail["idle@x"])
}

// Removing a peer zeroes its kernel counters, so the engine banks the final
// reading before it issues the Remove or those bytes are spent for free. This is
// every client edit, not only a revocation: Local.UpdateUser is remove + add.
func TestLiveRemoveBanksTheFinalReading(t *testing.T) {
	m, name := liveManager(t)
	srv, srvPub := genKey(t)
	cliPriv, cliPub := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: cliPub, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	drive := liveTunnel(t, "drain", name, srvPub, cliPriv)

	m.CollectTraffic(context.Background())
	// read after the baseline scrape, so this can only understate what moved
	banked := peerBytes(t, name, cliPub)
	drive()
	moved := peerBytes(t, name, cliPub) - banked
	if moved <= 0 {
		t.Fatalf("no traffic reached the peer; the rig is broken, not the engine")
	}

	if err := m.RemovePeer(context.Background(), in, "a@x", cliPub); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if got := len(liveDevice(t, name).Peers); got != 0 {
		t.Fatalf("peers = %d, want 0", got)
	}

	if billed := billedTo(collect(m), "a@x"); billed < moved {
		t.Errorf("at least %d bytes were on the peer when it was removed and the panel billed %d; the kernel zeroes a removed peer, so anything not banked first is gone", moved, billed)
	}
}

// The two halves of the preflight answer, on a host where the module is present
// but not yet loaded: nothing else on the box will load it, so Probe must.
//
// Gated separately because a kernel module is global. A network namespace
// confines devices, not modules, so unloading it here would drop every
// WireGuard tunnel on the machine -- including a live panel's inbounds.
func TestLiveProbeLoadsTheModule(t *testing.T) {
	e2e(t)
	if os.Getenv("PUI_WG_E2E_UNLOAD_MODULE") != "1" {
		t.Skip("set PUI_WG_E2E_UNLOAD_MODULE=1 only on a host with no WireGuard traffic: this unloads the module machine-wide, which no namespace can contain")
	}
	if out, err := exec.Command("modprobe", "-r", "wireguard").CombinedOutput(); err != nil {
		t.Skipf("wireguard.ko is in use here, so the not-yet-loaded case cannot be staged: %s", out)
	}
	t.Cleanup(func() { _ = exec.Command("modprobe", "wireguard").Run() })
	if _, err := os.Stat("/sys/module/wireguard"); err == nil {
		t.Skip("the module reloaded before the probe could run")
	}

	if err := hostPlane().Probe(context.Background()); err != nil {
		t.Fatalf("Probe = %v on a host whose kernel supports WireGuard; the picker greys the core out and nothing the operator can do from the UI ever loads the module", err)
	}
}

// An absent mtu means the kernel's own default, which is what a device created
// without one gets. Pins the constant against the kernel rather than recall.
func TestLiveClearingTheMTUReturnsTheKernelDefault(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv)
	in.MTU = 1280
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := deviceMTU(t, name); got != 1280 {
		t.Fatalf("mtu = %d, want the 1280 the operator set", got)
	}

	in.MTU = 0
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure after clearing the mtu: %v", err)
	}
	if got := deviceMTU(t, name); got != defaultMTU {
		t.Fatalf("mtu = %d after the operator cleared the field, want the kernel default %d", got, defaultMTU)
	}

	t.Run("that default is the kernel's own", func(t *testing.T) {
		probe := "pwgmtu" + strconv.Itoa(os.Getpid()%500)
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", probe).Run() })
		if out, err := exec.Command("ip", "link", "add", probe, "type", "wireguard").CombinedOutput(); err != nil {
			t.Fatalf("ip link add: %v: %s", err, out)
		}
		if got := deviceMTU(t, probe); got != defaultMTU {
			t.Fatalf("a plain `ip link add type wireguard` gives mtu %d, but the engine writes %d back when the field is cleared", got, defaultMTU)
		}
	})
}

func deviceMTU(t *testing.T, name string) int {
	t.Helper()
	l, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatalf("InterfaceByName(%s): %v", name, err)
	}
	return l.MTU
}

// A device whose inbound left the desired set while the panel was not running is
// in no in-memory map, so only the host itself can name it.
func TestLiveReconcileDeletesADeviceStrandedByARestart(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// A second wgkernel inbound, inside the panel's own pwg namespace. A bystander
	// named anything else proves nothing: the panel would never claim it.
	keptID := id + 1
	kept := InterfaceName(keptID)
	keptSrv, _ := genKey(t)
	keptInst := inst(keptID, keptSrv)
	keptInst.Port = 51821
	keptInst.Address = []string{"10.124.0.1/24"}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", kept).Run() })
	if err := m.Ensure(context.Background(), keptInst); err != nil {
		t.Fatalf("Ensure the second inbound: %v", err)
	}

	// The desired set production passes: the surviving inbound's row is still in
	// the database, this test's is not. Never nil -- an empty desired set means
	// "no inbound owns a device" and deletes every pwg<N> the host can see.
	restarted := NewManager(hostPlane())
	if err := restarted.Reconcile(context.Background(), []Instance{keptInst}); err != nil {
		t.Fatalf("Reconcile after a restart: %v", err)
	}
	if _, err := net.InterfaceByName(name); err == nil {
		t.Errorf("%s outlived its inbound across a panel restart: it still serves every peer with a valid key, is never billed, and no UI action removes it", name)
	}
	if _, err := net.InterfaceByName(kept); err != nil {
		t.Errorf("the panel deleted %s, a wgkernel inbound still present in the desired set", kept)
	}
}

// The device-level twin of TestLiveRemoveBanksTheFinalReading. Deleting the link
// zeroes every peer counter, so the whole interval since the last scrape is lost
// unless it is banked first.
func TestLiveDeviceRemovalBanksTheFinalReading(t *testing.T) {
	m, name := liveManager(t)
	srv, srvPub := genKey(t)
	cliPriv, cliPub := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: cliPub, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	drive := liveTunnel(t, "wipe", name, srvPub, cliPriv)

	m.CollectTraffic(context.Background())
	// read after the baseline scrape, so this can only understate what moved
	banked := peerBytes(t, name, cliPub)
	drive()
	moved := peerBytes(t, name, cliPub) - banked
	if moved <= 0 {
		t.Fatalf("no traffic reached the peer; the rig is broken, not the engine")
	}

	if err := m.Remove(context.Background(), id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := net.InterfaceByName(name); err == nil {
		t.Fatalf("%s survived Remove", name)
	}
	if billed := billedTo(collect(m), "a@x"); billed < moved {
		t.Errorf("at least %d bytes were on the device when its link was deleted and the panel billed %d", moved, billed)
	}
}

// countingPlane counts the pushes that carry peers, so a test can prove a pass
// wrote nothing rather than infer it from the device's end state.
type countingPlane struct {
	Plane
	peerWrites int
}

func (p *countingPlane) Configure(ctx context.Context, name string, cfg wgtypes.Config) error {
	if len(cfg.Peers) > 0 {
		p.peerWrites++
	}
	return p.Plane.Configure(ctx, name, cfg)
}

// The kernel MOVES an allowed-IP two peers claim to the later one, so a pass that
// pushes both is undone by its own last write and rewrites the device forever.
func TestLiveASharedAllowedIPConvergesInsteadOfOscillating(t *testing.T) {
	e2e(t)
	id := 9000 + (os.Getpid() % 500)
	name := InterfaceName(id)
	_ = exec.Command("ip", "link", "del", name).Run()
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })

	plane := &countingPlane{Plane: hostPlane()}
	m := NewManager(plane)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	_, pubB := genKey(t)
	in := inst(id, srv,
		Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}},
		Peer{Email: "b@x", PublicKey: pubB, AllowedIPs: []string{"10.123.0.2/32"}},
	)

	perPass := make([]int, 0, 4)
	for range 4 {
		before := plane.peerWrites
		if err := m.Ensure(context.Background(), in); err == nil {
			t.Fatalf("Ensure = nil, want the later claimant refused by name")
		}
		perPass = append(perPass, plane.peerWrites-before)
	}
	if perPass[1] != 0 || perPass[2] != 0 || perPass[3] != 0 {
		t.Fatalf("peer writes per pass = %v, want no write after the first pass", perPass)
	}
	d := liveDevice(t, name)
	if len(d.Peers) != 1 || d.Peers[0].PublicKey.String() != pubA {
		t.Fatalf("device serves %d peer(s), want the first claimant alone", len(d.Peers))
	}
	if got := d.Peers[0].AllowedIPs[0].String(); got != "10.123.0.2/32" {
		t.Fatalf("the surviving peer holds %q, want 10.123.0.2/32", got)
	}
}
