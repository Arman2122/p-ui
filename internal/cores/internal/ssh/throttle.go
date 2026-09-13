package ssh

import (
	"net/netip"
	"sync"
	"time"
)

// A source that fails this often within the window is refused for the ban:
// fail2ban's sshd defaults, keyed on the address alone and never the username.
const (
	throttleFailures   = 5
	throttleWindow     = 5 * time.Minute
	throttleBan        = 10 * time.Minute
	throttleMaxSources = 4096
)

type loginThrottle struct {
	mu  sync.Mutex
	now func() time.Time
	by  map[netip.Addr]*sourceRecord
}

type sourceRecord struct {
	failures    []time.Time
	bannedUntil time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{now: time.Now, by: map[netip.Addr]*sourceRecord{}}
}

// banned reports whether a connection from addr is closed before its handshake.
func (t *loginThrottle) banned(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.by[addr]
	return ok && t.now().Before(rec.bannedUntil)
}

// failed records one refused login and reports whether it started a ban.
func (t *loginThrottle) failed(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	rec, ok := t.by[addr]
	if !ok {
		if len(t.by) >= throttleMaxSources {
			t.evictLocked(now)
		}
		rec = &sourceRecord{}
		t.by[addr] = rec
	}
	kept := rec.failures[:0]
	for _, at := range rec.failures {
		if now.Sub(at) < throttleWindow {
			kept = append(kept, at)
		}
	}
	rec.failures = append(kept, now)
	if len(rec.failures) < throttleFailures || now.Before(rec.bannedUntil) {
		return false
	}
	rec.bannedUntil, rec.failures = now.Add(throttleBan), nil
	return true
}

func (t *loginThrottle) succeeded(addr netip.Addr) {
	t.mu.Lock()
	delete(t.by, addr)
	t.mu.Unlock()
}

// evictLocked drops every source with nothing current, then the oldest, so a
// spray from many addresses cannot grow the map without bound.
func (t *loginThrottle) evictLocked(now time.Time) {
	for addr, rec := range t.by {
		if now.Before(rec.bannedUntil) {
			continue
		}
		stale := true
		for _, at := range rec.failures {
			if now.Sub(at) < throttleWindow {
				stale = false
				break
			}
		}
		if stale {
			delete(t.by, addr)
		}
	}
	for len(t.by) >= throttleMaxSources {
		var oldest netip.Addr
		var oldestAt time.Time
		first := true
		for addr, rec := range t.by {
			at := rec.bannedUntil
			if n := len(rec.failures); n > 0 && rec.failures[n-1].After(at) {
				at = rec.failures[n-1]
			}
			if first || at.Before(oldestAt) {
				oldest, oldestAt, first = addr, at, false
			}
		}
		delete(t.by, oldest)
	}
}
