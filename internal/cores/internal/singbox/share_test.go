package singbox

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// The same two links are pinned in frontend/src/test/singbox-links.test.ts:
// the panel's copy and the subscription's must never disagree.
func TestRenderClientLinksMatchTheFrontend(t *testing.T) {
	c := New(Deps{})
	user := core.User{Email: "a@example.com", Enable: true, Credentials: map[string]any{
		core.CredUUID: "c0ffee00-0000-4000-8000-000000000001", core.CredPassword: "secret-password-1",
	}}
	for _, tt := range []struct {
		kind     core.Kind
		settings string
		want     string
	}{
		{
			KindTUIC, `{"certFile":"/c","keyFile":"/k","serverName":"x.example","congestionControl":"cubic"}`,
			"tuic://c0ffee00-0000-4000-8000-000000000001:secret-password-1@1.2.3.4:8443?alpn=h3&congestion_control=cubic&sni=x.example&udp_relay_mode=native#tuic-1-a%40example.com",
		},
		{
			KindAnyTLS, `{"certFile":"/c","keyFile":"/k"}`,
			"anytls://secret-password-1@1.2.3.4:8443?sni=1.2.3.4#tuic-1-a%40example.com",
		},
	} {
		inst := core.Instance{ID: 1, Kind: tt.kind, Tag: "tuic-1", Port: 8443, Settings: tt.settings}
		share, err := c.RenderClient(inst, user, "1.2.3.4")
		if err != nil {
			t.Fatalf("%s: %v", tt.kind, err)
		}
		if share.Kind != "link" || share.URI != tt.want {
			t.Errorf("%s: got kind %q uri %q\nwant %q", tt.kind, share.Kind, share.URI, tt.want)
		}
	}
}
