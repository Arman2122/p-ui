package routing

import (
	"testing"
)

/*
An operator's template does not have to carry a freedom or a blackhole outbound.

outboundsByProtocol answers "" when it holds neither, and a rule sent to direct
or block then compiled to `"outboundTag": ""` — which xray-core's
RoutingRule.Build refuses outright ("neither outboundTag nor balancerTag is
specified"). The whole config is rejected, so every client on the box goes off
because one rule named a destination the template no longer has. Refusing the
rule at compile time costs that one rule instead of the core.
*/
func TestARuleSentNowhereIsRefusedRatherThanEmitted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dest     string
		input    func(Input) Input
		protocol string
	}{
		{
			name: "no blackhole outbound in the template", dest: DestBlock, protocol: "blackhole",
			input: func(in Input) Input { in.Blackhole = ""; return in },
		},
		{
			name: "no freedom outbound in the template", dest: DestDirect, protocol: "freedom",
			input: func(in Input) Input { in.Direct = ""; return in },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input(baseInput(
				[]Subject{internalSubject(1, "vless")},
				[]Rule{{ID: 9, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: tc.dest}}},
				nil,
			))

			got := Plan(in)

			if len(got.XrayRules) != 0 {
				t.Fatalf("a rule was emitted with nowhere to send traffic: %s — xray refuses the whole config over it", got.XrayRules[0])
			}
			refusals := got.Refusals()
			if len(refusals) != 1 {
				t.Fatalf("want exactly one refusal naming the missing outbound, got %+v", refusals)
			}
			if key := refusals[0].MessageKey; key != KeyNoDefaultOutbound {
				t.Fatalf("MessageKey = %q, want %q", key, KeyNoDefaultOutbound)
			}
			if got, want := refusals[0].Args["protocol"], tc.protocol; got != want {
				t.Fatalf("the refusal names %q, want the outbound the template is missing, %q", got, want)
			}
		})
	}
}

/*
The same missing blackhole silently dropped the private-destination guard the
panel puts ahead of every L3 front — the one class of forwarded traffic that is
otherwise exempt from the template's own private block. The front still works,
so this warns rather than refusing; a refusal would take the ingress offline.
*/
func TestAFrontWithNoPrivateGuardSaysSo(t *testing.T) {
	in := baseInput(
		[]Subject{deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{{ID: 3, Enable: true, Scope: ScopeSelected, IngressIDs: []int{7}, Dest: Dest{Kind: DestOutbound, Tag: "direct"}}},
		map[int]int{7: 1},
	)
	in.Blackhole = ""

	got := Plan(in)

	if len(got.Refusals()) != 0 {
		t.Fatalf("the front is still routable and must not be refused: %+v", got.Refusals())
	}
	var warned bool
	for _, diag := range got.Diags {
		if diag.Severity == SeverityWarn && diag.MessageKey == KeyNoDefaultOutbound && diag.SubjectTag == "wg-home" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("the front lost its private-destination guard with no diagnostic: %+v", got.Diags)
	}
}

// The guard is still emitted where the template does carry a blackhole, which is
// what makes the warning above mean something.
func TestAFrontKeepsItsPrivateGuardWhenTheTemplateHasOne(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{{ID: 3, Enable: true, Scope: ScopeSelected, IngressIDs: []int{7}, Dest: Dest{Kind: DestOutbound, Tag: "direct"}}},
		map[int]int{7: 1},
	))

	var guarded bool
	for _, raw := range got.XrayRules {
		if decode(t, raw)["outboundTag"] == "blocked" {
			guarded = true
		}
	}
	if !guarded {
		t.Fatal("no private-destination guard was emitted ahead of the front")
	}
	for _, diag := range got.Diags {
		if diag.MessageKey == KeyNoDefaultOutbound {
			t.Fatalf("the template has a blackhole, so nothing should be reported: %+v", diag)
		}
	}
}
