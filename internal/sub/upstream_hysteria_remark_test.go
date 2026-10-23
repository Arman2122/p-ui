package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/link"
)

func TestGenHysteriaLinkEmitsGeckoParamsForPacketSize(t *testing.T) {
	in := &model.Inbound{
		Id: 920001, Listen: "203.0.113.1", Port: 443, Protocol: model.Hysteria,
		Settings: `{"version":2,"clients":[{"auth":"secret","email":"user"}]}`,
		StreamSettings: `{"security":"tls","finalmask":{"udp":[{"type":"salamander","settings":` +
			`{"password":"pw","packetSize":"512-1200"}}]}}`,
	}
	got := (&SubService{}).genHysteriaLink(in, "user")
	for _, want := range []string{"obfs=gecko", "obfs-password=pw", "minPacketSize=512", "maxPacketSize=1200"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n got: %s", want, got)
		}
	}
	if strings.Contains(got, "obfs=salamander") {
		t.Fatalf("gecko mask exported as plain salamander:\n %s", got)
	}
	if strings.Contains(got, "fm=") {
		t.Fatalf("expressed salamander mask must not leak into fm= dump:\n %s", got)
	}
}

func TestGenHysteriaLinkSalamanderWithoutPacketSizeUnchanged(t *testing.T) {
	in := &model.Inbound{
		Id: 920002, Listen: "203.0.113.1", Port: 443, Protocol: model.Hysteria,
		Settings:       `{"version":2,"clients":[{"auth":"secret","email":"user"}]}`,
		StreamSettings: `{"security":"tls","finalmask":{"udp":[{"type":"salamander","settings":{"password":"pw"}}]}}`,
	}
	got := (&SubService{}).genHysteriaLink(in, "user")
	if !strings.Contains(got, "obfs=salamander") || !strings.Contains(got, "obfs-password=pw") {
		t.Fatalf("password-only mask lost its standard export:\n %s", got)
	}
	for _, bad := range []string{"minPacketSize=", "maxPacketSize="} {
		if strings.Contains(got, bad) {
			t.Fatalf("unexpected %s in:\n %s", bad, got)
		}
	}
}

func TestParseLinkAcceptsGeckoObfs(t *testing.T) {
	parsed, err := link.ParseLink(
		"hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=512&maxPacketSize=1200#geo")
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	rawStream, _ := parsed.Outbound["streamSettings"].(map[string]any)
	if rawStream == nil {
		t.Fatalf("no streamSettings in outbound: %v", parsed.Outbound)
	}
	streamJSON, err := json.Marshal(rawStream)
	if err != nil {
		t.Fatalf("marshal stream: %v", err)
	}
	var stream map[string]any
	if err := json.Unmarshal(streamJSON, &stream); err != nil {
		t.Fatalf("stream json: %v", err)
	}
	fm, _ := stream["finalmask"].(map[string]any)
	if fm == nil {
		t.Fatalf("no finalmask rebuilt: %s", streamJSON)
	}
	udp, _ := fm["udp"].([]any)
	var mask map[string]any
	for _, m := range udp {
		if mm, ok := m.(map[string]any); ok && mm["type"] == "salamander" {
			mask = mm
		}
	}
	if mask == nil {
		t.Fatalf("no salamander mask rebuilt: %s", streamJSON)
	}
	settings, _ := mask["settings"].(map[string]any)
	if pw, _ := settings["password"].(string); pw != "pw" {
		t.Fatalf("password = %v", settings["password"])
	}
	if ps, _ := settings["packetSize"].(string); ps != "512-1200" {
		t.Fatalf("packetSize = %v, want 512-1200", settings["packetSize"])
	}
}

func TestParseLinkRejectsInvalidGeckoPacketSize(t *testing.T) {
	cases := map[string]string{
		"half min only": "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=512#geo",
		"half max only": "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&maxPacketSize=1200#geo",
		"non-numeric":   "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=abc&maxPacketSize=def#geo",
		"zero min":      "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=0&maxPacketSize=1200#geo",
		"inverted":      "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=1200&maxPacketSize=512#geo",
		"over cap":      "hysteria2://secret@203.0.113.1:443?security=tls&obfs=gecko&obfs-password=pw&minPacketSize=512&maxPacketSize=4096#geo",
	}
	for name, uri := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, err := link.ParseLink(uri)
			if err != nil {
				t.Fatalf("ParseLink: %v", err)
			}
			rawStream, _ := parsed.Outbound["streamSettings"].(map[string]any)
			streamJSON, _ := json.Marshal(rawStream)
			var stream map[string]any
			_ = json.Unmarshal(streamJSON, &stream)
			fm, _ := stream["finalmask"].(map[string]any)
			if fm == nil {
				t.Fatalf("no finalmask rebuilt: %s", streamJSON)
			}
			udp, _ := fm["udp"].([]any)
			for _, m := range udp {
				if mm, ok := m.(map[string]any); ok && mm["type"] == "salamander" {
					settings, _ := mm["settings"].(map[string]any)
					if ps, _ := settings["packetSize"].(string); ps != "" {
						t.Fatalf("invalid gecko stored packetSize %q", ps)
					}
				}
			}
		})
	}
}

func TestSharedSubIDRemark_FullInfoOncePerSubscription(t *testing.T) {
	const tmpl = "{{INBOUND}}-{{EMAIL}}"
	s := &SubService{
		remarkTemplate:   tmpl,
		subscriptionBody: true,
		usageShown:       map[string]bool{},
	}
	first := model.Client{Email: "first@example", SubID: "shared-sub"}
	second := model.Client{Email: "second@example", SubID: "shared-sub"}
	if got := s.genTemplatedRemark(&model.Inbound{Remark: "DE"}, first, "", "tcp"); got != "DE-first@example" {
		t.Fatalf("first credential remark = %q", got)
	}
	if got := s.genTemplatedRemark(&model.Inbound{Remark: "FI"}, second, "", "tcp"); got != "FI" {
		t.Fatalf("second credential with shared subId remark = %q, want identity suppressed", got)
	}
}
