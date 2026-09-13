package routing

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
"Every routable inbound" expanded to every inbound: a blocked subject (an
MTProto inbound with its bridge off, the shipped default) refused every scope-all
rule at save, naming an inbound the operator never picked, and so did a mask
narrower than the rule's criteria. The all-rule now covers what can carry it.
*/
func TestAScopeAllRuleSkipsWhatCannotCarryIt(t *testing.T) {
	blocked := internalSubject(2, "mt-off")
	blocked.Handle = core.IngressHandle{BlockedKey: "pages.xray.subjects.reasonBridgeOff"}
	narrow := internalSubject(4, "mt-on")
	narrow.Bridged = true
	narrow.CriteriaMask = []string{"ip", "port", "network"}
	rule := Rule{ID: 1, Enable: true, Scope: ScopeAll, Criteria: map[string]json.RawMessage{"domain": json.RawMessage(`["geosite:category-ads-all"]`)}, Dest: Dest{Kind: DestBlock}}
	got := Plan(Input{Subjects: []Subject{vlessSubject(), blocked, narrow}, Rules: []Rule{rule}, Blackhole: "blocked", Direct: "direct"})
	if refusals := got.Refusals(); len(refusals) != 0 {
		t.Fatalf("a scope-all rule was refused for %+v; the inbounds that cannot carry it must be skipped", refusals)
	}
	if warnings := got.Warnings(); len(warnings) != 2 {
		t.Fatalf("warnings = %+v; want one per inbound the all-rule does not cover, so the list still says so", warnings)
	}
	rules := 0
	for _, raw := range got.XrayRules {
		var xr struct {
			InboundTag []string `json:"inboundTag"`
			Domain     []string `json:"domain"`
		}
		if json.Unmarshal(raw, &xr) == nil && len(xr.Domain) == 1 {
			rules++
			if len(xr.InboundTag) != 1 || xr.InboundTag[0] != "vless-in" {
				t.Fatalf("the all-rule compiled for %v; want vless-in alone", xr.InboundTag)
			}
		}
	}
	if rules != 1 {
		t.Fatalf("%d domain rules compiled; want the one for the inbound that can carry it", rules)
	}
}
