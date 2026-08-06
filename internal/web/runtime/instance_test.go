package runtime

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The regression for an inbound that restarts Xray without changing.

A core renders from the instance and the full-config generator renders from
GenXrayInboundConfig. If the two heal differently — or if either reformats — one
inbound produces two byte-different configs and every regeneration looks like a
change. Wireguard is the sharp case: healing rewrites clients into peers and
indents the result.
*/
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

// Healing a wireguard inbound replaces its clients with peers, which carry no
// email, so projecting users from the healed settings loses every user on it.
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
			if got := usersOf(tc.settings, ""); got != nil {
				t.Fatalf("usersOf(%s) = %+v, want none", tc.settings, got)
			}
		})
	}

	t.Run("fields and scalar credentials", func(t *testing.T) {
		users := usersOf(`{"clients":[{"email":"a@example.com","enable":true,"totalGB":1024,`+
			`"expiryTime":1700000000000,"id":"beef","limitIp":3,"allowedIPs":["10.0.0.2/32"]}]}`, "")
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
			t.Errorf("id credential = %v, want beef", u.Credentials["id"])
		}
		// A list credential is the reason Credentials is map[string]any: wireguard
		// cannot express allowedIPs as a scalar, and the hot-add path needs it.
		allowed, ok := u.Credentials["allowedIPs"].([]any)
		if !ok {
			t.Fatalf("allowedIPs credential = %#v, want a list", u.Credentials["allowedIPs"])
		}
		if len(allowed) != 1 || allowed[0] != "10.0.0.2/32" {
			t.Errorf("allowedIPs = %v, want [10.0.0.2/32]", allowed)
		}
	})

	// Naming one client projects that client and no other, credentials included:
	// the whole projection is what costs a second per edit on a huge inbound.
	t.Run("only one client", func(t *testing.T) {
		settings := `{"clients":[{"email":"a@example.com","id":"aaa"},{"email":"b@example.com","id":"bbb"}]}`
		users := usersOf(settings, "b@example.com")
		if len(users) != 1 {
			t.Fatalf("got %d users, want only the one named", len(users))
		}
		if users[0].Email != "b@example.com" || users[0].Credentials["id"] != "bbb" {
			t.Errorf("user = %q id=%v, want b@example.com/bbb", users[0].Email, users[0].Credentials["id"])
		}
		if got := usersOf(settings, "ghost@example.com"); got != nil {
			t.Errorf("usersOf named a client the inbound does not carry = %+v, want none", got)
		}
	})
}
