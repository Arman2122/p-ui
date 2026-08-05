package xray

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

func tag(name string, outbound bool, up, down int64) *engine.Traffic {
	return &engine.Traffic{Tag: name, IsInbound: !outbound, IsOutbound: outbound, Up: up, Down: down}
}

/*
Tag deltas ride in on the per-user scrape, and that read is destructive: the
counter advances, so whatever is not kept is gone. Two failure modes follow, and
they pull in opposite directions — drop the deltas and an inbound's total stops
moving, replay them and a lifetime total doubles.
*/
func TestTagTrafficIsBankedThenDrainedExactlyOnce(t *testing.T) {
	c := &Core{}

	c.stashTagTraffic([]*engine.Traffic{
		tag("in-443", false, 100, 200),
		tag("proxy", true, 10, 20),
	})

	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	want := []core.TagDelta{
		{Tag: "in-443", Outbound: false, Up: 100, Down: 200},
		{Tag: "proxy", Outbound: true, Up: 10, Down: 20},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delta %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The second drain must be empty. These are deltas: handing the same bytes
	// out again is how a lifetime total doubles.
	if again, err := c.CollectTagTraffic(t.Context()); err != nil || len(again) != 0 {
		t.Errorf("second drain returned %+v (err=%v), want nothing", again, err)
	}
}

// Scrapes between drains must add up, or a caller that polls users more often
// than tags silently loses the bytes in between.
func TestTagTrafficAccumulatesBetweenDrains(t *testing.T) {
	c := &Core{}
	c.stashTagTraffic([]*engine.Traffic{tag("in-443", false, 100, 200)})
	c.stashTagTraffic([]*engine.Traffic{tag("in-443", false, 5, 7)})

	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want one merged delta", got)
	}
	if got[0].Up != 105 || got[0].Down != 207 {
		t.Errorf("merged delta = %+v, want up=105 down=207", got[0])
	}
}

/*
An inbound and an outbound can share a tag — mtproto's egress bridge is named
after the inbound it serves — and they are billed to different tables.
*/
func TestAnInboundAndAnOutboundSharingATagStaySeparate(t *testing.T) {
	c := &Core{}
	c.stashTagTraffic([]*engine.Traffic{
		tag("mtp-1", false, 1, 2),
		tag("mtp-1", true, 30, 40),
	})

	got, err := c.CollectTagTraffic(t.Context())
	if err != nil {
		t.Fatalf("CollectTagTraffic: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want the inbound and the outbound kept apart", got)
	}
	if got[0].Outbound || got[0].Up != 1 {
		t.Errorf("first = %+v, want the inbound side", got[0])
	}
	if !got[1].Outbound || got[1].Up != 30 {
		t.Errorf("second = %+v, want the outbound side", got[1])
	}
}

// A counter that is neither an inbound nor an outbound is not ours to bill.
func TestUnattributedCountersAreIgnored(t *testing.T) {
	c := &Core{}
	c.stashTagTraffic([]*engine.Traffic{{Tag: "whatever", Up: 9, Down: 9}, nil})

	if got, err := c.CollectTagTraffic(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("got %+v (err=%v), want nothing", got, err)
	}
}
