package sub

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestDefaultJSONUsesCompatibleLocalInbounds(t *testing.T) {
	svc := NewSubJsonService("", "", "", nil)
	inbounds, ok := svc.configJson["inbounds"].([]any)
	if !ok {
		t.Fatalf("default JSON inbounds = %#v, want array", svc.configJson["inbounds"])
	}

	byPort := make(map[float64]map[string]any, len(inbounds))
	for _, raw := range inbounds {
		inbound, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("default JSON inbound = %#v, want object", raw)
		}
		port, ok := inbound["port"].(float64)
		if !ok {
			t.Fatalf("default JSON inbound port = %#v, want number", inbound["port"])
		}
		byPort[port] = inbound
	}

	socks := byPort[10808]
	if socks == nil {
		t.Fatal("default JSON is missing the local inbound on port 10808")
	}
	if socks["protocol"] != "socks" || socks["tag"] != "mixed" {
		t.Fatalf("port 10808 protocol/tag = %v/%v, want socks/mixed", socks["protocol"], socks["tag"])
	}
	settings, _ := socks["settings"].(map[string]any)
	if settings == nil || settings["udp"] != true {
		t.Fatalf("port 10808 settings = %#v, want udp enabled", socks["settings"])
	}
	if socks["listen"] != "127.0.0.1" {
		t.Fatalf("port 10808 listen = %#v, want 127.0.0.1 (an unbound local inbound is exposed to the LAN and is not reachable by iOS packet tunnels)", socks["listen"])
	}

	http := byPort[10809]
	if http == nil || http["protocol"] != "http" {
		t.Fatalf("port 10809 inbound = %#v, want http protocol", http)
	}
	if http["listen"] != "127.0.0.1" {
		t.Fatalf("port 10809 listen = %#v, want 127.0.0.1", http["listen"])
	}
}

func TestSubJsonServiceVisionFlowDisablesTCPMuxOnly(t *testing.T) {
	globalMux := `{"enabled":true,"concurrency":8,"xudpConcurrency":16,"xudpProxyUDP443":"reject"}`
	svc := NewSubJsonService(globalMux, "", "", nil)
	inbound := &model.Inbound{Listen: "1.2.3.4", Port: 443, Protocol: model.VLESS, Settings: `{"encryption":"none"}`}

	decode := func(raw []byte) map[string]any {
		t.Helper()
		var ob map[string]any
		if err := json.Unmarshal(raw, &ob); err != nil {
			t.Fatalf("unmarshal outbound: %v", err)
		}
		return ob
	}

	vision := decode([]byte(svc.genVless(&SubService{}, inbound, nil, model.Client{ID: "uuid-1", Flow: "xtls-rprx-vision"}, globalMux)))
	mux, _ := vision["mux"].(map[string]any)
	if mux == nil {
		t.Fatalf("vision outbound must keep its mux object for the XUDP keys, got %#v", vision["mux"])
	}
	if mux["concurrency"] != float64(-1) {
		t.Fatalf("vision outbound mux.concurrency = %v, want -1 (TCP mux.cool is rejected by XTLS flows)", mux["concurrency"])
	}
	if mux["enabled"] != true || mux["xudpConcurrency"] != float64(16) || mux["xudpProxyUDP443"] != "reject" {
		t.Fatalf("vision outbound lost its XUDP settings: %#v", mux)
	}

	plain := decode([]byte(svc.genVless(&SubService{}, inbound, nil, model.Client{ID: "uuid-1"}, globalMux)))
	mux, _ = plain["mux"].(map[string]any)
	if mux == nil || mux["concurrency"] != float64(8) {
		t.Fatalf("flow-less outbound must keep the global mux unchanged, got %#v", plain["mux"])
	}

	noMux := decode([]byte(svc.genVless(&SubService{}, inbound, nil, model.Client{ID: "uuid-1", Flow: "xtls-rprx-vision"}, "")))
	if _, has := noMux["mux"]; has {
		t.Fatalf("no global mux must still mean no mux key, got %#v", noMux["mux"])
	}
}

func TestSubJsonServiceTLSCipherSuitesForwarded(t *testing.T) {
	svc := NewSubJsonService("", "", "", nil)
	stream := svc.streamData(`{"network":"tcp","security":"tls","tlsSettings":{"serverName":"a.example.com","cipherSuites":"TLS_AES_256_GCM_SHA384","settings":{}}}`, "")

	tls, _ := stream["tlsSettings"].(map[string]any)
	if got := tls["cipherSuites"]; got != "TLS_AES_256_GCM_SHA384" {
		t.Fatalf("cipherSuites = %#v, want %q", got, "TLS_AES_256_GCM_SHA384")
	}

	stream = svc.streamData(`{"network":"tcp","security":"tls","tlsSettings":{"serverName":"a.example.com","cipherSuites":"","settings":{}}}`, "")
	tls, _ = stream["tlsSettings"].(map[string]any)
	if _, present := tls["cipherSuites"]; present {
		t.Fatalf("empty cipherSuites must be omitted, got %#v", tls["cipherSuites"])
	}
}

func TestGetProxies_CustomIPv6ShareAddrIsUnbracketed(t *testing.T) {
	svc := &SubClashService{SubService: &SubService{}}
	inbound := &model.Inbound{
		Protocol:          model.VLESS,
		Port:              443,
		Remark:            "r",
		Settings:          `{"encryption":"none"}`,
		StreamSettings:    `{"network":"tcp","security":"none"}`,
		ShareAddrStrategy: "custom",
		ShareAddr:         "[2001:db8::1]",
	}
	client := model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "a@example.com"}

	proxies := svc.getProxies(svc.SubService, inbound, client, "panel.example.com")
	if len(proxies) != 1 {
		t.Fatalf("getProxies returned %d proxies, want 1", len(proxies))
	}
	if got := proxies[0]["server"]; got != "2001:db8::1" {
		t.Fatalf("server = %v, want 2001:db8::1", got)
	}
}

func TestBuildHysteriaProxyIncludesCertificateFingerprint(t *testing.T) {
	const pin = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	const want = "00:01:02:03:04:05:06:07:08:09:0A:0B:0C:0D:0E:0F:10:11:12:13:14:15:16:17:18:19:1A:1B:1C:1D:1E:1F"
	const externalPin = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
	const wantExternal = "FF:EE:DD:CC:BB:AA:99:88:77:66:55:44:33:22:11:00:FF:EE:DD:CC:BB:AA:99:88:77:66:55:44:33:22:11:00"

	svc := &SubClashService{}
	subReq := &SubService{}
	inbound := &model.Inbound{
		Protocol: model.Hysteria,
		Listen:   "192.0.2.1",
		Port:     443,
		Remark:   "hysteria 2",
		Settings: `{"version":2}`,
		StreamSettings: `{
			"tlsSettings": {
				"alpn": ["h3"],
				"settings": {
					"fingerprint": "chrome",
					"pinnedPeerCertSha256": ["` + pin + `"]
				}
			}
		}`,
	}
	client := model.Client{Email: "client", Auth: "secret", Enable: true}

	var rawStream map[string]any
	if err := json.Unmarshal([]byte(inbound.StreamSettings), &rawStream); err != nil {
		t.Fatal(err)
	}
	proxy := svc.buildHysteriaProxy(subReq, inbound, client, rawStream, nil)
	if got := proxy["fingerprint"]; got != want {
		t.Fatalf("fingerprint = %v, want %s", got, want)
	}
	if got := proxy["client-fingerprint"]; got != "chrome" {
		t.Fatalf("client-fingerprint = %v, want chrome", got)
	}

	externalProxy := svc.buildHysteriaProxy(subReq, inbound, client, rawStream, map[string]any{
		"pinnedPeerCertSha256": []any{externalPin},
	})
	if got := externalProxy["fingerprint"]; got != wantExternal {
		t.Fatalf("external fingerprint = %v, want %s", got, wantExternal)
	}
}

func TestClashExternalVlessEncryption(t *testing.T) {
	svc := NewSubClashService(false, "", false, &SubService{})
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"encrypted", "&encryption=" + url.QueryEscape(testMlkemEncryption), testMlkemEncryption},
		{"trimmed", "&encryption=" + url.QueryEscape(" \t"+testMlkemEncryption+" \n"), testMlkemEncryption},
		{"none", "&encryption=none", ""},
		{"trimmed none", "&encryption=%20none%20", ""},
		{"empty", "&encryption=", ""},
		{"whitespace", "&encryption=%20%09", ""},
		{"missing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := svc.clashProxyFromExternal("vless://22222222-2222-4222-8222-222222222222@198.51.100.9:443?type=tcp&security=reality&sni=example.com&pbk=test-public-key&sid=ab12&fp=chrome&flow=xtls-rprx-vision"+tc.query+"#external", "external")
			if proxy == nil {
				t.Fatal("expected a VLESS proxy")
			}
			if got, exists := proxy["encryption"]; tc.want == "" {
				if exists {
					t.Errorf("plain VLESS encryption should be omitted, got %v", got)
				}
			} else if got != tc.want {
				t.Errorf("encryption = %v, want %q", got, tc.want)
			}
			for key, want := range map[string]any{
				"type": "vless", "uuid": "22222222-2222-4222-8222-222222222222",
				"server": "198.51.100.9", "port": 443, "network": "tcp",
				"flow": "xtls-rprx-vision", "tls": true, "servername": "example.com",
			} {
				if got := proxy[key]; got != want {
					t.Errorf("%s = %v, want %v", key, got, want)
				}
			}
			if _, exists := proxy["packet-encoding"]; exists {
				t.Error("VLESS encryption must not be exported as packet-encoding")
			}
		})
	}
}

func TestClashExternalShadowsocksMatchesInboundPath(t *testing.T) {
	const settings = `{"method":"aes-256-gcm","password":"inboundpw","clients":[{"password":"clientpw","email":"user"}]}`

	const userinfo = "YWVzLTI1Ni1nY206Y2xpZW50cHc"

	tests := []struct {
		name        string
		stream      string
		link        string
		wantDropped bool
	}{
		{
			name:   "plain tcp",
			stream: `{"network":"tcp","security":"none"}`,
			link:   "ss://" + userinfo + "@203.0.113.1:8443?type=tcp#ss",
		},
		{
			name:        "tcp http header",
			stream:      `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"http","request":{"path":["/"],"headers":{"Host":["test"]}}}}}`,
			link:        "ss://" + userinfo + "@203.0.113.1:8443?type=tcp&headerType=http&host=test#ss",
			wantDropped: true,
		},
		{
			name:   "tls",
			stream: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"ss.sni"}}`,
			link:   "ss://" + userinfo + "@203.0.113.1:8443?type=tcp&security=tls&sni=ss.sni#ss",
		},
	}

	svc := NewSubClashService(false, "", false, &SubService{})
	client := model.Client{Password: "clientpw", Email: "user"}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inbound := &model.Inbound{
				Listen:         "203.0.113.1",
				Port:           8443,
				Protocol:       model.Shadowsocks,
				Remark:         "ss",
				Settings:       settings,
				StreamSettings: tc.stream,
			}

			fromInbound := svc.buildProxy(svc.SubService, inbound, client, svc.streamData(tc.stream, ""), nil, nil)
			fromLink := svc.clashProxyFromExternal(tc.link, "external")

			if tc.wantDropped {
				if fromInbound != nil || fromLink != nil {
					t.Fatalf("not representable in clash, want both dropped: inbound %#v, link %#v", fromInbound, fromLink)
				}
				return
			}
			if fromInbound == nil || fromLink == nil {
				t.Fatalf("want a proxy from both paths: inbound %#v, link %#v", fromInbound, fromLink)
			}
			delete(fromInbound, "name")
			delete(fromLink, "name")
			if !reflect.DeepEqual(fromInbound, fromLink) {
				t.Fatalf("inbound %#v != link %#v", fromInbound, fromLink)
			}
		})
	}
}

func TestClashRealityIncludesMLKEM(t *testing.T) {
	svc := &SubClashService{}
	proxy := map[string]any{}
	if !svc.applySecurity(proxy, "reality", map[string]any{"realitySettings": map[string]any{"publicKey": "key", "shortId": "ab"}}) {
		t.Fatal("reality rejected")
	}
	opts := proxy["reality-opts"].(map[string]any)
	if opts["support-x25519mlkem768"] != true || proxy["client-fingerprint"] != "chrome" {
		t.Fatalf("reality settings=%v; want ML-KEM with default browser fingerprint", proxy)
	}
}

func TestExternalHysteriaKeepsConnectionParameters(t *testing.T) {
	uri := "hysteria2://secret@example.com:443?obfs=salamander&obfs-password=mask&mport=5000-6000&pinSHA256=000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f#external"
	proxy := (&SubClashService{}).clashProxyFromExternal(uri, "external")
	for key, want := range map[string]any{
		"obfs": "salamander", "obfs-password": "mask", "ports": "5000-6000",
		"fingerprint": "00:01:02:03:04:05:06:07:08:09:0A:0B:0C:0D:0E:0F:10:11:12:13:14:15:16:17:18:19:1A:1B:1C:1D:1E:1F",
	} {
		if proxy[key] != want {
			t.Errorf("%s=%v, want%v", key, proxy[key], want)
		}
	}
}
