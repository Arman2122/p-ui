package presence

import (
	"slices"
	"testing"
)

/*
The presence set used to live on xray.Process, so these tests built one. They
now build the thing itself, which is the point of F089: the panel's view of who
is connected does not depend on a core it may not be running.
*/
func newTestSet() *Set {
	return &Set{}
}

func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// TestMergedNodeTreesScopesPerGuid pins #4983/#4809: each node's clients stay
// under that node's GUID, so a client on one node is never attributed to
// another — and a sub-node's clients (reported under their own GUID inside a
// parent's tree) compose upward without collapsing onto the parent.
func TestMergedNodeTreesScopesPerGuid(t *testing.T) {
	p := newTestSet()
	// Node A (direct) reports its own clients plus sub-node B's tree.
	p.SetNodeTree(1, map[string][]string{
		"guid-a": {"user1", "user2"},
		"guid-b": {"user3"}, // B is behind A; still keyed by B's own GUID
	})
	p.SetNodeTree(2, map[string][]string{
		"guid-c": {"user4"},
	})

	merged := p.MergedNodeTrees()
	assertSameSet(t, "guid-a", merged["guid-a"], []string{"user1", "user2"})
	assertSameSet(t, "guid-b", merged["guid-b"], []string{"user3"})
	assertSameSet(t, "guid-c", merged["guid-c"], []string{"user4"})

	if slices.Contains(merged["guid-a"], "user3") {
		t.Errorf("user3 (on sub-node B) leaked onto node A: %v", merged["guid-a"])
	}
}

// TestMergedNodeTreesOmitsEmpty keeps the payload small: empty GUID sets don't
// appear as keys.
func TestMergedNodeTreesOmitsEmpty(t *testing.T) {
	p := newTestSet()
	p.SetNodeTree(1, map[string][]string{
		"guid-a": {"user1"},
		"guid-x": {},
	})
	if _, ok := p.MergedNodeTrees()["guid-x"]; ok {
		t.Errorf("empty GUID set should be omitted: %v", p.MergedNodeTrees())
	}
}

// TestGetOnlineClientsUnionDedupes confirms the flat union (client-centric /
// total-count views) merges local + every node and dedupes.
func TestGetOnlineClientsUnionDedupes(t *testing.T) {
	p := newTestSet()
	p.RefreshLocal([]string{"user1"}, nil, 1000, 20000)
	p.SetNodeTree(1, map[string][]string{"guid-a": {"user1", "user2"}})

	assertSameSet(t, "union", p.Online(), []string{"user1", "user2"})
}

// TestRefreshLocalOnlineGraceWindow checks the in-memory local set honours the
// grace window: idle-but-recent clients stay online, stale ones age out, and
// the set is derived only from local activity (never the shared DB column).
func TestRefreshLocalOnlineGraceWindow(t *testing.T) {
	p := newTestSet()
	const grace = 20000

	p.RefreshLocal([]string{"user1"}, nil, 1000, grace)
	if got := p.LocalOnline(); !slices.Contains(got, "user1") {
		t.Fatalf("user1 should be online right after activity, got %v", got)
	}

	p.RefreshLocal([]string{"user2"}, nil, 11000, grace)
	got := p.LocalOnline()
	if !slices.Contains(got, "user1") || !slices.Contains(got, "user2") {
		t.Fatalf("both within grace window, got %v", got)
	}

	p.RefreshLocal(nil, nil, 22000, grace)
	got = p.LocalOnline()
	if slices.Contains(got, "user1") {
		t.Errorf("user1 (idle 21s, past grace) should have aged out, got %v", got)
	}
	if !slices.Contains(got, "user2") {
		t.Errorf("user2 (idle 11s, within grace) should still be online, got %v", got)
	}
}

// TestGetLocalActiveInboundsTracksGraceWindow pins #4859: a multi-inbound
// client only counts online on inbounds that actually carried traffic, and the
// active-inbound signal honours the same grace window as the online signal.
func TestGetLocalActiveInboundsTracksGraceWindow(t *testing.T) {
	p := newTestSet()
	const grace = 20000

	p.RefreshLocal([]string{"alice"}, []string{"inbound-a"}, 1000, grace)
	assertSameSet(t, "active after first poll", p.LocalActiveTags(), []string{"inbound-a"})

	p.RefreshLocal([]string{"alice"}, []string{"inbound-b"}, 11000, grace)
	assertSameSet(t, "both within grace", p.LocalActiveTags(), []string{"inbound-a", "inbound-b"})

	p.RefreshLocal(nil, nil, 22000, grace)
	assertSameSet(t, "inbound-a (idle 21s) aged out, inbound-b kept", p.LocalActiveTags(), []string{"inbound-b"})

	p.RefreshLocal(nil, nil, 40000, grace)
	if got := p.LocalActiveTags(); len(got) != 0 {
		t.Errorf("all inbounds idle past grace, want empty, got %v", got)
	}
}

// TestClearNodeOnlineClientsDropsNode mirrors a failed node probe: the node's
// whole subtree contribution disappears immediately.
func TestClearNodeOnlineClientsDropsNode(t *testing.T) {
	p := newTestSet()
	p.SetNodeTree(3, map[string][]string{"guid-a": {"user1"}})
	p.ClearNodeTree(3)

	if _, ok := p.MergedNodeTrees()["guid-a"]; ok {
		t.Errorf("node 3's subtree should be absent after ClearNodeOnlineClients")
	}
}

/*
The grace window is half-open, and both maps use the same comparison.

Idle for EXACTLY graceMs ages out; idle for graceMs-1 does not. A mutated `<=`
would keep a client listed as online one tick past its own rule, and the two
maps drifting apart would report an email as online with none of its inbounds
active. Lifted from internal/xray with the state itself. F089.
*/
func TestRefreshLocalGraceBoundaryEmails(t *testing.T) {
	set := newTestSet()
	const grace = int64(20000)

	set.RefreshLocal([]string{"edge"}, nil, 0, grace)
	// now-ts == grace exactly: the age is not strictly < grace, so it must drop.
	set.RefreshLocal(nil, nil, grace, grace)
	for _, email := range set.LocalOnline() {
		if email == "edge" {
			t.Fatalf("email idle exactly graceMs must age out (half-open window), got online %v", set.LocalOnline())
		}
	}

	inside := newTestSet()
	inside.RefreshLocal([]string{"edge"}, nil, 0, grace)
	inside.RefreshLocal(nil, nil, grace-1, grace)
	if !slices.Contains(inside.LocalOnline(), "edge") {
		t.Fatalf("email idle graceMs-1 must still be online, got %v", inside.LocalOnline())
	}
}

func TestRefreshLocalGraceBoundaryInbounds(t *testing.T) {
	set := newTestSet()
	const grace = int64(20000)

	set.RefreshLocal(nil, []string{"in-edge"}, 0, grace)
	set.RefreshLocal(nil, nil, grace, grace)
	for _, tag := range set.LocalActiveTags() {
		if tag == "in-edge" {
			t.Fatalf("inbound idle exactly graceMs must age out, got active %v", set.LocalActiveTags())
		}
	}

	inside := newTestSet()
	inside.RefreshLocal(nil, []string{"in-edge"}, 0, grace)
	inside.RefreshLocal(nil, nil, grace-1, grace)
	if !slices.Contains(inside.LocalActiveTags(), "in-edge") {
		t.Fatalf("inbound idle graceMs-1 must still be active, got %v", inside.LocalActiveTags())
	}
}

/*
The presence set answers with no core running at all.

This is the whole of F089. The state lived on xray.Process, so
RefreshLocalOnlineClients was a silent no-op when Xray had never started and
every reader returned an empty set on a nil process — a panel serving only
MTProto or kernel WireGuard reported every client offline, forever, while
CoreSupervise was watching those cores come up and reporting their tags.

Nothing here can even reference a process, which is the property that keeps it
fixed.
*/
func TestPresenceNeedsNoCore(t *testing.T) {
	set := &Set{}
	const grace = int64(60000)

	set.RefreshLocal([]string{"tg@mtproto"}, []string{"in-mtproto"}, 1000, grace)

	if got := set.Online(); !slices.Contains(got, "tg@mtproto") {
		t.Fatalf("online = %v, want the client this host just saw", got)
	}
	if got := set.LocalActiveTags(); !slices.Contains(got, "in-mtproto") {
		t.Fatalf("active tags = %v, want the inbound that carried it", got)
	}

	// And a node's clients arrive the same way, on a host with no Xray either.
	set.SetNodeTree(7, map[string][]string{"guid-node": {"far@vless"}})
	if got := set.Online(); !slices.Contains(got, "far@vless") {
		t.Fatalf("online = %v, want the node's client too", got)
	}
	set.ClearNodeTree(7)
	if got := set.Online(); slices.Contains(got, "far@vless") {
		t.Fatalf("online = %v: a node whose probe failed is still contributing", got)
	}
}
