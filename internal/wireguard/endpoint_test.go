package wireguard

import (
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// currentPeerFrom renders a desired peer as the kernel would report it back.
func currentPeerFrom(cfg wgtypes.PeerConfig) wgtypes.Peer {
	return wgtypes.Peer{
		PublicKey:                   cfg.PublicKey,
		PresharedKey:                *cfg.PresharedKey,
		PersistentKeepaliveInterval: *cfg.PersistentKeepaliveInterval,
		AllowedIPs:                  cfg.AllowedIPs,
		Endpoint:                    cfg.Endpoint,
	}
}

/*
A peer with an endpoint is what turns this engine into a dialling side.

Serving a client and dialling an uplink are the same device with the same key
material; the only difference is who knows where the other one is. Without the
endpoint the kernel waits to be contacted, which for an uplink never happens.
*/
func TestPeerEndpointIsCarriedToTheKernel(t *testing.T) {
	const key = "Tylvh+Ubyd7AdN7BEcoTadk7hGnHygdk3BXYwtjXvy0="

	for _, tc := range []struct {
		name     string
		endpoint string
		want     string
	}{
		{"an uplink dials a host and port", "127.0.0.1:51820", "127.0.0.1:51820"},
		{"a client of ours has none, and that is not an error", "", ""},
		{"whitespace is not an endpoint", "   ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := desiredPeer(Peer{Email: "e", PublicKey: key, AllowedIPs: []string{"0.0.0.0/0"}, Endpoint: tc.endpoint})
			if err != nil {
				t.Fatalf("desiredPeer: %v", err)
			}
			got := ""
			if cfg.Endpoint != nil {
				got = cfg.Endpoint.String()
			}
			if got != tc.want {
				t.Fatalf("Endpoint = %q, want %q", got, tc.want)
			}
		})
	}
}

// An unusable endpoint is refused where it is written rather than accepted and
// then silently never dialled.
func TestUnresolvableEndpointIsRefused(t *testing.T) {
	const key = "Tylvh+Ubyd7AdN7BEcoTadk7hGnHygdk3BXYwtjXvy0="

	_, err := desiredPeer(Peer{Email: "e", PublicKey: key, AllowedIPs: []string{"0.0.0.0/0"}, Endpoint: "no-port-here"})
	if err == nil {
		t.Fatal("an endpoint with no port must be refused")
	}
}

/*
A moved endpoint has to read as a change, or the device keeps dialling the
address it had when it was first configured and the uplink never comes back.
*/
func TestPeerEqualNoticesAMovedEndpoint(t *testing.T) {
	const key = "Tylvh+Ubyd7AdN7BEcoTadk7hGnHygdk3BXYwtjXvy0="
	peer := Peer{Email: "e", PublicKey: key, AllowedIPs: []string{"0.0.0.0/0"}, Endpoint: "127.0.0.1:51820"}

	want, err := desiredPeer(peer)
	if err != nil {
		t.Fatalf("desiredPeer: %v", err)
	}
	moved, err := desiredPeer(Peer{Email: "e", PublicKey: key, AllowedIPs: []string{"0.0.0.0/0"}, Endpoint: "127.0.0.1:51821"})
	if err != nil {
		t.Fatalf("desiredPeer: %v", err)
	}

	current := currentPeerFrom(want)
	if !peerEqual(current, want) {
		t.Fatal("a peer that has not moved must compare equal")
	}
	if peerEqual(current, moved) {
		t.Fatal("a peer whose endpoint moved must not compare equal")
	}
}
