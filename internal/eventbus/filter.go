package eventbus

import (
	"sync"
	"time"
)

// RateLimiter prevents notification spam from flapping events.
type RateLimiter struct {
	mu        sync.Mutex
	lastSent  map[string]time.Time
	cooldown  time.Duration
	lastPrune time.Time
}

// NewRateLimiter creates a rate limiter with the given cooldown period.
func NewRateLimiter(cooldown time.Duration) *RateLimiter {
	return &RateLimiter{
		lastSent: make(map[string]time.Time),
		cooldown: cooldown,
	}
}

// Allow returns true if the event should be sent (cooldown has elapsed).
func (r *RateLimiter) Allow(eventType EventType, source string) bool {
	key := string(eventType) + ":" + source
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if now.Sub(r.lastSent[key]) < r.cooldown {
		return false
	}
	r.lastSent[key] = now
	return true
}

// pruneLocked drops keys whose cooldown has elapsed. Such an entry no longer
// affects Allow's result, so removing it is safe and keeps the map from
// retaining one entry per (eventType, source) ever seen. Throttled to once per
// cooldown so a busy bus doesn't sweep the whole map on every event.
func (r *RateLimiter) pruneLocked(now time.Time) {
	if now.Sub(r.lastPrune) < r.cooldown {
		return
	}
	r.lastPrune = now
	for k, v := range r.lastSent {
		if now.Sub(v) >= r.cooldown {
			delete(r.lastSent, k)
		}
	}
}

/*
LimiterKey is the cooldown bucket an event shares with its own kind.

For everything else that is the source. For a login attempt it must not be:
Source is the client IP, so an attacker rotating addresses — or rotating an
X-Forwarded-For header behind a trusted proxy — mints a fresh bucket per
request and the cooldown never applies. Keying on the outcome instead gives a
closed two-value space the caller cannot influence, and keeps a real admin's
success notification out of the same bucket as the flood drowning it.
*/
func LimiterKey(e Event) string {
	if e.Type != EventLoginAttempt {
		return e.Source
	}
	if data, ok := e.Data.(*LoginEventData); ok && data.Status != "" {
		return data.Status
	}
	return "fail"
}
