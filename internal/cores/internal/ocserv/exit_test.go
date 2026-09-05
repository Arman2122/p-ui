package ocserv

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

func TestExitSettingsAreChecked(t *testing.T) {
	cases := []struct{ name, settings, want string }{
		{"no server", `{"username":"u","password":"p"}`, "a server is required"},
		{"no username", `{"server":"vpn.example.net","password":"p"}`, "a username is required"},
		{"no password", `{"server":"vpn.example.net","username":"u"}`, "a password is required"},
		{"a bad port", `{"server":"vpn.example.net:99999","username":"u","password":"p"}`, "out of range"},
		{"a bad pin", `{"server":"vpn.example.net","username":"u","password":"p","serverCertPin":"sha256:abc"}`, "pin-sha256"},
		{"a multi-line password", "{\"server\":\"x\",\"username\":\"u\",\"password\":\"a\\nb\"}", "line break"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseExitSettings(tc.settings)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to say %q", err, tc.want)
			}
		})
	}
	s, err := parseExitSettings(`{"server":" vpn.example.net:8443 ","username":"u","password":"p","serverCertPin":"pin-sha256:AAAA","group":"staff"}`)
	if err != nil || s.Server != "vpn.example.net:8443" {
		t.Fatalf("settings = %+v, %v", s, err)
	}
}

type unitLedger struct {
	mu      sync.Mutex
	units   map[string]supervisor.Spec
	ensures int
}

func newUnitLedger() *unitLedger { return &unitLedger{units: map[string]supervisor.Spec{}} }

func (b *unitLedger) Name() string { return "ledger" }

func (b *unitLedger) Ensure(_ context.Context, spec supervisor.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.units[spec.Name] = spec
	b.ensures++
	return nil
}

func (b *unitLedger) Status(_ context.Context, name string) (supervisor.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.units[name]; ok {
		return supervisor.Status{State: supervisor.StateRunning}, nil
	}
	return supervisor.Status{}, supervisor.ErrNotRunning
}

func (b *unitLedger) Stop(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.units, name)
	return nil
}

func (b *unitLedger) Reload(context.Context, string) error { return nil }

func (b *unitLedger) List(context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(maps.Keys(b.units)), nil
}

func (b *unitLedger) Close() error { return nil }

func (b *unitLedger) has(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.units[name]
	return ok
}

/*
The client is run under panel control: its own unit apart from the inbounds',
the password on stdin from a file rather than on the command line, a script
that addresses the tunnel and touches nothing else, and the pin the operator
gave. Stopping the exit takes the password with it.
*/
func TestExitClientIsRunUnderPanelControl(t *testing.T) {
	ledger := newUnitLedger()
	c := New(Deps{Backend: ledger, Openconnect: "/usr/sbin/openconnect", StateDir: t.TempDir()})
	ctx := context.Background()
	settings := `{"server":"vpn.example.net:8443","username":"alice","password":"s3cret","serverCertPin":"pin-sha256:AAAA","group":"staff"}`
	exit := core.Exit{ID: 4, Kind: Kind, Enable: true, Settings: settings}
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatal(err)
	}
	unit := exitUnitName(4)
	if !ledger.has(unit) || ledger.has(supervisor.UnitName(string(ID), 4)) {
		names, _ := ledger.List(ctx)
		t.Fatalf("units = %v; want the exit under its own name and not an inbound's", names)
	}
	spec := ledger.units[unit]
	if !slices.Equal(spec.Exec[:3], []string{"/bin/sh", "-c", `exec "$@" <"$PUI_PASSWD_FILE"`}) || spec.Exec[4] != "/usr/sbin/openconnect" {
		t.Fatalf("exec = %v", spec.Exec)
	}
	for _, want := range []string{"--protocol=anyconnect", "--interface=pcx4", "--user=alice", "--passwd-on-stdin", "--non-inter", "--servercert=pin-sha256:AAAA", "--authgroup=staff", "--disable-ipv6", "vpn.example.net:8443"} {
		if !slices.Contains(spec.Exec, want) {
			t.Errorf("exec %v lacks %q", spec.Exec, want)
		}
	}
	if strings.Contains(strings.Join(spec.Exec, " "), "s3cret") || strings.Contains(strings.Join(spec.Env, " "), "s3cret") {
		t.Fatal("the password is on the command line or in the environment")
	}
	dir := filepath.Join(c.stateDir(), "exit-4")
	passwd, err := os.ReadFile(filepath.Join(dir, "passwd"))
	if err != nil || string(passwd) != "s3cret\n" {
		t.Fatalf("passwd = %q, %v", passwd, err)
	}
	if !slices.Contains(spec.Env, "PUI_PASSWD_FILE="+filepath.Join(dir, "passwd")) {
		t.Fatalf("env = %v; want the password file named for the shell", spec.Env)
	}
	script, err := os.ReadFile(filepath.Join(dir, "vpnc-script"))
	if err != nil || !strings.Contains(string(script), `"$INTERNAL_IP4_ADDRESS/32"`) || strings.Contains(string(script), "route") || strings.Contains(string(script), "resolv") {
		t.Fatalf("script = %q, %v; want it to address the tunnel and nothing more", script, err)
	}
	if !slices.Contains(spec.Exec, "--script="+filepath.Join(dir, "vpnc-script")) {
		t.Fatalf("exec %v does not run the panel's script", spec.Exec)
	}

	if err := c.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !ledger.has(unit) {
		t.Fatal("the inbound reconcile stopped the exit: its unit name reads as an inbound to the orphan sweep")
	}
	before := ledger.ensures
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatal(err)
	}
	if ledger.ensures != before {
		t.Fatal("an unchanged exit was restarted")
	}
	exit.Settings = strings.Replace(settings, "s3cret", "other", 1)
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatal(err)
	}
	if ledger.ensures != before+1 {
		t.Fatal("a changed password did not restart the client")
	}
	if err := ledger.Ensure(ctx, supervisor.Spec{Name: supervisor.UnitName(string(ID), 2), Exec: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileExits(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if ledger.has(unit) || !ledger.has(supervisor.UnitName(string(ID), 2)) {
		t.Fatal("stopping the exits touched the wrong units")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the password outlived the row")
	}
	handle, _ := c.ExitHandle(ctx, core.Exit{ID: 4, Kind: Kind})
	if handle.Device != "pcx4" || handle.Source != core.SourceOwnerDaemon || c.ExitHandleKind(Kind) != core.ExitDevice {
		t.Fatalf("handle = %+v", handle)
	}
}
