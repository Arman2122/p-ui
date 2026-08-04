package runtime

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// TestInstanceCarriesWhatTheConfigGeneratorWouldEmit is the regression for an
// inbound that restarts Xray without changing. A core renders from the instance
// and the full-config generator renders from GenXrayInboundConfig; if the two
// heal differently — or if one of them reformats — the same inbound produces two
// byte-different configs and every regeneration looks like a change. Wireguard
// is the sharp case: healing rewrites clients into peers AND indents the result.
func TestInstanceCarriesWhatTheConfigGeneratorWouldEmit(t *testing.T) {
	for _, tc := range []struct {
		name string
		ib   *model.Inbound
	}{
		{
			name: "wireguard clients become peers",
			ib: &model.Inbound{
				Id: 1, Protocol: model.WireGuard, Port: 51820, Tag: "wg-in", Enable: true,
				Settings: `{"secretKey":"k","peers":[],"clients":[{"email":"alice","enable":true,"publicKey":"cHVi","allowedIPs":["10.0.0.2/32"]}]}`,
			},
		},
		{
			name: "vless is passed through untouched",
			ib: &model.Inbound{
				Id: 2, Protocol: model.VLESS, Port: 443, Tag: "vless-in", Enable: true,
				Settings:       `{"clients":[{"id":"beef","email":"a@example.com"}],"decryption":"none"}`,
				StreamSettings: `{"network":"tcp"}`,
				Sniffing:       `{"enabled":true}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := instanceOf(tc.ib)
			want := tc.ib.GenXrayInboundConfig()
			if inst.Settings != string(want.Settings) {
				t.Errorf("settings differ from the generated config\n got: %s\nwant: %s", inst.Settings, want.Settings)
			}
			if inst.StreamSettings != string(want.StreamSettings) {
				t.Errorf("streamSettings differ from the generated config\n got: %s\nwant: %s", inst.StreamSettings, want.StreamSettings)
			}
			if inst.Sniffing != string(want.Sniffing) {
				t.Errorf("sniffing differs from the generated config\n got: %s\nwant: %s", inst.Sniffing, want.Sniffing)
			}
		})
	}
}

// TestUsersReadTheStoredClientsNotTheHealedOnes pins which blob the projection
// comes from. Healing a wireguard inbound replaces its clients with peers, which
// carry no email, so reading the healed settings loses every user on it.
func TestUsersReadTheStoredClientsNotTheHealedOnes(t *testing.T) {
	ib := &model.Inbound{
		Id: 1, Protocol: model.WireGuard, Port: 51820, Tag: "wg-in", Enable: true,
		Settings: `{"secretKey":"k","clients":[{"email":"alice@example.com","enable":true,"publicKey":"cHVi","allowedIPs":["10.0.0.2/32"]}]}`,
	}
	inst := instanceOf(ib)
	if len(inst.Users) != 1 || inst.Users[0].Email != "alice@example.com" {
		t.Fatalf("users = %+v, want the stored client", inst.Users)
	}
}

func TestUsersOf(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
	}{
		{
			name:     "no clients key",
			settings: `{"decryption":"none"}`,
		},
		{
			name:     "unparseable settings",
			settings: `not json`,
		},
		{
			name:     "a client with no email cannot be acted on",
			settings: `{"clients":[{"id":"beef"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usersOf(tc.settings); got != nil {
				t.Fatalf("usersOf(%s) = %+v, want none", tc.settings, got)
			}
		})
	}

	t.Run("fields and scalar credentials", func(t *testing.T) {
		users := usersOf(`{"clients":[{"email":"a@example.com","enable":true,"totalGB":1024,` +
			`"expiryTime":1700000000000,"id":"beef","limitIp":3,"allowedIPs":["10.0.0.2/32"]}]}`)
		if len(users) != 1 {
			t.Fatalf("got %d users, want 1", len(users))
		}
		u := users[0]
		if u.Email != "a@example.com" || !u.Enable {
			t.Errorf("identity = %q enable=%v", u.Email, u.Enable)
		}
		if u.QuotaBytes != 1024 || u.ExpiryUnixMilli != 1700000000000 {
			t.Errorf("quota = %d, expiry = %d", u.QuotaBytes, u.ExpiryUnixMilli)
		}
		if u.Credentials["id"] != "beef" {
			t.Errorf("id credential = %q, want beef", u.Credentials["id"])
		}
		// A number must not arrive as 3.000000: mtg's secrets and xray's ids are
		// compared as strings, and the formatting is the value.
		if u.Credentials["limitIp"] != "3" {
			t.Errorf("limitIp credential = %q, want 3", u.Credentials["limitIp"])
		}
		if _, present := u.Credentials["allowedIPs"]; present {
			t.Error("a non-scalar credential must be left to the settings blob, which renders it losslessly")
		}
	})
}
