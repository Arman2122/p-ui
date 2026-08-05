package mtproto

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

/*
An mtproto inbound's total is the sum of its clients', because mtg meters per
secret and nothing else — but only when the inbound egresses directly.

With routeThroughXray the bytes leave through Xray's loopback bridge, which
meters them under the same tag. Billing them here as well doubles that inbound's
total, which is the whole reason mtproto_job skipped routed tags.
*/
func TestRoutedInboundsAreNotBilledTwice(t *testing.T) {
	c := &Core{}
	c.noteEgress(engine.Instance{Tag: "direct", RouteThroughXray: false})
	c.noteEgress(engine.Instance{Tag: "bridged", RouteThroughXray: true})

	c.stashTagTraffic([]engine.Traffic{
		{Email: "a@x", Tag: "direct", Up: 10, Down: 20},
		{Email: "b@x", Tag: "direct", Up: 5, Down: 6},
		{Email: "c@x", Tag: "bridged", Up: 1000, Down: 2000},
	})

	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	want := []core.TagDelta{{Tag: "direct", Up: 15, Down: 26}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v — the bridged tag is already billed by xray", got, want)
	}
}

// An inbound that flips to direct egress must start being billed here, or its
// total silently stops moving.
func TestUnroutingAnInboundResumesBilling(t *testing.T) {
	c := &Core{}
	c.noteEgress(engine.Instance{Tag: "in-1", RouteThroughXray: true})
	c.stashTagTraffic([]engine.Traffic{{Email: "a@x", Tag: "in-1", Up: 9, Down: 9}})
	if got, _ := c.CollectTagTraffic(t.Context()); len(got) != 0 {
		t.Fatalf("got %+v while routed, want nothing", got)
	}

	c.noteEgress(engine.Instance{Tag: "in-1", RouteThroughXray: false})
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
	c := &Core{}
	c.noteEgress(engine.Instance{Tag: "in-1"})
	c.stashTagTraffic([]engine.Traffic{{Email: "a@x", Tag: "in-1", Up: 1, Down: 2}})

	if got, _ := c.CollectTagTraffic(t.Context()); len(got) != 1 {
		t.Fatalf("first drain = %+v, want one delta", got)
	}
	if again, _ := c.CollectTagTraffic(t.Context()); len(again) != 0 {
		t.Errorf("second drain = %+v, want nothing", again)
	}
}
