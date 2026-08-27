package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A Host's own Final Mask could not change a Hysteria2 obfs password.

The Hosts page offers a Final Mask field, and its form says the masks are merged
into that host's stream. They were — mergeFinalMask APPENDS a host's udp masks
after the inbound's — but both readers stopped at the first salamander entry in
the list, which is always the inbound's. So the field was inert: an operator
could set it, be told it saved, and every link kept the inbound's password.

The raw generator had a second problem on top: it read the inbound's masks
before the host fan-out, so it never saw a host's Final Mask at all.

Last match wins now, which is what "a host override wins" means once the merge
is an append, and both renderers go through one reader. Ledger L18.
*/

const hysteriaObfsStream = `{
  "network": "quic",
  "security": "tls",
  "tlsSettings": {"serverName": "origin.example", "settings": {"fingerprint": "chrome"}},
  "finalmask": {"udp": [{"type": "salamander", "settings": {"password": "INBOUND-PW"}}]},
  "externalProxy": [
    {"isHost": true, "forceTls": "same", "dest": "cdn-a.example", "port": 443, "remark": "A",
     "finalMask": "{\"udp\":[{\"type\":\"salamander\",\"settings\":{\"password\":\"HOST-PW\"}}]}"},
    {"isHost": true, "forceTls": "same", "dest": "cdn-b.example", "port": 443, "remark": "B"}
  ]
}`

func hysteriaObfsInbound() *model.Inbound {
	return &model.Inbound{
		Id: 3, Tag: "hy", Enable: true, Listen: "203.0.113.20", Port: 443,
		Protocol: model.Hysteria, Remark: "HY",
		Settings:       `{"clients":[{"email":"u@x","auth":"pw","subId":"s3"}],"version":2}`,
		StreamSettings: hysteriaObfsStream,
	}
}

// obfsByHost maps a link's remark suffix to the obfs password it carries.
func obfsByHost(t *testing.T, links string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, raw := range strings.Split(links, "\n") {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("link does not parse: %v\n%s", err, raw)
		}
		out[u.Fragment] = u.Query().Get("obfs-password")
	}
	return out
}

func TestAHostsFinalMaskSetsItsOwnHysteriaObfsPassword(t *testing.T) {
	got := obfsByHost(t, (&SubService{}).GetLink(hysteriaObfsInbound(), "u@x"))
	if len(got) != 2 {
		t.Fatalf("want one link per host row, got %d: %v", len(got), got)
	}
	if got["HY-A-u@x"] != "HOST-PW" {
		t.Errorf("the host that set its own obfs password got %q; its Final Mask was inert", got["HY-A-u@x"])
	}
	// And a host that set none keeps the inbound's, rather than inheriting the
	// previous host's — the leak this would otherwise reintroduce.
	if got["HY-B-u@x"] != "INBOUND-PW" {
		t.Errorf("the host that set nothing got %q, want the inbound's INBOUND-PW", got["HY-B-u@x"])
	}
}

func TestTheClashHysteriaEntryAgreesAboutTheObfsPassword(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	proxies := svc.getProxies(svc.SubService, hysteriaObfsInbound(),
		model.Client{Email: "u@x", Auth: "pw", SubID: "s3"}, "fallback.example")
	if len(proxies) != 2 {
		t.Fatalf("want one proxy per host row, got %d", len(proxies))
	}
	want := map[string]string{"HY-A-u@x": "HOST-PW", "HY-B-u@x": "INBOUND-PW"}
	for _, p := range proxies {
		name, _ := p["name"].(string)
		if got, _ := p["obfs-password"].(string); got != want[name] {
			t.Errorf("%s obfs-password = %q, want %q — Clash and the raw link must not disagree", name, got, want[name])
		}
	}
}

// An inbound with no masks at all emits no obfs pair, and a host that adds one
// to such an inbound still gets it.
func TestSalamanderIsOmittedWhenNoMaskDeclaresIt(t *testing.T) {
	ib := hysteriaObfsInbound()
	ib.StreamSettings = strings.Replace(hysteriaObfsStream,
		`"finalmask": {"udp": [{"type": "salamander", "settings": {"password": "INBOUND-PW"}}]},`, "", 1)

	got := obfsByHost(t, (&SubService{}).GetLink(ib, "u@x"))
	if got["HY-A-u@x"] != "HOST-PW" {
		t.Errorf("a host adding obfs to an inbound that has none got %q", got["HY-A-u@x"])
	}
	if got["HY-B-u@x"] != "" {
		t.Errorf("a link with no salamander anywhere still carries obfs-password %q", got["HY-B-u@x"])
	}
}
