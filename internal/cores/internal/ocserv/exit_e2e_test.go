//go:build linux

package ocserv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/coretun"
)

/*
The exit against a real server: an OpenConnect inbound on this host is the
remote, pinned by the certificate the panel minted for it, and the exit dials
it with the panel's own openconnect client. Proven the way the panel uses it:
the plane's manager fills the exit's table from the coretun driver, and a
marked socket reaches an origin in another namespace from the tunnel address
the server assigned, where the same request unmarked arrives from the veth.
*/
func TestExitCarriesMarkedTrafficThroughTheServer(t *testing.T) {
	daemon, ctl, openconnect := e2eGate(t)
	skipWhenAPanelWouldSweepTheUnit(t)
	ctx := context.Background()
	c := newE2ECore(t, daemon, ctl)
	c.deps.Openconnect = openconnect
	t.Cleanup(func() { _ = c.StopAll(ctx) })

	port := freeTCPPort(t)
	inst := core.Instance{ID: 5, Kind: Kind, Tag: "ocs-5", Listen: "127.0.0.1", Port: port, Enable: true, Settings: mintedSettings(t, c, "10.220.0.0/24"), Users: []core.User{e2eUser(1)}}
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile the server: %v", err)
	}
	c.mu.Lock()
	p := c.instances[5].paths
	c.mu.Unlock()
	serverErr := filepath.Join(p.dir, "ocserv.err")
	waitFor(t, 20*time.Second, func() bool { _, err := os.Stat(p.ctl); return err == nil }, func() string {
		return "the server never opened its control socket:\n" + tailOf(serverErr)
	})

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
	origin := coretest.OriginInNamespace(t, "pui-ocs-exit", "vcx", "10.250.3")

	settings, err := parseSettings(inst.Settings)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := spkiPin(settings.ServerCert)
	if err != nil {
		t.Fatal(err)
	}
	alice := inst.Users[0]
	raw, _ := json.Marshal(ExitSettings{
		Server: "127.0.0.1:" + strconv.Itoa(port), ServerCertPin: pin,
		Username: core.CredString(alice.Credentials, core.CredUsername), Password: core.CredString(alice.Credentials, core.CredPassword),
	})
	exit := core.Exit{ID: 9, Kind: Kind, Enable: true, Settings: string(raw)}
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatalf("reconcile the exit: %v", err)
	}
	t.Cleanup(func() { _ = c.ReconcileExits(context.Background(), nil) })
	dev := exitDeviceName(9)
	addr := func() string { return shellOut("ip -4 -o addr show " + dev + " 2>/dev/null") }
	exitErr := func() string { return tailOf(filepath.Join(c.exits[9].dir, "openconnect.err")) }
	waitFor(t, 40*time.Second, func() bool { return strings.Contains(addr(), "10.220.0.") }, func() string {
		return "the exit never came up: " + addr() + "\n--- exit output:\n" + exitErr() + "\n--- server:\n" + tailOf(serverErr)
	})
	if routes := shellOut("ip route show"); strings.Contains(routes, dev) {
		t.Fatalf("the exit client changed the main routing table, which the panel's script exists to prevent:\n%s", routes)
	}

	registry := egress.NewRegistry()
	if err := registry.Register(coretun.New(string(Kind), func(id int) (string, error) { return exitDeviceName(id), nil })); err != nil {
		t.Fatal(err)
	}
	plane := egress.New(egress.HostPlane(), registry)
	if err := plane.Reconcile(ctx, []egress.Egress{{ID: 9, Type: string(Kind), Enable: true}}); err != nil {
		t.Fatalf("plane: %v", err)
	}
	t.Cleanup(func() { _ = plane.Reconcile(context.Background(), nil) })

	marked := origin.SourceSeenWithMark(t, egress.Mark(9))
	if !strings.HasPrefix(marked, "10.220.0.") {
		t.Fatalf("the origin saw the marked request from %q; want the exit's tunnel address\n--- exit output:\n%s", marked, exitErr())
	}
	if plain := origin.SourceSeenWithMark(t, 0); !strings.HasPrefix(plain, "10.250.3.") {
		t.Fatalf("the unmarked request did not arrive from the veth (%q), so the proof above proved nothing", plain)
	}

	if err := c.ReconcileExits(ctx, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return addr() == "" }, func() string { return "the device outlived the exit: " + addr() })
}
