package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Credentials the core would parse and never check make an inbound an open proxy.

xray's SocksServerConfig.Build maps auth=="password" to AuthType_PASSWORD and
every other value — an absent key included — to AuthType_NO_AUTH, with its own
warning for the unknown case commented out. proxy/socks/protocol.go consults the
accounts map only under AuthType_PASSWORD, so the panel showed credentials it
was not enforcing. F317.
*/
func TestAnInboundWhoseAccountsAreNeverCheckedIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		protocol model.Protocol
		settings string
		refused  bool
	}{
		{
			name:     "accounts with no auth key at all",
			protocol: model.Mixed,
			settings: `{"accounts":[{"user":"a","pass":"b"}],"udp":false}`,
			refused:  true,
		},
		{
			name:     "accounts with auth explicitly noauth",
			protocol: model.Mixed,
			settings: `{"auth":"noauth","accounts":[{"user":"a","pass":"b"}]}`,
			refused:  true,
		},
		{
			name:     "accounts with an auth value xray does not know",
			protocol: model.Mixed,
			settings: `{"auth":"basic","accounts":[{"user":"a","pass":"b"}]}`,
			refused:  true,
		},
		{
			// xray reads `accounts` in preference to `users` and falls back to it,
			// so naming either one carries credentials.
			name:     "legacy users array instead of accounts",
			protocol: model.Mixed,
			settings: `{"users":[{"user":"a","pass":"b"}]}`,
			refused:  true,
		},
		{
			name:     "a stored legacy socks row is the same protocol to xray",
			protocol: model.Protocol("socks"),
			settings: `{"accounts":[{"user":"a","pass":"b"}]}`,
			refused:  true,
		},
		{
			name:     "accounts enforced",
			protocol: model.Mixed,
			settings: `{"auth":"password","accounts":[{"user":"a","pass":"b"}]}`,
			refused:  false,
		},
		{
			// The panel's own MTProto and node bridges are exactly this shape. A
			// guard that widened onto them would break what the panel depends on.
			name:     "a deliberate open proxy with no credentials to mislead anyone",
			protocol: model.Mixed,
			settings: `{"auth":"noauth","udp":false}`,
			refused:  false,
		},
		{
			name:     "no accounts and no auth",
			protocol: model.Mixed,
			settings: `{"udp":false}`,
			refused:  false,
		},
		{
			/*
				HTTP does NOT share this defect, verified against the pinned core:
				HTTPServerConfig has no auth field at all, and proxy/http/server.go
				enforces whenever len(Accounts) > 0. There is no switch that can
				park credentials unchecked, so the guard must not touch it.
			*/
			name:     "http is a different config shape and always enforces",
			protocol: model.HTTP,
			settings: `{"accounts":[{"user":"a","pass":"b"}],"allowTransparent":false}`,
			refused:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := refuseUnenforcedAccounts(&model.Inbound{Protocol: c.protocol, Settings: c.settings})
			if c.refused && err == nil {
				t.Fatal("accepted an inbound whose credentials xray would never check")
			}
			if !c.refused && err != nil {
				t.Fatalf("refused a legitimate inbound: %v", err)
			}
			if c.refused && !strings.Contains(err.Error(), "password") {
				t.Fatalf("the refusal must say how to fix it, got: %v", err)
			}
		})
	}
}

// An unparseable settings blob is not this guard's to judge: other validation
// owns that, and refusing here would turn one bad field into a second message.
func TestUnreadableSettingsAreNotThisGuardsRefusal(t *testing.T) {
	err := refuseUnenforcedAccounts(&model.Inbound{Protocol: model.Mixed, Settings: `{"accounts":`})
	if err != nil {
		t.Fatalf("malformed settings belong to the parser, not to this refusal: %v", err)
	}
}
