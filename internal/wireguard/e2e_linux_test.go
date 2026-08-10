//go:build linux

package wireguard

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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
*/

func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("PUI_WG_E2E") != "1" {
		t.Skip("set PUI_WG_E2E=1 to run against the real kernel (needs root + the wireguard module)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
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
// that reset is observed rather than billed as a delta.
func TestLiveLinkRecreateIsObservedAsAReset(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	m.CollectTraffic(context.Background())

	if out, err := exec.Command("ip", "link", "del", name).CombinedOutput(); err != nil {
		t.Fatalf("link del: %v: %s", err, out)
	}
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure after delete: %v", err)
	}
	if got := len(liveDevice(t, name).Peers); got != 1 {
		t.Fatalf("peers = %d after recreate, want 1", got)
	}

	deltas, _ := m.CollectTraffic(context.Background())
	for _, d := range deltas {
		if d.Up < 0 || d.Down < 0 {
			t.Errorf("negative delta after a counter reset: %+v", d)
		}
	}
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
	if out, err := exec.Command("ip", "link", "set", name, "up").CombinedOutput(); err != nil {
		t.Fatalf("link up: %v: %s", err, out)
	}

	// a client in its own namespace, reached over a veth pair
	ns := "puiwge2e"
	_ = exec.Command("ip", "netns", "del", ns).Run()
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", ns).Run() })
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	sh("ip", "netns", "add", ns)
	sh("ip", "link", "add", "pwge2e-h", "type", "veth", "peer", "name", "pwge2e-c")
	sh("ip", "link", "set", "pwge2e-c", "netns", ns)
	sh("ip", "addr", "add", "192.168.66.1/24", "dev", "pwge2e-h")
	sh("ip", "link", "set", "pwge2e-h", "up")
	sh("ip", "netns", "exec", ns, "ip", "addr", "add", "192.168.66.2/24", "dev", "pwge2e-c")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "pwge2e-c", "up")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "pwge2e-h").Run() })

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

	m.CollectTraffic(context.Background())
	_ = exec.Command("ip", "netns", "exec", ns, "ping", "-c", "20", "-i", "0.05", "-s", "1200", "-W", "2", "10.123.0.1").Run()
	time.Sleep(600 * time.Millisecond)

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

// Removing a peer zeroes its kernel counters, so the engine must bank the final
// reading before it issues the Remove or those bytes are lost.
func TestLiveRemoveDrainsBeforeDeleting(t *testing.T) {
	m, name := liveManager(t)
	srv, _ := genKey(t)
	_, pubA := genKey(t)
	id := 9000 + (os.Getpid() % 500)
	in := inst(id, srv, Peer{Email: "a@x", PublicKey: pubA, AllowedIPs: []string{"10.123.0.2/32"}})
	if err := m.Ensure(context.Background(), in); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	m.CollectTraffic(context.Background())
	if err := m.RemovePeer(context.Background(), in, "a@x", pubA); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if got := len(liveDevice(t, name).Peers); got != 0 {
		t.Fatalf("peers = %d, want 0", got)
	}
	deltas, _ := m.CollectTraffic(context.Background())
	for _, d := range deltas {
		if d.Up < 0 || d.Down < 0 {
			t.Errorf("negative delta after remove: %+v", d)
		}
	}
	_ = fmt.Sprint(deltas)
}
