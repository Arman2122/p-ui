package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/link"
)

/*
The panel must be able to re-import its own share link.

The generator here and the parser in internal/util/link are the two halves of
one contract, and nothing compared them. buildXhttpExtra writes every
client-side SplitHTTPConfig field into the link; the parser read four of them
from query params and three from a vmess object, and dropped verifyPeerCertByName
in both. So an operator exporting a tuned XHTTP outbound from one panel and
importing it into another got a stripped one that reports success — and vcn is
how xray-core replaced allowInsecure, so losing it fails the TLS handshake
rather than degrading it. F081.

Asserted as a round trip through both real functions, because "the two lists
agree" is a property that a list-comparison test would restate rather than test.
*/

// Every knob buildXhttpExtra is willing to emit, at a non-default value: the
// two it filters as xray-core defaults would round-trip vacuously otherwise.
const roundTripXhttpStream = `{
	"network":"xhttp","security":"tls",
	"tlsSettings":{
		"serverName":"rt.example.com","alpn":["h2","http/1.1"],
		"settings":{
			"fingerprint":"firefox",
			"echConfigList":"ECHBLOB",
			"verifyPeerCertByName":"real-cert.example.com",
			"pinnedPeerCertSha256":["4a5b6c"]
		}
	},
	"xhttpSettings":{
		"path":"/rt","host":"rt.host","mode":"packet-up",
		"xPaddingBytes":"7-77",
		"xPaddingObfsMode":true,
		"xPaddingKey":"pk","xPaddingHeader":"ph","xPaddingPlacement":"pp","xPaddingMethod":"pm",
		"uplinkHTTPMethod":"PUT",
		"sessionIDPlacement":"sp","sessionIDKey":"sk","sessionIDTable":"st","sessionIDLength":"12",
		"seqPlacement":"qp","seqKey":"qk",
		"uplinkDataPlacement":"up","uplinkDataKey":"uk",
		"scMaxEachPostBytes":"2000000","scMinPostsIntervalMs":"55",
		"uplinkChunkSize":"64-128",
		"noGRPCHeader":true,
		"xmux":{"maxConcurrency":"8-16"},
		"downloadSettings":{"address":"dl.example.com","port":8443}
	}
}`

func roundTripInbound(protocol model.Protocol, settings string) *model.Inbound {
	return &model.Inbound{
		Listen:         "203.0.113.4",
		Port:           443,
		Protocol:       protocol,
		Remark:         "roundtrip",
		Settings:       settings,
		StreamSettings: roundTripXhttpStream,
	}
}

func parsedStreamSub(t *testing.T, raw string, key string) map[string]any {
	t.Helper()
	if raw == "" {
		t.Fatal("the generator produced no link")
	}
	res, err := link.ParseLink(raw)
	if err != nil {
		t.Fatalf("the panel cannot parse its own link: %v\n got: %s", err, raw)
	}
	stream, _ := res.Outbound["streamSettings"].(map[string]any)
	if stream == nil {
		t.Fatalf("parsed outbound has no streamSettings: %#v", res.Outbound)
	}
	sub, _ := stream[key].(map[string]any)
	if sub == nil {
		t.Fatalf("parsed stream has no %s: %#v", key, stream)
	}
	return sub
}

// wantXhttp is what the inbound above declares, keyed as the outbound stores it.
var wantXhttp = map[string]any{
	"mode":                 "packet-up",
	"xPaddingBytes":        "7-77",
	"xPaddingObfsMode":     true,
	"xPaddingKey":          "pk",
	"xPaddingHeader":       "ph",
	"xPaddingPlacement":    "pp",
	"xPaddingMethod":       "pm",
	"uplinkHTTPMethod":     "PUT",
	"sessionIDPlacement":   "sp",
	"sessionIDKey":         "sk",
	"sessionIDTable":       "st",
	"sessionIDLength":      "12",
	"seqPlacement":         "qp",
	"seqKey":               "qk",
	"uplinkDataPlacement":  "up",
	"uplinkDataKey":        "uk",
	"scMaxEachPostBytes":   "2000000",
	"scMinPostsIntervalMs": "55",
	"uplinkChunkSize":      "64-128",
	"noGRPCHeader":         true,
}

func assertXhttpRoundTrip(t *testing.T, xh map[string]any) {
	t.Helper()
	for key, want := range wantXhttp {
		if got := xh[key]; got != want {
			t.Errorf("xhttpSettings[%q] = %#v, want %#v", key, got, want)
		}
	}
	for _, key := range []string{"xmux", "downloadSettings"} {
		if obj, _ := xh[key].(map[string]any); len(obj) == 0 {
			t.Errorf("xhttpSettings[%q] did not survive the round trip: %#v", key, xh[key])
		}
	}
}

func assertTLSRoundTrip(t *testing.T, tls map[string]any) {
	t.Helper()
	for key, want := range map[string]any{
		"serverName":           "rt.example.com",
		"fingerprint":          "firefox",
		"echConfigList":        "ECHBLOB",
		"verifyPeerCertByName": "real-cert.example.com",
		"pinnedPeerCertSha256": "4a5b6c",
	} {
		if got := tls[key]; got != want {
			t.Errorf("tlsSettings[%q] = %#v, want %#v", key, got, want)
		}
	}
}

func TestVlessLinkSurvivesItsOwnParser(t *testing.T) {
	in := roundTripInbound(model.VLESS,
		`{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"decryption":"none","encryption":"none"}`)
	raw := (&SubService{}).genVlessLink(in, "user")
	assertXhttpRoundTrip(t, parsedStreamSub(t, raw, "xhttpSettings"))
	assertTLSRoundTrip(t, parsedStreamSub(t, raw, "tlsSettings"))
}

func TestVmessLinkSurvivesItsOwnParser(t *testing.T) {
	in := roundTripInbound(model.VMESS,
		`{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}]}`)
	raw := (&SubService{}).genVmessLink(in, "user")
	assertXhttpRoundTrip(t, parsedStreamSub(t, raw, "xhttpSettings"))
	assertTLSRoundTrip(t, parsedStreamSub(t, raw, "tlsSettings"))
}

// A knob the client never reads must stay out of the imported outbound. This one
// fences the key list above from growing; it passes before the fix too, and is
// not evidence for it.
func TestServerOnlyXhttpKnobsAreNotImported(t *testing.T) {
	raw := "vless://11111111-2222-4333-8444-555555555555@h.example:443" +
		"?type=xhttp&security=none&mode=auto" +
		"&noSSEHeader=true&scMaxBufferedPosts=99&scStreamUpServerSecs=5-10&serverMaxHeaderBytes=8192#r"
	xh := parsedStreamSub(t, raw, "xhttpSettings")
	for _, key := range []string{"noSSEHeader", "scMaxBufferedPosts", "scStreamUpServerSecs", "serverMaxHeaderBytes"} {
		if _, present := xh[key]; present {
			t.Errorf("imported the server-only knob %q into a client outbound", key)
		}
	}
}
