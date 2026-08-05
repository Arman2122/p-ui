package mtproto

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// coreServing builds a core whose engine reports these tags as routed.
func coreServing(routed map[string]bool) *Core {
	return &Core{routedTags: func() map[string]bool { return routed }}
}

/*
An mtproto inbound's total is the sum of its clients', because mtg meters per
secret and nothing else — but only when the inbound egresses directly.

With routeThroughXray the bytes leave through Xray's loopback bridge, which is
tagged with the inbound's own tag and metered there. Billing them here as well
doubles that inbound's total.
*/
func TestRoutedInboundsAreNotBilledTwice(t *testing.T) {
	c := coreServing(map[string]bool{"bridged": true})

	c.stashTagTraffic([]engine.Traffic{
		{Email: "a@x", Tag: "direct", Up: 10, Down: 20},
		{Email: "b@x", Tag: "direct", Up: 5, Down: 6},
		{Email: "c@x", Tag: "bridged", Up: 1000, Down: 2000},
	})

	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	want := core.TagDelta{Tag: "direct", Up: 15, Down: 26}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want only %+v — the bridged tag is already billed by xray", got, want)
	}
}

/*
The routed set is read from the engine on every scrape, and this is why.

It used to be a map the core filled as it applied instances. Nothing calls this
core's Reconcile — the job drives the engine manager directly — so after a panel
restart that map was empty until a human edited the inbound, and an empty one
bills every routed inbound twice for as long as it lasts.
*/
func TestRoutedSetIsReReadEveryScrape(t *testing.T) {
	routed := map[string]bool{}
	c := &Core{routedTags: func() map[string]bool { return routed }}

	// The engine has not reported it yet — the old bug billed here.
	routed = map[string]bool{"in-1": true}
	c.stashTagTraffic([]engine.Traffic{{Email: "a@x", Tag: "in-1", Up: 9, Down: 9}})
	if got, _ := c.CollectTagTraffic(t.Context()); len(got) != 0 {
		t.Fatalf("got %+v while routed, want nothing", got)
	}

	// The admin turns routing off; the next scrape must bill it again.
	routed = map[string]bool{}
	c.stashTagTraffic([]engine.Traffic{{Email: "a@x", Tag: "in-1", Up: 3, Down: 4}})
	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	if len(got) != 1 || got[0].Up != 3 || got[0].Down != 4 {
		t.Fatalf("got %+v, want only the bytes accrued since it went direct", got)
	}
}

// Same drain-once rule as the xray core: these are deltas.
func TestMtprotoTagTrafficDrainsOnce(t *testing.T) {
	c := coreServing(nil)
	c.stashTagTraffic([]engine.Traffic{{Email: "a@x", Tag: "in-1", Up: 1, Down: 2}})

	if got, _ := c.CollectTagTraffic(t.Context()); len(got) != 1 {
		t.Fatalf("first drain = %+v, want one delta", got)
	}
	if again, _ := c.CollectTagTraffic(t.Context()); len(again) != 0 {
		t.Errorf("second drain = %+v, want nothing", again)
	}
}
