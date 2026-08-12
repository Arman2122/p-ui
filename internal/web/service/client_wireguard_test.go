package service

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

func TestAllocateWireguardAddress(t *testing.T) {
	tests := []struct {
		name string
		used []string
		base string
		want string
		err  bool
	}{
		{name: "empty starts at .2", used: nil, base: "10.0.0.0/24", want: "10.0.0.2/32"},
		{name: "skips used", used: []string{"10.0.0.2/32"}, base: "10.0.0.0/24", want: "10.0.0.3/32"},
		{name: "fills gap", used: []string{"10.0.0.3/32", "10.0.0.4/32"}, base: "10.0.0.0/24", want: "10.0.0.2/32"},
		{name: "ignores catch-all", used: []string{"0.0.0.0/0", "::/0"}, base: "10.0.0.0/24", want: "10.0.0.2/32"},
		{name: "default base when empty", used: nil, base: "", want: "10.0.0.2/32"},
		{name: "full ipv4 scope widens instead of failing", used: []string{"10.9.0.2/32", "10.9.0.3/32"}, base: "10.9.0.0/30", want: "10.9.0.4/32"},
		{name: "exhausted ipv6 scope errors", used: []string{"fd00::2/128", "fd00::3/128"}, base: "fd00::/126", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := allocateWireguardAddress(tt.used, tt.base, nil)
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultWireguardClientsGeneratesKeypair(t *testing.T) {
	clients := []model.Client{{Email: "a@wg"}}
	ifaces := []any{map[string]any{"email": "a@wg"}}
	if err := defaultWireguardClients(nil, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	c := clients[0]
	if c.PrivateKey == "" || c.PublicKey == "" {
		t.Fatalf("keypair not generated: priv=%q pub=%q", c.PrivateKey, c.PublicKey)
	}
	if len(c.AllowedIPs) != 1 || c.AllowedIPs[0] != "10.0.0.2/32" {
		t.Fatalf("allowedIPs not allocated: %v", c.AllowedIPs)
	}
	m := ifaces[0].(map[string]any)
	if m["privateKey"] != c.PrivateKey || m["publicKey"] != c.PublicKey {
		t.Fatalf("interface map not updated: %v", m)
	}
}

func TestDefaultWireguardClientsDerivesPublicKey(t *testing.T) {
	priv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	wantPub, err := wgutil.PublicKeyFromPrivate(priv)
	if err != nil {
		t.Fatal(err)
	}
	clients := []model.Client{{Email: "b@wg", PrivateKey: priv}}
	ifaces := []any{map[string]any{"email": "b@wg"}}
	if err := defaultWireguardClients(nil, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	if clients[0].PublicKey != wantPub {
		t.Fatalf("derived public key = %q, want %q", clients[0].PublicKey, wantPub)
	}
}

func TestDefaultWireguardClientsPreservesProvided(t *testing.T) {
	clients := []model.Client{{
		Email:      "c@wg",
		PrivateKey: "keep-priv",
		PublicKey:  "keep-pub",
		AllowedIPs: []string{"10.0.0.50/32"},
	}}
	ifaces := []any{map[string]any{"email": "c@wg"}}
	if err := defaultWireguardClients(nil, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	if clients[0].PrivateKey != "keep-priv" || clients[0].PublicKey != "keep-pub" {
		t.Fatalf("provided keys were rotated: %+v", clients[0])
	}
	if clients[0].AllowedIPs[0] != "10.0.0.50/32" {
		t.Fatalf("provided allowedIPs changed: %v", clients[0].AllowedIPs)
	}
}

func TestWireguardAllocationBase(t *testing.T) {
	tests := []struct {
		name     string
		used     []string
		fallback string
		want     string
	}{
		{name: "no peers uses fallback", used: nil, fallback: "10.0.0.0/24", want: "10.0.0.0/24"},
		{name: "derives subnet from a peer outside the device prefix", used: []string{"172.16.0.2/32"}, fallback: "10.0.0.0/24", want: "172.16.0.0/24"},
		{name: "skips catch-all and ipv6", used: []string{"0.0.0.0/0", "::/0", "fd00::2/128", "192.168.5.7/32"}, fallback: "10.0.0.0/24", want: "192.168.5.0/24"},
		// The size the operator asked for is the size they get. Deriving a /24 here
		// strands 768 of a /22's own addresses and puts client 255 outside it.
		{name: "a wider device prefix keeps its whole range", used: []string{"10.90.4.2/32"}, fallback: "10.90.4.0/22", want: "10.90.4.0/22"},
		{name: "a device prefix wider than the peers still wins", used: []string{"10.90.7.9/32"}, fallback: "10.90.4.0/22", want: "10.90.4.0/22"},
		{name: "an unparseable device prefix falls back to the peer's /24", used: []string{"10.90.4.2/32"}, fallback: "not-a-prefix", want: "10.90.4.0/24"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wireguardAllocationBase(tt.used, tt.fallback); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultWireguardClientsHonorsExistingSubnet(t *testing.T) {
	existing := []model.Client{{Email: "old@wg", AllowedIPs: []string{"172.16.0.2/32"}}}
	clients := []model.Client{{Email: "new@wg"}}
	ifaces := []any{map[string]any{"email": "new@wg"}}
	if err := defaultWireguardClients(existing, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	if got := clients[0].AllowedIPs[0]; got != "172.16.0.3/32" {
		t.Fatalf("new client address = %q, want 172.16.0.3/32 in existing subnet", got)
	}
}

func TestAllocateWireguardAddressWidensPastFullSlash24(t *testing.T) {
	used := make([]string, 0, 254)
	for i := 2; i <= 255; i++ {
		used = append(used, fmt.Sprintf("10.0.0.%d/32", i))
	}

	got, err := allocateWireguardAddress(used, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("allocate with a full /24: %v", err)
	}
	if got != "10.0.1.0/32" {
		t.Fatalf("address after a full /24 = %q, want 10.0.1.0/32", got)
	}

	used = append(used, got)
	next, err := allocateWireguardAddress(used, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("allocate after widening: %v", err)
	}
	if next != "10.0.1.1/32" {
		t.Fatalf("second widened address = %q, want 10.0.1.1/32", next)
	}
}

func TestAllocateWireguardAddressFillsItsOwnSlash24First(t *testing.T) {
	got, err := allocateWireguardAddress([]string{"172.16.0.2/32"}, "172.16.0.0/24", nil)
	if err != nil {
		t.Fatalf("allocateWireguardAddress: %v", err)
	}
	if got != "172.16.0.3/32" {
		t.Fatalf("address = %q, want 172.16.0.3/32 — the inbound's own /24 comes first", got)
	}
}

func TestDefaultWireguardClientsAllocatesDistinctIPs(t *testing.T) {
	clients := []model.Client{{Email: "x@wg"}, {Email: "y@wg"}}
	ifaces := []any{map[string]any{"email": "x@wg"}, map[string]any{"email": "y@wg"}}
	if err := defaultWireguardClients(nil, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	if clients[0].AllowedIPs[0] == clients[1].AllowedIPs[0] {
		t.Fatalf("two clients got the same address: %v", clients[0].AllowedIPs)
	}
}

func TestNormalizeWireguardAllowedIPs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
		err  bool
	}{
		{name: "cidr passes through", in: []string{"10.0.0.5/32"}, want: []string{"10.0.0.5/32"}},
		{name: "bare ipv4 becomes /32", in: []string{"10.0.0.5"}, want: []string{"10.0.0.5/32"}},
		{name: "bare ipv6 becomes /128", in: []string{"fd00::5"}, want: []string{"fd00::5/128"}},
		{name: "trims and drops empties", in: []string{" 10.0.0.5/32 ", "", "  "}, want: []string{"10.0.0.5/32"}},
		{name: "dedupes", in: []string{"10.0.0.5/32", "10.0.0.5/32"}, want: []string{"10.0.0.5/32"}},
		{name: "routed subnet allowed", in: []string{"10.0.0.5/32", "192.168.1.0/24"}, want: []string{"10.0.0.5/32", "192.168.1.0/24"}},
		{name: "garbage rejected", in: []string{"not-an-ip"}, err: true},
		{name: "bad prefix rejected", in: []string{"10.0.0.5/99"}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeWireguardAllowedIPs(tt.in)
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestDefaultWireguardClientsHonorsAndValidatesSuppliedAllowedIPs(t *testing.T) {
	existing := []model.Client{{Email: "old@wg", AllowedIPs: []string{"10.0.0.2/32"}}}

	clients := []model.Client{{Email: "c@wg", AllowedIPs: []string{"10.0.0.9"}}}
	ifaces := []any{map[string]any{"email": "c@wg"}}
	if err := defaultWireguardClients(existing, clients, ifaces, wireguardPool{base: defaultWireguardBase}); err != nil {
		t.Fatalf("defaultWireguardClients: %v", err)
	}
	if len(clients[0].AllowedIPs) != 1 || clients[0].AllowedIPs[0] != "10.0.0.9/32" {
		t.Fatalf("supplied allowedIPs not normalized: %v", clients[0].AllowedIPs)
	}

	dup := []model.Client{{Email: "d@wg", AllowedIPs: []string{"10.0.0.2/32"}}}
	err := defaultWireguardClients(existing, dup, []any{map[string]any{"email": "d@wg"}}, wireguardPool{base: defaultWireguardBase})
	if err == nil {
		t.Fatal("duplicate allowedIPs across clients must be rejected")
	}

	bad := []model.Client{{Email: "e@wg", AllowedIPs: []string{"not-an-ip"}}}
	if err := defaultWireguardClients(existing, bad, []any{map[string]any{"email": "e@wg"}}, wireguardPool{base: defaultWireguardBase}); err == nil {
		t.Fatal("invalid allowedIPs entry must be rejected")
	}
}

/*
Two inbounds allocating their first client out of one package-global pool hand
two customers the same tunnel address. On a kernel device those addresses are
real routes in the one host routing table, so the second customer's return
traffic is encrypted into the first customer's tunnel.
*/
func TestTheFirstClientComesFromItsOwnInboundsSubnet(t *testing.T) {
	alloc := func(t *testing.T, base string) string {
		t.Helper()
		clients := []model.Client{{Email: "first@wg"}}
		ifaces := []any{map[string]any{"email": "first@wg"}}
		if err := defaultWireguardClients(nil, clients, ifaces, wireguardPool{base: base}); err != nil {
			t.Fatalf("defaultWireguardClients: %v", err)
		}
		return clients[0].AllowedIPs[0]
	}
	a := alloc(t, "10.20.0.0/24")
	b := alloc(t, "10.21.0.0/24")
	if a != "10.20.0.2/32" || b != "10.21.0.2/32" {
		t.Fatalf("first clients got %q and %q, want each inside its own inbound's subnet", a, b)
	}
}

// The widened /16 reaches past this inbound's own /24, so it has to step over
// anything another host device already answers for.
func TestAllocationStepsOverAnotherHostDevicesSubnet(t *testing.T) {
	used := make([]string, 0, 254)
	for i := 2; i <= 255; i++ {
		used = append(used, fmt.Sprintf("10.0.0.%d/32", i))
	}
	blocked := []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24")}

	got, err := allocateWireguardAddress(used, "10.0.0.0/24", blocked)
	if err != nil {
		t.Fatalf("allocate with a full /24: %v", err)
	}
	if got != "10.0.2.0/32" {
		t.Fatalf("address after a full /24 = %q, want 10.0.2.0/32 — 10.0.1.0/24 is another device's", got)
	}
}

// A hand-entered allowedIPs entry is the other way one inbound reaches into
// another's tunnel, and it is checked against every host device's clients.
func TestAHandEnteredAddressCollidesAcrossInbounds(t *testing.T) {
	pool := wireguardPool{base: "10.20.0.0/24", taken: []string{"10.21.0.5/32"}}
	clients := []model.Client{{Email: "greedy@wg", AllowedIPs: []string{"10.21.0.5/32"}}}
	ifaces := []any{map[string]any{"email": "greedy@wg"}}

	err := defaultWireguardClients(nil, clients, ifaces, pool)
	if err == nil || !strings.Contains(err.Error(), "10.21.0.5/32") {
		t.Fatalf("defaultWireguardClients = %v, want the address another inbound's client holds refused by name", err)
	}
}

/*
TestAWiderPrefixIsFilledBeforeItSpills.

The whole point of sizing up front. With the base derived from a client's /24
instead of the device, the 255th client of a /22 lands on 10.90.0.2 — outside the
prefix the operator configured, needing its own kernel route diffed every pass —
while 768 addresses inside that /22 stay free. Measured before the fix.
*/
func TestAWiderPrefixIsFilledBeforeItSpills(t *testing.T) {
	const device = "10.90.4.0/22"
	used := make([]string, 0, 254)
	for i := 2; i < 256; i++ {
		used = append(used, fmt.Sprintf("10.90.4.%d/32", i))
	}

	base := wireguardAllocationBase(used, device)
	got, err := allocateWireguardAddress(used, base, nil)
	if err != nil {
		t.Fatalf("allocate the 255th client: %v", err)
	}
	prefix := netip.MustParsePrefix(device)
	addr := netip.MustParsePrefix(got).Addr()
	if !prefix.Contains(addr) {
		t.Fatalf("the 255th client of a %s got %s, which is outside it; the addresses 10.90.5.2 onward are free and need no route", device, got)
	}
	// .5.0 and not .5.2: inside a /22 that is an ordinary host address, and the
	// scan is linear from .4.2 — the same shape TestAllocateWireguardAddressWidensPastFullSlash24 pins.
	if got != "10.90.5.0/32" {
		t.Fatalf("the 255th client got %s, want the next address in the prefix 10.90.5.0/32", got)
	}
}
