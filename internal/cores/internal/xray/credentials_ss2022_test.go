package xray

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
2022-blake3-chacha20-poly1305 with even one client is not a broken shadowsocks
inbound, it is a dead box: buildShadowsocks2022 refuses the method the moment a
clients array makes it multi-user, and xray-core aborts the whole config. The
panel is client-based end to end, so the pairing has to be refused where a
client is attached — the inbound form and the Clients page reach the same call.
*/
func TestShadowsocksSingleUserMethodRefusesClients(t *testing.T) {
	c := &Core{}
	have := map[string]string{
		core.CredPassword: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
	}

	tests := []struct {
		name     string
		settings string
		wantErr  bool
	}{
		{
			name:     "chacha20 refuses a client",
			settings: `{"method":"2022-blake3-chacha20-poly1305","password":"x"}`,
			wantErr:  true,
		},
		{
			name:     "the aes methods are the multi-user ones",
			settings: `{"method":"2022-blake3-aes-128-gcm","password":"x"}`,
		},
		{
			name:     "a legacy cipher is unaffected",
			settings: `{"method":"chacha20-ietf-poly1305","password":"x"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := c.ValidateClient("shadowsocks", tc.settings, "a@b", have)
			if tc.wantErr {
				if err == nil {
					t.Fatal("a client was accepted onto an inbound xray-core would then refuse to load")
				}
				if !strings.Contains(err.Error(), "single-user") {
					t.Fatalf("error %q does not say why the client cannot be attached", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("a multi-user shadowsocks method must accept clients: %v", err)
			}
		})
	}
}

/*
A Shadowsocks client with no password is not a client.

Every other kind's ValidateClient arm refuses an empty credential — trojan its
password, hysteria its auth, wireguard its key, everything else its uuid. The
shadowsocks arm checked the email and the method's multi-user capability, and
never looked at the password at all.

Nothing upstream covers it either: normalizeShadowsocksClientKeys returns on its
second line for a legacy cipher, which is precisely where an empty password is
possible, and MintClientCredentials runs only on the Clients page — an inbound
posted with its clients inline never reaches it. xray then refuses the user, and
with it the whole config: every inbound of every protocol stops answering at the
next restart.

Emptiness only. A wrong-SIZE 2022 key is repaired before this runs, and refusing
it here would strand a client an older build let in. F328.
*/
func TestShadowsocksClientRequiresAPassword(t *testing.T) {
	c := &Core{}

	for _, method := range []string{"chacha20-ietf-poly1305", "aes-256-gcm", "2022-blake3-aes-128-gcm"} {
		t.Run(method+"/empty", func(t *testing.T) {
			err := c.ValidateClient("shadowsocks", `{"method":"`+method+`","password":"server"}`,
				"alice@e", map[string]string{core.CredPassword: ""})
			if err == nil {
				t.Fatal("a client with no password was accepted; xray refuses that user and aborts the whole config with it")
			}
			if !strings.Contains(err.Error(), "password") {
				t.Fatalf("the refusal does not name the password, so nothing can point at the field: %v", err)
			}
		})
		t.Run(method+"/present", func(t *testing.T) {
			key := "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"
			if err := c.ValidateClient("shadowsocks", `{"method":"`+method+`","password":"server"}`,
				"alice@e", map[string]string{core.CredPassword: key}); err != nil {
				t.Fatalf("a client with a password was refused: %v", err)
			}
		})
	}
}
