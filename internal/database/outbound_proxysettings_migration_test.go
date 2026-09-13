package database

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
TestMigrateOutboundProxySettings_MovesTagToDialerProxy covers what xray-core
v26.9.9 refuses in a stored outbound: "proxySettings" (removed upstream, its
replacement is sockopt.dialerProxy) and a freedom "sockopt.addressPortStrategy".
Either one in the template makes the whole config unbuildable, so startup
rewrites them and leaves every other outbound as it was.
*/
func TestMigrateOutboundProxySettings_MovesTagToDialerProxy(t *testing.T) {
	initTestDB(t)

	template := `{"outbounds":[` +
		`{"tag":"chained","protocol":"vless","settings":{},"proxySettings":{"tag":"upstream","transportLayer":true}},` +
		`{"tag":"fronted","protocol":"trojan","settings":{},"streamSettings":{"network":"tcp","sockopt":{"dialerProxy":"front"}},"proxySettings":{"tag":"upstream"}},` +
		`{"tag":"tagless","protocol":"vmess","settings":{},"streamSettings":{"network":"ws"},"proxySettings":{"transportLayer":true}},` +
		`{"tag":"srv","protocol":"freedom","settings":{},"streamSettings":{"sockopt":{"addressPortStrategy":"srvportonly","mark":7}}},` +
		`{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"AsIs"},"streamSettings":{"sockopt":{"addressPortStrategy":"none"}}}` +
		`]}`
	if err := db.Create(&model.Setting{Key: "xrayTemplateConfig", Value: template}).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}

	if err := migrateOutboundProxySettings(); err != nil {
		t.Fatalf("migrateOutboundProxySettings: %v", err)
	}

	var setting model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&setting).Error; err != nil {
		t.Fatalf("reload template: %v", err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		t.Fatalf("parse rewritten template: %v", err)
	}
	byTag := map[string]map[string]any{}
	for _, ob := range cfg.Outbounds {
		byTag[ob["tag"].(string)] = ob
	}
	sockoptOf := func(ob map[string]any) map[string]any {
		stream, _ := ob["streamSettings"].(map[string]any)
		sockopt, _ := stream["sockopt"].(map[string]any)
		return sockopt
	}

	for _, tag := range []string{"chained", "fronted", "tagless"} {
		if legacy, still := byTag[tag]["proxySettings"]; still {
			t.Fatalf("outbound %q still carries proxySettings %v", tag, legacy)
		}
	}
	if got := sockoptOf(byTag["chained"])["dialerProxy"]; got != "upstream" {
		t.Fatalf("chained: dialerProxy = %v, want the proxySettings tag", got)
	}
	if got := sockoptOf(byTag["fronted"])["dialerProxy"]; got != "front" {
		t.Fatalf("fronted: dialerProxy = %v, want the one the row already had", got)
	}
	if sockopt := sockoptOf(byTag["tagless"]); sockopt != nil {
		t.Fatalf("tagless: a proxySettings without a tag must not invent a sockopt, got %v", sockopt)
	}
	if strategy, still := sockoptOf(byTag["srv"])["addressPortStrategy"]; still {
		t.Fatalf("srv: freedom kept addressPortStrategy %v", strategy)
	}
	if got := sockoptOf(byTag["srv"])["mark"]; got != float64(7) {
		t.Fatalf("srv: the rest of sockopt must survive, mark = %v", got)
	}

	var wantDirect map[string]any
	if err := json.Unmarshal([]byte(`{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"AsIs"},"streamSettings":{"sockopt":{"addressPortStrategy":"none"}}}`), &wantDirect); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(byTag["direct"])
	wantJSON, _ := json.Marshal(wantDirect)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("direct: an outbound the core accepts was rewritten:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

/*
TestMigrateOutboundUdpHop_MovesItOntoTheMask covers the other v26.9.9 change to
a stored outbound: finalmask.quicParams.udpHop is no longer read, so a Hysteria
outbound the panel dials would silently stop hopping. Startup moves it onto the
udphop UDP mask with the modes that reproduce the old behaviour, keeps the rest
of quicParams, and leaves an outbound without the key untouched.
*/
func TestMigrateOutboundUdpHop_MovesItOntoTheMask(t *testing.T) {
	initTestDB(t)

	template := `{"outbounds":[` +
		`{"tag":"hop","protocol":"hysteria","settings":{},"streamSettings":{"network":"hysteria","finalmask":{"udp":[{"type":"salamander","settings":{"password":"p"}}],"quicParams":{"congestion":"bbr","udpHop":{"ports":"20000-50000","interval":5}}}}},` +
		`{"tag":"plain","protocol":"freedom","settings":{},"streamSettings":{"finalmask":{"quicParams":{"congestion":"bbr"}}}}` +
		`]}`
	if err := db.Create(&model.Setting{Key: "xrayTemplateConfig", Value: template}).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}

	if err := migrateOutboundUdpHop(); err != nil {
		t.Fatalf("migrateOutboundUdpHop: %v", err)
	}

	var setting model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&setting).Error; err != nil {
		t.Fatalf("reload template: %v", err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		t.Fatalf("parse rewritten template: %v", err)
	}
	finalmaskOf := func(ob map[string]any) map[string]any {
		stream, _ := ob["streamSettings"].(map[string]any)
		finalmask, _ := stream["finalmask"].(map[string]any)
		return finalmask
	}

	hop := finalmaskOf(cfg.Outbounds[0])
	qp, _ := hop["quicParams"].(map[string]any)
	if _, still := qp["udpHop"]; still || qp["congestion"] != "bbr" {
		t.Fatalf("hop: quicParams must lose udpHop and keep congestion, got %v", qp)
	}
	udp, _ := hop["udp"].([]any)
	if len(udp) != 2 {
		t.Fatalf("hop: udp masks = %v, want salamander then udphop", udp)
	}
	mask, _ := udp[1].(map[string]any)
	settings, _ := mask["settings"].(map[string]any)
	if mask["type"] != "udphop" || settings["mode"] != "intervalLocal,intervalRemote" ||
		settings["remotePorts"] != "20000-50000" || settings["interval"] != "5" {
		t.Fatalf("hop: udphop mask = %v, want the legacy range and a numeric interval as a string", mask)
	}

	var wantPlain map[string]any
	if err := json.Unmarshal([]byte(`{"tag":"plain","protocol":"freedom","settings":{},"streamSettings":{"finalmask":{"quicParams":{"congestion":"bbr"}}}}`), &wantPlain); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(cfg.Outbounds[1])
	wantJSON, _ := json.Marshal(wantPlain)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("plain: an outbound without udpHop was rewritten:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}
