package singbox

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
One transient failure of the connections read wiped what the core knew of
every client's activity: the next successful read baselined each connection
at its creation time, so a device idle since it connected looked freshly
active and one active a moment ago looked idle since it connected.
*/
func TestASessionsReadThatFailsOnceKeepsTheActivityItKnew(t *testing.T) {
	realConns := readConnections
	t.Cleanup(func() { readConnections = realConns })
	source := netip.MustParseAddr("198.51.100.1")
	conn := liveConnection{id: "c1", user: "alice@example.com", source: source, createdAt: 1_000, bytes: 10}
	var fail bool
	readConnections = func(context.Context, int, string) ([]liveConnection, error) {
		if fail {
			return nil, errors.New("the clash API timed out")
		}
		return []liveConnection{conn}, nil
	}
	c := New(Deps{})
	c.instances[1] = &instance{running: true, ctl: control{apiPort: 4141, secret: "s"}}
	if _, err := c.Sessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn.bytes = 20
	seen, err := c.Sessions(context.Background())
	if err != nil || len(seen) != 1 || seen[0].LastSeenUnixMilli <= 1_000 {
		t.Fatalf("sessions after activity = %+v, %v; want the activity stamped now", seen, err)
	}
	active := seen[0].LastSeenUnixMilli

	fail = true
	if _, err := c.Sessions(context.Background()); err == nil {
		t.Fatal("a failed read must be reported")
	}
	fail = false
	seen, err = c.Sessions(context.Background())
	if err != nil || len(seen) != 1 || seen[0].LastSeenUnixMilli != active {
		t.Fatalf("after one failed read the session reads %+v, %v; want last seen %d kept", seen, err, active)
	}
}

/*
A crashed daemon systemd brought back counts from zero; the engine, told of
no restart, billed only what exceeded the old total. Every restart the panel
did not make is caught by the daemon's own start time, and the reading after
it is billed in full.
*/
func TestADaemonRestartThePanelDidNotMakeIsBilledFromZero(t *testing.T) {
	realStats, realStarted := readUserStats, readStartedAt
	t.Cleanup(func() { readUserStats, readStartedAt = realStats, realStarted })
	up, startedAt := int64(1_000), int64(100)
	readUserStats = func(context.Context, int) (map[string]int64, error) {
		return map[string]int64{"user>>>alice@example.com>>>traffic>>>uplink": up}, nil
	}
	readStartedAt = func(context.Context, int, string) (int64, error) { return startedAt, nil }
	c := New(Deps{})
	c.instances[1] = &instance{running: true, inst: core.Instance{Tag: "tuic-1"}, ctl: control{statsPort: 1, apiPort: 2, secret: "s"}, epoch: newEpoch(), counter: core.NewCounter()}
	collect := func() int64 {
		t.Helper()
		deltas, err := c.CollectTraffic(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, d := range deltas {
			total += d.Up
		}
		return total
	}
	collect()
	up = 1_500
	if got := collect(); got != 500 {
		t.Fatalf("a running daemon billed %d, want 500", got)
	}
	// The daemon died and systemd restarted it: 1,600 bytes since, from zero.
	startedAt, up = 200, 1_600
	if got := collect(); got != 1_600 {
		t.Fatalf("after a restart the panel did not make, %d bytes were billed; want the full 1600 the new daemon counted", got)
	}
}
