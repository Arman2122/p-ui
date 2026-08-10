package service

import "testing"

// TestInboundWireguardHints covers the fields the clients page turns into a
// WireGuard .conf; nothing else asserted them, so the gate could widen unseen.
func TestInboundWireguardHints(t *testing.T) {
	const secretKey = "AAIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eH2A="

	cases := []struct {
		name          string
		protocol      string
		settings      string
		wantPublicKey string
		wantMTU       int
		wantDNS       string
	}{
		{
			name:          "wireguard reports every hint",
			protocol:      "wireguard",
			settings:      `{"publicKey":"pub=","mtu":1420,"dns":"1.1.1.1"}`,
			wantPublicKey: "pub=",
			wantMTU:       1420,
			wantDNS:       "1.1.1.1",
		},
		{
			name:          "public key is derived from the secret key",
			protocol:      "wireguard",
			settings:      `{"secretKey":"` + secretKey + `","mtu":1280}`,
			wantPublicKey: "B6N8vBQgk8i3VdwbEOhstCY3StFqqFPtC9/AsrhtHHw=",
			wantMTU:       1280,
		},
		{name: "vless reports nothing", protocol: "vless", settings: `{"publicKey":"pub=","mtu":1420,"dns":"1.1.1.1"}`},
		{name: "mtproto reports nothing", protocol: "mtproto", settings: `{"publicKey":"pub=","mtu":1420}`},
		{name: "empty settings report nothing", protocol: "wireguard", settings: "  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publicKey, mtu, dns := inboundWireguardHints(tc.protocol, tc.settings)
			if publicKey != tc.wantPublicKey {
				t.Errorf("public key = %q, want %q", publicKey, tc.wantPublicKey)
			}
			if mtu != tc.wantMTU {
				t.Errorf("mtu = %d, want %d", mtu, tc.wantMTU)
			}
			if dns != tc.wantDNS {
				t.Errorf("dns = %q, want %q", dns, tc.wantDNS)
			}
		})
	}
}
