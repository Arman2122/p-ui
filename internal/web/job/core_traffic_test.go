package job

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

type fakeCore struct {
	id      core.Kind
	users   []core.TrafficDelta
	tags    []core.TagDelta
	online  []string
	userErr error
	drained int
}

func (f *fakeCore) Describe() core.Descriptor       { return core.Descriptor{ID: f.id, TitleKey: "t"} }
func (f *fakeCore) Kinds() []core.Kind              { return []core.Kind{f.id} }
func (f *fakeCore) Preflight(context.Context) error { return nil }

func (f *fakeCore) CollectTraffic(context.Context) ([]core.TrafficDelta, error) {
	return f.users, f.userErr
}

func (f *fakeCore) CollectTagTraffic(context.Context) ([]core.TagDelta, error) {
	f.drained++
	return f.tags, nil
}

func (f *fakeCore) OnlineEmails(context.Context) ([]string, error) { return f.online, nil }

func registryOf(t *testing.T, cores ...core.Core) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	for _, c := range cores {
		if err := reg.Register(c); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	return reg
}

/*
One poll must bill every core, which is the whole point of there being one job
instead of one per core.
*/
func TestOnePollBillsEveryCore(t *testing.T) {
	a := &fakeCore{
		id:    "alpha",
		users: []core.TrafficDelta{{Email: "a@x", Up: 1, Down: 2}},
		tags:  []core.TagDelta{{Tag: "in-a", Up: 3, Down: 4}},
	}
	b := &fakeCore{
		id:    "beta",
		users: []core.TrafficDelta{{Email: "b@x", Up: 10, Down: 20}},
		tags:  []core.TagDelta{{Tag: "out-b", Outbound: true, Up: 30, Down: 40}},
	}
	Cores = registryOf(t, a, b)
	t.Cleanup(func() { Cores = nil })

	traffics, clients := collectCoreTraffic()

	if len(clients) != 2 {
		t.Fatalf("clients = %+v, want one row per core", clients)
	}
	if len(traffics) != 2 {
		t.Fatalf("traffics = %+v, want one row per core", traffics)
	}
	byTag := map[string]bool{}
	for _, tr := range traffics {
		byTag[tr.Tag] = tr.IsOutbound
	}
	if byTag["in-a"] {
		t.Error("in-a came back marked outbound")
	}
	if !byTag["out-b"] {
		t.Error("out-b lost its outbound flag, so it would be billed as an inbound")
	}
}

/*
One client, two cores, one row.

client_traffics is email-keyed and UNIQUE — that is what makes a quota span every
core — and AddTraffic indexes the slice it is given by email and keeps the last
entry. So a client who is on a vless inbound and an mtproto inbound must arrive
here summed. Appending both silently threw one core's bytes away, and because
the Counter had already advanced past them they were never offered again.
*/
func TestAClientOnTwoCoresIsBilledForBoth(t *testing.T) {
	a := &fakeCore{id: "alpha", users: []core.TrafficDelta{{Email: "alice@x", Up: 3, Down: 5}}}
	b := &fakeCore{id: "beta", users: []core.TrafficDelta{{Email: "alice@x", Up: 10, Down: 20}}}
	Cores = registryOf(t, a, b)
	t.Cleanup(func() { Cores = nil })

	_, clients := collectCoreTraffic()

	if len(clients) != 1 {
		t.Fatalf("clients = %+v, want one row for the one email", clients)
	}
	if clients[0].Up != 13 || clients[0].Down != 25 {
		t.Errorf("alice = up %d down %d, want 13/25 — one core's bytes were dropped",
			clients[0].Up, clients[0].Down)
	}
}

/*
A core whose daemon is down must not stop the others being billed. Before the
merge each core had its own job and this was free; sharing one loop is what
makes it a risk worth pinning.
*/
func TestOneFailingCoreDoesNotStopTheRest(t *testing.T) {
	broken := &fakeCore{id: "broken", userErr: errors.New("daemon is down")}
	healthy := &fakeCore{
		id:    "healthy",
		users: []core.TrafficDelta{{Email: "a@x", Up: 7, Down: 8}},
	}
	Cores = registryOf(t, broken, healthy)
	t.Cleanup(func() { Cores = nil })

	_, clients := collectCoreTraffic()
	if len(clients) != 1 || clients[0].Email != "a@x" {
		t.Fatalf("clients = %+v, want the healthy core still billed", clients)
	}
}

// Tag deltas are banked during the per-user call, so that call has to happen —
// and exactly once, or a bank-and-drain core loses or repeats a poll.
func TestEachCoreIsDrainedOncePerPoll(t *testing.T) {
	c := &fakeCore{id: "solo", tags: []core.TagDelta{{Tag: "in-1", Up: 1}}}
	Cores = registryOf(t, c)
	t.Cleanup(func() { Cores = nil })

	collectCoreTraffic()
	collectCoreTraffic()
	if c.drained != 2 {
		t.Errorf("drained %d times over two polls, want 2", c.drained)
	}
}

/*
Only clients that moved no bytes count as idle-online. A client already seen in
the deltas is live traffic, and re-reporting it would bump last_online twice.
*/
func TestIdleOnlineExcludesClientsThatMovedBytes(t *testing.T) {
	a := &fakeCore{id: "alpha", online: []string{"moved@x", "idle@x"}}
	b := &fakeCore{id: "beta", online: []string{"idle@x", "other@x"}}
	Cores = registryOf(t, a, b)
	t.Cleanup(func() { Cores = nil })

	got := collectIdleOnline(map[string]bool{"moved@x": true})

	seen := map[string]int{}
	for _, e := range got {
		seen[e]++
	}
	if seen["moved@x"] != 0 {
		t.Error("a client that moved bytes was reported idle-online")
	}
	if seen["idle@x"] != 1 {
		t.Errorf("idle@x reported %d times, want once even though two cores name it", seen["idle@x"])
	}
	if seen["other@x"] != 1 {
		t.Error("other@x was dropped")
	}
}

// No registry means no billing, not a panic — every test that never polls
// traffic runs with it nil.
func TestNoRegistryIsSafe(t *testing.T) {
	Cores = nil
	traffics, clients := collectCoreTraffic()
	if traffics != nil || clients != nil {
		t.Errorf("got %+v / %+v, want nothing", traffics, clients)
	}
	if idle := collectIdleOnline(nil); idle != nil {
		t.Errorf("idle = %+v, want nothing", idle)
	}
}
