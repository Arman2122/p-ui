package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A JSON subscription must not carry a profile that proxies nothing.

getConfig's switch covers vmess / vless / trojan / shadowsocks / hysteria and
then asks the registry about WireGuard. An mtproto inbound matches none of them,
so its outbound list was only the defaults — a config whose "remarks" names the
inbound, whose outbounds are freedom and blackhole, and which sends every byte
direct. A client importing it believes it is connected and is not.

Xray has no mtproto outbound to emit instead: there is no such directory under
xray-core/proxy, because MTProto is a Telegram transport rather than a general
one. So the honest answer is the one the Clash renderer already gives from its
default arm — render nothing, and let the tg://proxy raw link serve that client.
F126 / F214.
*/

func seedMtprotoForSub(t *testing.T, subId, tag string, port int) *model.Inbound {
	t.Helper()
	db := database.GetDB()
	email := tag + "@mt"
	in := &model.Inbound{
		UserId: 1, Port: port, Protocol: model.MTProto, Enable: true, Tag: tag, Remark: tag,
		Listen:   "203.0.113.9",
		Settings: `{"fakeTlsDomain":"www.cloudflare.com","clients":[{"email":"` + email + `","enable":true,"subId":"` + subId + `","secret":"` + mtprotoTestSecret + `"}]}`,
	}
	if err := db.Create(in).Error; err != nil {
		t.Fatalf("create mtproto inbound: %v", err)
	}
	rec := &model.ClientRecord{Email: email, SubID: subId, Enable: true}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: in.Id}).Error; err != nil {
		t.Fatalf("link client: %v", err)
	}
	if err := db.Create(&model.ClientCredential{
		ClientId: rec.Id, InboundId: in.Id, Key: model.CredentialSecret, Value: mtprotoTestSecret,
	}).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	return in
}

func TestJsonSubscriptionOmitsAnMtprotoOnlyProfile(t *testing.T) {
	initSubDB(t)
	seedMtprotoForSub(t, "jsonmt", "mt-only", 8461)

	body, _, err := NewSubJsonService("", "", "", NewSubService("")).GetJson("jsonmt", "sub.example.com", false)
	if err != nil {
		t.Fatalf("GetJson: %v", err)
	}
	if strings.TrimSpace(body) != "" {
		t.Fatalf("an mtproto-only subscription produced a config; every outbound in it is a default, so it proxies nothing:\n%s", body)
	}

	// The client is still served — by the format that can carry them.
	links, _, _, _, err := (&SubService{}).GetSubs("jsonmt", "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 || !strings.HasPrefix(links[0], "tg://proxy") {
		t.Fatalf("dropping the JSON profile must not drop the client: %v", links)
	}
}

// And the mtproto inbound is what gets dropped, not the subscription: a
// subscriber holding both keeps the profile that works.
func TestJsonSubscriptionKeepsTheUsableInboundBesideAnMtprotoOne(t *testing.T) {
	initSubDB(t)
	seedSubInbound(t, "jsonmix", "mix-vless", 8462, 1, `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	seedMtprotoForSub(t, "jsonmix", "mix-mt", 8463)

	body, _, err := NewSubJsonService("", "", "", NewSubService("")).GetJson("jsonmix", "sub.example.com", true)
	if err != nil {
		t.Fatalf("GetJson: %v", err)
	}

	var profiles []map[string]any
	if err := json.Unmarshal([]byte(body), &profiles); err != nil {
		t.Fatalf("subscription body is not a JSON array: %v\n%s", err, body)
	}
	if len(profiles) != 1 {
		t.Fatalf("want exactly the one usable profile, got %d:\n%s", len(profiles), body)
	}
	if remark, _ := profiles[0]["remarks"].(string); !strings.Contains(remark, "mix-vless") {
		t.Fatalf("the surviving profile is not the VLESS one: remarks = %q", remark)
	}

	outbounds, _ := profiles[0]["outbounds"].([]any)
	first, _ := outbounds[0].(map[string]any)
	if protocol, _ := first["protocol"].(string); protocol != "vless" {
		t.Fatalf("the surviving profile's first outbound is %q, not the proxy: %s", protocol, body)
	}
}
