package service

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The safety net for unifying the panel's two inbound renderers.

GetXrayConfig does not merely call GenXrayInboundConfig: it rebuilds
settings.clients from the clients table, attaches fallbacks, and rewrites
streamSettings — about 175 lines that runtime.Local's per-inbound path does not
run. Folding the two together is a refactor with no observable behaviour of its
own, so it needs a fixture that fails on any change to the bytes.

Bytes, not shape: InboundConfig.Equals compares raw JSON, so a reformat that
parses identically still restarts Xray under live connections. The fixture is
therefore a text dump of the raw sections, never a re-marshalled struct.
*/

var updateGolden = flag.Bool("update-golden", false, "rewrite the xray inbound golden fixture")

const goldenPath = "testdata/xray_inbounds.golden"

// seedGoldenInbound creates one inbound and attaches clients to it. It takes the
// settings verbatim so a fixture case can carry legacy shapes the panel heals.
func seedGoldenInbound(t *testing.T, in *model.Inbound, clients []model.Client) *model.Inbound {
	t.Helper()
	if err := database.GetDB().Create(in).Error; err != nil {
		t.Fatalf("create inbound %q: %v", in.Tag, err)
	}
	if len(clients) > 0 {
		if err := (&ClientService{}).SyncInbound(nil, in.Id, clients); err != nil {
			t.Fatalf("sync clients for %q: %v", in.Tag, err)
		}
	}
	return in
}

/*
seedGoldenFixture covers every branch of the per-inbound rendering block:
each protocol's client projection, the two ways a client is dropped, the
wireguard peer rewrite, fallbacks, and each streamSettings rewrite.

Ports are fixed and distinct so the dump is stable; ids are serial from a fresh
schema. Deliberately no mtproto inbound — it is skipped from InboundConfigs and
its egress bridge picks a port by scanning, which would not be reproducible.
*/
func seedGoldenFixture(t *testing.T) {
	t.Helper()

	vless := seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-vless", Enable: true, Port: 43301, Protocol: model.VLESS,
		Settings: `{"clients":[{"email":"stale@x","id":"stale"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"reality",` +
			`"realitySettings":{"dest":"example.com:443","serverNames":["example.com"],"settings":{"fingerprint":"chrome"}},` +
			`"externalProxy":[{"forceTls":"same","dest":"a.example.com","port":443}],` +
			`"finalmask":{"tcp":[{"type":"rand","rand":""}]}}`,
		Sniffing: `{"enabled":true,"destOverride":["http","tls"]}`,
	}, []model.Client{
		{Email: "vless-plain@x", ID: "11111111-1111-1111-1111-111111111111", Enable: true},
		{Email: "vless-flow@x", ID: "22222222-2222-2222-2222-222222222222", Enable: true, Flow: "xtls-rprx-vision-udp443"},
		{Email: "vless-off@x", ID: "33333333-3333-3333-3333-333333333333", Enable: false},
		{Email: "vless-depleted@x", ID: "44444444-4444-4444-4444-444444444444", Enable: true},
	})
	/*
		The two independent ways a client leaves the config. Neither can be
		seeded through the client itself: GORM's `default:true` turns an
		Enable:false insert back into true, and SyncInbound writes no
		client_traffics row at all — that is the quota job's output, seeded here
		directly. Both silently un-cover their branch if done the obvious way.
	*/
	disableClients(t, "vless-off@x")
	depleted := &core.ClientTraffic{InboundId: vless.Id, Email: "vless-depleted@x", Enable: false}
	if err := database.GetDB().Create(depleted).Error; err != nil {
		t.Fatalf("deplete client: %v", err)
	}

	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-vmess", Enable: true, Port: 43302, Protocol: model.VMESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"xhttp","xhttpSettings":{"path":"/x","sessionPlacement":"query","sessionKey":"sid"}}`,
	}, []model.Client{
		{Email: "vmess-a@x", ID: "55555555-5555-5555-5555-555555555555", Enable: true, Security: "auto"},
	})

	trojan := seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-trojan", Enable: true, Port: 43303, Protocol: model.Trojan,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"t.example.com","settings":{"allowInsecure":true}}}`,
	}, []model.Client{
		{Email: "trojan-a@x", Password: "trojan-pass", Enable: true, Flow: "xtls-rprx-vision"},
	})

	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-ss", Enable: true, Port: 43304, Protocol: model.Shadowsocks,
		Settings: `{"clients":[],"method":"chacha20-poly1305","password":"inbound-pass","network":"tcp,udp"}`,
	}, []model.Client{
		{Email: "ss-a@x", Password: "ss-pass", Enable: true},
	})

	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-hysteria", Enable: true, Port: 43305, Protocol: model.Hysteria,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"h.example.com"}}`,
	}, []model.Client{
		{Email: "hy-a@x", Auth: "hy-auth", Enable: true},
	})

	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-wg", Enable: true, Port: 43306, Protocol: model.WireGuard,
		Settings: `{"secretKey":"` + wgTestSecretKey() + `","mtu":1420}`,
	}, []model.Client{
		{Email: "wg-a@x", Enable: true, PublicKey: wgTestSecretKey(), AllowedIPs: []string{"10.0.0.2/32"}},
	})

	// An inbound whose row predates the guard that rejects finalmask with
	// REALITY, plus an XMC mask with no Minecraft profile: both are dropped
	// while rendering rather than at save time.
	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-legacy-stream", Enable: true, Port: 43307, Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"reality",` +
			`"realitySettings":{"dest":"example.com:443","serverNames":["example.com"]},` +
			`"finalmask":{"tcp":[{"type":"xmc"}]}}`,
	}, []model.Client{
		{Email: "legacy-a@x", ID: "66666666-6666-6666-6666-666666666666", Enable: true},
	})

	// A disabled inbound and a node-owned one are both absent from the dump:
	// the local core must never be handed either.
	seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-disabled", Enable: false, Port: 43308, Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none"}`,
	}, nil)

	fallbackChild := seedGoldenInbound(t, &model.Inbound{
		Tag: "golden-fallback-child", Enable: true, Port: 43309, Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none"}`,
	}, nil)
	for _, parent := range []*model.Inbound{vless, trojan} {
		row := &model.InboundFallback{MasterId: parent.Id, ChildId: fallbackChild.Id, SortOrder: 1}
		if err := database.GetDB().Create(row).Error; err != nil {
			t.Fatalf("create fallback for %q: %v", parent.Tag, err)
		}
	}
}

// dumpInbounds renders the generated inbounds as text, keeping every JSON
// section byte-for-byte: a reformat that parses the same still restarts Xray.
func dumpInbounds(t *testing.T) string {
	t.Helper()
	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	var b strings.Builder
	for i := range cfg.InboundConfigs {
		ic := cfg.InboundConfigs[i]
		fmt.Fprintf(&b, "=== %s ===\nlisten: %s\nport: %d\nprotocol: %s\n", ic.Tag, ic.Listen, ic.Port, ic.Protocol)
		for _, section := range []struct {
			name string
			body []byte
		}{
			{"settings", ic.Settings},
			{"streamSettings", ic.StreamSettings},
			{"sniffing", ic.Sniffing},
		} {
			fmt.Fprintf(&b, "--- %s ---\n%s\n", section.name, section.body)
		}
	}
	return b.String()
}

/*
TestGetXrayConfigInboundsMatchGolden pins today's output so the renderer
unification can prove it changed nothing.

Rerun with -update-golden ONLY for a change you meant to make, and read the diff
before committing it. Regenerating to clear a red test is how a fixture stops
being evidence.
*/
func TestGetXrayConfigInboundsMatchGolden(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	got := dumpInbounds(t)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("rewrote %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("generated inbounds differ from %s.\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
	}
}

/*
TestGetXrayConfigDropsClientsThatCannotConnect states the intent the golden can
only pin.

A client is dropped for two unrelated reasons — the client row is disabled, or
its client_traffics row was disabled by the quota/expiry job — and a refactor
that keeps one and loses the other still produces a plausible fixture. Naming
the emails is what makes the golden's coverage claim checkable.
*/
func TestGetXrayConfigDropsClientsThatCannotConnect(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	dump := dumpInbounds(t)
	for _, email := range []string{"vless-off@x", "vless-depleted@x"} {
		if strings.Contains(dump, email) {
			t.Errorf("%q is in the generated config; a client that cannot connect must not be rendered", email)
		}
	}
	for _, email := range []string{"vless-plain@x", "vless-flow@x"} {
		if !strings.Contains(dump, email) {
			t.Errorf("%q is missing from the generated config; an enabled client must be rendered", email)
		}
	}
}

/*
TestGetXrayConfigIsStableAcrossCalls is the property the fixture cannot show on
its own: rendering the same rows twice must produce the same bytes.

Without it, the config regenerated on every restart-check would compare unequal
to the running one and restart Xray on a timer, and a golden taken from a single
call would still be green.
*/
func TestGetXrayConfigIsStableAcrossCalls(t *testing.T) {
	initTestDB(t)
	seedGoldenFixture(t)

	first := dumpInbounds(t)
	if second := dumpInbounds(t); first != second {
		t.Errorf("two renders of the same rows differ, so every regeneration reads as a config change.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
