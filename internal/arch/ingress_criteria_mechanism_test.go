package arch

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
)

/*
Every criterion a core declares must be one the panel can actually produce.

The criteria mask makes one promise: a rule the editor lets you write is a rule
that can match. A payload criterion -- domain, protocol, attrs -- is only
answerable where the panel has an Xray inbound to attach a sniffer to: the
inbound an internal ingress already answers to, or the xray-tun front the panel
synthesises for a device ingress. A per-session ingress (IngressSubnet, for the
PPP family and OpenConnect) has neither, so it can be routed by address and by
nothing else until the egress plane grows `from <prefix>` rules.

coretest catches this in the core author's own conformance run. This catches the
other direction: a change to InspectableSelector that orphans a core already in
the tree, which no single core's suite would ever see.

Add a shape to core.InspectableSelector the day the panel can mechanize it, and
this guard names every core that was waiting.
*/
func TestNoCoreDeclaresACriterionThePanelCannotProduce(t *testing.T) {
	kinds := cores.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no kinds are registered; this guard cannot mean anything")
	}
	for _, kind := range kinds {
		selector := cores.IngressSelectorFor(kind)
		if selector == core.IngressNone || core.InspectableSelector(selector) {
			continue
		}
		for _, criterion := range cores.IngressCriteriaFor(kind) {
			if !criterion.NeedsInspection() {
				continue
			}
			t.Errorf(
				"kind %q declares criterion %q, which can only be answered by reading the payload,\n"+
					"but its ingress selector %q gives the panel no inbound to attach a sniffer to.\n"+
					"A rule using it would save, display as active, and never match.\n"+
					"Either drop the criterion from that core's IngressCriteria, or teach\n"+
					"core.InspectableSelector how this shape is sniffed and build the mechanism.",
				kind, criterion, selector)
		}
	}
}

/*
And the other half: a kind the panel CAN sniff for must not be silently denied
the domain.

Not a hard failure -- a core may have a real protocol reason, as mtproto does:
its bridge carries Telegram's own wire format, so a sniffer finds nothing there.
This reports so the reason is a decision someone made rather than a line nobody
revisited.
*/
func TestAnInspectableKindWithoutDomainIsReported(t *testing.T) {
	for _, kind := range cores.Kinds() {
		if !core.InspectableSelector(cores.IngressSelectorFor(kind)) {
			continue
		}
		hasDomain := false
		for _, criterion := range cores.IngressCriteriaFor(kind) {
			if criterion == core.CriterionDomain {
				hasDomain = true
				break
			}
		}
		if hasDomain {
			continue
		}
		// mtproto is the one deliberate case; see its IngressCriteria comment.
		if kind == "mtproto" {
			continue
		}
		t.Errorf(
			"kind %q has an ingress shape the panel can sniff, but declares no domain criterion.\n"+
				"If its traffic genuinely carries no name, say so in its IngressCriteria comment\n"+
				"and add it to the exception in this guard.", kind)
	}
}
