package service

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
`attrs` cannot match on a fronted L3 ingress, so it must not be offered there.

The criteria mask exists to stop the panel offering a field that saves fine and
then never matches -- the comment above it says exactly that about `user`. But
`attrs` sat in the shared base, offered to every subject including a device one.

Xray matches attrs out of sniffed metadata, and the front inbound the panel
builds for an L3 ingress carries no sniffing block at all, so AttributeMatcher
returns false for every packet. An operator writing an attrs rule on such a
subject got a rule that saves, displays as active, and silently matches nothing
-- which is the entire class of bug this mask was added to remove.

Moving it into the group that already needs sniffing and identity makes the
existing refusal machinery cover it: forbiddenCriterion catches it, and the save
is refused with the message that already exists in both locales. F188.
*/
func TestAttrsIsNotOfferedToAnL3Subject(t *testing.T) {
	device := criteriaMaskFor(core.IngressHandle{Device: "pwg7"})
	if slices.Contains(device, "attrs") {
		t.Errorf("attrs is offered on a device ingress, where the front carries no sniffing block so it can never match: %v", device)
	}

	tagged := criteriaMaskFor(core.IngressHandle{Tag: "in-1"})
	if !slices.Contains(tagged, "attrs") {
		t.Errorf("attrs was removed from tagged subjects too, where it works: %v", tagged)
	}
	// The rest of the device mask is unchanged.
	for _, want := range []string{"ip", "port", "sourcePort", "network", "source"} {
		if !slices.Contains(device, want) {
			t.Errorf("the device mask lost %q", want)
		}
	}
}

/*
And an attrs pattern that will not compile must be refused at save.

xray-core stores an attrs pattern as a template string and compiles it only when
a packet arrives, so Build accepts anything. The validator's own comment claims
"one unreadable character here is a config the core refuses whole"; it did not.
An unclosed group saved cleanly and then failed inside the running router, on
traffic, with no way for the operator to connect the two.
*/
func TestAnUncompilableAttrsPatternIsRefusedAtSave(t *testing.T) {
	bad := map[string]json.RawMessage{
		"attrs": json.RawMessage(`{":path":"^/api/("}`),
	}
	if err := xray.ValidateRoutingCriteria(bad); err == nil {
		t.Fatal("an attrs pattern that cannot compile was accepted; it fails inside the running router instead, on traffic")
	}

	ok := map[string]json.RawMessage{
		"attrs": json.RawMessage(`{":path":"^/api/"}`),
	}
	if err := xray.ValidateRoutingCriteria(ok); err != nil {
		t.Fatalf("a valid attrs pattern was refused: %v", err)
	}
}
