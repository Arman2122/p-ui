package sub

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"
)

/*
A REALITY serverName the operator pinned must be the one every renderer emits.

realitySettings.settings.serverName is what the inbound form writes when an
operator names one SNI instead of leaving the serverNames list to speak. All
three Go renderers read straight past it and took from serverNames instead —
raw and JSON at random on every fetch, Clash the first entry. The frontend's own
builder has always preferred it, so the panel displayed one SNI while the
subscription handed out another, and a REALITY handshake offering the wrong
serverName is refused by the server. F023.
*/

const pinnedRealityStream = `{
	"network":"tcp","security":"reality",
	"tcpSettings":{"header":{"type":"none"}},
	"realitySettings":{
		"serverNames":["decoy-a.example.com","decoy-b.example.com","decoy-c.example.com"],
		"shortIds":["ab12cd"],
		"settings":{"publicKey":"PBKvalue","fingerprint":"chrome","serverName":"pinned.example.com"}
	}
}`

// The same inbound with the pin removed: nothing else differs, so a renderer
// that regresses to the list is visible as a change in exactly one field.
const unpinnedRealityStream = `{
	"network":"tcp","security":"reality",
	"tcpSettings":{"header":{"type":"none"}},
	"realitySettings":{
		"serverNames":["decoy-a.example.com","decoy-b.example.com","decoy-c.example.com"],
		"shortIds":["ab12cd"],
		"settings":{"publicKey":"PBKvalue","fingerprint":"chrome"}
	}
}`

var realityDecoys = []string{"decoy-a.example.com", "decoy-b.example.com", "decoy-c.example.com"}

func realitySettingsMap(t *testing.T, stream string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stream), &parsed); err != nil {
		t.Fatalf("parse stream: %v", err)
	}
	rData, _ := parsed["realitySettings"].(map[string]any)
	if rData == nil {
		t.Fatal("stream has no realitySettings")
	}
	return rData
}

// The raw link is the one an operator copies out of the panel, so a divergence
// here is the one they see first.
func TestRawRealityLinkHonoursThePinnedServerName(t *testing.T) {
	link := (&SubService{}).genVlessLink(shareLinkInbound(pinnedRealityStream), "user")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	if got := u.Query().Get("sni"); got != "pinned.example.com" {
		t.Fatalf("sni = %q, want the pinned pinned.example.com; the link offers a serverName the operator did not choose", got)
	}
}

func TestRawRealityLinkStillSpreadsAcrossTheListWhenNothingIsPinned(t *testing.T) {
	s := &SubService{}
	for range 20 {
		link := s.genVlessLink(shareLinkInbound(unpinnedRealityStream), "user")
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("link does not parse: %v\n got: %s", err, link)
		}
		if got := u.Query().Get("sni"); !slices.Contains(realityDecoys, got) {
			t.Fatalf("sni = %q, want a member of serverNames %v", got, realityDecoys)
		}
	}
}

func TestJsonRealityConfigHonoursThePinnedServerName(t *testing.T) {
	out := (&SubJsonService{}).realityData(realitySettingsMap(t, pinnedRealityStream), "clientKey")
	if got, _ := out["serverName"].(string); got != "pinned.example.com" {
		t.Fatalf("serverName = %q, want the pinned pinned.example.com", got)
	}
}

func TestJsonRealityConfigStillUsesTheListWhenNothingIsPinned(t *testing.T) {
	out := (&SubJsonService{}).realityData(realitySettingsMap(t, unpinnedRealityStream), "clientKey")
	got, _ := out["serverName"].(string)
	if !slices.Contains(realityDecoys, got) {
		t.Fatalf("serverName = %q, want a member of serverNames %v", got, realityDecoys)
	}
}

func TestClashRealityProxyHonoursThePinnedServerName(t *testing.T) {
	out := (&SubClashService{}).realityData(realitySettingsMap(t, pinnedRealityStream))
	if got, _ := out["serverName"].(string); got != "pinned.example.com" {
		t.Fatalf("serverName = %q, want the pinned pinned.example.com", got)
	}
}

func TestClashRealityProxyStillUsesTheListWhenNothingIsPinned(t *testing.T) {
	out := (&SubClashService{}).realityData(realitySettingsMap(t, unpinnedRealityStream))
	if got, _ := out["serverName"].(string); got != "decoy-a.example.com" {
		t.Fatalf("serverName = %q, want the first of serverNames; Clash has always been deterministic here", got)
	}
}
