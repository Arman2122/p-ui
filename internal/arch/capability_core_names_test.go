package arch

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
)

/*
The capability table names cores, and nothing made it keep up.

CapSniffing is a NEGATIVE rule — "any protocol not in {mtproto, wgkernel}" —
because sniffing is an Xray inbound block and applies to no kind Xray does not
serve. Its own comment says "List each new such core", which is a requirement
nothing enforced: a core added without that edit inherits the default, so the
inbound form offers a sniffing toggle written into a config the core never
reads. The operator sets it, the panel saves it, and it does nothing.

The table has to stay pure data — internal/core/capability.go is emitted into a
golden fixture the frontend replays, so it cannot ask the registry while
evaluating. This is the enforcement instead, and it asks the registry the same
way the rest of the panel does: cores.ServedByXray.
*/
func TestSniffingExcludesEveryKindXrayDoesNotServe(t *testing.T) {
	kinds := cores.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no kinds are registered; this guard cannot mean anything")
	}
	for _, kind := range kinds {
		if cores.ServedByXray(kind) {
			continue
		}
		if core.Can(core.CapSniffing, core.Facts{Protocol: string(kind)}) {
			t.Errorf(
				"kind %q is served by a core other than xray, but the capability table still allows sniffing on it.\n"+
					"Sniffing is an Xray inbound block, so the form would offer a toggle nothing reads.\n"+
					"Add %q to the CapSniffing exclusion in internal/core/capability.go.",
				kind, kind)
		}
	}
}

// The exclusion must not widen past what it is for: a kind Xray does serve keeps
// sniffing, or a real Xray inbound quietly loses a real control.
func TestSniffingStaysAvailableOnXraysOwnKinds(t *testing.T) {
	for _, kind := range cores.Kinds() {
		if !cores.ServedByXray(kind) {
			continue
		}
		if !core.Can(core.CapSniffing, core.Facts{Protocol: string(kind)}) {
			t.Errorf("kind %q is served by xray but sniffing is excluded for it", kind)
		}
	}
}
