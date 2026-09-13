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
