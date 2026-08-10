package wireguard_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/wireguard/wgtest"
)

// iface is the device InterfaceName derives for the inbound every test uses.
const iface = "pwg7"

func key(t *testing.T, seed byte) wgtypes.Key {
	t.Helper()
	var k wgtypes.Key
	for i := range k {
		k[i] = seed
	}
	return k
}

func peer(t *testing.T, seed byte, email, allowed string) wireguard.Peer {
	t.Helper()
	return wireguard.Peer{Email: email, PublicKey: key(t, seed).String(), AllowedIPs: []string{allowed}}
}

func instance(t *testing.T, peers ...wireguard.Peer) wireguard.Instance {
	t.Helper()
	return wireguard.Instance{
		ID:         7,
		Tag:        "inbound-7",
		Port:       51820,
		PrivateKey: key(t, 200).String(),
		Address:    []string{"10.0.0.1/24"},
		MTU:        1420,
		Peers:      peers,
	}
}

func served(t *testing.T, k *wgtest.Kernel) []string {
	t.Helper()
	out := make([]string, 0)
	for _, key := range k.PeerKeys(iface) {
		out = append(out, key.String())
	}
	slices.Sort(out)
	return out
}

func routes(t *testing.T, k *wgtest.Kernel) []string {
	t.Helper()
	out := make([]string, 0)
	for _, p := range k.Routes(iface) {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

func totalBytes(deltas []wireguard.Traffic) int64 {
	var sum int64
	for _, d := range deltas {
		sum += d.Up + d.Down
	}
	return sum
}

func TestEnsureWritesTheWholeDevice(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))

	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := k.PrivateKey(iface); got != key(t, 200) {
		t.Fatalf("private key = %s, want %s", got, key(t, 200))
	}
	if got := k.ListenPort(iface); got != 51820 {
		t.Fatalf("listen port = %d, want 51820; it comes from the inbound's port, never from the settings blob", got)
	}
	if got := k.MTU(iface); got != 1420 {
		t.Fatalf("mtu = %d, want 1420", got)
	}
	if got := k.Addrs(iface); !slices.Equal(got, []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}) {
		t.Fatalf("addresses = %v, want [10.0.0.1/24]", got)
	}
	if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
		t.Fatalf("device serves %v, want the one client", got)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))

	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	creates, configures := k.LinkCreates, k.Configures
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if k.LinkCreates != creates {
		t.Fatalf("link creates = %d, was %d; reconciling unchanged state rebuilt the device", k.LinkCreates, creates)
	}
	if k.Configures != configures {
		t.Fatalf("configures = %d, was %d; an unchanged reconcile rewrote the peer set and cost every client a handshake", k.Configures, configures)
	}
}

// TestEnsureRepairsAnOutOfBandChange is what coretest cannot reach: its
// idempotency check passes an Ensure that trusts a cache and pushes nothing.
func TestEnsureRepairsAnOutOfBandChange(t *testing.T) {
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))

	t.Run("a peer flushed behind the panel's back is put back", func(t *testing.T) {
		k := wgtest.New()
		m := wireguard.NewManager(k)
		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("Ensure: %v", err)
		}

		k.FlushPeers(iface)
		if got := served(t, k); len(got) != 0 {
			t.Fatalf("the stand-in still serves %v after a flush", got)
		}

		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("repairing Ensure: %v", err)
		}
		if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
			t.Fatalf("device serves %v, want the client back; Ensure must read the device every call, not trust what it last wrote", got)
		}
		if k.LinkDeletes != 0 {
			t.Fatalf("the repair took the link down %d times; every other client would have been dropped", k.LinkDeletes)
		}
	})

	t.Run("a link recreated by someone else is rebuilt in full", func(t *testing.T) {
		k := wgtest.New()
		m := wireguard.NewManager(k)
		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		before := k.Index(iface)

		k.RecreateLink(iface)
		if k.Index(iface) == before {
			t.Fatal("the stand-in reused the ifindex; a recreate has to be observable")
		}
		if k.PrivateKey(iface) != (wgtypes.Key{}) || len(k.Addrs(iface)) != 0 || len(k.PeerKeys(iface)) != 0 {
			t.Fatal("the stand-in kept state across a recreate; a real ip link del/add keeps none")
		}

		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("repairing Ensure: %v", err)
		}
		if got := k.PrivateKey(iface); got != key(t, 200) {
			t.Fatalf("private key = %s, want it pushed again; a recreated link authenticates nobody", got)
		}
		if got := k.ListenPort(iface); got != 51820 {
			t.Fatalf("listen port = %d, want 51820", got)
		}
		if got := k.Addrs(iface); !slices.Equal(got, []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}) {
			t.Fatalf("addresses = %v, want them installed again", got)
		}
		if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
			t.Fatalf("device serves %v, want the client back", got)
		}
	})
}

// TestEnsureConvergesAroundAnUnusableClient holds one bad client's blast radius to
// itself. This diff is the only revocation a non-Xray core has.
func TestEnsureConvergesAroundAnUnusableClient(t *testing.T) {
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	depleted := peer(t, 2, "b@example.com", "10.0.0.12/32")

	for _, tc := range []struct {
		name    string
		arrival wireguard.Peer
		wantErr string
	}{
		{
			name:    "a key that does not decode",
			arrival: wireguard.Peer{Email: "c@example.com", PublicKey: "not-a-key", AllowedIPs: []string{"10.0.0.13/32"}},
			wantErr: `client "c@example.com" has an unusable public key`,
		},
		{
			name:    "a key another client already holds",
			arrival: wireguard.Peer{Email: "c@example.com", PublicKey: a.PublicKey, AllowedIPs: []string{"10.0.0.13/32"}},
			wantErr: `clients "a@example.com" and "c@example.com" share one public key`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := wgtest.New()
			m := wireguard.NewManager(k)
			if err := m.Ensure(t.Context(), instance(t, a, depleted)); err != nil {
				t.Fatalf("Ensure: %v", err)
			}

			err := m.Ensure(t.Context(), instance(t, a, tc.arrival))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Ensure = %v, want an error containing %q", err, tc.wantErr)
			}
			if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
				t.Fatalf("device serves %v, want only a@example.com; the depleted client kept its peer because one unusable client stopped the whole pass", got)
			}
		})
	}
}

// TestEnsureAfterADeviceAddressChange covers the route the kernel deletes on the
// panel's behalf: deleting it again answers ESRCH and fails a healthy edit.
func TestEnsureAfterADeviceAddressChange(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	// A client outside either pool keeps a route of its own across the change, so
	// the connected route is the only one that moves.
	inst := instance(t, peer(t, 1, "a@example.com", "10.9.0.7/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	moved := inst
	moved.Address = []string{"10.1.0.1/24"}
	if err := m.Ensure(t.Context(), moved); err != nil {
		t.Fatalf("Ensure after an address change = %v; the connected route went out with the old address, so the route diff must not delete it again", err)
	}
	if got := routes(t, k); !slices.Equal(got, []string{"10.1.0.0/24", "10.9.0.7/32"}) {
		t.Fatalf("routes = %v, want the new connected route and the peer route", got)
	}
	if got := k.Addrs(iface); !slices.Equal(got, []netip.Prefix{netip.MustParsePrefix("10.1.0.1/24")}) {
		t.Fatalf("addresses = %v, want only the new one", got)
	}
}

// TestAddPeerFailsWhenTheDeviceIsGone refuses the half-build: a single-user
// instance carries no other peer, no address and no route to rebuild from.
func TestAddPeerFailsWhenTheDeviceIsGone(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	arriving := peer(t, 2, "b@example.com", "10.0.0.12/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := k.DeleteLink(t.Context(), iface); err != nil {
		t.Fatalf("out-of-band delete: %v", err)
	}

	err := m.AddPeer(t.Context(), instance(t, arriving), arriving)
	if !errors.Is(err, wireguard.ErrNoDevice) {
		t.Fatalf("AddPeer against a missing device = %v, want %v; the edit must fail legibly and let the next reconcile rebuild in full", err, wireguard.ErrNoDevice)
	}
	if k.Exists(iface) {
		t.Fatalf("AddPeer rebuilt the device: it serves %v with addresses %v, so every other client on the inbound is offline and nothing said so", served(t, k), k.Addrs(iface))
	}
}

// TestAddPeerRefusesAKeyAnotherClientHolds enforces on the add path what
// desiredPeers enforces on the set. Only this path is taken by a client edit.
func TestAddPeerRefusesAKeyAnotherClientHolds(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	twin := wireguard.Peer{Email: "b@example.com", PublicKey: a.PublicKey, AllowedIPs: []string{"10.0.0.12/32"}}
	err := m.AddPeer(t.Context(), instance(t, twin), twin)
	if err == nil || !strings.Contains(err.Error(), `clients "a@example.com" and "b@example.com" share one public key`) {
		t.Fatalf("AddPeer = %v, want the refusal the set-based path gives; the kernel holds one peer per key, so b@ would be billed a@'s traffic", err)
	}
	allowed := k.AllowedIPs(iface, key(t, 1))
	if len(allowed) != 1 || allowed[0].String() != "10.0.0.11/32" {
		t.Fatalf("allowedIPs = %v, want a@example.com's own; the pasted key took over its peer and put it off the network", allowed)
	}
}

// TestConfigureNeverReplacesThePeerSet pins the one flag that turns a partial
// push into a mass revocation.
func TestConfigureNeverReplacesThePeerSet(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	b := peer(t, 2, "b@example.com", "10.0.0.12/32")

	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := m.AddPeer(t.Context(), instance(t, b), b); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := m.RemovePeer(t.Context(), instance(t), b.Email, ""); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if err := m.Ensure(t.Context(), instance(t, a, b)); err != nil {
		t.Fatalf("converging Ensure: %v", err)
	}

	if len(k.Configs) == 0 {
		t.Fatal("the stand-in recorded no configuration push at all")
	}
	for i, cfg := range k.Configs {
		if cfg.ReplacePeers {
			t.Fatalf("push %d set ReplacePeers; it wipes every peer the diff did not mention, which on a one-client edit is everyone else", i)
		}
	}
}

func TestAddPeerIsAnUpsert(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	t.Run("an unchanged client is not rewritten", func(t *testing.T) {
		configures := k.Configures
		if err := m.AddPeer(t.Context(), instance(t, a), a); err != nil {
			t.Fatalf("AddPeer: %v", err)
		}
		if k.Configures != configures {
			t.Fatalf("configures = %d, was %d; re-adding an identical peer cost the client a fresh handshake", k.Configures, configures)
		}
	})

	t.Run("a re-addressed client replaces its prefix", func(t *testing.T) {
		moved := peer(t, 1, "a@example.com", "10.0.0.99/32")
		if err := m.AddPeer(t.Context(), instance(t, moved), moved); err != nil {
			t.Fatalf("AddPeer: %v", err)
		}
		allowed := k.AllowedIPs(iface, key(t, 1))
		if len(allowed) != 1 || allowed[0].String() != "10.0.0.99/32" {
			t.Fatalf("allowedIPs = %v, want only 10.0.0.99/32; merging leaves the client reachable on an address it no longer owns", allowed)
		}
	})

	t.Run("a new client joins without disturbing the others", func(t *testing.T) {
		b := peer(t, 2, "b@example.com", "10.0.0.12/32")
		if err := m.AddPeer(t.Context(), instance(t, b), b); err != nil {
			t.Fatalf("AddPeer: %v", err)
		}
		want := []string{key(t, 1).String(), key(t, 2).String()}
		slices.Sort(want)
		if got := served(t, k); !slices.Equal(got, want) {
			t.Fatalf("device serves %v, want %v", got, want)
		}
		if k.LinkDeletes != 0 {
			t.Fatalf("adding a client took the link down %d times", k.LinkDeletes)
		}
	})
}

// TestRemovePeerResolvesTheEmailWithoutAUserSet covers the shape the runtime
// actually calls with: a removal is handed an instance carrying no users at all.
func TestRemovePeerResolvesTheEmailWithoutAUserSet(t *testing.T) {
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	b := peer(t, 2, "b@example.com", "10.0.0.12/32")

	t.Run("from the live index", func(t *testing.T) {
		k := wgtest.New()
		m := wireguard.NewManager(k)
		if err := m.Ensure(t.Context(), instance(t, a, b)); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if err := m.RemovePeer(t.Context(), instance(t), a.Email, ""); err != nil {
			t.Fatalf("RemovePeer: %v", err)
		}
		if got := served(t, k); !slices.Equal(got, []string{key(t, 2).String()}) {
			t.Fatalf("device serves %v, want only the client that was kept; a revoked client that keeps its peer keeps spending", got)
		}
	})

	t.Run("from the caller's fallback when the index is cold", func(t *testing.T) {
		k := wgtest.New()
		if err := wireguard.NewManager(k).Ensure(t.Context(), instance(t, a, b)); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		restarted := wireguard.NewManager(k)
		if err := restarted.RemovePeer(t.Context(), instance(t), a.Email, a.PublicKey); err != nil {
			t.Fatalf("RemovePeer: %v", err)
		}
		if got := served(t, k); !slices.Equal(got, []string{key(t, 2).String()}) {
			t.Fatalf("device serves %v; a removal landing before the first reconcile has nothing but the fallback to resolve with", got)
		}
	})

	t.Run("an unknown client is not an error", func(t *testing.T) {
		k := wgtest.New()
		m := wireguard.NewManager(k)
		if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if err := m.RemovePeer(t.Context(), instance(t), "nobody@example.com", ""); err != nil {
			t.Fatalf("RemovePeer for an unknown client = %v, want nil", err)
		}
		if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
			t.Fatalf("device serves %v, want the client left alone", got)
		}
	})
}

func TestCollectTrafficBillsTheClientBehindTheKey(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	k.FeedTraffic(iface, key(t, 1), 1_000, 2_000)
	if first, _ := m.CollectTraffic(t.Context()); totalBytes(first) != 0 {
		t.Fatalf("first collection reported %d bytes; the opening read only establishes baselines", totalBytes(first))
	}

	k.FeedTraffic(iface, key(t, 1), 1_500, 2_500)
	second, online := m.CollectTraffic(t.Context())
	if len(second) != 1 {
		t.Fatalf("collected %+v, want one client's delta", second)
	}
	got := second[0]
	if got.Email != "a@example.com" || got.Tag != "inbound-7" {
		t.Fatalf("delta is attributed to %q on %q, want a@example.com on inbound-7; the kernel counts by public key and client_traffics is keyed by email", got.Email, got.Tag)
	}
	if got.Up != 500 || got.Down != 500 {
		t.Fatalf("delta = up %d down %d, want 500/500", got.Up, got.Down)
	}
	if !slices.Contains(online, "a@example.com") {
		t.Fatalf("online = %v, without the client that just handshook", online)
	}

	t.Run("a link recreated by someone else is billed from zero", func(t *testing.T) {
		k.RecreateLink(iface)
		k.FeedTraffic(iface, key(t, 1), 300, 400)
		after, _ := m.CollectTraffic(t.Context())
		if total := totalBytes(after); total != 700 {
			t.Fatalf("collected %d bytes after a recreate, want 700; the counters restarted at zero, so the whole reading is unbilled traffic", total)
		}
	})

	// The counter has to outlive the link it counts. A fresh one on every rebuild
	// re-enters its baseline pass and silently drops the reading that follows.
	t.Run("a link the panel rebuilt itself keeps its counter", func(t *testing.T) {
		if err := k.DeleteLink(t.Context(), iface); err != nil {
			t.Fatalf("out-of-band delete: %v", err)
		}
		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("rebuilding Ensure: %v", err)
		}
		k.FeedTraffic(iface, key(t, 1), 250, 250)
		after, _ := m.CollectTraffic(t.Context())
		if total := totalBytes(after); total != 500 {
			t.Fatalf("collected %d bytes after the panel rebuilt the link, want 500; replacing the counter makes the epoch flip unobservable and throws the reading away", total)
		}
	})
}

// billedTo sums one client's share of a collection.
func billedTo(deltas []wireguard.Traffic, email string) int64 {
	var sum int64
	for _, d := range deltas {
		if d.Email == email {
			sum += d.Up + d.Down
		}
	}
	return sum
}

// TestRemovePeerBanksItsFinalReading is drain-before-destroy: the kernel zeroes a
// removed peer's counters, so bytes moved since the last scrape are billable only
// if the engine banks them before it issues the removal.
func TestRemovePeerBanksItsFinalReading(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	k.FeedTraffic(iface, key(t, 1), 1_000, 2_000)
	m.CollectTraffic(t.Context())

	k.FeedTraffic(iface, key(t, 1), 6_000, 9_000)
	if err := m.RemovePeer(t.Context(), instance(t), a.Email, ""); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}

	got, _ := m.CollectTraffic(t.Context())
	if total := totalBytes(got); total != 12_000 {
		t.Fatalf("the client moved 12000 bytes and was billed %d; the removal zeroed the kernel counters before anything read them, so a quota revoke or any client edit spends the interval for free", total)
	}
	if got[0].Email != a.Email || got[0].Tag != "inbound-7" {
		t.Fatalf("banked delta is %+v, want it attributed to %s on inbound-7", got[0], a.Email)
	}
	if again, _ := m.CollectTraffic(t.Context()); totalBytes(again) != 0 {
		t.Fatalf("the next collection billed %d bytes again; a banked delta handed out twice doubles every removal", totalBytes(again))
	}
}

// TestBankedBytesSurviveTheDeviceGoingAway: the client whose peer was drained is
// the one most likely to be on an inbound that is being torn down.
func TestBankedBytesSurviveTheDeviceGoingAway(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	k.FeedTraffic(iface, key(t, 1), 1_000, 1_000)
	m.CollectTraffic(t.Context())

	k.FeedTraffic(iface, key(t, 1), 4_000, 4_000)
	if err := m.RemovePeer(t.Context(), instance(t), a.Email, ""); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if err := k.DeleteLink(t.Context(), iface); err != nil {
		t.Fatalf("out-of-band delete: %v", err)
	}

	got, _ := m.CollectTraffic(t.Context())
	if total := totalBytes(got); total != 6_000 {
		t.Fatalf("billed %d bytes, want the 6000 already banked; a scrape that gives up on an unreadable device drops them on the floor", total)
	}
}

// TestEnsureBanksThePeersItRevokes covers the other destroy: quota exhaustion and
// expiry drop the client from the desired set, and the 10s supervise pass removes
// the peer through Ensure rather than through RemovePeer.
func TestEnsureBanksThePeersItRevokes(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	b := peer(t, 2, "b@example.com", "10.0.0.12/32")
	if err := m.Ensure(t.Context(), instance(t, a, b)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	k.FeedTraffic(iface, key(t, 1), 1_000, 1_000)
	k.FeedTraffic(iface, key(t, 2), 1_000, 1_000)
	m.CollectTraffic(t.Context())

	k.FeedTraffic(iface, key(t, 2), 500_000, 1_500_000)
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("revoking Ensure: %v", err)
	}

	got, _ := m.CollectTraffic(t.Context())
	if billed := billedTo(got, b.Email); billed != 1_998_000 {
		t.Fatalf("the depleted client moved 1998000 bytes and was billed %d; the pass that revokes a client is exactly the pass that must bank its last reading", billed)
	}
}

// TestCollectTrafficDropsAnUnownedKey holds the accounting to its stated bias:
// bytes that cannot be attributed are dropped, never billed to whoever is near.
func TestCollectTrafficDropsAnUnownedKey(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	if err := m.Ensure(t.Context(), instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	k.FeedTraffic(iface, key(t, 1), 1_000, 1_000)
	k.FeedTraffic(iface, key(t, 9), 5_000, 5_000)
	m.CollectTraffic(t.Context())

	k.FeedTraffic(iface, key(t, 1), 1_400, 1_600)
	k.FeedTraffic(iface, key(t, 9), 9_000, 9_000)
	got, _ := m.CollectTraffic(t.Context())
	if len(got) != 1 || got[0].Email != "a@example.com" {
		t.Fatalf("collected %+v, want only the known client", got)
	}
	if totalBytes(got) != 1_000 {
		t.Fatalf("collected %d bytes, want 1000; a peer added outside the panel was billed to a client who never sent it", totalBytes(got))
	}
}

// hookPlane fires a callback around one Snapshot. before is the window between a
// scrape reading its epoch and reading the device, after the one before it bills.
type hookPlane struct {
	wireguard.Plane
	mu     sync.Mutex
	before func()
	after  func()
}

func (p *hookPlane) armBefore(hook func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.before = hook
}

func (p *hookPlane) armAfter(hook func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.after = hook
}

// take returns an armed hook and disarms it, so a Snapshot the hook itself makes
// does not re-enter it.
func (p *hookPlane) take(slot *func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	hook := *slot
	*slot = nil
	return hook
}

func (p *hookPlane) Snapshot(ctx context.Context, name string) (wireguard.Snapshot, error) {
	if hook := p.take(&p.before); hook != nil {
		hook()
	}
	snap, err := p.Plane.Snapshot(ctx, name)
	if hook := p.take(&p.after); hook != nil {
		hook()
	}
	return snap, err
}

// settle is how long a hook waits for the operation it raced to finish. Reaching
// it means the scrape holds the lock, which is the whole point of the test.
const settle = 150 * time.Millisecond

// waitFor gives a racing operation time to land and leaves its result for the
// test to read. Timing out is the passing case: the scrape holds the lock.
func waitFor(done chan error) {
	select {
	case err := <-done:
		done <- err
	case <-time.After(settle):
	}
}

// TestCollectTrafficIsAtomicPerDevice covers what -race cannot: every field is
// correctly guarded, and splitting the read from the billing still bills twice.
func TestCollectTrafficIsAtomicPerDevice(t *testing.T) {
	t.Run("a client edit landing mid-scrape is not billed a lifetime", func(t *testing.T) {
		k := wgtest.New()
		hooked := &hookPlane{Plane: k}
		m := wireguard.NewManager(hooked)
		a := peer(t, 1, "a@example.com", "10.0.0.11/32")
		if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
			t.Fatalf("Ensure: %v", err)
		}

		k.FeedTraffic(iface, key(t, 1), 10_000_000_000, 0)
		m.CollectTraffic(t.Context())

		k.FeedTraffic(iface, key(t, 1), 10_000_001_000, 0)
		done := make(chan error, 1)
		hooked.armAfter(func() {
			go func() { done <- m.RemovePeer(context.Background(), instance(t), a.Email, "") }()
			waitFor(done)
		})

		got, _ := m.CollectTraffic(t.Context())
		if err := <-done; err != nil {
			t.Fatalf("RemovePeer: %v", err)
		}
		if total := totalBytes(got); total != 1_000 {
			t.Fatalf("the client moved 1000 bytes and was billed %d; an edit dropped the baseline between the reading and the billing, so Counter saw an unseen key and charged the whole lifetime total", total)
		}
	})

	t.Run("a link the panel rebuilds mid-scrape is billed once", func(t *testing.T) {
		k := wgtest.New()
		hooked := &hookPlane{Plane: k}
		m := wireguard.NewManager(hooked)
		inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
		if err := m.Ensure(t.Context(), inst); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		k.FeedTraffic(iface, key(t, 1), 600, 0)
		m.CollectTraffic(t.Context())

		if err := k.DeleteLink(t.Context(), iface); err != nil {
			t.Fatalf("out-of-band delete: %v", err)
		}
		done := make(chan error, 1)
		hooked.armBefore(func() {
			go func() { done <- m.Ensure(context.Background(), inst) }()
			waitFor(done)
			k.FeedTraffic(iface, key(t, 1), 600, 0)
		})

		duringRebuild, _ := m.CollectTraffic(t.Context())
		if err := <-done; err != nil {
			t.Fatalf("rebuilding Ensure: %v", err)
		}
		k.FeedTraffic(iface, key(t, 1), 600, 0)
		afterRebuild, _ := m.CollectTraffic(t.Context())

		if total := totalBytes(duringRebuild) + totalBytes(afterRebuild); total != 600 {
			t.Fatalf("the client moved 600 bytes since the rebuild and was billed %d; the ifindex and the generation were read in different critical sections, so one recreate became two epochs and the interval between them was billed twice", total)
		}
	})
}

// TestStopAllLeavesEveryDeviceUp is the deliberate difference from the two
// shipped cores: an mtg sidecar dies with the panel, a kernel interface does not.
func TestStopAllLeavesEveryDeviceUp(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	for _, pass := range []string{"first", "second"} {
		if err := m.StopAll(t.Context()); err != nil {
			t.Fatalf("%s StopAll: %v", pass, err)
		}
	}
	if k.LinkDeletes != 0 {
		t.Fatalf("StopAll deleted %d devices; a panel upgrade would drop every WireGuard client", k.LinkDeletes)
	}
	if !k.Exists(iface) || len(k.PeerKeys(iface)) != 1 {
		t.Fatal("StopAll left the device without its peers; the clients reconnect to nothing until the next reconcile")
	}
}

func TestRemoveDeletesTheDevice(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	if err := m.Ensure(t.Context(), instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if k.Exists(iface) {
		t.Fatal("Remove left the device up; a deleted inbound would keep serving")
	}
	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("second Remove = %v, want nil; an inbound can be dropped twice", err)
	}
}

func TestReconcileDropsAnInboundNoLongerDesired(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
	if err := m.Reconcile(t.Context(), []wireguard.Instance{inst}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !k.Exists(iface) {
		t.Fatal("Reconcile did not create the device")
	}
	if err := m.Reconcile(t.Context(), nil); err != nil {
		t.Fatalf("Reconcile with nothing desired: %v", err)
	}
	if k.Exists(iface) {
		t.Fatal("a disabled inbound kept its device, so its clients keep connecting")
	}
}

// TestReconcileDeletesADeviceStrandedByARestart: m.devices is empty in every new
// process, so a device whose inbound left the desired set while the panel was
// down is invisible to the reconcile loop unless the host itself is asked.
func TestReconcileDeletesADeviceStrandedByARestart(t *testing.T) {
	k := wgtest.New()
	before := wireguard.NewManager(k)
	if err := before.Ensure(t.Context(), instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// interfaces the panel did not create: someone else's tunnel, and the name
	// just outside its own scheme, since inbound ids start at 1
	for _, name := range []string{"wg0", "pwg0"} {
		if _, err := k.EnsureLink(t.Context(), wireguard.LinkSpec{Name: name, MTU: 1420}); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}

	restarted := wireguard.NewManager(k)
	if err := restarted.Reconcile(t.Context(), nil); err != nil {
		t.Fatalf("Reconcile after a restart: %v", err)
	}
	if k.Exists(iface) {
		t.Fatal("the device outlived the inbound across a panel restart: it keeps serving revoked clients with valid keys, is never billed, and no UI action removes it")
	}
	for _, name := range []string{"wg0", "pwg0"} {
		if !k.Exists(name) {
			t.Fatalf("the panel deleted %s, an interface it did not create", name)
		}
	}

	t.Run("a desired inbound is adopted, not deleted", func(t *testing.T) {
		inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
		if err := restarted.Reconcile(t.Context(), []wireguard.Instance{inst}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if !k.Exists(iface) || len(k.PeerKeys(iface)) != 1 {
			t.Fatal("an inbound the panel still wants lost its device")
		}
		if err := restarted.Reconcile(t.Context(), []wireguard.Instance{inst}, 7); err != nil {
			t.Fatalf("Reconcile with the id retained: %v", err)
		}
		if !k.Exists(iface) {
			t.Fatal("a retained id was deleted; a device is dropped on an answer, never on a failure to read one")
		}
	})
}

// TestRemovePeerWithoutTheDeviceAddressesKeepsTheRoutes covers the revocation the
// adapter drives when the inbound's settings will not parse: it hands over the id
// and nothing else, and a route diff against no addresses cannot tell the
// kernel's own connected route from a surplus one.
func TestRemovePeerWithoutTheDeviceAddressesKeepsTheRoutes(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	b := peer(t, 2, "b@example.com", "10.0.0.12/32")
	if err := m.Ensure(t.Context(), instance(t, a, b)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	blind := wireguard.Instance{ID: 7, Tag: "inbound-7", Port: 51820}
	if err := m.RemovePeer(t.Context(), blind, a.Email, ""); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if got := routes(t, k); !slices.Equal(got, []string{"10.0.0.0/24"}) {
		t.Fatalf("routes = %v, want [10.0.0.0/24]; the connected route was deleted, so every client left on the device black-holes and no later reconcile puts it back", got)
	}
	if got := served(t, k); !slices.Equal(got, []string{key(t, 2).String()}) {
		t.Fatalf("device serves %v, want the other client alone; the revocation itself must still land", got)
	}
}

// TestClearingTheMTUReturnsTheDeviceToTheKernelDefault: mtu is optional, so an
// absent one means the kernel's own value, not whatever the device last held.
func TestClearingTheMTUReturnsTheDeviceToTheKernelDefault(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.0.0.11/32"))
	inst.MTU = 1280
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := k.MTU(iface); got != 1280 {
		t.Fatalf("mtu = %d, want the 1280 the operator set", got)
	}

	cleared := inst
	cleared.MTU = 0
	if cleared.DeviceFingerprint() == inst.DeviceFingerprint() {
		t.Fatal("clearing the mtu left the device fingerprint unchanged, so PlanChange would not even call this an edit")
	}
	if err := m.Ensure(t.Context(), cleared); err != nil {
		t.Fatalf("Ensure after clearing the mtu: %v", err)
	}
	if got := k.MTU(iface); got != 1420 {
		t.Fatalf("mtu = %d after the operator cleared the field, want the kernel default 1420; PlanChange reported the edit applied and nothing wrote it", got)
	}
}

// refusingPlane fails every push that carries peers, which is what EPERM after a
// lost CAP_NET_ADMIN or a link torn out mid-pass looks like.
type refusingPlane struct {
	wireguard.Plane
	refuse bool
}

func (p *refusingPlane) Configure(ctx context.Context, name string, cfg wgtypes.Config) error {
	if p.refuse && len(cfg.Peers) > 0 {
		return errors.New("configure refused")
	}
	return p.Plane.Configure(ctx, name, cfg)
}

// TestAPeerTheKernelRefusedToDropStaysBillable: the device still holds it and it
// is still moving bytes, so narrowing the index first makes it an unowned key.
func TestAPeerTheKernelRefusedToDropStaysBillable(t *testing.T) {
	k := wgtest.New()
	plane := &refusingPlane{Plane: k}
	m := wireguard.NewManager(plane)
	a := peer(t, 1, "a@example.com", "10.0.0.11/32")
	b := peer(t, 2, "b@example.com", "10.0.0.12/32")
	if err := m.Ensure(t.Context(), instance(t, a, b)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	k.FeedTraffic(iface, key(t, 1), 1_000, 1_000)
	k.FeedTraffic(iface, key(t, 2), 1_000, 1_000)
	m.CollectTraffic(t.Context())

	plane.refuse = true
	if err := m.Ensure(t.Context(), instance(t, a)); err == nil {
		t.Fatal("Ensure = nil though the kernel refused the peer push")
	}
	if got := served(t, k); len(got) != 2 {
		t.Fatalf("device serves %v, want both: the refused push left the peer in place", got)
	}

	k.FeedTraffic(iface, key(t, 2), 9_000, 9_000)
	got, _ := m.CollectTraffic(t.Context())
	if billed := billedTo(got, b.Email); billed != 16_000 {
		t.Fatalf("the peer the kernel would not drop moved 16000 more bytes and was billed %d; it is still serving its client and the panel cannot even see the overage", billed)
	}
}

// TestPeerRoutes covers the gap wgctrl leaves: it configures allowedIPs and
// installs no route, so a client outside the device subnet black-holes.
func TestPeerRoutes(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inPool := peer(t, 1, "a@example.com", "10.0.0.11/32")
	outOfPool := peer(t, 2, "b@example.com", "10.0.5.7/32")

	if err := m.Ensure(t.Context(), instance(t, inPool, outOfPool)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := []string{"10.0.0.0/24", "10.0.5.7/32"}
	if got := routes(t, k); !slices.Equal(got, want) {
		t.Fatalf("routes = %v, want %v; a client the device address does not cover needs one of its own", got, want)
	}

	t.Run("a second reconcile adds nothing", func(t *testing.T) {
		if err := m.Ensure(t.Context(), instance(t, inPool, outOfPool)); err != nil {
			t.Fatalf("second Ensure: %v; the stand-in answers EEXIST, so a route the diff keeps re-adding fails here", err)
		}
	})

	t.Run("removing the client takes its route with it", func(t *testing.T) {
		if err := m.RemovePeer(t.Context(), instance(t), outOfPool.Email, ""); err != nil {
			t.Fatalf("RemovePeer: %v", err)
		}
		if got := routes(t, k); !slices.Equal(got, []string{"10.0.0.0/24"}) {
			t.Fatalf("routes = %v, want the connected route alone", got)
		}
	})

	t.Run("adding one back installs its route", func(t *testing.T) {
		if err := m.AddPeer(t.Context(), instance(t, outOfPool), outOfPool); err != nil {
			t.Fatalf("AddPeer: %v", err)
		}
		if got := routes(t, k); !slices.Equal(got, []string{"10.0.0.0/24", "10.0.5.7/32"}) {
			t.Fatalf("routes = %v, want the peer route back", got)
		}
	})
}
