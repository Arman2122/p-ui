package sub

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Every protocol the panel can serve must be able to appear in a subscription.

The list was a literal in the SQL that selects a subscriber's inbounds, so
AmneziaWG was filtered out before anything could render it: the inbound existed,
the client was attached, the device was serving them, and the subscription page
was empty with no error anywhere. Reported from the panel, not caught by any
test, because nothing asked whether the literal still matched the registry.
*/
func TestEveryServableProtocolCanAppearInASubscription(t *testing.T) {
	got := subscribableProtocols()
	if len(got) == 0 {
		t.Fatal("no protocols are subscribable at all")
	}
	for _, want := range []string{"vless", "vmess", "trojan", "wgkernel", "awgkernel", "mtproto"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s cannot appear in a subscription: %v", want, got)
		}
	}
}

// The keyed paths -- the wireguard:// link, the Clash block, the JSON outbound --
// all ask this one question, so a kind missing here renders nothing for a client
// that has a perfectly good keypair.
func TestAmneziawgClientsAreRecognisedAsWireguard(t *testing.T) {
	for _, protocol := range []model.Protocol{model.WireGuard, model.WGKernel, model.AWGKernel} {
		if !carriesWireguardClient(protocol) {
			t.Errorf("%s clients carry a WireGuard keypair but are not recognised as doing so", protocol)
		}
	}
	for _, protocol := range []model.Protocol{model.VLESS, model.MTProto} {
		if carriesWireguardClient(protocol) {
			t.Errorf("%s was treated as a WireGuard client", protocol)
		}
	}
}
