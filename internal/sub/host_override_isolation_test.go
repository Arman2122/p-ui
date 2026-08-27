package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
One host row's overrides leaked into every host row after it.

getProxies and the JSON renderer build a working stream per endpoint with
cloneStreamForExternalProxy, then fold that endpoint's host into it. The clone
was shallow apart from tlsSettings, but the fold writes deep: a host's path and
Host header go into wsSettings/xhttpSettings, and its SNI goes into
realitySettings. Those maps were shared, so the first host that overrode
anything wrote it into the stream every later host was about to read.

An operator with three CDN host rows got a Clash config where rows two and three
carried row one's path and Host header. On a CDN that routes by Host that does
not reach the inbound at all, and it looked like a server fault rather than a
subscription one, because the raw links beside it were correct: the raw path
resolves each host into its own params map before the fan-out and never touches
the shared stream.
*/

func hostIsolationInbound(network, security, extra, hosts string) *model.Inbound {
	return &model.Inbound{
		Id: 1, Tag: "t", Enable: true, Listen: "203.0.113.10", Port: 443,
		Protocol: model.VLESS, Remark: "R",
		Settings: `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"u@x","subId":"s1"}],"decryption":"none"}`,
		StreamSettings: `{"network":"` + network + `","security":"` + security + `",` + extra +
			`,"externalProxy":[` + hosts + `]}`,
	}
}

func clashProxies(t *testing.T, ib *model.Inbound) []map[string]any {
	t.Helper()
	svc := &SubClashService{SubService: &SubService{}}
	client := model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "u@x", SubID: "s1"}
	got := svc.getProxies(svc.SubService, ib, client, "fallback.example")
	if len(got) != 3 {
		t.Fatalf("want three proxies, one per host row, got %d", len(got))
	}
	return got
}

const threeHostsOneOverridesTransport = `{"isHost":true,"forceTls":"same","dest":"cdn-a.example","port":443,"remark":"A","path":"/a-path","hostHeader":"a.example"},` +
	`{"isHost":true,"forceTls":"same","dest":"cdn-b.example","port":443,"remark":"B"},` +
	`{"isHost":true,"forceTls":"same","dest":"cdn-c.example","port":443,"remark":"C"}`

const threeHostsOneOverridesSNI = `{"isHost":true,"forceTls":"same","dest":"cdn-a.example","port":443,"remark":"A","sni":"a.override.example"},` +
	`{"isHost":true,"forceTls":"same","dest":"cdn-b.example","port":443,"remark":"B"},` +
	`{"isHost":true,"forceTls":"same","dest":"cdn-c.example","port":443,"remark":"C"}`

const realityStreamBody = `"realitySettings":{"dest":"origin.example:443","serverNames":["origin.example"],` +
	`"shortIds":["ab12cd34"],"settings":{"publicKey":"PBK","fingerprint":"chrome"}}`

// A host that pins nothing keeps the inbound's own transport, whatever the host
// before it pinned.
func TestAHostsPathDoesNotLeakIntoTheNextHost(t *testing.T) {
	ib := hostIsolationInbound("ws", "tls",
		`"tlsSettings":{"serverName":"origin.example","settings":{"fingerprint":"chrome"}},`+
			`"wsSettings":{"path":"/origin-path","host":"origin.example"}`,
		threeHostsOneOverridesTransport)

	proxies := clashProxies(t, ib)

	first, _ := proxies[0]["ws-opts"].(map[string]any)
	if first["path"] != "/a-path" {
		t.Fatalf("the host that DID override lost its own path: %v", first)
	}

	for _, i := range []int{1, 2} {
		opts, _ := proxies[i]["ws-opts"].(map[string]any)
		if opts["path"] != "/origin-path" {
			t.Errorf("proxy %d took the first host's path: ws-opts = %v", i+1, opts)
		}
		headers, _ := opts["headers"].(map[string]any)
		if headers["Host"] != "origin.example" {
			t.Errorf("proxy %d took the first host's Host header: %v", i+1, headers)
		}
	}
}

// Same defect, reality's SNI. Kept separate because it travels through a
// different key and would survive a fix that only deep-copied the transport.
func TestAHostsRealitySNIDoesNotLeakIntoTheNextHost(t *testing.T) {
	ib := hostIsolationInbound("tcp", "reality", realityStreamBody, threeHostsOneOverridesSNI)

	proxies := clashProxies(t, ib)

	if proxies[0]["servername"] != "a.override.example" {
		t.Fatalf("the host that DID override lost its own SNI: %v", proxies[0]["servername"])
	}
	for _, i := range []int{1, 2} {
		if proxies[i]["servername"] != "origin.example" {
			t.Errorf("proxy %d took the first host's SNI: servername = %v", i+1, proxies[i]["servername"])
		}
	}
}

// The JSON subscription walks the same endpoint loop and had the same leak.
func TestAHostsOverrideDoesNotLeakInTheJSONSubscription(t *testing.T) {
	ib := hostIsolationInbound("tcp", "reality", realityStreamBody, threeHostsOneOverridesSNI)

	svc := &SubJsonService{SubService: &SubService{}}
	client := model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "u@x", SubID: "s1"}
	configs := svc.getConfig(svc.SubService, ib, client, "fallback.example")
	if len(configs) != 3 {
		t.Fatalf("want three configs, one per host row, got %d", len(configs))
	}

	overridden := 0
	for i, raw := range configs {
		if strings.Contains(string(raw), `"serverName": "a.override.example"`) {
			overridden++
			if i != 0 {
				t.Errorf("config %d carries the first host's SNI", i+1)
			}
		}
	}
	if overridden != 1 {
		t.Errorf("the first host's SNI appears in %d of 3 configs, want 1", overridden)
	}
}

// The clone is what holds all three, so pin it directly: mutating a copy must
// not reach the original, at any depth.
func TestCloningAStreamForOneEndpointReachesEveryLevel(t *testing.T) {
	var stream map[string]any
	if err := json.Unmarshal([]byte(`{
		"network":"ws",
		"wsSettings":{"path":"/orig","headers":{"Host":"orig.example"}},
		"realitySettings":{"serverNames":["orig.example"],"settings":{"fingerprint":"chrome"}},
		"tlsSettings":{"serverName":"orig.example","settings":{"fingerprint":"chrome"}}
	}`), &stream); err != nil {
		t.Fatalf("seed: %v", err)
	}

	clone := cloneStreamForExternalProxy(stream)
	clone["wsSettings"].(map[string]any)["path"] = "/mutated"
	clone["wsSettings"].(map[string]any)["headers"].(map[string]any)["Host"] = "mutated.example"
	clone["realitySettings"].(map[string]any)["serverNames"] = []any{"mutated.example"}
	clone["realitySettings"].(map[string]any)["settings"].(map[string]any)["fingerprint"] = "safari"
	clone["tlsSettings"].(map[string]any)["settings"].(map[string]any)["fingerprint"] = "safari"

	ws, _ := stream["wsSettings"].(map[string]any)
	if ws["path"] != "/orig" {
		t.Errorf("wsSettings.path was reached through the clone: %v", ws["path"])
	}
	if hdr, _ := ws["headers"].(map[string]any); hdr["Host"] != "orig.example" {
		t.Errorf("wsSettings.headers was reached through the clone: %v", hdr)
	}
	rs, _ := stream["realitySettings"].(map[string]any)
	if names, _ := rs["serverNames"].([]any); len(names) != 1 || names[0] != "orig.example" {
		t.Errorf("realitySettings.serverNames was reached through the clone: %v", rs["serverNames"])
	}
	if inner, _ := rs["settings"].(map[string]any); inner["fingerprint"] != "chrome" {
		t.Errorf("realitySettings.settings was reached through the clone: %v", inner)
	}
	if ts, _ := stream["tlsSettings"].(map[string]any); ts != nil {
		if inner, _ := ts["settings"].(map[string]any); inner["fingerprint"] != "chrome" {
			t.Errorf("tlsSettings.settings was reached through the clone: %v", inner)
		}
	}
}
