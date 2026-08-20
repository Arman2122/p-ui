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
