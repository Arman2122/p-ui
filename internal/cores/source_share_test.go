package cores

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/util/sharelink"
)

func TestCoreClientShareUsesExplicitEndpointAndOnlySelectedClient(t *testing.T) {
	for _, kind := range []core.Kind{"vmess", "vless", "trojan", "shadowsocks", "hysteria", "mtproto"} {
		t.Run(string(kind), func(t *testing.T) {
			inst := core.Instance{
				ID: 1, Kind: kind, Tag: "assigned", Listen: "127.0.0.99", Port: 8443,
				Settings:       `{"encryption":"none","decryption":"none","method":"aes-128-gcm","clients":[{"id":"other-client-credential","password":"other-client-password"}],"serverKey":"server-private-material","provider":{"password":"provider-private-material"},"controlToken":"control-private-material"}`,
				StreamSettings: `{"network":"xhttp","security":"tls","xhttpSettings":{"host":"front.example","path":"/assigned","mode":"auto"},"tlsSettings":{"serverName":"verify.example","certificates":[{"key":["certificate-private-material"]}],"settings":{"fingerprint":"chrome"}}}`,
			}
			user := core.User{Email: "selected", Credentials: map[string]any{core.CredUUID: "01234567-89ab-4cde-8123-456789abcdef", core.CredPassword: "selected/a=b", core.CredAuth: "selected/a=b", core.CredSecret: "ee1234567890abcdef1234567890abcd7777772e6578616d706c652e636f6d", "subId": "stable-subscription"}}
			before, _ := json.Marshal(struct {
				I core.Instance
				U core.User
			}{inst, user})
			share, ok, err := ClientShare(inst, user, "2001:db8::7")
			if err != nil || !ok || share.Kind != "link" || share.URI == "" || SharesFile(kind) {
				t.Fatalf("share unavailable: ok=%v kind=%s err=%v", ok, share.Kind, err)
			}
			visible := share.URI
			if kind == "vmess" {
				raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(share.URI, "vmess://"))
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				if err := json.Unmarshal(raw, &obj); err != nil {
					t.Fatal(err)
				}
				if obj["add"] != "2001:db8::7" || obj["port"] != float64(8443) || obj["id"] != user.Credentials[core.CredUUID] || obj["path"] != "/assigned" || obj["sni"] != "verify.example" {
					t.Fatal("VMess lost explicit endpoint, selected credential or transport")
				}
				visible = string(raw)
			} else {
				u, err := url.Parse(share.URI)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "mtproto" {
					if u.Scheme != "tg" || u.Query().Get("server") != "2001:db8::7" || u.Query().Get("port") != "8443" || u.Query().Get("secret") != user.Credentials[core.CredSecret] || u.Fragment != "" {
						t.Fatal("Telegram proxy did not preserve exact endpoint and secret")
					}
				} else {
					if u.Host != "[2001:db8::7]:8443" || u.Query().Get("sni") != "verify.example" {
						t.Fatal("share used another endpoint or TLS name")
					}
					if kind == "vless" && (u.User.Username() != user.Credentials[core.CredUUID] || u.Query().Get("path") != "/assigned") {
						t.Fatal("VLESS lost its selected credential or transport")
					}
					if (kind == "trojan" || kind == "hysteria") && u.User.Username() != "selected/a=b" {
						t.Fatal("escaped credential changed")
					}
					if kind == "shadowsocks" {
						raw, err := base64.RawURLEncoding.DecodeString(u.User.Username())
						if err != nil || string(raw) != "aes-128-gcm:selected/a=b" {
							t.Fatal("Shadowsocks selected credential changed")
						}
					}
				}
			}
			for _, secret := range []string{"127.0.0.99", "other-client-credential", "other-client-password", "server-private-material", "provider-private-material", "control-private-material", "certificate-private-material"} {
				if strings.Contains(visible, secret) {
					t.Fatalf("share exposed unrelated material: %s", secret)
				}
			}
			after, _ := json.Marshal(struct {
				I core.Instance
				U core.User
			}{inst, user})
			if !bytes.Equal(before, after) {
				t.Fatal("render mutated caller input")
			}
		})
	}
}

func TestCoreClientShareHonoursCompilerUUIDAliasAndRefusesConflicts(t *testing.T) {
	inst := core.Instance{Kind: "vless", Port: 443, Settings: `{"encryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`}
	user := core.User{Email: "selected", Credentials: map[string]any{"id": "01234567-89ab-4cde-8123-456789abcdef"}}
	share, ok, err := ClientShare(inst, user, "vpn.example")
	if err != nil || !ok || !strings.HasPrefix(share.URI, "vless://01234567-89ab-4cde-8123-456789abcdef@vpn.example:443") {
		t.Fatalf("compiler UUID alias lost: %v", err)
	}
	user.Credentials[core.CredUUID] = "fedcba98-7654-4321-8123-456789abcdef"
	if _, _, err := ClientShare(inst, user, "vpn.example"); err == nil || err.Error() != "conflicting client share UUIDs" {
		t.Fatalf("ambiguous credential accepted: %v", err)
	}
	delete(user.Credentials, core.CredUUID)
	if _, _, err := ClientShare(inst, user, ""); err == nil || err.Error() != "client share requires an explicit endpoint and identity" {
		t.Fatalf("missing endpoint accepted: %v", err)
	}
	inst.Kind = "http"
	if _, _, err := ClientShare(inst, user, "vpn.example"); !errors.Is(err, sharelink.ErrUnsupported) {
		t.Fatalf("unsupported connection synthesized: %v", err)
	}
}
