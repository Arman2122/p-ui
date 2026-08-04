package core

import "sync"

/*
The one monotonic-counter-to-delta engine.

Most cores expose cumulative counters (Xray stats, `wg show transfer`, occtl
RX/TX, RADIUS gigawords) while the panel bills in deltas. Turning one into the
other looks trivial and is not: the counter restarts when the daemon restarts, a
peer re-add zeroes it, an interface recreate zeroes it, and the first reading
after the panel starts may already contain traffic that was billed by a previous
panel process.

This logic was written twice. internal/mtproto/manager.go's copy dropped every byte
moved since an mtg restart and now calls this instead; internal/xray/api.go still
has its own. Because client_traffics is email-keyed and shared, a delta bug in one
core corrupts the quota of users who never touched that core, so there must be
exactly one implementation.

Undercounting is the deliberate bias. Bytes that cannot be attributed are dropped,
never estimated: an undercount costs bandwidth, an overcount costs trust.
*/

// Counter converts successive cumulative readings into per-key deltas.
// The zero value is not usable; call NewCounter.
type Counter struct {
	mu     sync.Mutex
	last   map[string]int64
	epoch  string
	primed bool
}

func NewCounter() *Counter {
	return &Counter{last: make(map[string]int64)}
}

// Observe records one full set of cumulative readings and returns what accrued
// since the previous call.
//
// The first call only records baselines and returns nothing: the counters may
// already hold traffic this process cannot attribute, and counting it would bill
// a user twice across a panel restart. A changed epoch means the source restarted
// from zero, so every reading is new traffic and is counted in full.
func (c *Counter) Observe(epoch string, readings map[string]int64) map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	deltas := make(map[string]int64, len(readings))
	// An empty epoch is the source declining to say, not evidence of a restart:
	// treating it as one flips the epoch twice and re-bills every counter.
	restarted := c.primed && epoch != "" && c.epoch != "" && epoch != c.epoch
	if restarted {
		// Source restarted: its counters are back at zero, so every previous
		// baseline is stale and the whole reading is new traffic.
		c.last = make(map[string]int64, len(readings))
	}
	if epoch != "" {
		c.epoch = epoch
	}

	for key, value := range readings {
		if value < 0 {
			// A negative cumulative reading is a broken source; refuse to guess.
			continue
		}
		baseline, seen := c.last[key]
		c.last[key] = value
		if !c.primed {
			continue
		}
		// An unseen key is a new subject counting from zero. A reading below its
		// baseline means the counter reset without the epoch changing, which is
		// the backstop for sources that expose no restart token.
		if !seen || value < baseline {
			baseline = 0
		}
		if d := value - baseline; d > 0 {
			deltas[key] = d
		}
	}

	if !c.primed {
		c.primed = true
		return map[string]int64{}
	}
	return deltas
}

// Forget is the ONLY way a baseline is dropped: an absent key is ambiguous, and
// dropping it bills a live user its whole counter. Drain before calling.
func (c *Counter) Forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, key)
}

// Primed reports whether a baseline pass has happened. Before that, Observe
// returns nothing and no traffic can be attributed.
func (c *Counter) Primed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.primed
}
