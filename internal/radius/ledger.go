package radius

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
Ledger turns per-session accounting records into the panel's deltas and live
sessions, the way the counter engine does for a cumulative daemon. A record
carries the session's totals so far; the delta is what moved since the last
record of that session, never below zero, and a Stop closes the session. A
core embeds one per inbound and drains it from CollectTraffic and Sessions.
*/
type Ledger struct {
	mu       sync.Mutex
	sessions map[string]*session
	pending  map[string]*core.TrafficDelta
	tag      string
	// Stale is how long a session may go without a record before it is dropped
	// as dead; the NAS's interim interval times a few. Zero keeps them forever.
	Stale time.Duration
	now   func() time.Time
}

type session struct {
	username string
	peer     Peer
	in, out  int64
	lastSeen time.Time
	// started is when this session opened, for the cap's arrival order. A session
	// adopted mid-flight (a panel restart) has no earlier record, so it is now.
	started time.Time
}

// NewLedger tags every delta it emits with the inbound's tag.
func NewLedger(tag string) *Ledger {
	return &Ledger{sessions: map[string]*session{}, pending: map[string]*core.TrafficDelta{}, tag: tag, now: time.Now}
}

// SetClock replaces the ledger's clock, for a test that stamps records; nil
// restores the wall clock.
func (l *Ledger) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	l.now = now
}

// Apply records one event. Bytes count from the session's previous totals; a
// record that goes backwards (a NAS restart reusing an id) counts from zero.
// An empty Username is a record for a user the caller no longer knows: it
// applies to a session already open (a removed client's Stop must still close
// it, and bill its tail to who it was) and opens nothing.
func (l *Ledger) Apply(ev Event) {
	if ev.SessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cur, ok := l.sessions[ev.SessionID]
	if ev.Username == "" {
		if !ok {
			return
		}
		ev.Username = cur.username
	}
	if !ok {
		cur = &session{username: ev.Username, peer: ev.Peer, started: l.now()}
		l.sessions[ev.SessionID] = cur
		// A session first heard of mid-flight (a panel restart adopted it) is
		// baselined: its earlier bytes were billed by the previous process or are
		// lost, and billing them now would charge a user twice. A Start is zero.
		if ev.Status != Start {
			cur.in, cur.out = ev.In, ev.Out
			cur.lastSeen = l.now()
			if ev.Status == Stop {
				delete(l.sessions, ev.SessionID)
			}
			return
		}
	}
	if ev.Peer.Source.IsValid() {
		cur.peer.Source = ev.Peer.Source
	}
	if ev.Peer.FramedIP.IsValid() {
		cur.peer.FramedIP = ev.Peer.FramedIP
	}
	dIn, dOut := ev.In-cur.in, ev.Out-cur.out
	if dIn < 0 {
		dIn = ev.In
	}
	if dOut < 0 {
		dOut = ev.Out
	}
	cur.in, cur.out = ev.In, ev.Out
	if dIn > 0 || dOut > 0 || cur.lastSeen.IsZero() {
		cur.lastSeen = l.now()
	}
	if dIn > 0 || dOut > 0 {
		agg, ok := l.pending[ev.Username]
		if !ok {
			agg = &core.TrafficDelta{Email: ev.Username, Tag: l.tag}
			l.pending[ev.Username] = agg
		}
		agg.Up += dIn
		agg.Down += dOut
	}
	if ev.Status == Stop {
		delete(l.sessions, ev.SessionID)
	}
}

// Drain hands out what accrued since the last call, by user, in a fixed order.
func (l *Ledger) Drain() []core.TrafficDelta {
	l.mu.Lock()
	defer l.mu.Unlock()
	emails := make([]string, 0, len(l.pending))
	for email := range l.pending {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	out := make([]core.TrafficDelta, 0, len(emails))
	for _, email := range emails {
		out = append(out, *l.pending[email])
	}
	l.pending = map[string]*core.TrafficDelta{}
	return out
}

// Sessions lists the open sessions, dropping any past Stale, with last-seen
// the time of the last record that moved bytes or opened the session.
func (l *Ledger) Sessions() []core.Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ids := make([]string, 0, len(l.sessions))
	for id, cur := range l.sessions {
		if l.Stale > 0 && now.Sub(cur.lastSeen) > l.Stale {
			delete(l.sessions, id)
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]core.Session, 0, len(ids))
	for _, id := range ids {
		cur := l.sessions[id]
		s := core.Session{Email: cur.username, Source: cur.peer.Source, LastSeenUnixMilli: cur.lastSeen.UnixMilli()}
		if !cur.started.IsZero() {
			s.StartedUnixMilli = cur.started.UnixMilli()
		}
		if cur.peer.FramedIP.IsValid() {
			s.Local = []netip.Addr{cur.peer.FramedIP}
		}
		out = append(out, s)
	}
	return out
}

// Online is the set of users with an open session.
func (l *Ledger) Online() []string {
	seen := map[string]struct{}{}
	for _, s := range l.Sessions() {
		seen[s.Email] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for email := range seen {
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}
