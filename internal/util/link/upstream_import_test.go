package link

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"testing"
)

func salamanderPassword(t *testing.T, res *ParseResult) (string, bool) {
	t.Helper()
	stream, ok := res.Outbound["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("missing streamSettings: %v", res.Outbound)
	}
	finalmask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		return "", false
	}
	udp, ok := finalmask["udp"].([]any)
	if !ok {
		return "", false
	}
	for _, m := range udp {
		mask, _ := m.(map[string]any)
		if mask == nil || mask["type"] != "salamander" {
			continue
		}
		settings, _ := mask["settings"].(map[string]any)
		pw, _ := settings["password"].(string)
		return pw, true
	}
	return "", false
}

func finalmaskUDP(t *testing.T, res *ParseResult) []any {
	t.Helper()
	stream, _ := res.Outbound["streamSettings"].(map[string]any)
	finalmask, _ := stream["finalmask"].(map[string]any)
	udp, _ := finalmask["udp"].([]any)
	return udp
}

func hopMask(t *testing.T, res *ParseResult) (map[string]any, bool) {
	t.Helper()
	for _, rawMask := range finalmaskUDP(t, res) {
		mask, _ := rawMask.(map[string]any)
		if maskType, _ := mask["type"].(string); maskType == "udphop" {
			settings, _ := mask["settings"].(map[string]any)
			return settings, true
		}
	}
	return nil, false
}

func hopPorts(t *testing.T, res *ParseResult) (string, bool) {
	t.Helper()
	settings, ok := hopMask(t, res)
	if !ok {
		return "", false
	}
	ports, _ := settings["remotePorts"].(string)
	return ports, true
}

func TestParseHysteria2_Obfs(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		wantPw  string
		wantSet bool
	}{
		{"standard", "obfs=salamander&obfs-password=s3cr3t", "s3cr3t", true},
		{"snake-case alias", "obfs=salamander&obfs_password=aliaspw", "aliaspw", true},
		{"camel-case alias", "obfs=salamander&obfsPassword=camelpw", "camelpw", true},
		{"case-insensitive type", "obfs=Salamander&obfs-password=mixed", "mixed", true},
		{"no obfs", "sni=ex.com", "", false},
		{"obfs without password", "obfs=salamander", "", false},
		{"unknown obfs type", "obfs=random&obfs-password=x", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ParseLink("hysteria2://auth@1.2.3.4:443?security=tls&" + c.query + "#node")
			if err != nil {
				t.Fatalf("parse hysteria2: %v", err)
			}
			if res.Outbound["protocol"] != "hysteria" {
				t.Fatalf("bad protocol: %v", res.Outbound["protocol"])
			}
			pw, ok := salamanderPassword(t, res)
			if ok != c.wantSet {
				t.Fatalf("salamander mask present = %v, want %v (stream: %v)", ok, c.wantSet, res.Outbound["streamSettings"])
			}
			if pw != c.wantPw {
				t.Errorf("salamander password: got %q, want %q", pw, c.wantPw)
			}
		})
	}
}

func TestParseHysteria2_ObfsFinalMaskPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		fm         string
		obfsPw     string
		wantPw     string
		wantUDPLen int
	}{
		{
			name:       "fm password wins over obfs",
			fm:         `{"udp":[{"type":"salamander","settings":{"password":"fromfm"}}]}`,
			obfsPw:     "fromobfs",
			wantPw:     "fromfm",
			wantUDPLen: 1,
		},
		{
			name:       "obfs fills password-less fm mask",
			fm:         `{"udp":[{"type":"salamander","settings":{}}]}`,
			obfsPw:     "fromobfs",
			wantPw:     "fromobfs",
			wantUDPLen: 1,
		},
		{
			name:       "obfs appends alongside a non-salamander mask",
			fm:         `{"udp":[{"type":"mkcp-legacy","settings":{"header":"srtp"}}]}`,
			obfsPw:     "fromobfs",
			wantPw:     "fromobfs",
			wantUDPLen: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link := "hysteria2://auth@1.2.3.4:443?security=tls&fm=" + url.QueryEscape(c.fm) +
				"&obfs=salamander&obfs-password=" + c.obfsPw + "#node"
			res, err := ParseLink(link)
			if err != nil {
				t.Fatalf("parse hysteria2: %v", err)
			}
			pw, ok := salamanderPassword(t, res)
			if !ok {
				t.Fatalf("salamander mask missing: %v", res.Outbound["streamSettings"])
			}
			if pw != c.wantPw {
				t.Errorf("salamander password: got %q, want %q", pw, c.wantPw)
			}
			if udp := finalmaskUDP(t, res); len(udp) != c.wantUDPLen {
				t.Errorf("udp mask count: got %d, want %d (%v)", len(udp), c.wantUDPLen, udp)
			}
		})
	}
}

func TestParseHysteria2_Mport(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantPorts string
		wantHop   bool
	}{
		{"standard mport", "mport=20000-50000", "20000-50000", true},
		{"no mport", "sni=ex.com", "", false},
		{
			name:      "fm udphop mask wins over mport",
			query:     "mport=1-2&fm=" + url.QueryEscape(`{"udp":[{"type":"udphop","settings":{"mode":"intervalremote","interval":"7-9","remotePorts":"30000-40000"}}]}`),
			wantPorts: "30000-40000",
			wantHop:   true,
		},
		{
			name:      "legacy fm hop is migrated before mport fallback",
			query:     "mport=1-2&fm=" + url.QueryEscape(`{"quicParams":{"udpHop":{"ports":"30000-40000","interval":"7-9"}}}`),
			wantPorts: "30000-40000",
			wantHop:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ParseLink("hysteria2://auth@1.2.3.4:443?security=tls&" + c.query + "#node")
			if err != nil {
				t.Fatalf("parse hysteria2: %v", err)
			}
			ports, ok := hopPorts(t, res)
			if ok != c.wantHop {
				t.Fatalf("udpHop present = %v, want %v (stream: %v)", ok, c.wantHop, res.Outbound["streamSettings"])
			}
			if ports != c.wantPorts {
				t.Errorf("hop ports: got %q, want %q", ports, c.wantPorts)
			}
		})
	}
}

func TestParseShadowsocksTLSQueryRoundTrip(t *testing.T) {
	user := base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secretpass"))
	link := "ss://" + user + "@example.com:443?alpn=h2%2Chttp%2F1.1&fp=firefox&security=tls&sni=example.com&type=tcp#user"
	res, err := ParseLink(link)
	if err != nil {
		t.Fatalf("parse ss tls: %v", err)
	}
	srv := res.Outbound["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)
	if srv["address"] != "example.com" || srv["port"] != 443 {
		t.Fatalf("server = %v", srv)
	}
	if srv["method"] != "chacha20-ietf-poly1305" || srv["password"] != "secretpass" {
		t.Fatalf("creds = %v", srv)
	}
	stream, ok := res.Outbound["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("missing streamSettings: %v", res.Outbound)
	}
	if stream["network"] != "tcp" {
		t.Errorf("network = %v, want tcp", stream["network"])
	}
	if stream["security"] != "tls" {
		t.Errorf("security = %v, want tls", stream["security"])
	}
	tls, ok := stream["tlsSettings"].(map[string]any)
	if !ok {
		t.Fatalf("missing tlsSettings: %v", stream)
	}
	if tls["serverName"] != "example.com" {
		t.Errorf("sni = %v, want example.com", tls["serverName"])
	}
	if tls["fingerprint"] != "firefox" {
		t.Errorf("fp = %v, want firefox", tls["fingerprint"])
	}
	alpn, _ := tls["alpn"].([]string)
	if len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "http/1.1" {
		t.Errorf("alpn = %v, want [h2 http/1.1]", alpn)
	}
}

func TestParseShadowsocksObfsLocalPlugin(t *testing.T) {
	user := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secretpass"))
	const httpObfs = "obfs-local;obfs=http;obfs-host=obfs.example.com"
	for _, tc := range []struct {
		name, query, wantHeader, wantHost string
	}{
		{"http obfs becomes the tcp header", "plugin=" + url.QueryEscape(httpObfs), "http", "obfs.example.com"},
		{"unencoded separators map the same way", "plugin=" + httpObfs, "http", "obfs.example.com"},
		{"tls obfs has no xray header", "plugin=" + url.QueryEscape("obfs-local;obfs=tls"), "none", ""},
		{"an unrelated plugin is left alone", "plugin=v2ray-plugin", "none", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseLink("ss://" + user + "@1.2.3.4:8388/?" + tc.query + "#node")
			if err != nil {
				t.Fatalf("parse ss: %v", err)
			}
			raw, err := json.Marshal(res.Outbound["streamSettings"])
			if err != nil {
				t.Fatalf("marshal stream: %v", err)
			}
			var stream map[string]any
			_ = json.Unmarshal(raw, &stream)
			tcp, _ := stream["tcpSettings"].(map[string]any)
			header, _ := tcp["header"].(map[string]any)
			if header == nil || header["type"] != tc.wantHeader {
				t.Fatalf("header = %v, want type %q", header, tc.wantHeader)
			}
			request, _ := header["request"].(map[string]any)
			headers, _ := request["headers"].(map[string]any)
			hosts, _ := headers["Host"].([]any)
			got := ""
			if len(hosts) > 0 {
				got, _ = hosts[0].(string)
			}
			if got != tc.wantHost {
				t.Errorf("host = %q, want %q", got, tc.wantHost)
			}
		})
	}
}

func TestLinkIdentityKeepsTLSServerName(t *testing.T) {
	a, errA := ParseLink("vless://uuid@1.2.3.4:443?type=ws&security=tls&sni=a.example.com#node")
	b, errB := ParseLink("vless://uuid@1.2.3.4:443?type=ws&security=tls&sni=b.example.com#node")
	if errA != nil || errB != nil {
		t.Fatalf("parse vless: %v, %v", errA, errB)
	}
	if a.Identity == b.Identity {
		t.Fatalf("TLS links for different SNIs share identity %q", a.Identity)
	}
}

type mkcpMask struct{ header, value string }

func mkcpLegacyMasks(t *testing.T, res *ParseResult) []mkcpMask {
	t.Helper()
	var out []mkcpMask
	for _, raw := range finalmaskUDP(t, res) {
		mask, _ := raw.(map[string]any)
		if mask["type"] != "mkcp-legacy" {
			t.Fatalf("unexpected udp mask %#v", mask)
		}
		settings, _ := mask["settings"].(map[string]any)
		header, _ := settings["header"].(string)
		value, _ := settings["value"].(string)
		out = append(out, mkcpMask{header, value})
	}
	return out
}

func TestParse_KcpShareParams(t *testing.T) {
	// The emitter flattens one mkcp-legacy mask per field into headerType/seed; a merged
	// mask drops the seed in xray-core (MkcpLegacy.Build), so import rebuilds them separately.
	cases := []struct {
		name      string
		link      string
		wantMTU   int
		wantTTI   int
		wantMasks []mkcpMask
	}{
		{
			name:      "vless header and seed become two masks, seed first",
			link:      "vless://uuid@h.com:443?type=kcp&headerType=wechat-video&seed=secret-seed&mtu=1400&tti=50&security=none#kcp1",
			wantMTU:   1400,
			wantTTI:   50,
			wantMasks: []mkcpMask{{"", "secret-seed"}, {"wechat", ""}},
		},
		{
			name:      "trojan header only adds no seed mask",
			link:      "trojan://pw@h.com:443?type=kcp&headerType=srtp&security=none#kcp-tj",
			wantMTU:   1350,
			wantTTI:   20,
			wantMasks: []mkcpMask{{"srtp", ""}},
		},
		{
			name:      "seed only adds no header mask",
			link:      "vless://uuid@h.com:443?type=kcp&headerType=none&seed=abc123&security=none",
			wantMTU:   1350,
			wantTTI:   20,
			wantMasks: []mkcpMask{{"", "abc123"}},
		},
		{
			name:    "mtu/tti outside KCPConfig.Build bounds keep the defaults",
			link:    "vless://uuid@h.com:443?type=kcp&mtu=10&tti=5000&security=none",
			wantMTU: 1350,
			wantTTI: 20,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ParseLink(c.link)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			kcp := streamSub(t, res, "kcpSettings")
			if kcp["mtu"] != c.wantMTU || kcp["tti"] != c.wantTTI {
				t.Fatalf("kcpSettings mtu/tti = %v/%v, want %d/%d", kcp["mtu"], kcp["tti"], c.wantMTU, c.wantTTI)
			}
			if got := mkcpLegacyMasks(t, res); !slices.Equal(got, c.wantMasks) {
				t.Fatalf("mkcp-legacy masks = %v, want %v", got, c.wantMasks)
			}
		})
	}
}

func TestSubscriptionIdentitySurvivesRealityRotation(t *testing.T) {
	before := "vless://uuid@host:443?security=reality&pbk=key&sid=aa&sni=a.example&spx=one#first"
	after := "vless://uuid@host:443?security=reality&pbk=key&sid=bb&sni=b.example&spx=two#second"
	_, oldIDs, err := ParseSubscriptionBody([]byte(before + "\n" + before))
	if err != nil {
		t.Fatal(err)
	}
	_, newIDs, err := ParseSubscriptionBody([]byte(after + "\n" + after))
	if err != nil {
		t.Fatal(err)
	}
	if len(oldIDs) != 2 || len(newIDs) != 2 || oldIDs[0] != newIDs[0] || oldIDs[1] != newIDs[1] || oldIDs[0] == oldIDs[1] {
		t.Fatalf("identities before %v, after %v; want stable and distinct entries", oldIDs, newIDs)
	}
}

func TestShadowsocksPluginKeepsExplicitTransport(t *testing.T) {
	parsed, err := ParseLink("ss://YWVzLTI1Ni1nY206cHc@example.com:443?type=ws&path=%2Fproxy&plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dfront.example")
	if err != nil {
		t.Fatal(err)
	}
	stream := parsed.Outbound["streamSettings"].(map[string]any)
	if stream["network"] != "ws" {
		t.Fatalf("network=%v, want explicit ws", stream["network"])
	}
}

func TestVmessImportPreservesExportedModeAndMask(t *testing.T) {
	raw := `{"add":"example.com","port":443,"id":"uuid","net":"xhttp","type":"stream-one","fm":"{\"tcp\":[{\"type\":\"header-http\",\"settings\":{}}]}"}`
	result, err := ParseLink("vmess://" + base64.StdEncoding.EncodeToString([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	stream := result.Outbound["streamSettings"].(map[string]any)
	xhttp := stream["xhttpSettings"].(map[string]any)
	if xhttp["mode"] != "stream-one" {
		t.Errorf("mode=%v, want stream-one", xhttp["mode"])
	}
	mask, _ := stream["finalmask"].(map[string]any)
	tcp, _ := mask["tcp"].([]any)
	if len(tcp) != 1 {
		t.Errorf("finalmask=%v, want exported header mask", mask)
	}
}
