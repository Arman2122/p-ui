package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A host row's overrides never reached a Clash Hysteria entry.

getProxies builds a working stream per endpoint and folds the host into it, but
buildHysteriaProxy threw that away and re-unmarshalled inbound.StreamSettings.
So for Hysteria, and only in the Clash rendering, every host override was
invisible: sni, alpn, allowInsecure, fingerprint, the salamander obfs block and
the finalmask hop range all came from the inbound as stored.

F138 recorded this as the hop-port field alone. It is the whole merge.

The stream is still taken unpruned -- streamData flattens tlsSettings and drops
allowInsecure, which Hysteria needs -- so the host is now merged into both
shapes and Hysteria gets the unpruned one.
*/

func hysteriaInboundWithHost(finalMask string) *model.Inbound {
	return &model.Inbound{
		Id: 1, Tag: "hy", Enable: true, Listen: "203.0.113.9", Port: 443,
		Protocol: model.Hysteria,
		Remark:   "hy",
		Settings: `{"clients":[{"email":"a@e","auth":"pw"}]}`,
		StreamSettings: `{"network":"quic","security":"tls",
			"tlsSettings":{"serverName":"inbound.example","alpn":["h3"],
				"settings":{"fingerprint":"chrome"}},
			"externalProxy":[{
				"isHost": true,
				"forceTls":"tls",
				"dest":"cdn.example",
				"port":8443,
				"remark":"via-host",
				"sni":"host.example",
				"alpn":["h3","http/1.1"],
				"fingerprint":"firefox",
				"allowInsecure":true,
				"finalMask":` + finalMask + `
			}]}`,
	}
}

func TestAHostOverridesReachAClashHysteriaEntry(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	client := model.Client{Email: "a@e", Auth: "pw"}

	fm := `"{\"udp\":[{\"type\":\"salamander\",\"settings\":{\"password\":\"from-host\"}}]}"`
	proxies := svc.getProxies(svc.SubService, hysteriaInboundWithHost(fm), client, "fallback.example")
	if len(proxies) != 1 {
		t.Fatalf("want one proxy, got %d: %+v", len(proxies), proxies)
	}
	p := proxies[0]

	checks := []struct {
		key  string
		want any
		why  string
	}{
		{"server", "cdn.example", "the host's address"},
		{"port", 8443, "the host's port"},
		{"sni", "host.example", "the host's SNI, not the inbound's serverName"},
		{"skip-cert-verify", true, "the host's allowInsecure"},
		{"client-fingerprint", "firefox", "the host's fingerprint, not the inbound's chrome"},
		{"obfs-password", "from-host", "the host's finalmask salamander block"},
	}
	for _, c := range checks {
		if got := p[c.key]; got != c.want {
			t.Errorf("%s = %#v, want %#v (%s)", c.key, got, c.want, c.why)
		}
	}
	alpn, _ := p["alpn"].([]string)
	if len(alpn) != 2 || alpn[0] != "h3" || alpn[1] != "http/1.1" {
		t.Errorf("alpn = %#v, want the host's two-entry list", p["alpn"])
	}
}

// The hop range is the half F138 did record, and it comes through the same
// merged finalmask.
func TestAHostHopRangeReachesAClashHysteriaEntry(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	client := model.Client{Email: "a@e", Auth: "pw"}

	fm := `"{\"quicParams\":{\"udpHop\":{\"ports\":\"20000-21000\"}}}"`
	proxies := svc.getProxies(svc.SubService, hysteriaInboundWithHost(fm), client, "fallback.example")
	if len(proxies) != 1 {
		t.Fatalf("want one proxy, got %d", len(proxies))
	}
	if got := proxies[0]["ports"]; got != "20000-21000" {
		t.Fatalf("ports = %#v, want the host's hop range", got)
	}
}

// And an inbound with no host still renders from its own stream.
func TestAHysteriaInboundWithNoHostIsUnchanged(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	client := model.Client{Email: "a@e", Auth: "pw"}

	ib := &model.Inbound{
		Id: 2, Tag: "hy2", Enable: true, Listen: "203.0.113.9", Port: 443,
		Protocol: model.Hysteria, Remark: "hy2",
		Settings: `{"clients":[{"email":"a@e","auth":"pw"}]}`,
		StreamSettings: `{"network":"quic","security":"tls",
			"tlsSettings":{"serverName":"inbound.example","alpn":["h3"],
				"settings":{"fingerprint":"chrome","allowInsecure":true}}}`,
	}

	proxies := svc.getProxies(svc.SubService, ib, client, "fallback.example")
	if len(proxies) != 1 {
		t.Fatalf("want one proxy, got %d", len(proxies))
	}
	p := proxies[0]
	if p["sni"] != "inbound.example" {
		t.Errorf("sni = %#v, want the inbound's own", p["sni"])
	}
	if p["client-fingerprint"] != "chrome" {
		t.Errorf("client-fingerprint = %#v, want the inbound's own", p["client-fingerprint"])
	}
	if p["skip-cert-verify"] != true {
		t.Errorf("skip-cert-verify = %#v, want true from the inbound's settings.allowInsecure", p["skip-cert-verify"])
	}
}
