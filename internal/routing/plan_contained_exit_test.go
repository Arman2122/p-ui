package routing

import (
	"testing"
)

/*
The row said marked and the planner believed it: with no ip rule catching the
mark (a failed add, a device that never came up) the marked socket left via
main with the server's own address. A contained exit now compiles to the
blackhole and warns; the config is rebuilt when the host routes it again.
*/
func TestAContainedDeviceExitCompilesToTheBlackhole(t *testing.T) {
	exit := deviceExit(3)
	exit.Contained = true
	got := Plan(Input{
		Subjects:  []Subject{internalSubject(1, "a-in")},
		Rules:     []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 3}}},
		Exits:     []ResolvedExit{exit},
		Blackhole: "blocked",
	})
	if len(got.XrayRules) != 1 {
		t.Fatalf("rules = %d, want the one rule, contained", len(got.XrayRules))
	}
	if rule := decode(t, got.XrayRules[0]); rule["outboundTag"] != "blocked" {
		t.Fatalf("rule = %v; want the blackhole, not a marked outbound nothing catches", rule)
	}
	if len(got.Outbounds) != 0 {
		t.Fatalf("outbounds = %s; a contained exit must emit no marked outbound", got.Outbounds)
	}
	if len(got.Diags) != 1 || got.Diags[0].Severity != SeverityWarn || got.Diags[0].MessageKey != KeyExitContained {
		t.Fatalf("diags = %+v, want one warning that the exit is contained", got.Diags)
	}
	if refusals := got.Refusals(); len(refusals) != 0 {
		t.Fatalf("a contained exit refused the save: %+v", refusals)
	}
}
