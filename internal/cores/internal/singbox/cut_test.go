package singbox

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

/*
A cut is by (client, source) and by connection id: of a client's connections,
only the ones from the named source are closed, another client's from the same
address is not, and a connection the daemon gave no id cannot be aimed at and is
left alone rather than guessed. A second cut of the same pair, once the stream
no longer lists it, is nothing to do.
*/
func TestCutSessionsClosesOnlyTheNamedDevicesConnections(t *testing.T) {
	alice, bob := "alice@example.com", "bob@example.com"
	a, b := netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("198.51.100.2")
	live := []liveConnection{
		{id: "keep", user: alice, source: a},
		{id: "cut-1", user: alice, source: b},
		{id: "cut-2", user: alice, source: b},
		{id: "other", user: bob, source: b},
		{id: "", user: alice, source: b},
	}
	realConns, realClose := readConnections, closeConn
	t.Cleanup(func() { readConnections, closeConn = realConns, realClose })
	readConnections = func(context.Context, int, string) ([]liveConnection, error) { return live, nil }
	var closed []string
	closeConn = func(_ context.Context, port int, secret, id string) error {
		if port != 4141 || secret != "s3cret" {
			return errors.New("closed on the wrong daemon")
		}
		closed = append(closed, id)
		return nil
	}

	c := New(Deps{})
	c.instances[1] = &instance{running: true, ctl: control{apiPort: 4141, secret: "s3cret"}}
	c.instances[2] = &instance{running: false, ctl: control{apiPort: 9, secret: "down"}}

	cut, err := c.CutSessions(context.Background(), alice, b)
	if err != nil || cut != 2 {
		t.Fatalf("CutSessions = (%d, %v), want (2, nil)", cut, err)
	}
	if want := []string{"cut-1", "cut-2"}; !slices.Equal(closed, want) {
		t.Fatalf("closed %v, want exactly %v: the other source, the other client and the id-less connection stay", closed, want)
	}

	live = []liveConnection{{id: "keep", user: alice, source: a}, {id: "other", user: bob, source: b}}
	closed = nil
	if again, err := c.CutSessions(context.Background(), alice, b); err != nil || again != 0 || len(closed) != 0 {
		t.Fatalf("a second cut of the same pair = (%d, %v) closing %v, want (0, nil) and nothing", again, err, closed)
	}
}
