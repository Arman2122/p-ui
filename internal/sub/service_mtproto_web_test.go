package sub

import (
	"net/url"
	"strings"
	"testing"

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
