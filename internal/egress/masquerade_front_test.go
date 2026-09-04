package egress

import (
	"strings"
	"testing"
)

/*
Traffic leaving on a front must not be translated.

A rule that sends an L3 inbound through a front makes the kernel forward the
packet out `peg<N>`, an xray-tun device. `nat postrouting` runs before that
device hands the packet to its userspace reader, so the plain rule rewrote the
source first and Xray read the front's own gateway address. Every routing rule
carrying a `source` criterion on such an inbound therefore matched nothing —
while the form offered the field, the compile accepted it, and it saved clean.

Measured in a network namespace rather than argued (the one step the finding
said could not be settled from source):

    ruleset   what the tun's reader saw
    none      10.66.1.2   the client's own address
    plain     10.66.2.1   the front device's address
    exempt    10.66.1.2   the client's address, restored

The exemption is also what the file already said was right: "This is NOT needed
for a routed inbound. A rule sends that traffic through a front, Xray
re-originates it." The rule was installed per-device regardless. F066.
*/

func TestAFrontIsExemptFromTranslation(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2"}, nil, []string{"peg9"})

	want := `iifname "pwg2" oifname != "pwg2" oifname != "peg9" masquerade`
	if !strings.Contains(got, want) {
		t.Fatalf("the rule must not translate traffic leaving on a front, so Xray reads the client's own source:\nwant %s\ngot:\n%s", want, got)
	}
}

// Every front is exempted from every device's rule: a front belongs to one
// ingress, so a packet crossing to another one is already somewhere it should
// not be, and translating it there would hide that rather than fix it.
func TestEveryFrontIsExemptFromEveryDevice(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2", "pwg7"}, nil, []string{"peg9", "peg4"})

	for _, want := range []string{
		`iifname "pwg2" oifname != "pwg2" oifname != "peg4" oifname != "peg9" masquerade`,
		`iifname "pwg7" oifname != "pwg7" oifname != "peg4" oifname != "peg9" masquerade`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\ngot:\n%s", want, got)
		}
	}
}

// Sorted, like the device list, so a reconciler comparing two applies of the
// same state sees the same text.
func TestFrontExemptionsAreOrderIndependent(t *testing.T) {
	first := masqueradeRuleset([]string{"pwg2"}, nil, []string{"peg9", "peg4"})
	second := masqueradeRuleset([]string{"pwg2"}, nil, []string{"peg4", "peg9"})
	if first != second {
		t.Fatalf("the same state produced two rulesets:\n%s\n---\n%s", first, second)
	}
}

/*
And a host with no fronts renders exactly what it rendered before.

Every deployment without a routing rule on an L3 inbound is in that state, so
this is the assertion that says the change costs them nothing.
*/
func TestWithNoFrontsTheRulesetIsUnchanged(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2", "pwg7"}, nil, nil)

	for _, want := range []string{
		`iifname "pwg2" oifname != "pwg2" masquerade`,
		`iifname "pwg7" oifname != "pwg7" masquerade`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a host with no fronts must render the original rule, got:\n%s", got)
		}
	}
	if strings.Contains(got, `oifname != "peg`) {
		t.Errorf("an exemption appeared for a front that does not exist:\n%s", got)
	}
}
