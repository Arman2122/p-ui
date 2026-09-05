//go:build linux

package openvpn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/coretun"
)

/*
The exit against a real provider: an OpenVPN inbound on this host is the
remote, and the exit dials it with the profile the panel itself hands a client.
Proven the way the panel uses it: the plane's own manager fills the exit's
table from the coretun driver, and a socket carrying the exit's mark reaches an
origin in another namespace ARRIVING FROM the tunnel address the provider
assigned, where the same request without the mark arrives from the veth. The
source is what tells the two apart; reachability alone would not, since the
provider and the exit share this host. The origin lives outside the host's
local table on purpose: a local address is answered before any fwmark rule.
*/
func TestExitCarriesMarkedTrafficThroughTheProvider(t *testing.T) {
	bin := e2eGate(t)
	skipWhenAPanelWouldSweepTheUnit(t)
	ctx := context.Background()
	c := newE2ECore(t, bin)
	t.Cleanup(func() { _ = c.StopAll(ctx) })

	port := freeUDPPort(t)
	inst := withClientKeys(t, c, core.Instance{ID: 4, Kind: Kind, Tag: "ovpn-4", Port: port, Enable: true, Settings: mintedSettings(t, c, "10.212.0.0/24", ProtoUDP), Users: []core.User{e2eUser(1)}})
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile the provider: %v", err)
	}
	serverErr := filepath.Join(c.instances[4].paths.dir, "openvpn.err")
	providerUp := func() bool {
		return strings.Contains(shellOut("ip -4 -o addr show "+deviceName(4)+" 2>/dev/null"), "10.212.0.1")
	}
	waitFor(t, 20*time.Second, providerUp, func() string { return "the provider never came up:\n" + tailOf(serverErr) })

	// A steady reconcile must leave the running daemon and its log alone: the
	// sink used to be reopened and truncated on every 10 s pass.
	if err := os.WriteFile(serverErr, []byte("marker: written by the test\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	starts := c.starts
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(serverErr); !strings.Contains(string(body), "marker: written by the test") || c.starts != starts {
		t.Fatalf("a steady reconcile truncated the log or restarted the daemon (starts %d -> %d)", starts, c.starts)
	}

	coretest.AllowLocalSources(t)
	origin := coretest.OriginInNamespace(t, "pui-ovpn-exit", "vex", "10.250.2")

	share, err := c.RenderClient(inst, inst.Users[0], "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(ExitSettings{Profile: share.Body})
	exit := core.Exit{ID: 8, Kind: Kind, Enable: true, Settings: string(settings)}
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatalf("reconcile the exit: %v", err)
	}
	t.Cleanup(func() { _ = c.ReconcileExits(context.Background(), nil) })
	dev := exitDeviceName(8)
	addr := func() string { return shellOut("ip -4 -o addr show " + dev + " 2>/dev/null") }
	waitFor(t, 30*time.Second, func() bool { return strings.Contains(addr(), "10.212.0.") }, func() string {
		return "the exit never came up: " + addr() + "\n--- exit output:\n" + tailOf(filepath.Join(c.exits[8].dir, "openvpn.err")) + "\n--- client.conf:\n" + tailOf(c.exits[8].conf) + "\n--- provider:\n" + tailOf(serverErr)
	})
	// The kernel's connected route for the tun's own address is expected; what
	// route-nopull must prevent is the provider's redirect of the default route.
	if def := shellOut("ip route show default"); !strings.Contains(def, "dev eth0") || strings.Contains(def, dev) {
		t.Fatalf("the exit client changed the default route, which route-nopull was meant to prevent:\n%s", shellOut("ip route show"))
	}
	if split := shellOut("ip route show 0.0.0.0/1; ip route show 128.0.0.0/1"); strings.Contains(split, dev) {
		t.Fatalf("the exit client installed a redirect-gateway split:\n%s", split)
	}

	// What the plane installs for this row, from the driver the panel registers.
	registry := egress.NewRegistry()
	if err := registry.Register(coretun.New(string(Kind), func(id int) (string, error) { return exitDeviceName(id), nil })); err != nil {
		t.Fatal(err)
	}
	plane := egress.New(egress.HostPlane(), registry)
	if err := plane.Reconcile(ctx, []egress.Egress{{ID: 8, Type: string(Kind), Enable: true}}); err != nil {
		t.Fatalf("plane: %v", err)
	}
	t.Cleanup(func() { _ = plane.Reconcile(context.Background(), nil) })

	marked := origin.SourceSeenWithMark(t, egress.Mark(8))
	if !strings.HasPrefix(marked, "10.212.0.") || marked == "10.212.0.1" {
		t.Fatalf("the origin saw the marked request from %q; want the exit's tunnel address, which only the tunnel path gives it\n--- exit output:\n%s", marked, tailOf(filepath.Join(c.exits[8].dir, "openvpn.err")))
	}
	if plain := origin.SourceSeenWithMark(t, 0); !strings.HasPrefix(plain, "10.250.2.") {
		t.Fatalf("the unmarked request did not arrive from the veth (%q), so the proof above proved nothing", plain)
	}

	if err := c.ReconcileExits(ctx, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return addr() == "" }, func() string { return "the device outlived the exit: " + addr() })
}
