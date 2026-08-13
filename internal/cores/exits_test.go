package cores

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The multi-core property, asserted rather than hoped for.

A caller asks the registry which kinds can terminate a route and gets an answer
without naming one; adding openvpn or ikev2 later is a Register line and a
driver, never an edit to a switch somewhere in the service layer. The second
half is the honesty check: a kind that claims it can be an exit has to offer a
handle somebody can actually route to.
*/
func TestRegistryAnswersExitsWithoutNamingACore(t *testing.T) {
	var exits []core.Kind
	for _, kind := range Kinds() {
		bound, ok := kindOwners().For(kind)
		if !ok || bound.Egress == nil {
			continue
		}
		if bound.Egress.ExitHandleKind(kind) != core.ExitNone {
			exits = append(exits, kind)
		}
	}
	if len(exits) == 0 {
		t.Fatal("no kind can be an exit; adding a driver must not require editing a switch")
	}
	t.Logf("kinds that can terminate a route today: %v", exits)

	for _, kind := range exits {
		bound, _ := kindOwners().For(kind)
		h, err := bound.Egress.ExitHandle(context.Background(), core.Exit{ID: 9, Kind: kind, Enable: true})
		if err != nil {
			t.Fatalf("ExitHandle(%s): %v", kind, err)
		}
		if h.Device == "" && h.SocksPort == 0 && h.XrayTag == "" {
			t.Errorf("%s declares it can be an exit but offers no handle", kind)
		}
	}
}
