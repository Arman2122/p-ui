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

// baselineGrace is how many consecutive readings a key must be absent from
// before its baseline is dropped. One absence proves nothing — a scrape can land
// while a daemon reloads — and dropping a live subject's baseline bills its whole
// counter again. Ten readings is 50s at Xray's poll and 100s at mtg's, far longer
// than any reload, and short enough that memory tracks the live set.
const baselineGrace = 10

// Counter converts successive cumulative readings into per-key deltas.
// The zero value is not usable; call NewCounter.
type Counter struct {
	mu   sync.Mutex
	last map[string]int64
	// absent counts consecutive readings each baseline has been missing from.
	absent map[string]int
	epoch  string
	primed bool
}

func NewCounter() *Counter {
	return &Counter{last: make(map[string]int64), absent: make(map[string]int)}
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
		c.absent = make(map[string]int, len(readings))
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
	c.expire(readings)
	return deltas
}

// expire drops baselines the source has stopped reporting, once it has stopped
// reporting them for baselineGrace readings running. Without it the map grows
// with every subject ever seen; with a grace of one it re-bills any subject that
// missed a single scrape, which is the bug the old prune shipped.
//
// A reading with no subjects at all is not evidence about any particular key, so
// it is ignored rather than counted as an absence for everything.
func (c *Counter) expire(readings map[string]int64) {
	if len(readings) == 0 {
		return
	}
	for key := range c.last {
		if _, present := readings[key]; present {
			delete(c.absent, key)
			continue
		}
		c.absent[key]++
		if c.absent[key] >= baselineGrace {
			delete(c.last, key)
			delete(c.absent, key)
		}
	}
}

// Tracked reports how many baselines are held. It exists so a test can prove the
// set follows the live subjects instead of growing without bound.
func (c *Counter) Tracked() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.last)
}

// NoteSourceRestart records that the source has restarted from zero. It is the
// push counterpart of the epoch, for when the panel causes the restart itself
// rather than reading an incarnation token back out of the source.
func (c *Counter) NoteSourceRestart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = make(map[string]int64, len(c.last))
	c.absent = make(map[string]int, len(c.absent))
}

// Forget drops a baseline immediately, for when the panel itself removes a
// subject and need not wait out baselineGrace. Drain its final reading first.
func (c *Counter) Forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, key)
	delete(c.absent, key)
}

// Primed reports whether a baseline pass has happened. Before that, Observe
// returns nothing and no traffic can be attributed.
func (c *Counter) Primed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.primed
}
