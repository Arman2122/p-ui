package routing

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

/*
A domain rule makes its subject's traffic readable, whatever shape that subject
has.

Xray matches a domain from the sniffer, and the sniffer runs on an Xray inbound.
So the question for every ingress shape is which inbound carries the block: for
a device ingress the xray-tun front the panel synthesises, for an internal one
the inbound the core already answers to.

Neither happened. FrontSpec.Sniff was produced and never read, Subject.Inspect
was never populated, and a real Xray inbound defaults to sniffing OFF -- so a
domain rule on a VLESS inbound saved, displayed as active, and never matched.
That is F060 on the kinds people actually use.
*/

func domainRule(id int) Rule {
	return Rule{
		ID: id, Enable: true, Scope: ScopeAll,
		Criteria: map[string]json.RawMessage{"domain": json.RawMessage(`["google.com"]`)},
		Dest:     Dest{Kind: DestDirect},
	}
}

func addressRule(id int) Rule {
	return Rule{
		ID: id, Enable: true, Scope: ScopeAll,
		Criteria: map[string]json.RawMessage{"ip": json.RawMessage(`["8.8.8.8"]`)},
		Dest:     Dest{Kind: DestDirect},
	}
}

// Built on plan_test.go's helpers, with the criteria mask this feature widened.
func wgSubject() Subject {
	s := deviceSubject(7, "wg-home", "pwg7")
	s.CriteriaMask = []string{"ip", "port", "sourcePort", "network", "source", "domain"}
	return s
}

func vlessSubject() Subject {
	s := internalSubject(3, "vless-in")
	s.CriteriaMask = []string{"ip", "port", "domain", "user"}
	return s
}

func planWith(rules []Rule, subjects []Subject) Compiled {
	return Plan(Input{
		Rules: rules, Subjects: subjects,
		Blackhole: "blocked", Direct: "direct",
		FrontIDFor: func(int) (int, bool) { return 40, true },
	})
}

func TestADomainRuleMakesAnL3FrontSniff(t *testing.T) {
	out := planWith([]Rule{domainRule(1)}, []Subject{wgSubject()})
	if len(out.Fronts) != 1 {
		t.Fatalf("want one front, got %d", len(out.Fronts))
	}
	if !out.Fronts[0].Sniff {
		t.Fatal("a domain rule on a kernel-device ingress left its front without a sniffing block; the rule saves and never matches")
	}
}

// An address rule needs no payload read, and a front that sniffs for nothing
// pays a cached reader on every connection.
func TestAnAddressRuleLeavesTheFrontClean(t *testing.T) {
	out := planWith([]Rule{addressRule(1)}, []Subject{wgSubject()})
	if len(out.Fronts) != 1 {
		t.Fatalf("want one front, got %d", len(out.Fronts))
	}
	if out.Fronts[0].Sniff {
		t.Fatal("a front sniffs for a rule that only matches addresses")
	}
}

func TestADomainRuleMarksAnInternalInboundForInspection(t *testing.T) {
	out := planWith([]Rule{domainRule(1)}, []Subject{vlessSubject()})
	if !slices.Contains(out.InspectTags, "vless-in") {
		t.Fatalf("a domain rule on an Xray inbound did not ask for it to sniff (tags: %v); the panel's own default is sniffing off, so the rule never matches", out.InspectTags)
	}
}

func TestAnAddressRuleLeavesAnInternalInboundAlone(t *testing.T) {
	out := planWith([]Rule{addressRule(1)}, []Subject{vlessSubject()})
	if len(out.InspectTags) != 0 {
		t.Fatalf("an address rule asked an inbound to sniff: %v", out.InspectTags)
	}
}

// A disabled rule asks for nothing: it is not compiled, so it must not turn
// inspection on for the subject either.
func TestADisabledDomainRuleAsksForNothing(t *testing.T) {
	rule := domainRule(1)
	rule.Enable = false
	out := planWith([]Rule{rule}, []Subject{wgSubject(), vlessSubject()})
	for _, front := range out.Fronts {
		if front.Sniff {
			t.Fatal("a disabled rule turned sniffing on")
		}
	}
	if len(out.InspectTags) != 0 {
		t.Fatalf("a disabled rule marked inbounds for inspection: %v", out.InspectTags)
	}
}

/*
The manual flag still works, because the diagnostics use it: an operator can
force a front to sniff to see what the router would match, without writing a
domain rule to find out.
*/
func TestTheInspectFlagForcesSniffingWithoutADomain(t *testing.T) {
	rule := addressRule(1)
	rule.Inspect = true
	out := planWith([]Rule{rule}, []Subject{wgSubject()})
	if len(out.Fronts) != 1 || !out.Fronts[0].Sniff {
		t.Fatal("the explicit inspect flag did not turn sniffing on")
	}
}

/*
And the front's containment guard is unchanged by any of it.

The guard matches `ip: geoip:private`, and the sniffing block the service layer
injects sets routeOnly so the dial target keeps its IP. Were the target rewritten
to the sniffed name, a rule pointing at a private address would have no IP for
this guard to see and would slip past the one protection a front is not exempt
from. The guard is asserted here; routeOnly is asserted where it is written.
*/
func TestTheFrontGuardSurvivesInspection(t *testing.T) {
	out := planWith([]Rule{domainRule(1)}, []Subject{wgSubject()})
	found := false
	for _, raw := range out.XrayRules {
		if strings.Contains(string(raw), "geoip:private") {
			found = true
		}
	}
	if !found {
		t.Fatal("the front's private-address guard is gone from an inspected front")
	}
}
