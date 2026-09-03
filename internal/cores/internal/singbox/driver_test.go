package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/Arman2122/p-ui/internal/core"
)

func userN(kind core.Kind, n int) core.User {
	u := core.User{Email: "user" + string(rune('0'+n)) + "@example.com", Enable: true, Credentials: map[string]any{
		core.CredPassword: "secret-password-" + string(rune('0'+n)),
	}}
	if kind == KindTUIC {
		u.Credentials[core.CredUUID] = "c0ffee00-0000-4000-8000-00000000000" + string(rune('0'+n))
	}
	return u
}

/*
sing-box creates a per-user counter only for a name listed under stats.users,
so a user left out of that list moves bytes nobody bills. The rendered config
must enumerate every enabled user there and omit a disabled one everywhere.
*/
func TestRenderConfigEnumeratesEveryBilledUser(t *testing.T) {
	inst := core.Instance{ID: 7, Kind: KindTUIC, Tag: "tuic-7", Port: 8443, Users: []core.User{userN(KindTUIC, 1), userN(KindTUIC, 2)}}
	inst.Users[1].Enable = false
	settings, err := parseSettings(KindTUIC, `{"certFile":"/c.pem","keyFile":"/k.pem","serverName":"x.example"}`)
	if err != nil {
		t.Fatal(err)
	}
	body, err := renderConfig(inst, settings, control{statsPort: 42014, apiPort: 42015, secret: "s"}, "/state/cert.pem", "/state/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Experimental struct {
			V2rayAPI struct {
				Listen string `json:"listen"`
				Stats  struct {
					Users    []string `json:"users"`
					Inbounds []string `json:"inbounds"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
		Inbounds []struct {
			Type              string           `json:"type"`
			CongestionControl string           `json:"congestion_control"`
			Users             []map[string]any `json:"users"`
			TLS               map[string]any   `json:"tls"`
		} `json:"inbounds"`
		Services []map[string]any `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("rendered config does not parse: %v\n%s", err, body)
	}
	if got := doc.Experimental.V2rayAPI.Stats.Users; len(got) != 1 || got[0] != "user1@example.com" {
		t.Fatalf("stats.users = %v, want only the enabled user", got)
	}
	if doc.Experimental.V2rayAPI.Listen != "127.0.0.1:42014" {
		t.Fatalf("stats listen = %q", doc.Experimental.V2rayAPI.Listen)
	}
	ib := doc.Inbounds[0]
	if ib.Type != "tuic" || ib.CongestionControl != "bbr" || len(ib.Users) != 1 || ib.Users[0]["name"] != "user1@example.com" {
		t.Fatalf("inbound = %+v", ib)
	}
	if ib.TLS["certificate_path"] != "/state/cert.pem" || ib.TLS["server_name"] != "x.example" {
		t.Fatalf("tls = %v: the daemon must read the panel's copy of the certificate", ib.TLS)
	}
	if doc.Services[0]["secret"] != "s" || doc.Services[0]["listen_port"] != float64(42015) {
		t.Fatalf("api service = %v", doc.Services[0])
	}
	if strings.Contains(string(body), "secret-password-2") {
		t.Fatal("a disabled user's password is still in the config")
	}
}

// A hand-encoded ConnectionEvents frame: one live connection for a user, one
// closed, one without a user (the daemon's own dialer). Only the first counts.
func TestDecodeConnectionEventsKeepsOnlyLiveUserConnections(t *testing.T) {
	connection := func(user, source string, closedAt int64) []byte {
		var b []byte
		b = protowire.AppendTag(b, 6, protowire.BytesType)
		b = protowire.AppendString(b, source)
		if user != "" {
			b = protowire.AppendTag(b, 10, protowire.BytesType)
			b = protowire.AppendString(b, user)
		}
		b = protowire.AppendTag(b, 12, protowire.VarintType)
		b = protowire.AppendVarint(b, 1700000000000)
		if closedAt != 0 {
			b = protowire.AppendTag(b, 13, protowire.VarintType)
			b = protowire.AppendVarint(b, uint64(closedAt))
		}
		return b
	}
	event := func(conn []byte) []byte {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, 0)
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, "id")
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendBytes(b, conn)
		return b
	}
	var frame []byte
	for _, ev := range [][]byte{
		event(connection("a@example.com", "203.0.113.7:51000", 0)),
		event(connection("b@example.com", "203.0.113.8:51001", 1700000001000)),
		event(connection("", "127.0.0.1:1", 0)),
	} {
		frame = protowire.AppendTag(frame, 1, protowire.BytesType)
		frame = protowire.AppendBytes(frame, ev)
	}
	frame = protowire.AppendTag(frame, 2, protowire.VarintType)
	frame = protowire.AppendVarint(frame, 1)

	got, err := decodeConnectionEvents(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].user != "a@example.com" || got[0].source.String() != "203.0.113.7" {
		t.Fatalf("decoded %+v, want the one live connection of a@example.com from 203.0.113.7", got)
	}
}

func TestPlanChangeReloadsForUsersAndRestartsForListeners(t *testing.T) {
	c := New(Deps{})
	base := core.Instance{ID: 1, Kind: KindAnyTLS, Port: 443, Settings: `{"certFile":"/c","keyFile":"/k"}`, Users: []core.User{userN(KindAnyTLS, 1)}}
	users := base
	users.Users = append(users.Users, userN(KindAnyTLS, 2))
	if got := c.PlanChange(base, users); got != core.ActionHotApply {
		t.Fatalf("user edit planned %s, want hot apply: a reload keeps every connection", got)
	}
	port := base
	port.Port = 8443
	if got := c.PlanChange(base, port); got != core.ActionRestart {
		t.Fatalf("port change planned %s, want restart", got)
	}
	if got := c.PlanChange(base, base); got != core.ActionNoop {
		t.Fatalf("no change planned %s", got)
	}
}

// A daemon that outlived the panel is reached through the ports and secret
// its config names; the file must give back exactly what was rendered into it.
func TestReadControlRecoversWhatRenderConfigWrote(t *testing.T) {
	inst := core.Instance{ID: 3, Kind: KindAnyTLS, Tag: "anytls-3", Port: 8443, Users: []core.User{userN(KindAnyTLS, 1)}}
	settings, _ := parseSettings(KindAnyTLS, `{"certFile":"/c.pem","keyFile":"/k.pem"}`)
	want := control{statsPort: 41777, apiPort: 41778, secret: "0123456789abcdef0123456789abcdef"}
	body, err := renderConfig(inst, settings, want, "/state/cert.pem", "/state/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := readControl(path)
	if !ok || got != want {
		t.Fatalf("readControl = %+v, %t; want %+v", got, ok, want)
	}
	if _, ok := readControl(filepath.Join(t.TempDir(), "missing.json")); ok {
		t.Fatal("a missing file must not yield a control surface")
	}
}
