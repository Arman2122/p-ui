package wireguard

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// key builds a valid, stable WireGuard key from one byte, so a test names a peer
// by an integer instead of by 44 characters of base64.
func key(t *testing.T, seed byte) wgtypes.Key {
	t.Helper()
	var k wgtypes.Key
	for i := range k {
		k[i] = seed
	}
	return k
}

func prefixes(t *testing.T, values ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			t.Fatalf("bad test prefix %q: %v", v, err)
		}
		out = append(out, p)
	}
	return out
}

func strings0(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

func TestParseAllowedIPs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		want    []string
		wantErr string
	}{
		{name: "host prefix", in: []string{"10.0.0.7/32"}, want: []string{"10.0.0.7/32"}},
		{name: "bare address becomes a host prefix", in: []string{"10.0.0.7"}, want: []string{"10.0.0.7/32"}},
		{name: "prefix is masked as wg stores it", in: []string{"10.0.0.7/24"}, want: []string{"10.0.0.0/24"}},
		{name: "ipv6", in: []string{"fd00::1"}, want: []string{"fd00::1/128"}},
		{name: "blank entries are skipped", in: []string{"", "  ", "10.0.0.7/32"}, want: []string{"10.0.0.7/32"}},
		{name: "garbage names itself", in: []string{"not-an-ip"}, wantErr: `invalid allowedIPs entry "not-an-ip"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAllowedIPs(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseAllowedIPs(%v) error = %v, want one containing %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAllowedIPs(%v) = %v", tc.in, err)
			}
			if !slices.Equal(strings0(got), tc.want) {
				t.Fatalf("parseAllowedIPs(%v) = %v, want %v", tc.in, strings0(got), tc.want)
			}
		})
	}
}

func TestDesiredPeer(t *testing.T) {
	pub := key(t, 1)
	psk := key(t, 2)

	t.Run("every optional field is written", func(t *testing.T) {
		got, err := desiredPeer(Peer{Email: "a@example.com", PublicKey: pub.String(), PreSharedKey: psk.String(), AllowedIPs: []string{"10.0.0.7/32"}, KeepAlive: 25})
		if err != nil {
			t.Fatalf("desiredPeer: %v", err)
		}
		if got.PublicKey != pub {
			t.Fatalf("public key = %s, want %s", got.PublicKey, pub)
		}
		if got.PresharedKey == nil || *got.PresharedKey != psk {
			t.Fatalf("preshared key = %v, want %s; a nil pointer leaves whatever the device held", got.PresharedKey, psk)
		}
		if got.PersistentKeepaliveInterval == nil || *got.PersistentKeepaliveInterval != 25*time.Second {
			t.Fatalf("keepalive = %v, want 25s", got.PersistentKeepaliveInterval)
		}
		if !got.ReplaceAllowedIPs {
			t.Fatal("ReplaceAllowedIPs is false; a re-addressed client would keep its old prefix alongside the new one")
		}
	})

	t.Run("a client with no key is refused", func(t *testing.T) {
		_, err := desiredPeer(Peer{Email: "a@example.com"})
		if err == nil || !strings.Contains(err.Error(), `client "a@example.com" has no public key`) {
			t.Fatalf("error = %v, want one naming the keyless client", err)
		}
	})

	t.Run("a shared public key costs the later claimant only", func(t *testing.T) {
		got, err := desiredPeers([]Peer{
			{Email: "a@example.com", PublicKey: pub.String(), AllowedIPs: []string{"10.0.0.7/32"}},
			{Email: "b@example.com", PublicKey: pub.String(), AllowedIPs: []string{"10.0.0.8/32"}},
		})
		if err == nil || !strings.Contains(err.Error(), `"a@example.com" and "b@example.com" share one public key`) {
			t.Fatalf("error = %v; the kernel holds one peer per key, so one client would be billed the other's traffic", err)
		}
		if len(got) != 1 || got[0].PublicKey != pub {
			t.Fatalf("peers = %+v, want the first claimant kept; dropping both revokes a client that did nothing wrong", got)
		}
	})

	t.Run("an unusable client is named and the rest are still served", func(t *testing.T) {
		got, err := desiredPeers([]Peer{
			{Email: "a@example.com", PublicKey: pub.String(), AllowedIPs: []string{"10.0.0.7/32"}},
			{Email: "b@example.com", PublicKey: "not-a-key", AllowedIPs: []string{"10.0.0.8/32"}},
		})
		if err == nil || !strings.Contains(err.Error(), `client "b@example.com" has an unusable public key`) {
			t.Fatalf("error = %v, want one naming the client that cannot be served", err)
		}
		if len(got) != 1 || got[0].PublicKey != pub {
			t.Fatalf("peers = %+v, want the usable client kept; returning nothing stops every revocation on the inbound", got)
		}
	})
}

func TestDiffPeers(t *testing.T) {
	a, b := key(t, 1), key(t, 2)
	want := func(t *testing.T, k wgtypes.Key, ips ...string) wgtypes.PeerConfig {
		t.Helper()
		cfg, err := desiredPeer(Peer{Email: "x@example.com", PublicKey: k.String(), AllowedIPs: ips})
		if err != nil {
			t.Fatalf("desiredPeer: %v", err)
		}
		return cfg
	}
	live := func(t *testing.T, cfg wgtypes.PeerConfig) wgtypes.Peer {
		t.Helper()
		return wgtypes.Peer{
			PublicKey:                   cfg.PublicKey,
			PresharedKey:                *cfg.PresharedKey,
			PersistentKeepaliveInterval: *cfg.PersistentKeepaliveInterval,
			AllowedIPs:                  cfg.AllowedIPs,
		}
	}

	peerA := want(t, a, "10.0.0.1/32")
	peerB := want(t, b, "10.0.0.2/32")

	t.Run("an already correct device diffs to nothing", func(t *testing.T) {
		if got := diffPeers([]wgtypes.Peer{live(t, peerA)}, []wgtypes.PeerConfig{peerA}, false); len(got) != 0 {
			t.Fatalf("diffPeers = %d writes, want 0; every reconcile would rewrite a peer that never changed", len(got))
		}
	})

	t.Run("a re-addressed client is upserted", func(t *testing.T) {
		moved := want(t, a, "10.0.9.1/32")
		got := diffPeers([]wgtypes.Peer{live(t, peerA)}, []wgtypes.PeerConfig{moved}, false)
		if len(got) != 1 || got[0].PublicKey != a || got[0].Remove {
			t.Fatalf("diffPeers = %+v, want one upsert of %s", got, a)
		}
	})

	t.Run("a peer the panel no longer serves is removed", func(t *testing.T) {
		got := diffPeers([]wgtypes.Peer{live(t, peerA), live(t, peerB)}, []wgtypes.PeerConfig{peerA}, false)
		if len(got) != 1 || got[0].PublicKey != b || !got[0].Remove {
			t.Fatalf("diffPeers = %+v, want one removal of %s; a revoked client keeps connecting otherwise", got, b)
		}
	})

	t.Run("a created link re-pushes every peer", func(t *testing.T) {
		got := diffPeers([]wgtypes.Peer{live(t, peerA)}, []wgtypes.PeerConfig{peerA}, true)
		if len(got) != 1 || got[0].PublicKey != a || got[0].Remove {
			t.Fatalf("diffPeers(full) = %+v, want %s pushed again", got, a)
		}
	})

	t.Run("upsertPeer never reads another peer", func(t *testing.T) {
		if _, changed := upsertPeer([]wgtypes.Peer{live(t, peerA)}, peerA); changed {
			t.Fatal("upsertPeer reported a change for an identical peer; a client rename would cost it a handshake")
		}
		if _, changed := upsertPeer([]wgtypes.Peer{live(t, peerA)}, peerB); !changed {
			t.Fatal("upsertPeer reported no change for a peer the device does not hold")
		}
	})
}

func TestDiffDevice(t *testing.T) {
	k := key(t, 3)
	cur := wgtypes.Device{PrivateKey: k, ListenPort: 51820, FirewallMark: 0}

	t.Run("nothing to write when the device already agrees", func(t *testing.T) {
		if got := diffDevice(cur, k, 51820, 0, false); !got.empty() {
			t.Fatalf("diffDevice = %+v, want empty", got)
		}
	})
	t.Run("a moved port is written", func(t *testing.T) {
		got := diffDevice(cur, k, 8443, 0, false)
		if got.ListenPort == nil || *got.ListenPort != 8443 {
			t.Fatalf("diffDevice port = %v, want 8443", got.ListenPort)
		}
		if got.PrivateKey != nil {
			t.Fatal("diffDevice rewrote the private key for a port change")
		}
	})
	t.Run("a created link re-pushes every scalar", func(t *testing.T) {
		got := diffDevice(cur, k, 51820, 0, true)
		if got.PrivateKey == nil || got.ListenPort == nil || got.FirewallMark == nil {
			t.Fatalf("diffDevice(full) = %+v; a link just created holds none of them", got)
		}
	})
}

func TestDiffAddrs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		current    []string
		desired    []string
		wantAdd    []string
		wantDelete []string
	}{
		{name: "empty device", current: nil, desired: []string{"10.0.0.1/24"}, wantAdd: []string{"10.0.0.1/24"}},
		{name: "already correct", current: []string{"10.0.0.1/24"}, desired: []string{"10.0.0.1/24"}},
		{
			name: "re-addressed", current: []string{"10.0.0.1/24"}, desired: []string{"10.1.0.1/24"},
			wantAdd: []string{"10.1.0.1/24"}, wantDelete: []string{"10.0.0.1/24"},
		},
		{
			name:    "the kernel's own link-local is left alone",
			current: []string{"10.0.0.1/24", "fe80::1/64"}, desired: []string{"10.0.0.1/24"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, del := diffAddrs(prefixes(t, tc.current...), prefixes(t, tc.desired...))
			if !slices.Equal(strings0(add), sortedOrEmpty(tc.wantAdd)) {
				t.Fatalf("add = %v, want %v", strings0(add), tc.wantAdd)
			}
			if !slices.Equal(strings0(del), sortedOrEmpty(tc.wantDelete)) {
				t.Fatalf("delete = %v, want %v", strings0(del), tc.wantDelete)
			}
		})
	}
}

func sortedOrEmpty(values []string) []string {
	out := slices.Clone(values)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return out
}

func TestDesiredRoutes(t *testing.T) {
	cfgFor := func(t *testing.T, seed byte, ips ...string) wgtypes.PeerConfig {
		t.Helper()
		cfg, err := desiredPeer(Peer{Email: "x@example.com", PublicKey: key(t, seed).String(), AllowedIPs: ips})
		if err != nil {
			t.Fatalf("desiredPeer: %v", err)
		}
		return cfg
	}

	t.Run("a client inside the device subnet needs no route", func(t *testing.T) {
		got := desiredRoutes(prefixes(t, "10.0.0.1/24"), []wgtypes.PeerConfig{cfgFor(t, 1, "10.0.0.9/32")})
		if len(got) != 0 {
			t.Fatalf("desiredRoutes = %v, want none; the kernel already routes it from the device address", strings0(got))
		}
	})

	t.Run("a client outside it gets its own route", func(t *testing.T) {
		got := desiredRoutes(prefixes(t, "10.0.0.1/24"), []wgtypes.PeerConfig{cfgFor(t, 1, "10.0.5.7/32")})
		if !slices.Equal(strings0(got), []string{"10.0.5.7/32"}) {
			t.Fatalf("desiredRoutes = %v, want [10.0.5.7/32]; without it the peer black-holes in the return direction", strings0(got))
		}
	})

	t.Run("a default prefix is never routed over the tunnel", func(t *testing.T) {
		got := desiredRoutes(prefixes(t, "10.0.0.1/24"), []wgtypes.PeerConfig{cfgFor(t, 1, "0.0.0.0/0")})
		if len(got) != 0 {
			t.Fatalf("desiredRoutes = %v; a default route over the device takes the host's own traffic with it", strings0(got))
		}
	})

	t.Run("two clients sharing a prefix produce one route", func(t *testing.T) {
		got := desiredRoutes(prefixes(t, "10.0.0.1/24"), []wgtypes.PeerConfig{
			cfgFor(t, 1, "10.0.5.0/24"), cfgFor(t, 2, "10.0.5.0/24"),
		})
		if len(got) != 1 {
			t.Fatalf("desiredRoutes = %v, want one entry; the second add would fail with EEXIST", strings0(got))
		}
	})
}

func TestDiffRoutes(t *testing.T) {
	addrs := prefixes(t, "10.0.0.1/24")

	t.Run("the kernel's connected route is never deleted", func(t *testing.T) {
		_, del := diffRoutes(prefixes(t, "10.0.0.0/24"), nil, addrs)
		if len(del) != 0 {
			t.Fatalf("delete = %v; dropping the connected route breaks every client in the pool", strings0(del))
		}
	})

	t.Run("a stale peer route is dropped", func(t *testing.T) {
		add, del := diffRoutes(prefixes(t, "10.0.0.0/24", "10.0.5.7/32"), nil, addrs)
		if len(add) != 0 {
			t.Fatalf("add = %v, want none", strings0(add))
		}
		if !slices.Equal(strings0(del), []string{"10.0.5.7/32"}) {
			t.Fatalf("delete = %v, want [10.0.5.7/32]", strings0(del))
		}
	})

	t.Run("the kernel's own link-local route is left alone", func(t *testing.T) {
		add, del := diffRoutes(prefixes(t, "10.0.0.0/24", "fe80::/64"), nil, addrs)
		if len(add) != 0 || len(del) != 0 {
			t.Fatalf("add = %v, delete = %v; the panel never installed fe80::/64, and diffAddrs already refuses to touch the address it belongs to", strings0(add), strings0(del))
		}
	})

	t.Run("an already routed prefix is not added twice", func(t *testing.T) {
		add, del := diffRoutes(prefixes(t, "10.0.0.0/24", "10.0.5.7/32"), prefixes(t, "10.0.5.7/32"), addrs)
		if len(add) != 0 || len(del) != 0 {
			t.Fatalf("add = %v, delete = %v, want neither", strings0(add), strings0(del))
		}
	})
}

func TestApplySettingsReadsTheDeviceHalfOnly(t *testing.T) {
	var inst Instance
	settings := `{"secretKey":"` + key(t, 4).String() + `","address":["10.0.0.1/24"],"mtu":1420,"fwmark":51,` +
		`"clients":[{"email":"ghost@example.com","publicKey":"` + key(t, 5).String() + `"}]}`
	if err := inst.ApplySettings(settings); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	if inst.PrivateKey != key(t, 4).String() {
		t.Fatalf("private key = %q", inst.PrivateKey)
	}
	if !slices.Equal(inst.Address, []string{"10.0.0.1/24"}) || inst.MTU != 1420 || inst.FWMark != 51 {
		t.Fatalf("device half = %+v", inst)
	}
	if len(inst.Peers) != 0 {
		t.Fatalf("ApplySettings read %d peers out of the settings blob; peers come from the user set, or a removed client returns on the next reconcile", len(inst.Peers))
	}
}

func TestPeerKeyFromSettingsIsLookupOnly(t *testing.T) {
	settings := `{"clients":[{"email":"a@example.com","publicKey":"` + key(t, 6).String() + `"}]}`
	if got := PeerKeyFromSettings(settings, "a@example.com"); got != key(t, 6).String() {
		t.Fatalf("PeerKeyFromSettings = %q, want the stored key", got)
	}
	if got := PeerKeyFromSettings(settings, "nobody@example.com"); got != "" {
		t.Fatalf("PeerKeyFromSettings for an unknown client = %q, want empty", got)
	}
	if got := PeerKeyFromSettings("not json", "a@example.com"); got != "" {
		t.Fatalf("PeerKeyFromSettings on unparseable settings = %q, want empty", got)
	}
}

func TestFingerprintsIgnoreOrder(t *testing.T) {
	a := Peer{Email: "a@example.com", PublicKey: key(t, 1).String(), AllowedIPs: []string{"10.0.0.1/32", "10.0.0.2/32"}}
	b := Peer{Email: "b@example.com", PublicKey: key(t, 2).String(), AllowedIPs: []string{"10.0.0.3/32"}}
	one := Instance{Port: 51820, Address: []string{"10.0.0.1/24"}, Peers: []Peer{a, b}}
	two := Instance{Port: 51820, Address: []string{"10.0.0.1/24"}, Peers: []Peer{b, a}}
	two.Peers[1].AllowedIPs = []string{"10.0.0.2/32", "10.0.0.1/32"}

	if one.PeersFingerprint() != two.PeersFingerprint() {
		t.Fatal("a reordered clients array reads as a change; every save would rewrite the peer set")
	}
	moved := one
	moved.Port = 8443
	if one.DeviceFingerprint() == moved.DeviceFingerprint() {
		t.Fatal("a port edit does not move the device fingerprint, so it would never be applied")
	}
}
