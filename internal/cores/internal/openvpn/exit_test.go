package openvpn

import (
	"context"
	"encoding/json"
	"errors"
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

const providerProfile = `client
dev tun
proto udp
remote vpn.example.net 1194
resolv-retry infinite
nobind
persist-key
persist-tun
remote-cert-tls server
auth-user-pass
redirect-gateway def1
dhcp-option DNS 10.8.0.1
route 192.168.50.0 255.255.255.0
verb 3
<ca>
-----BEGIN CERTIFICATE-----
MIIB
-----END CERTIFICATE-----
</ca>
<tls-crypt>
-----BEGIN OpenVPN Static key V1-----
00
-----END OpenVPN Static key V1-----
</tls-crypt>
`

/*
A provider's profile is written for a laptop; the exit runs it on a server that
routes for everybody else. What the panel decides is dropped, the panel's block
comes last, and the pushed routes and DNS never apply: the plane's table is the
only way into the device.
*/
func TestExitProfileIsRenderedUnderPanelControl(t *testing.T) {
	profile, err := parseExitProfile(providerProfile)
	if err != nil {
		t.Fatal(err)
	}
	conf := renderExitConfig(7, profile, "/var/lib/p-ui/openvpn/exit-7/auth")
	for _, want := range []string{
		"\nremote vpn.example.net 1194\n", "\nremote-cert-tls server\n", "<ca>\n-----BEGIN CERTIFICATE-----\nMIIB\n", "</tls-crypt>\n",
		"\ndev pvx7\n", "\ndev-type tun\n", "\nroute-nopull\n", "\nnobind\n", "\ndisable-dco\n",
		"\nauth-user-pass /var/lib/p-ui/openvpn/exit-7/auth\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered config lacks %q:\n%s", want, conf)
		}
	}
	for _, refuse := range []string{"redirect-gateway", "dhcp-option", "route 192.168.50.0", "dev tun\n", "\nauth-user-pass\n"} {
		if strings.Contains(conf, refuse) {
			t.Errorf("rendered config still carries %q, which the panel decides:\n%s", refuse, conf)
		}
	}
	if strings.Count(conf, "\nnobind\n") != 1 || strings.Count(conf, "\npersist-tun\n") != 1 {
		t.Errorf("a directive the profile and the panel both write appears twice:\n%s", conf)
	}
}

func TestExitProfileRefusals(t *testing.T) {
	cases := []struct{ name, profile, want string }{
		{"a server profile", "server 10.8.0.0 255.255.255.0\nremote x 1\n<ca>\na\n</ca>\n", "server profile"},
		{"a script hook", "client\nremote x 1\nup /etc/openvpn/update-resolv-conf\n<ca>\na\n</ca>\n", "runs a command on the panel host"},
		{"a plugin", "client\nremote x 1\nplugin /usr/lib/openvpn/foo.so\n<ca>\na\n</ca>\n", "runs a command on the panel host"},
		{"script-security", "client\nremote x 1\nscript-security 2\n<ca>\na\n</ca>\n", "runs a command on the panel host"},
		{"a file reference", "client\nremote x 1\nca /etc/openvpn/ca.crt\n", "names a file; inline it as <ca>…</ca>"},
		{"an included config", "client\nremote x 1\nconfig /etc/openvpn/more.conf\n", "runs a command on the panel host"},
		{"no remote", "client\n<ca>\na\n</ca>\n", "names no remote"},
		{"no client", "remote x 1\n<ca>\na\n</ca>\n", "not a client profile"},
		{"an unclosed block", "client\nremote x 1\n<ca>\na\n", "never closed"},
		{"nothing", "  \n", "a profile is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseExitProfile(tc.profile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to say %q", err, tc.want)
			}
		})
	}
}

// The login the daemon presents comes from the settings first, then from the
// profile's own inline block; a profile that asks for one and gets none is refused.
func TestExitLoginIsResolvedOnce(t *testing.T) {
	inline := "client\nremote x 1\n<ca>\na\n</ca>\n<auth-user-pass>\nalice\nsecret\n</auth-user-pass>\n"
	settings, profile, err := parseExitSettings(`{"profile":` + jsonString(inline) + `}`)
	if err != nil {
		t.Fatal(err)
	}
	if user, pass, _ := settings.exitLogin(profile); user != "alice" || pass != "secret" {
		t.Fatalf("login = %q/%q; want the inline block's", user, pass)
	}
	if conf := renderExitConfig(1, profile, "/x/auth"); strings.Contains(conf, "<auth-user-pass>") || strings.Contains(conf, "alice") {
		t.Fatalf("the inline login was rendered into the config instead of the credentials file:\n%s", conf)
	}
	settings, profile, err = parseExitSettings(`{"profile":` + jsonString(inline) + `,"username":"bob","password":"pw"}`)
	if err != nil {
		t.Fatal(err)
	}
	if user, _, _ := settings.exitLogin(profile); user != "bob" {
		t.Fatalf("login = %q; want the settings to win over the inline block", user)
	}
	asking := "client\nremote x 1\nauth-user-pass\n<ca>\na\n</ca>\n"
	if _, _, err := parseExitSettings(`{"profile":` + jsonString(asking) + `}`); !errors.Is(err, errProfileNoLogin) {
		t.Fatalf("a profile asking for a login with none given: %v", err)
	}
	if _, _, err := parseExitSettings(`{"profile":` + jsonString(asking) + `,"username":"bob"}`); err == nil {
		t.Fatal("a username without a password was accepted")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// unitLedger is a supervisor that remembers what it was asked to run.
type unitLedger struct {
	mu    sync.Mutex
	units map[string]supervisor.Spec
	// dead names units whose daemon exited on its own, reported the way the
	// exec backend does: failed, with the spec still on the books.
	dead    map[string]bool
	ensures int
}

func newUnitLedger() *unitLedger {
	return &unitLedger{units: map[string]supervisor.Spec{}, dead: map[string]bool{}}
}

func (b *unitLedger) Name() string { return "ledger" }

func (b *unitLedger) Ensure(_ context.Context, spec supervisor.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.units[spec.Name] = spec
	delete(b.dead, spec.Name)
	b.ensures++
	return nil
}

func (b *unitLedger) Status(_ context.Context, name string) (supervisor.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.units[name]; !ok {
		return supervisor.Status{}, supervisor.ErrNotRunning
	}
	if b.dead[name] {
		return supervisor.Status{State: supervisor.StateFailed, Result: "signal: killed"}, nil
	}
	return supervisor.Status{State: supervisor.StateRunning}, nil
}

func (b *unitLedger) Stop(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.units, name)
	delete(b.dead, name)
	return nil
}

// die is the daemon exiting behind the panel's back.
func (b *unitLedger) die(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dead[name] = true
}

func (b *unitLedger) Reload(context.Context, string) error { return nil }

func (b *unitLedger) Logs(context.Context, string, int) ([]string, error) { return nil, nil }

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
An exit is a unit of its own, apart from the inbounds': the inbound sweep reads
the number after the core's prefix and stops what it does not want, so an exit
unit named like an inbound would be killed on the next inbound reconcile. The
exit sweep, in turn, must leave inbound units alone.
*/
func TestExitsAreSupervisedApartFromInbounds(t *testing.T) {
	ledger := newUnitLedger()
	c := New(Deps{Backend: ledger, Binary: "/usr/sbin/openvpn", StateDir: t.TempDir()})
	ctx := context.Background()
	settings := `{"profile":` + jsonString(providerProfile) + `,"username":"alice","password":"secret"}`
	exit := core.Exit{ID: 5, Kind: Kind, Enable: true, Settings: settings}
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatal(err)
	}
	unit := exitUnitName(5)
	if !ledger.has(unit) || ledger.has(supervisor.UnitName(string(ID), 5)) {
		names, _ := ledger.List(ctx)
		t.Fatalf("units = %v; want the exit under its own name and not an inbound's", names)
	}
	spec := ledger.units[unit]
	if !slices.Equal(spec.Exec[:2], []string{"/usr/sbin/openvpn", "--config"}) || !strings.HasSuffix(spec.Exec[2], filepath.Join("exit-5", "client.conf")) {
		t.Fatalf("exec = %v", spec.Exec)
	}
	auth, err := os.ReadFile(filepath.Join(filepath.Dir(spec.Exec[2]), "auth"))
	if err != nil || string(auth) != "alice\nsecret\n" {
		t.Fatalf("auth file = %q, %v", auth, err)
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
	exit.Settings = strings.Replace(settings, `"secret"`, `"changed"`, 1)
	if err := c.ReconcileExits(ctx, []core.Exit{exit}); err != nil {
		t.Fatal(err)
	}
	if ledger.ensures != before+1 {
		t.Fatal("a changed password did not restart the client")
	}

	if err := ledger.Ensure(ctx, supervisor.Spec{Name: exitUnitName(9), Exec: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Ensure(ctx, supervisor.Spec{Name: supervisor.UnitName(string(ID), 3), Exec: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileExits(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if ledger.has(unit) || ledger.has(exitUnitName(9)) {
		t.Fatal("stopping every exit left one running")
	}
	if !ledger.has(supervisor.UnitName(string(ID), 3)) {
		t.Fatal("the exit sweep stopped an inbound's unit")
	}
	if _, err := os.Stat(filepath.Dir(spec.Exec[2])); !os.IsNotExist(err) {
		t.Fatal("the credentials outlived the row")
	}
}

func TestExitHandleIsPureAndDistinctFromTheInbound(t *testing.T) {
	c := New(Deps{})
	handle, err := c.ExitHandle(context.Background(), core.Exit{ID: 4, Kind: Kind})
	if err != nil || handle.Device != "pvx4" || handle.Source != core.SourceOwnerDaemon {
		t.Fatalf("handle = %+v, %v", handle, err)
	}
	if handle.Device == deviceName(4) {
		t.Fatal("an exit and an inbound of one id share a device name")
	}
	if c.ExitHandleKind(Kind) != core.ExitDevice || c.ExitHandleKind("ssh") != core.ExitNone {
		t.Fatal("the handle kind is not a device for this kind alone")
	}
}
