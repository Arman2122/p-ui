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

/*
A rule the subject cannot match must not make it sniff.

The two are decided in different places: forbiddenCriterion refuses the rule as
inert further down, and the inspection question is asked before that. So a
domain rule aimed at an mtproto ingress -- or at a future per-session core that
routes by address alone -- was correctly refused AND still put a sniffer on
every connection through that inbound, to answer a question no rule would ever
read. A cost paid forever, for nothing.

Found by probing the mixed-fleet case rather than by the feature's own tests,
which is why both shapes are pinned here.
*/
func TestAnInertDomainRuleDoesNotMakeAnInternalInboundSniff(t *testing.T) {
	subject := internalSubject(9, "mt-in")
	subject.CriteriaMask = []string{"ip", "port"}

	out := planWith([]Rule{domainRule(1)}, []Subject{subject})
	if len(out.InspectTags) != 0 {
		t.Fatalf("sniffing was turned on for a subject whose core cannot match a domain: %v", out.InspectTags)
	}
	if len(out.Refusals()) == 0 {
		t.Fatal("the rule was not reported inert either, so the operator learns nothing")
	}
}

func TestAnInertDomainRuleDoesNotMakeAnL3FrontSniff(t *testing.T) {
	subject := deviceSubject(5, "ppp-in", "ppp5")
	subject.CriteriaMask = []string{"ip", "port"}

	out := planWith([]Rule{domainRule(1)}, []Subject{subject})
	if len(out.Fronts) != 1 {
		t.Fatalf("want one front, got %d", len(out.Fronts))
	}
	if out.Fronts[0].Sniff {
		t.Fatal("a per-session core's front sniffs for a rule that can never match on it")
	}
}

/*
And the whole fleet at once, which is the question a multi-core panel has to
answer: five cores, one domain rule, and each core gets what its own shape can
deliver.

wgkernel and a device-ingress core that lands later (OpenVPN, IKEv2) sniff at
their fronts. An Xray inbound sniffs in place. mtproto and a per-session core
are told the rule is inert and are left alone. Every front keeps the
private-address guard it is not exempt from.
*/
func TestOneDomainRuleAcrossAMixedFleet(t *testing.T) {
	wg := deviceSubject(1, "wg-home", "pwg1")
	wg.CriteriaMask = []string{"ip", "port", "domain"}
	mt := internalSubject(2, "mt-in")
	mt.CriteriaMask = []string{"ip", "port"}
	vless := internalSubject(3, "vless-in")
	vless.CriteriaMask = []string{"ip", "port", "domain", "user"}
	ovpn := deviceSubject(4, "ovpn-in", "pov4")
	ovpn.CriteriaMask = []string{"ip", "port", "domain"}
	ppp := deviceSubject(5, "ppp-in", "ppp5")
	ppp.CriteriaMask = []string{"ip", "port"}

	fronts := map[int]int{1: 41, 4: 44, 5: 45}
	out := Plan(Input{
		Rules: []Rule{domainRule(1)}, Subjects: []Subject{wg, mt, vless, ovpn, ppp},
		Blackhole: "blocked", Direct: "direct",
		FrontIDFor: func(id int) (int, bool) { front, ok := fronts[id]; return front, ok },
	})

	if !slices.Equal(out.InspectTags, []string{"vless-in"}) {
		t.Errorf("InspectTags = %v, want only the Xray inbound that can match a domain", out.InspectTags)
	}
	wantSniff := map[int]bool{41: true, 44: true, 45: false}
	for _, front := range out.Fronts {
		if want, known := wantSniff[front.ID]; known && front.Sniff != want {
			t.Errorf("front %d sniff = %v, want %v", front.ID, front.Sniff, want)
		}
	}
	guards := 0
	for _, raw := range out.XrayRules {
		if strings.Contains(string(raw), "geoip:private") {
			guards++
		}
	}
	if guards != len(out.Fronts) {
		t.Errorf("%d private-address guards for %d fronts; a front without one is the single class of forwarded traffic exempt from the template's private block", guards, len(out.Fronts))
	}
}
