package controller

import (
	"strings"
	"sync"
	"time"
)

const (
	loginLimitMaxFailures = 5
	loginLimitWindow      = 5 * time.Minute
	loginLimitCooldown    = 15 * time.Minute
	// Hard ceiling on tracked (ip, username) records. The key includes the
	// caller-supplied username, so an unauthenticated attacker rotating
	// usernames would otherwise grow the map without bound.
	loginLimitMaxRecords = 10000
)

var defaultLoginLimiter = newLoginLimiter(loginLimitMaxFailures, loginLimitWindow, loginLimitCooldown)

type loginLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	maxFailures int
	window      time.Duration
	cooldown    time.Duration
	attempts    map[string]*loginLimitRecord
}

type loginLimitRecord struct {
	failures     []time.Time
	blockedUntil time.Time
	// inFlight is one timestamp per admitted attempt whose outcome is not known
	// yet. They count toward the cap the moment they are admitted, which is what
	// makes it hold under concurrency; they are pruned by age so a handler that
	// dies between admission and outcome cannot leak budget forever.
	inFlight []time.Time
}

func newLoginLimiter(maxFailures int, window, cooldown time.Duration) *loginLimiter {
	return &loginLimiter{
		now:         time.Now,
		maxFailures: maxFailures,
		window:      window,
		cooldown:    cooldown,
		attempts:    make(map[string]*loginLimitRecord),
	}
}

/*
allow admits one login attempt, RESERVING it against the cap.

It used to check and return, leaving the caller to count only after the password
check had finished. Between those two points the lock is not held, so N
simultaneous requests all read the same under-cap state and all proceeded: the
5-attempt limit became "5 per round, unbounded rounds in parallel". Counting the
attempt at admission is what closes that — the reservation is visible to every
other request the instant it is taken.
*/
func (l *loginLimiter) allow(ip, username string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	key := loginLimitKey(ip, username)
	record := l.attempts[key]
	if record == nil {
		l.evictForRoom(now)
		record = &loginLimitRecord{}
		l.attempts[key] = record
	}
	if now.Before(record.blockedUntil) {
		return record.blockedUntil, false
	}
	record.blockedUntil = time.Time{}
	horizon := now.Add(-l.window)
	record.failures = pruneLoginFailures(record.failures, horizon)
	record.inFlight = pruneLoginFailures(record.inFlight, horizon)

	if len(record.failures)+len(record.inFlight) >= l.maxFailures {
		return time.Time{}, false
	}
	record.inFlight = append(record.inFlight, now)
	return time.Time{}, true
}

// releaseInFlight drops one reservation. Callers hold l.mu.
func (record *loginLimitRecord) releaseInFlight() {
	if len(record.inFlight) > 0 {
		record.inFlight = record.inFlight[1:]
	}
}

func (l *loginLimiter) registerFailure(ip, username string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	key := loginLimitKey(ip, username)
	record := l.attempts[key]
	if record == nil {
		l.evictForRoom(now)
		record = &loginLimitRecord{}
		l.attempts[key] = record
	}
	// The reservation this outcome resolves becomes a recorded failure, so it is
	// released rather than counted twice.
	record.releaseInFlight()
	record.failures = pruneLoginFailures(record.failures, now.Add(-l.window))
	record.failures = append(record.failures, now)
	if len(record.failures) >= l.maxFailures {
		record.failures = nil
		record.blockedUntil = now.Add(l.cooldown)
		return record.blockedUntil, true
	}
	return time.Time{}, false
}

func (l *loginLimiter) registerSuccess(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, loginLimitKey(ip, username))
}

// evictForRoom keeps the attempts map bounded before inserting a new record.
// It first reclaims records that are no longer blocked and whose failures have
// aged out of the window; if the map is still at the ceiling (a genuine
// broad flood), it drops one arbitrary record so memory can never grow past the
// cap. Callers hold l.mu.
func (l *loginLimiter) evictForRoom(now time.Time) {
	if len(l.attempts) < loginLimitMaxRecords {
		return
	}
	cutoff := now.Add(-l.window)
	for key, record := range l.attempts {
		if now.Before(record.blockedUntil) {
			continue
		}
		record.failures = pruneLoginFailures(record.failures, cutoff)
		if len(record.failures) == 0 {
			delete(l.attempts, key)
		}
	}
	if len(l.attempts) < loginLimitMaxRecords {
		return
	}
	for key, record := range l.attempts {
		if now.Before(record.blockedUntil) {
			continue
		}
		delete(l.attempts, key)
		return
	}
	for key := range l.attempts {
		delete(l.attempts, key)
		return
	}
}

func loginLimitKey(ip, username string) string {
	return strings.TrimSpace(ip) + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func pruneLoginFailures(failures []time.Time, cutoff time.Time) []time.Time {
	keepFrom := 0
	for keepFrom < len(failures) && failures[keepFrom].Before(cutoff) {
		keepFrom++
	}
	return failures[keepFrom:]
}
