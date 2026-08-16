package wireguard

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

/*
The first LinkRenderer in the tree, held to the exact file it hands out.

The panel frontend builds the same .conf in buildWireguardClientConfig
(frontend/src/pages/clients/wireguardConfig.ts); the two are hand-synced, so a
field added on one side belongs on the other in the same change.
*/
func TestRenderClientProducesTheConf(t *testing.T) {
	const serverSecret = "aP2niiHV0Ao0ZBRDvBrEG4XeAAJmyzWonh9eNe4ZaVw="
	serverPublic, err := wgutil.PublicKeyFromPrivate(serverSecret)
	if err != nil {
		t.Fatalf("derive the server public key: %v", err)
	}

	inst := core.Instance{
		ID: 7, Kind: Kind, Tag: "wg-home", Port: 51820,
		Settings: `{"secretKey":"` + serverSecret + `","dns":"9.9.9.9","mtu":1380}`,
	}
	user := core.User{
		Email: "alice@example.com",
		Credentials: map[string]any{
			core.CredPrivateKey:   "clientPrivateKey=",
			core.CredPreSharedKey: "presharedKey=",
			core.CredAllowedIPs:   []any{"10.8.0.4/32", "fd00::4/128"},
			credKeepAlive:         float64(25),
		},
	}

	share, err := New().RenderClient(inst, user, "vpn.example.com")
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}
	if share.Kind != "file" {
		t.Fatalf("Kind = %q, want file: a WireGuard client is configured by one", share.Kind)
	}
	if share.Filename != "alice-example.c.conf" {
		t.Fatalf("Filename = %q; the email reduced to a wg-quick-safe name", share.Filename)
	}

	want := strings.Join([]string{
		"[Interface]",
		"PrivateKey = clientPrivateKey=",
		"Address = 10.8.0.4/32, fd00::4/128",
		"DNS = 9.9.9.9",
		"MTU = 1380",
		"",
		"# wg-home - alice@example.com",
		"[Peer]",
		"PublicKey = " + serverPublic,
		"PresharedKey = presharedKey=",
		"AllowedIPs = 0.0.0.0/0, ::/0",
		"Endpoint = vpn.example.com:51820",
		"PersistentKeepalive = 25",
		"",
	}, "\n")
	if share.Body != want {
		t.Fatalf("rendered .conf differs:\n--- got ---\n%s\n--- want ---\n%s", share.Body, want)
	}
}

func TestRenderClientRefusesAKeylessClient(t *testing.T) {
	_, err := New().RenderClient(core.Instance{ID: 1, Kind: Kind}, core.User{Email: "a@x"}, "h")
	if err == nil {
		t.Fatal("a client with no private key got a config; the panel cannot know that key")
	}
}

// Optional fields drop out instead of rendering empty: an empty DNS line or a
// PresharedKey with no value breaks parsers that took the field as present.
func TestRenderClientOmitsWhatItDoesNotHave(t *testing.T) {
	share, err := New().RenderClient(
		core.Instance{ID: 1, Kind: Kind, Tag: "t", Port: 51820, Settings: `{}`},
		core.User{Email: "b@x", Credentials: map[string]any{core.CredPrivateKey: "k="}},
		"host",
	)
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}
	for _, absent := range []string{"MTU =", "PresharedKey =", "PersistentKeepalive =", "PublicKey ="} {
		if strings.Contains(share.Body, absent) {
			t.Errorf("%q rendered without a value to carry:\n%s", absent, share.Body)
		}
	}
	if !strings.Contains(share.Body, "DNS = 1.1.1.1, 1.0.0.1") {
		t.Errorf("the DNS default is missing:\n%s", share.Body)
	}
	if !strings.Contains(share.Body, "Address = 10.0.0.2/32") {
		t.Errorf("the address fallback is missing:\n%s", share.Body)
	}
}
