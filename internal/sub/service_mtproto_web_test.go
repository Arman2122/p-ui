package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// An inbound running the WEB relay hands out a second link: tg://webproxy with
// the hostname alone and the client's key as dd, the only form a WEB client
// holds. Without the relay, or without a host, there is no such link.
func TestGenMtprotoLinkAddsTheWebLink(t *testing.T) {
	s := &SubService{}
	base := `"fakeTlsDomain":"www.cloudflare.com","clients":[{"email":"user","enable":true,"secret":"` + mtprotoTestSecret + `"}]`

	inbound := &model.Inbound{
		Listen: "203.0.113.7", Port: 8443, Protocol: model.MTProto,
		Settings: `{"webEnable":true,"webHost":"web.example.com",` + base + `}`,
	}
	lines := strings.Split(s.genMtprotoLink(inbound, "user"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want the tg://proxy link and the WEB link, got %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	web, err := url.Parse(lines[1])
	if err != nil || web.Scheme != "tg" || web.Host != "webproxy" {
		t.Fatalf("second line = %q, want a tg://webproxy deep link (%v)", lines[1], err)
	}
	q := web.Query()
	if q.Get("server") != "web.example.com" {
		t.Fatalf("server = %q, want the WEB hostname", q.Get("server"))
	}
	if q.Get("secret") != "dd8196fe6ed8b637d001f91d6952cfcdf0" {
		t.Fatalf("secret = %q, want the same key as dd with no domain", q.Get("secret"))
	}
	if q.Has("port") {
		t.Fatal("a WEB link carries no port: the client fixes https and 443")
	}

	for name, settings := range map[string]string{
		"relay off":  `{"webEnable":false,"webHost":"web.example.com",` + base + `}`,
		"no host":    `{"webEnable":true,` + base + `}`,
		"no web key": `{` + base + `}`,
	} {
		plain := &model.Inbound{Listen: "203.0.113.7", Port: 8443, Protocol: model.MTProto, Settings: settings}
		if got := s.genMtprotoLink(plain, "user"); strings.Contains(got, "webproxy") || strings.Count(got, "\n") != 0 {
			t.Fatalf("%s: want the tg://proxy link alone, got %q", name, got)
		}
	}
}

// A relay inbound on a loopback listen behind a fallback master keeps its own
// links: the WEB one carries no port, and the FakeTLS one is not the master's
// to front, so the projection every other child gets must not touch it.
func TestAWebRelayInboundIsNotProjectedThroughItsMaster(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	master := &model.Inbound{
		Tag: "fbw-sub-master", Listen: "0.0.0.0", Port: 24871, Protocol: model.VLESS, Enable: true,
		Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"alpn":["http/1.1"]}}`,
	}
	child := &model.Inbound{
		Tag: "fbw-sub-child", Listen: "127.0.0.1", Port: 24872, Protocol: model.MTProto, Enable: true,
		Settings: `{"webEnable":true,"webHost":"web.example.com","webBindPort":8099,"clients":[]}`, StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	for _, row := range []*model.Inbound{master, child} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.InboundFallback{MasterId: master.Id, ChildId: child.Id, Name: "web.example.com", Alpn: "http/1.1", Xver: 2}).Error; err != nil {
		t.Fatal(err)
	}
	s := NewSubService("")
	if s.projectThroughFallbackMaster(child) || child.Port != 24872 || child.Listen != "127.0.0.1" {
		t.Fatalf("the relay inbound was projected through its master: %s:%d", child.Listen, child.Port)
	}
}
