package singbox

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
One of these inbounds is offered to the routing editor only when its daemon's
traffic actually reaches Xray, and is told why when it does not.

Offering the tag while the switch is off is the F060 shape: the rule saves, the
editor lists it as active, and no packet ever matches it.
*/
func TestIngressHandleAnswersOnlyWhenTheBridgeIsUp(t *testing.T) {
	c := New(Deps{})
	const certs = `"certFile":"/c.pem","keyFile":"/k.pem"`
	for _, tc := range []struct {
		name     string
		kind     core.Kind
		settings string
		wantTag  string
		wantKey  string
	}{
		{"tuic, switch off", KindTUIC, `{` + certs + `}`, "", blockedBridgeOff},
		{"tuic, no port yet", KindTUIC, `{` + certs + `,"routeThroughXray":true}`, "", blockedBridgeOff},
		{"tuic, bridged", KindTUIC, `{` + certs + `,"routeThroughXray":true,"routeXrayPort":2402}`, "sb-1", ""},
		{"anytls, bridged", KindAnyTLS, `{` + certs + `,"routeThroughXray":true,"routeXrayPort":2403}`, "sb-1", ""},
		{"unparsable", KindTUIC, `{`, "", blockedBridgeOff},
		{"no certificate", KindTUIC, `{"routeThroughXray":true,"routeXrayPort":2404}`, "", blockedBridgeOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle, err := c.IngressHandle(t.Context(), core.Instance{ID: 1, Kind: tc.kind, Tag: "sb-1", Settings: tc.settings})
			if err != nil {
				t.Fatalf("IngressHandle: %v", err)
			}
			if handle.Tag != tc.wantTag || handle.BlockedKey != tc.wantKey {
				t.Fatalf("handle = {tag %q blocked %q}, want {tag %q blocked %q}", handle.Tag, handle.BlockedKey, tc.wantTag, tc.wantKey)
			}
		})
	}
	for _, kind := range []core.Kind{KindTUIC, KindAnyTLS} {
		if got := c.IngressSelector(kind); got != core.IngressInternal {
			t.Fatalf("selector for %s = %q, want internal", kind, got)
		}
	}
	if got := c.IngressSelector("vless"); got != core.IngressNone {
		t.Fatalf("this core answered for a kind it does not serve: %q", got)
	}
}

// The criteria are the ones a rule can match through the bridge; source and
// source port would save and never match, since every connection arrives from
// the daemon on loopback.
func TestIngressCriteriaExcludeWhatTheBridgeCannotCarry(t *testing.T) {
	got := New(Deps{}).IngressCriteria(KindTUIC)
	for _, want := range []core.Criterion{core.CriterionIP, core.CriterionPort, core.CriterionNetwork, core.CriterionDomain} {
		if !slices.Contains(got, want) {
			t.Errorf("criteria %v lack %q", got, want)
		}
	}
	for _, unwanted := range []core.Criterion{core.CriterionSource, core.CriterionSourcePort, core.CriterionUser} {
		if slices.Contains(got, unwanted) {
			t.Errorf("criteria %v offer %q, which the bridge cannot carry", got, unwanted)
		}
	}
	if New(Deps{}).IngressCriteria("vless") != nil {
		t.Fatal("this core answered criteria for a kind it does not serve")
	}
}

/*
The generated config sends the daemon's own traffic where the switch says, and
refuses to send it anywhere when it cannot send it through the bridge.

Falling back to direct in that window would put every client of the inbound on
the server's own address at the moment the operator asked for otherwise, and
the daemon would start cleanly and say nothing.
*/
func TestTheConfigRoutesThroughTheBridgeOrRefuses(t *testing.T) {
	const certs = `"certFile":"/c.pem","keyFile":"/k.pem"`
	inst := core.Instance{ID: 4, Kind: KindTUIC, Tag: "tuic-4", Port: 8443, Settings: `{` + certs + `}`}
	ctl := control{apiPort: 9001, statsPort: 9002, secret: "s"}

	direct := renderOrFail(t, inst, `{`+certs+`}`, ctl)
	if final := direct["route"].(map[string]any)["final"]; final != "direct" {
		t.Fatalf("an unrouted inbound leaves through %q", final)
	}
	if strings.Contains(mustJSON(t, direct), xrayBridgeTag) {
		t.Fatalf("an unrouted inbound carries a bridge outbound:\n%s", mustJSON(t, direct))
	}

	bridged := renderOrFail(t, inst, `{`+certs+`,"routeThroughXray":true,"routeXrayPort":2402}`, ctl)
	if final := bridged["route"].(map[string]any)["final"]; final != xrayBridgeTag {
		t.Fatalf("a routed inbound leaves through %q, want the bridge", final)
	}
	var bridge map[string]any
	for _, raw := range bridged["outbounds"].([]any) {
		out := raw.(map[string]any)
		if out["tag"] == xrayBridgeTag {
			bridge = out
		}
	}
	if bridge == nil {
		t.Fatalf("no bridge outbound:\n%s", mustJSON(t, bridged))
	}
	if bridge["type"] != "socks" || bridge["server"] != "127.0.0.1" || bridge["server_port"] != float64(2402) {
		t.Fatalf("the bridge outbound is not a loopback socks client: %v", bridge)
	}

	settings, err := parseSettings(KindTUIC, `{`+certs+`,"routeThroughXray":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderConfig(inst, settings, ctl, "/c.pem", "/k.pem"); err == nil {
		t.Fatal("a config was rendered for an inbound that routes through Xray with no bridge port; the daemon would start and go direct")
	}
}

func renderOrFail(t *testing.T, inst core.Instance, raw string, ctl control) map[string]any {
	t.Helper()
	settings, err := parseSettings(inst.Kind, raw)
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	body, err := renderConfig(inst, settings, ctl, "/c.pem", "/k.pem")
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the generated config is not JSON: %v", err)
	}
	return doc
}

func mustJSON(t *testing.T, doc map[string]any) string {
	t.Helper()
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
