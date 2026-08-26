package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

/*
Every raw generator must answer the inbound's Host rows.

linkFromHosts implements the whole Hosts feature by injecting the rows into a
clone's streamSettings.externalProxy and calling GetLink, so a generator that
does not read that array opts its protocol out of the feature entirely — while
the panel still lists the hosts and the JSON and Clash formats still honour
them. wireguard:// and tg://proxy read past it and kept the origin address, and
the Hysteria generator carried only the pins and the insecure flag from the row.
F129 / F143 / F139 / F138.
*/

// twoHostStream is the shape injectExternalProxy writes: two rows carrying the
// isHost marker, so these travel the host path rather than the legacy one.
const twoHostStream = `{"externalProxy":[
	{"isHost":true,"forceTls":"same","dest":"first.cdn.example","port":8443,"remark":"FIRST"},
	{"isHost":true,"forceTls":"same","dest":"second.cdn.example","port":2096,"remark":"SECOND"}
]}`

func linkLines(t *testing.T, out string) []string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		t.Fatal("generator produced no link at all")
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

func TestWireguardLinkFansOutOverHosts(t *testing.T) {
	serverPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	clientPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	inbound := &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     51820,
		Protocol: model.WireGuard,
		Remark:   "wg",
		Settings: `{"secretKey":"` + serverPriv + `","mtu":1420,"clients":[{"email":"user","privateKey":"` + clientPriv + `","allowedIPs":["10.0.0.2/32"]}]}`,
	}

	// No hosts: one link at the inbound's own address, the pre-Hosts behaviour.
	if lines := linkLines(t, (&SubService{}).genWireguardLink(inbound, "user")); len(lines) != 1 ||
		!strings.Contains(lines[0], "203.0.113.7:51820") {
		t.Fatalf("a hostless inbound must render one link at its own address, got %v", lines)
	}

	inbound.StreamSettings = twoHostStream
	lines := linkLines(t, (&SubService{}).genWireguardLink(inbound, "user"))
	if len(lines) != 2 {
		t.Fatalf("two hosts rendered %d link(s); the rows are stored, listed in the panel and honoured by JSON and Clash, and this format ignored them: %v", len(lines), lines)
	}
	for i, want := range []string{"first.cdn.example:8443", "second.cdn.example:2096"} {
		u, err := url.Parse(lines[i])
		if err != nil {
			t.Fatalf("link %d does not parse: %v\n got: %s", i, err, lines[i])
		}
		if u.Host != want {
			t.Fatalf("link %d points at %q, want the host row's %q", i, u.Host, want)
		}
		if u.Query().Get("mtu") != "1420" {
			t.Fatalf("link %d lost the inbound's own parameters: %s", i, lines[i])
		}
		if u.User.Username() != clientPriv {
			t.Fatalf("link %d lost the client's private key", i)
		}
	}
}

func TestMtprotoLinkFansOutOverHosts(t *testing.T) {
	inbound := &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     8443,
		Protocol: model.MTProto,
		Remark:   "mt",
		Settings: `{"fakeTlsDomain":"www.cloudflare.com","clients":[{"email":"user","enable":true,"secret":"` + mtprotoTestSecret + `"}]}`,
	}

	if lines := linkLines(t, (&SubService{}).genMtprotoLink(inbound, "user")); len(lines) != 1 ||
		!strings.Contains(lines[0], "server=203.0.113.7") {
		t.Fatalf("a hostless inbound must render one link at its own address, got %v", lines)
	}

	inbound.StreamSettings = twoHostStream
	lines := linkLines(t, (&SubService{}).genMtprotoLink(inbound, "user"))
	if len(lines) != 2 {
		t.Fatalf("two hosts rendered %d link(s): %v", len(lines), lines)
	}
	for i, want := range [][2]string{{"first.cdn.example", "8443"}, {"second.cdn.example", "2096"}} {
		u, err := url.Parse(lines[i])
		if err != nil {
			t.Fatalf("link %d does not parse: %v\n got: %s", i, err, lines[i])
		}
		q := u.Query()
		if q.Get("server") != want[0] || q.Get("port") != want[1] {
			t.Fatalf("link %d is %s:%s, want the host row's %s:%s", i, q.Get("server"), q.Get("port"), want[0], want[1])
		}
		if q.Get("secret") != mtprotoTestSecret {
			t.Fatalf("link %d lost the client's FakeTLS secret", i)
		}
	}
}

// hysteriaHostInbound is a Hysteria2 inbound whose own TLS names one SNI, so an
// inherited field is distinguishable from an overridden one in every assertion.
func hysteriaHostInbound(externalProxy, finalmask string) *model.Inbound {
	stream := `{"security":"tls","tlsSettings":{"serverName":"origin.sni","alpn":["h3"],"settings":{"fingerprint":"chrome"}}`
	if finalmask != "" {
		stream += `,"finalmask":` + finalmask
	}
	stream += `,"externalProxy":[` + externalProxy + `]}`
	return &model.Inbound{
		Listen:         "203.0.113.1",
		Port:           443,
		Protocol:       model.Hysteria,
		Remark:         "hy",
		Settings:       `{"version":2,"clients":[{"auth":"hyauth","email":"user"}]}`,
		StreamSettings: stream,
	}
}

func TestHysteriaLinkHonoursAHostsTlsFields(t *testing.T) {
	row := `{"isHost":true,"forceTls":"same","dest":"hop.example.com","port":9443,"remark":"H",` +
		`"sni":"host.sni","fingerprint":"firefox","alpn":["h3","h2"]}`
	link := (&SubService{}).genHysteriaLink(hysteriaHostInbound(row, ""), "user")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	q := u.Query()
	for _, c := range []struct{ key, want, why string }{
		{"sni", "host.sni", "the Hosts page offers an SNI per host and it was dropped"},
		{"fp", "firefox", "the Hosts page offers a fingerprint per host and it was dropped"},
		{"alpn", "h3,h2", "the Hosts page offers an ALPN list per host and it was dropped"},
	} {
		if got := q.Get(c.key); got != c.want {
			t.Fatalf("%s = %q, want %q: %s", c.key, got, c.want, c.why)
		}
	}
}

// The override is additive: a row that leaves the fields blank must render
// exactly what it rendered before, or every existing deployment moves.
func TestHysteriaLinkInheritsWhenTheHostSaysNothing(t *testing.T) {
	row := `{"isHost":true,"forceTls":"same","dest":"hop.example.com","port":9443,"remark":"H"}`
	link := (&SubService{}).genHysteriaLink(hysteriaHostInbound(row, ""), "user")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	q := u.Query()
	if q.Get("sni") != "origin.sni" || q.Get("fp") != "chrome" || q.Get("alpn") != "h3" {
		t.Fatalf("a blank host row changed the inherited TLS fields: %s", link)
	}
}

/*
Port hopping survives a host.

mport was written in the tail the fan-out returns before, so switching on a
single Host row silently turned Hysteria2 port hopping off for every client of
that inbound — the panel still showed the range, and the link stopped carrying
it. F138.
*/
func TestHysteriaPortHoppingSurvivesAHost(t *testing.T) {
	const hop = `{"quicParams":{"udpHop":{"ports":"20000-30000"}}}`
	s := &SubService{}

	hostless := hysteriaHostInbound("", hop)
	if got := s.genHysteriaLink(hostless, "user"); !strings.Contains(got, "mport=20000-30000") {
		t.Fatalf("the hostless link lost port hopping; this test is checking the wrong thing: %s", got)
	}

	row := `{"isHost":true,"forceTls":"same","dest":"hop.example.com","port":9443,"remark":"H"}`
	link := s.genHysteriaLink(hysteriaHostInbound(row, hop), "user")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	if got := u.Query().Get("mport"); got != "20000-30000" {
		t.Fatalf("mport = %q, want the inbound's 20000-30000: adding a host turned port hopping off without saying so", got)
	}
}

// And an inbound with no hop range must not gain an mport from anywhere.
func TestHysteriaWithoutPortHoppingGainsNoMport(t *testing.T) {
	row := `{"isHost":true,"forceTls":"same","dest":"hop.example.com","port":9443,"remark":"H"}`
	if link := (&SubService{}).genHysteriaLink(hysteriaHostInbound(row, ""), "user"); strings.Contains(link, "mport") {
		t.Fatalf("invented an mport for an inbound that has no hop range: %s", link)
	}
}
