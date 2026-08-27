package link

import (
	"testing"
)

/*
A wireguard:// link with no address must not be given one.

`address` on a WireGuard outbound is the tunnel interface's OWN local address.
This parser filled an absent one in with `["0.0.0.0/0", "::/0"]` — which is an
allowedIPs value, and means nothing as a local address. An interface configured
that way does not carry traffic.

Omission is the fix, not emptiness. xray-core picks its own bogon when the key
is ABSENT:

	// infra/conf/wireguard.go:80
	if c.Address == nil {
	    config.Endpoint = []string{"10.0.0.1", "fd59:...:0001"}
	} else {
	    config.Endpoint = c.Address
	}

A JSON `[]` unmarshals to a non-nil empty slice, so emitting one takes the else
branch and leaves the outbound with no endpoint at all — worse than the invented
value it replaces. F080.
*/
func TestAWireguardLinkWithNoAddressOmitsTheKey(t *testing.T) {
	res, err := ParseLink("wireguard://cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA@vpn.example:51820?publickey=cHVia2V5&allowedips=0.0.0.0%2F0#peer")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	settings, _ := res.Outbound["settings"].(map[string]any)
	if settings == nil {
		t.Fatalf("parsed outbound has no settings: %#v", res.Outbound)
	}

	value, present := settings["address"]
	if present {
		t.Fatalf("address = %#v; the key must be omitted so the core supplies its own bogon, and an invented 0.0.0.0/0 is an allowedIPs value that means nothing as a local address", value)
	}
}

// And an address the link DOES carry survives untouched: omission is only for
// the absent case.
func TestAWireguardLinkKeepsTheAddressItCarries(t *testing.T) {
	res, err := ParseLink("wireguard://cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA@vpn.example:51820?publickey=cHVia2V5&address=10.7.0.2%2F32,fd00::2%2F128#peer")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	settings, _ := res.Outbound["settings"].(map[string]any)
	addrs, _ := settings["address"].([]string)
	if len(addrs) != 2 || addrs[0] != "10.7.0.2/32" || addrs[1] != "fd00::2/128" {
		t.Fatalf("address = %#v, want the two the link carried", settings["address"])
	}
}

/*
allowedIPs keeps its fallback, and that is not an oversight.

"Route everything through the tunnel" is what an absent allowedIPs means in
every WireGuard config on earth, and unlike `address` the core has no default of
its own to fall back to. The two keys look alike and are opposites.
*/
func TestAWireguardLinkStillDefaultsAllowedIPs(t *testing.T) {
	res, err := ParseLink("wireguard://cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA@vpn.example:51820?publickey=cHVia2V5#peer")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	settings, _ := res.Outbound["settings"].(map[string]any)
	peers, _ := settings["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("want one peer, got %#v", settings["peers"])
	}
	peer, _ := peers[0].(map[string]any)
	allowed, _ := peer["allowedIPs"].([]string)
	if len(allowed) != 2 || allowed[0] != "0.0.0.0/0" {
		t.Fatalf("allowedIPs = %#v, want the route-everything default", peer["allowedIPs"])
	}
}
