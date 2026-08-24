package routing

import (
	"testing"
)

/*
An operator's template does not have to carry a freedom or a blackhole outbound.

outboundsByProtocol answers "" when it holds neither, and a rule sent to direct
or block then compiled to `"outboundTag": ""` — which xray-core's
RoutingRule.Build refuses outright. The whole config is rejected, so every
client on the box goes off because one rule named a destination the template no
longer has.

The two halves are answered differently, and deliberately. Both land fail-closed:

  - block, with no blackhole: SYNTHESIZE one. A blackhole has no egress, so
    supplying it cannot leak anything, and refusing instead would silently stop
    a rule the operator explicitly marked Block from blocking.
  - direct, with no freedom: REFUSE. A template with no freedom outbound is
    plausibly the policy "nothing leaves this box directly", and synthesizing
    one would punch a hole through it. Refusing leaves the traffic to the
    operator's own template, which is never more permissive than what they set.

This file used to assert that block was refused too. That was the smaller fix,
and it fails open: Refusals() still has no production caller and
KeyNoDefaultOutbound is in neither locale, so the dropped rule was silent.
*/
func TestABlockRuleGetsTheBlackholeItsTemplateLacks(t *testing.T) {
	got := Plan(withoutOutbound(baseInput(
		[]Subject{internalSubject(1, "vless")},
		[]Rule{{ID: 9, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestBlock}}},
		nil,
	), DestBlock))

	if len(got.Refusals()) != 0 {
		t.Fatalf("a block rule is realisable without the operator's help, got refusals %+v", got.Refusals())
	}
	if len(got.XrayRules) != 1 {
		t.Fatalf("want the one block rule, got %d", len(got.XrayRules))
	}
	if tag := decode(t, got.XrayRules[0])["outboundTag"]; tag != synthBlockTag {
		t.Fatalf("outboundTag = %v, want the synthesized %q — an empty tag is what xray refuses", tag, synthBlockTag)
	}
	if len(got.Outbounds) != 1 {
		t.Fatalf("want exactly one synthesized outbound, got %d: a tag with no handler is refused just as hard as an empty one", len(got.Outbounds))
	}
	if p := decode(t, got.Outbounds[0])["protocol"]; p != "blackhole" {
		t.Fatalf("the synthesized outbound is %v, want blackhole — anything else is an egress the operator did not ask for", p)
	}
}

// One outbound however many rules need it: two handlers on one tag is a config
// xray-core refuses to load, which is the same outage by another route.
func TestTheSynthesizedBlackholeIsEmittedOnce(t *testing.T) {
	got := Plan(withoutOutbound(baseInput(
		[]Subject{internalSubject(1, "vless"), internalSubject(2, "vmess")},
		[]Rule{
			{ID: 9, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestBlock}},
			{ID: 10, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestBlock}},
		},
		nil,
	), DestBlock))

	if len(got.Outbounds) != 1 {
		t.Fatalf("the blackhole was synthesized %d times; xray refuses a config with two handlers on one tag", len(got.Outbounds))
	}
}

/*
The direct half, unchanged: refused, and the refusal names what the template is
missing so a later consumer of Refusals() can say it.
*/
func TestADirectRuleWithNoFreedomOutboundIsRefused(t *testing.T) {
	got := Plan(withoutOutbound(baseInput(
		[]Subject{internalSubject(1, "vless")},
		[]Rule{{ID: 9, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestDirect}}},
		nil,
	), DestDirect))

	if len(got.XrayRules) != 0 {
		t.Fatalf("a rule was emitted with nowhere to send traffic: %s", got.XrayRules[0])
	}
	if len(got.Outbounds) != 0 {
		t.Fatalf("a freedom outbound was synthesized: %s — that punches through a deliberate no-direct-egress policy", got.Outbounds[0])
	}
	refusals := got.Refusals()
	if len(refusals) != 1 || refusals[0].MessageKey != KeyNoDefaultOutbound {
		t.Fatalf("want one refusal naming the missing outbound, got %+v", refusals)
	}
	if got, want := refusals[0].Args["protocol"], "freedom"; got != want {
		t.Fatalf("the refusal names %q, want %q", got, want)
	}
}

/*
The L3 front's private-destination guard is the one thing standing between a
fronted WireGuard-family ingress and the box's own private ranges — every other
class of forwarded traffic is covered by the template's own private block. It
used to be skipped whenever the template had no blackhole. It is now synthesized
like any other, because "we could not find an outbound" is not a reason to stop
guarding.
*/
func TestAFrontKeepsItsPrivateGuardWithoutATemplateBlackhole(t *testing.T) {
	got := Plan(withoutOutbound(baseInput(
		[]Subject{deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{{ID: 3, Enable: true, Scope: ScopeSelected, IngressIDs: []int{7}, Dest: Dest{Kind: DestOutbound, Tag: "direct"}}},
		map[int]int{7: 1},
	), DestBlock))

	var guarded bool
	for _, raw := range got.XrayRules {
		rule := decode(t, raw)
		if rule["outboundTag"] == synthBlockTag && rule["ip"] != nil {
			guarded = true
		}
	}
	if !guarded {
		t.Fatalf("the front was emitted with no private-destination guard: %v", got.XrayRules)
	}
	if len(got.Outbounds) != 1 {
		t.Fatalf("the guard names a tag with %d handlers behind it, want 1", len(got.Outbounds))
	}
}

// The template's own outbound still wins where it has one, so nothing is
// synthesized on the ordinary path.
func TestNothingIsSynthesizedWhenTheTemplateHasABlackhole(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{internalSubject(1, "vless")},
		[]Rule{{ID: 9, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestBlock}}},
		nil,
	))

	if len(got.Outbounds) != 0 {
		t.Fatalf("the template carries a blackhole and one was synthesized anyway: %s", got.Outbounds[0])
	}
	if tag := decode(t, got.XrayRules[0])["outboundTag"]; tag != "blocked" {
		t.Fatalf("outboundTag = %v, want the template's own %q", tag, "blocked")
	}
}

func withoutOutbound(in Input, kind string) Input {
	if kind == DestBlock {
		in.Blackhole = ""
	} else {
		in.Direct = ""
	}
	return in
}
