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

The unpinned pick is no longer random either — every renderer now derives one
member per client and holds it. Ledger L17.
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

func TestRawRealityLinkStillUsesTheListWhenNothingIsPinned(t *testing.T) {
	s := &SubService{}
	first := ""
	for i := range 20 {
		link := s.genVlessLink(shareLinkInbound(unpinnedRealityStream), "user")
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("link does not parse: %v\n got: %s", err, link)
		}
		got := u.Query().Get("sni")
		if !slices.Contains(realityDecoys, got) {
			t.Fatalf("sni = %q, want a member of serverNames %v", got, realityDecoys)
		}
		// Twenty renders of one client's link, all identical. Ledger L17.
		if i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("render %d gave sni %q after %q; the same client's link moved between fetches", i+1, got, first)
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
	out := (&SubClashService{}).realityData(realitySettingsMap(t, pinnedRealityStream), "clientKey")
	if got, _ := out["serverName"].(string); got != "pinned.example.com" {
		t.Fatalf("serverName = %q, want the pinned pinned.example.com", got)
	}
}

// Clash was always deterministic here and still is, but the value is now the
// client's own rather than index 0 — so it agrees with the raw and JSON links
// that same client holds. Ledger L17.
func TestClashRealityProxyStillUsesTheListWhenNothingIsPinned(t *testing.T) {
	out := (&SubClashService{}).realityData(realitySettingsMap(t, unpinnedRealityStream), "clientKey")
	got, _ := out["serverName"].(string)
	if !slices.Contains(realityDecoys, got) {
		t.Fatalf("serverName = %q, want a member of serverNames %v", got, realityDecoys)
	}
	again := (&SubClashService{}).realityData(realitySettingsMap(t, unpinnedRealityStream), "clientKey")
	if second, _ := again["serverName"].(string); second != got {
		t.Fatalf("the same client got %q then %q; Clash has always been deterministic here", got, second)
	}
}
