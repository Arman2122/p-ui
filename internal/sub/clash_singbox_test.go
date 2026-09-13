package sub

import (
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A TUIC or AnyTLS inbound was absent from the Clash subscription: the proxy
builder knew Xray's protocols alone, and mihomo, which every Clash client of
the panel runs, has carried both for years. The entry mirrors the tuic:// and
anytls:// links, with a host row's SNI and allowInsecure honoured.
*/
func TestTUICAndAnyTLSReachTheClashSubscription(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	const uuid, password = "c0ffee00-0000-4000-8000-000000000001", "secret-password-1"
	client := model.Client{Email: "a@e", ID: uuid, Password: password}

	tuic := &model.Inbound{
		Id: 1, Tag: "tuic-1", Enable: true, Listen: "203.0.113.9", Port: 24443, Protocol: model.Protocol("tuic"), Remark: "berlin",
		Settings:       `{"certFile":"/etc/p-ui/tuic.crt","keyFile":"/etc/p-ui/tuic.key","serverName":"tuic.example.com","congestionControl":"cubic","clients":[{"email":"a@e","uuid":"` + uuid + `","password":"` + password + `"}]}`,
		StreamSettings: "{}",
	}
	proxies := svc.getProxies(svc.SubService, tuic, client, "fallback.example")
	if len(proxies) != 1 {
		t.Fatalf("want one tuic proxy, got %d: %+v", len(proxies), proxies)
	}
	want := map[string]any{
		"name": "berlin-a@e", "type": "tuic", "server": "203.0.113.9", "port": 24443, "sni": "tuic.example.com", "udp": true,
		"uuid": uuid, "password": password, "alpn": []string{"h3"}, "congestion-controller": "cubic", "udp-relay-mode": "native",
	}
	if !reflect.DeepEqual(proxies[0], want) {
		t.Fatalf("tuic proxy = %#v\nwant %#v", proxies[0], want)
	}

	anytls := &model.Inbound{
		Id: 2, Tag: "anytls-2", Enable: true, Listen: "203.0.113.9", Port: 24444, Protocol: model.Protocol("anytls"), Remark: "berlin-tcp",
		Settings:       `{"certFile":"/etc/p-ui/a.crt","keyFile":"/etc/p-ui/a.key","clients":[{"email":"a@e","password":"` + password + `"}]}`,
		StreamSettings: `{"externalProxy":[{"isHost":true,"dest":"relay.example","port":8443,"sni":"relay.example","allowInsecure":true}]}`,
	}
	proxies = svc.getProxies(svc.SubService, anytls, client, "fallback.example")
	if len(proxies) != 1 {
		t.Fatalf("want one anytls proxy, got %d: %+v", len(proxies), proxies)
	}
	want = map[string]any{
		"name": "berlin-tcp-a@e", "type": "anytls", "server": "relay.example", "port": 8443, "sni": "relay.example", "udp": true,
		"password": password, "client-fingerprint": "chrome", "skip-cert-verify": true,
	}
	if !reflect.DeepEqual(proxies[0], want) {
		t.Fatalf("anytls proxy = %#v\nwant %#v", proxies[0], want)
	}
}
