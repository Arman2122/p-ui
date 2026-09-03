package radius

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

func ev(status Status, session, user string, in, out int64) Event {
	return Event{Status: status, SessionID: session, Username: user, In: in, Out: out, Peer: Peer{Source: netip.MustParseAddr("203.0.113.7")}}
}

/*
A record carries the session's totals so far; what the panel bills is the
difference since the previous record, so a session updated every minute is
billed once per byte, a Stop after an Interim adds only its tail, and two
sessions of one user add up. A NAS that restarts and reuses an id reports
totals below the last ones; those count from zero rather than as a refund.
*/
func TestLedgerBillsEachByteOnce(t *testing.T) {
	l := NewLedger("ovpn-1")
	l.Apply(ev(Start, "s1", "alice", 0, 0))
	l.Apply(ev(Interim, "s1", "alice", 1000, 5000))
	l.Apply(ev(Interim, "s1", "alice", 1500, 9000))
	l.Apply(ev(Start, "s2", "alice", 0, 0))
	l.Apply(ev(Interim, "s2", "alice", 10, 20))
	l.Apply(ev(Stop, "s1", "alice", 1600, 9100))
	got := l.Drain()
	want := []core.TrafficDelta{{Email: "alice", Tag: "ovpn-1", Up: 1610, Down: 9120}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("drained %+v, want %+v", got, want)
	}
	if again := l.Drain(); len(again) != 0 {
		t.Fatalf("a second drain handed out %+v again", again)
	}
	// A session heard of only mid-flight is baselined, not billed: a panel that
	// restarted under a live tunnel must not charge its first half again.
	l.Apply(ev(Interim, "adopted", "carol", 5000, 6000))
	if got := l.Drain(); len(got) != 0 {
		t.Fatalf("an adopted session's first record was billed: %+v", got)
	}
	l.Apply(ev(Interim, "adopted", "carol", 5100, 6300))
	if got := l.Drain(); len(got) != 1 || got[0].Up != 100 || got[0].Down != 300 {
		t.Fatalf("an adopted session's later record billed %+v, want 100/300", got)
	}
	l.Apply(ev(Stop, "adopted", "carol", 5100, 6300))
	if got := l.Drain(); len(got) != 0 {
		t.Fatalf("a stop with no new bytes billed %+v", got)
	}
	// A nameless record closes a session the ledger knows and bills its tail to
	// whoever opened it; one for an unknown session opens nothing.
	l.Apply(Event{Status: Stop, SessionID: "s2", In: 20, Out: 30})
	if got := l.Drain(); len(got) != 1 || got[0].Email != "alice" || got[0].Up != 10 || got[0].Down != 10 {
		t.Fatalf("a nameless stop billed %+v, want alice 10/10", got)
	}
	l.Apply(Event{Status: Start, SessionID: "ghost"})
	if s := l.Sessions(); len(s) != 0 {
		t.Fatalf("a nameless start opened %+v", s)
	}
	l.Apply(ev(Start, "s2", "alice", 0, 0))
	l.Apply(ev(Interim, "s2", "alice", 3, 4))
	if got := l.Drain(); len(got) != 1 || got[0].Up != 3 || got[0].Down != 4 {
		t.Fatalf("a session that went backwards billed %+v, want 3/4 from zero", got)
	}
	if sessions := l.Sessions(); len(sessions) != 1 || sessions[0].Email != "alice" {
		t.Fatalf("open sessions = %+v, want s2 only after s1 stopped", sessions)
	}
}

// A session's last-seen moves only with bytes; one that stops reporting is
// dropped after Stale, so a dead tunnel does not sit in the IP-limit count.
func TestLedgerSessionsGoStale(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLedger("ovpn-1")
	l.now = func() time.Time { return now }
	l.Stale = 5 * time.Minute
	l.Apply(ev(Start, "s1", "bob", 0, 0))
	opened := now
	now = now.Add(time.Minute)
	l.Apply(ev(Interim, "s1", "bob", 0, 0))
	if s := l.Sessions(); len(s) != 1 || s[0].LastSeenUnixMilli != opened.UnixMilli() {
		t.Fatalf("an idle interim moved last-seen: %+v", s)
	}
	now = now.Add(time.Minute)
	l.Apply(ev(Interim, "s1", "bob", 10, 0))
	if s := l.Sessions(); len(s) != 1 || s[0].LastSeenUnixMilli != now.UnixMilli() {
		t.Fatalf("bytes did not move last-seen: %+v", s)
	}
	now = now.Add(6 * time.Minute)
	if s := l.Sessions(); len(s) != 0 {
		t.Fatalf("a session silent for 6 minutes is still listed: %+v", s)
	}
	if online := l.Online(); len(online) != 0 {
		t.Fatalf("online = %v after the session went stale", online)
	}
}
