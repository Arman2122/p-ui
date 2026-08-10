package wireguard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/wireguard/wgtest"
)

// inbound is the desired state the driver tests start from: one enabled wgkernel
// inbound with a server key and a /24 of its own.
func inbound(users ...core.User) core.Instance {
	return core.Instance{
		ID:       7,
		Kind:     Kind,
		Tag:      "inbound-7",
		Port:     51820,
		Enable:   true,
		Settings: fmt.Sprintf(`{"secretKey":%q,"address":["10.0.0.1/24"],"mtu":1420}`, serverKey),
		Users:    users,
	}
}

// client renders one client the way runtime.usersOf does: everything outside the
// four fields the contract names arrives in Credentials, decoded out of JSON.
func client(email string, key wgtypes.Key, allowed, psk string, keepAlive int) core.User {
	credentials := map[string]any{
		core.CredPublicKey:  key.String(),
		core.CredAllowedIPs: []any{allowed},
	}
	if psk != "" {
		credentials[core.CredPreSharedKey] = psk
	}
	if keepAlive > 0 {
		credentials[credKeepAlive] = float64(keepAlive)
	}
	return core.User{Email: email, Enable: true, Credentials: credentials}
}

// edit mirrors runtime.Local.UpdateUser, which is the path every client change
// takes: a removal by name, then an add of the client as it now stands.
func edit(t *testing.T, c *Core, inst core.Instance, oldEmail string, next core.User) {
	t.Helper()
	dropped := inst
	dropped.Users = nil
	if err := c.RemoveUser(t.Context(), dropped, oldEmail); err != nil {
		t.Fatalf("RemoveUser(%q): %v", oldEmail, err)
	}
	added := inst
	added.Users = []core.User{next}
	if err := c.AddUser(t.Context(), added, next); err != nil {
		t.Fatalf("AddUser(%q): %v", next.Email, err)
	}
}

func peerOf(t *testing.T, k *wgtest.Kernel, key wgtypes.Key) wgtypes.Peer {
	t.Helper()
	for _, peer := range k.Device(iface).Peers {
		if peer.PublicKey == key {
			return peer
		}
	}
	t.Fatalf("the device holds no peer for %s", key)
	return wgtypes.Peer{}
}

func allowedOf(peer wgtypes.Peer) []string {
	out := make([]string, 0, len(peer.AllowedIPs))
	for _, n := range peer.AllowedIPs {
		out = append(out, n.String())
	}
	slices.Sort(out)
	return out
}

// sortedKeys orders keys the way wgtest reports a device's peer set.
func sortedKeys(keys ...wgtypes.Key) []wgtypes.Key {
	slices.SortFunc(keys, func(a, b wgtypes.Key) int { return strings.Compare(a.String(), b.String()) })
	return keys
}

// TestUsersComeFromTheContractNotTheSettingsBlob pins where a client lives: the
// settings JSON still carries a clients array, and reading it revives dead ones.
func TestUsersComeFromTheContractNotTheSettingsBlob(t *testing.T) {
	inst := inbound(client("real@example.com", fill(1), "10.0.0.11/32", "", 0))
	inst.Settings = fmt.Sprintf(
		`{"secretKey":%q,"address":["10.0.0.1/24"],`+
			`"clients":[{"email":"ghost@example.com","publicKey":%q}]}`, serverKey, ghostKey)

	got, serve, err := toEngine(inst)
	if err != nil {
		t.Fatalf("toEngine: %v", err)
	}
	if !serve {
		t.Fatal("an enabled inbound holding a server key must be serveable")
	}
	if len(got.Peers) != 1 || got.Peers[0].Email != "real@example.com" {
		t.Fatalf("peers came out as %+v; they must come from Users alone, or a revoked client is put back on every reconcile", got.Peers)
	}
}

// TestTheListenPortComesFromTheInbound covers the field nothing else would: the
// inbound form writes inbounds.port, and no schema writes a listenPort.
func TestTheListenPortComesFromTheInbound(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	inst := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
	inst.Settings = fmt.Sprintf(`{"secretKey":%q,"address":["10.0.0.1/24"],"listenPort":9999}`, serverKey)

	if err := c.ApplyInstance(t.Context(), inst); err != nil {
		t.Fatalf("ApplyInstance: %v", err)
	}
	if got := k.ListenPort(iface); got != inst.Port {
		t.Fatalf("the device listens on %d, want %d; a listenPort read out of the settings blob makes a port edit in the UI a no-op", got, inst.Port)
	}
}

// TestPlanChangeIsolatesTheRename guards the answer UpdateInbound acts on: a
// non-noop rename has it drop the old instance, which for this core is the device.
func TestPlanChangeIsolatesTheRename(t *testing.T) {
	c := &Core{}
	base := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))

	renamed := base
	renamed.Tag = "inbound-7-renamed"
	added := inbound(base.Users[0], client("b@example.com", fill(2), "10.0.0.12/32", "", 0))
	moved := base
	moved.Port = 51821
	disabled := base
	disabled.Enable = false

	for _, tc := range []struct {
		name string
		next core.Instance
		want core.Action
		why  string
	}{
		{"identical", base, core.ActionNoop, "a save that changed nothing would rewrite the device"},
		{"renamed tag", renamed, core.ActionNoop, "the device is keyed by inbound id, so anything but a noop has UpdateInbound delete it and drop every client on a rename"},
		{"added client", added, core.ActionHotApply, "the new peer would never be pushed"},
		{"moved port", moved, core.ActionHotApply, "the device would keep listening on the old port"},
		{"disabled inbound", disabled, core.ActionRestart, "the device is deleted, which ends every session on it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.PlanChange(base, tc.next); got != tc.want {
				t.Fatalf("PlanChange = %s, want %s: %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestClientEditsNeverTakeTheLinkDown drives the shape runtime.Local.UpdateUser
// has: a removal then an add, for a rename, a quota, an expiry or a re-key alike.
func TestClientEditsNeverTakeTheLinkDown(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	kept := client("b@example.com", fill(2), "10.0.0.12/32", "", 0)
	current := client("a@example.com", fill(1), "10.0.0.11/32", "", 0)
	base := inbound(current, kept)
	psk := fill(77).String()

	if err := c.Reconcile(t.Context(), []core.Instance{base}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	t.Run("an edit costs one peer removal and one peer add", func(t *testing.T) {
		from := len(k.Configs)
		edit(t, c, base, current.Email, current)
		removed, added := 0, 0
		for _, cfg := range k.Configs[from:] {
			for _, p := range cfg.Peers {
				if p.Remove {
					removed++
					continue
				}
				added++
			}
		}
		if removed != 1 || added != 1 {
			t.Fatalf("the device saw %d removals and %d adds, want one of each; more means the edit wrote a client it was never handed", removed, added)
		}
	})

	for _, tc := range []struct {
		name      string
		next      core.User
		allowed   string
		psk       wgtypes.Key
		keepAlive time.Duration
	}{
		{"a re-keyed client", client("a@example.com", fill(3), "10.0.0.11/32", "", 0), "10.0.0.11/32", wgtypes.Key{}, 0},
		{"a moved allowedIPs", client("a@example.com", fill(3), "10.0.5.7/32", "", 0), "10.0.5.7/32", wgtypes.Key{}, 0},
		{"a rotated pre-shared key", client("a@example.com", fill(3), "10.0.5.7/32", psk, 0), "10.0.5.7/32", fill(77), 0},
		{"a changed keepAlive", client("a@example.com", fill(3), "10.0.5.7/32", psk, 25), "10.0.5.7/32", fill(77), 25 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edit(t, c, base, "a@example.com", tc.next)

			key := fill(3)
			if got, want := k.PeerKeys(iface), sortedKeys(key, fill(2)); !slices.Equal(got, want) {
				t.Fatalf("device serves %v, want %v; an edit must reach the client it names and no other", got, want)
			}
			peer := peerOf(t, k, key)
			if got := allowedOf(peer); !slices.Equal(got, []string{tc.allowed}) {
				t.Fatalf("allowedIPs = %v, want [%s]; a merged prefix leaves the client reachable on an address it no longer owns", got, tc.allowed)
			}
			if peer.PresharedKey != tc.psk {
				t.Fatalf("pre-shared key = %s, want %s", peer.PresharedKey, tc.psk)
			}
			if peer.PersistentKeepaliveInterval != tc.keepAlive {
				t.Fatalf("keepalive = %s, want %s", peer.PersistentKeepaliveInterval, tc.keepAlive)
			}
		})
	}

	t.Run("a removed client", func(t *testing.T) {
		dropped := base
		dropped.Users = nil
		if err := c.RemoveUser(t.Context(), dropped, "a@example.com"); err != nil {
			t.Fatalf("RemoveUser: %v", err)
		}
		if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(2)}) {
			t.Fatalf("device serves %v, want only the client that stayed; a revoked peer keeps connecting and keeps spending", got)
		}
	})

	if k.LinkDeletes != 0 {
		t.Fatalf("the edits took the link down %d times; one device carries every client on the inbound, so a single edit would disconnect all of them", k.LinkDeletes)
	}
}

// TestRemoveUserResolvesTheClientWithoutAUserSet covers what the runtime passes:
// local.go hands a removal an instance with Users nil and the client's name only.
func TestRemoveUserResolvesTheClientWithoutAUserSet(t *testing.T) {
	a := client("a@example.com", fill(1), "10.0.0.11/32", "", 0)
	b := client("b@example.com", fill(2), "10.0.0.12/32", "", 0)

	t.Run("from the engine's index", func(t *testing.T) {
		k := wgtest.New()
		c := &Core{mgr: engine.NewManager(k)}
		if err := c.Reconcile(t.Context(), []core.Instance{inbound(a, b)}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if err := c.RemoveUser(t.Context(), inbound(), a.Email); err != nil {
			t.Fatalf("RemoveUser: %v", err)
		}
		if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(2)}) {
			t.Fatalf("device serves %v, want only the client that stayed", got)
		}
	})

	t.Run("from the stored settings when the index is cold", func(t *testing.T) {
		k := wgtest.New()
		if err := (&Core{mgr: engine.NewManager(k)}).Reconcile(t.Context(), []core.Instance{inbound(a, b)}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		stored := inbound()
		stored.Settings = fmt.Sprintf(
			`{"secretKey":%q,"address":["10.0.0.1/24"],"clients":[{"email":%q,"publicKey":%q}]}`,
			serverKey, a.Email, fill(1).String())

		restarted := &Core{mgr: engine.NewManager(k)}
		if err := restarted.RemoveUser(t.Context(), stored, a.Email); err != nil {
			t.Fatalf("RemoveUser: %v", err)
		}
		if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(2)}) {
			t.Fatalf("device serves %v; a removal landing before the first reconcile has nothing but the stored settings to resolve the key from", got)
		}
	})
}

// TestUserOpsTouchOneClient pins the marker this core must NOT declare: it would
// have the runtime reload the row and project every client for each edit.
func TestUserOpsTouchOneClient(t *testing.T) {
	bound := core.Bind(&Core{})
	if bound.Users == nil {
		t.Fatal("wgkernel no longer provisions users at all, so every client edit converges the whole inbound instead of one peer")
	}
	if bound.UserSet != nil {
		t.Error("wgkernel declares core.WholeSetUserProvisioner, but its user ops read one client and write one peer; the marker only buys a projection of every other client, 1.3s of it on a 200k-client inbound")
	}
}

// replacePeersValue returns what a node writes into wgtypes.Config.ReplacePeers.
func replacePeersValue(n ast.Node) (ast.Expr, bool) {
	switch node := n.(type) {
	case *ast.KeyValueExpr:
		if key, ok := node.Key.(*ast.Ident); ok && key.Name == "ReplacePeers" {
			return node.Value, true
		}
	case *ast.AssignStmt:
		for i, target := range node.Lhs {
			field, ok := target.(*ast.SelectorExpr)
			if ok && field.Sel.Name == "ReplacePeers" && i < len(node.Rhs) {
				return node.Rhs[i], true
			}
		}
	}
	return nil, false
}

// TestReplacePeersIsNeverSet is a source guard rather than a behavioural one: the
// flag revokes every peer a push leaves out, and a test only sees what it drives.
func TestReplacePeersIsNeverSet(t *testing.T) {
	fset := token.NewFileSet()
	found := 0
	for _, dir := range []string{".", filepath.Join("..", "..", "..", "wireguard")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				value, ok := replacePeersValue(n)
				if !ok {
					return true
				}
				found++
				if written := types.ExprString(value); written != "false" {
					t.Errorf("%s writes ReplacePeers: %s — it wipes every peer the push left out, which on a one-client edit is everybody else",
						fset.Position(n.Pos()), written)
				}
				return true
			})
		}
	}
	if found == 0 {
		t.Fatal("neither the adapter nor the engine writes ReplacePeers any more; it must stay spelled out, or nothing tells the next author that the zero value is load-bearing")
	}
}

func TestPreflightReportsAnUnusableKernel(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	if err := c.Preflight(t.Context()); err != nil {
		t.Fatalf("Preflight on a usable host = %v, want nil", err)
	}

	k.ProbeErr = engine.ErrNoKernelSupport
	if err := c.Preflight(t.Context()); !errors.Is(err, engine.ErrNoKernelSupport) {
		t.Fatalf("Preflight = %v, want %v; a host with no wireguard module must disable this core rather than fail one inbound at a time", err, engine.ErrNoKernelSupport)
	}
}

// TestAddUserToADisabledInboundIsANoop covers what client_inbound_apply.go does:
// push is decided by the node plan, never by the inbound being enabled.
func TestAddUserToADisabledInboundIsANoop(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	user := client("a@example.com", fill(1), "10.0.0.11/32", "", 0)
	inst := inbound(user)
	inst.Enable = false

	if err := c.AddUser(t.Context(), inst, user); err != nil {
		t.Fatalf("AddUser on a disabled inbound = %v, want nil; the caller reads an error here as a failed edit and flags a restart", err)
	}
	if k.Exists(iface) {
		t.Fatal("adding a client to a disabled inbound brought its device up, so a disabled inbound started carrying traffic")
	}
}

// TestUnreadableSettingsNeverDeleteTheDevice separates "must serve nobody" from
// "cannot be read". Only the first may take a live device and every peer with it.
func TestUnreadableSettingsNeverDeleteTheDevice(t *testing.T) {
	// Shapes a hand-written client or an import script produces: a number as a
	// string, a list as a scalar. Nothing on the Go side validates the blob.
	for _, tc := range []struct {
		name     string
		settings string
	}{
		{"an mtu written as a string", fmt.Sprintf(`{"secretKey":%q,"address":["10.0.0.1/24"],"mtu":"1420"}`, serverKey)},
		{"an address written as a scalar", fmt.Sprintf(`{"secretKey":%q,"address":"10.0.0.1/24"}`, serverKey)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := wgtest.New()
			c := &Core{mgr: engine.NewManager(k)}
			live := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
			if err := c.Reconcile(t.Context(), []core.Instance{live}); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			broken := live
			broken.Settings = tc.settings
			for _, pass := range []struct {
				name string
				run  func() error
			}{
				{"Reconcile", func() error { return c.Reconcile(t.Context(), []core.Instance{broken}) }},
				{"ApplyInstance", func() error { return c.ApplyInstance(t.Context(), broken) }},
			} {
				if err := pass.run(); err == nil {
					t.Fatalf("%s = nil; the panel reports the edit succeeded and nothing names the settings it could not read", pass.name)
				}
				if !k.Exists(iface) {
					t.Fatalf("%s deleted the device: every client on the inbound is disconnected, and their unscraped bytes went with the counter", pass.name)
				}
				if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(1)}) {
					t.Fatalf("after %s the device serves %v, want the client left exactly as it was", pass.name, got)
				}
			}
			if k.LinkDeletes != 0 {
				t.Fatalf("the device was deleted %d times over settings that merely will not parse", k.LinkDeletes)
			}
		})
	}
}

// TestAnInboundWithNoServerKeyIsNotServed keeps a half-configured inbound off the
// host: a device with no private key completes no handshake, and the operator is
// told rather than shown an inbound that is up.
func TestAnInboundWithNoServerKeyIsNotServed(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	inst := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
	inst.Settings = `{"address":["10.0.0.1/24"]}`

	err := c.Reconcile(t.Context(), []core.Instance{inst})
	if err == nil || !strings.Contains(err.Error(), "secretKey") {
		t.Fatalf("Reconcile = %v, want an error naming the missing secretKey", err)
	}
	if k.Exists(iface) {
		t.Fatal("an inbound with no secretKey was given a device; it authenticates nobody, and the panel would show the inbound up")
	}
}

// TestLosingTheServerKeyNeverDeletesTheDevice is the edit arm of the same rule as
// TestUnreadableSettingsNeverDeleteTheDevice: an enabled inbound whose secretKey
// went missing is a broken edit, never an instruction to destroy a live device.
func TestLosingTheServerKeyNeverDeletesTheDevice(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	live := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
	if err := c.Reconcile(t.Context(), []core.Instance{live}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	broken := live
	broken.Settings = `{"address":["10.0.0.1/24"],"clients":[]}`
	for _, pass := range []struct {
		name string
		run  func() error
	}{
		{"ApplyInstance", func() error { return c.ApplyInstance(t.Context(), broken) }},
		{"Reconcile", func() error { return c.Reconcile(t.Context(), []core.Instance{broken}) }},
	} {
		if err := pass.run(); err == nil {
			t.Fatalf("%s = nil; the panel reports the edit succeeded", pass.name)
		}
		if !k.Exists(iface) {
			t.Fatalf("%s deleted the device: every client on the inbound is disconnected over a settings edit", pass.name)
		}
		if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(1)}) {
			t.Fatalf("after %s the device serves %v, want the client left exactly as it was", pass.name, got)
		}
	}
	if k.LinkDeletes != 0 {
		t.Fatalf("the device was deleted %d times over a missing secretKey", k.LinkDeletes)
	}
}

// TestADisabledClientIsNotAuthorised: every production caller gates on Enable, so
// this is the contract holding for a caller that trusts it instead.
func TestADisabledClientIsNotAuthorised(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	live := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
	if err := c.Reconcile(t.Context(), []core.Instance{live}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	blocked := client("b@example.com", fill(2), "10.0.0.12/32", "", 0)
	blocked.Enable = false
	if err := c.AddUser(t.Context(), live, blocked); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if got := k.PeerKeys(iface); !slices.Equal(got, []wgtypes.Key{fill(1)}) {
		t.Fatalf("device serves %v; a disabled client's key was authorised, so a depleted or expired client keeps its tunnel", got)
	}
}

// TestAnInboundsOwnTotalIsBilled: without it inbounds.up/down never move, so an
// inbound-level traffic limit is never reached and the row reports 0 B forever.
func TestAnInboundsOwnTotalIsBilled(t *testing.T) {
	k := wgtest.New()
	c := &Core{mgr: engine.NewManager(k)}
	live := inbound(client("a@example.com", fill(1), "10.0.0.11/32", "", 0))
	if err := c.Reconcile(t.Context(), []core.Instance{live}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	k.FeedTraffic(iface, fill(1), 1_000, 1_000)
	if _, err := c.CollectTraffic(t.Context()); err != nil {
		t.Fatalf("baseline CollectTraffic: %v", err)
	}
	k.FeedTraffic(iface, fill(1), 4_000, 6_000)
	if _, err := c.CollectTraffic(t.Context()); err != nil {
		t.Fatalf("CollectTraffic: %v", err)
	}

	tags, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	if len(tags) != 1 || tags[0].Tag != live.Tag || tags[0].Up != 3_000 || tags[0].Down != 5_000 {
		t.Fatalf("tag deltas = %+v, want inbound %q billed 3000/5000", tags, live.Tag)
	}
	if tags[0].Outbound {
		t.Fatal("the inbound's own bytes were reported as egress")
	}
	if again, _ := c.CollectTagTraffic(t.Context()); len(again) != 0 {
		t.Fatalf("draining twice returned %+v; replaying a delta doubles the inbound's total", again)
	}
}
