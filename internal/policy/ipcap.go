package policy

import (
	"sort"
	"strings"
)

/*
Observation is one address a client was seen at.

LastSeenUnixMilli is load-bearing: a frozen value is a dead connection, not a
reconnect. FirstSeenUnixMilli is when the address began its current run of being
live — the order the devices arrived in, which is what decides who holds a slot.
Zero means nobody recorded it, and Decide stamps one.
*/
type Observation struct {
	IP                 string
	LastSeenUnixMilli  int64
	FirstSeenUnixMilli int64
}

/*
Verdict splits one client's known addresses. Keep holds the slots; Over is every
live address beyond the cap, latest arrival last; Retain is history, seen lately
and live nowhere now. The caller persists all three and acts on Over alone.
*/
type Verdict struct {
	Keep   []Observation
	Over   []Observation
	Retain []Observation
}

/*
Decide applies one client's cap to what is live.

The devices that arrived first hold the slots and the latest arrival is the one
over. It is the only order under which a cap stops a shared account: keeping the
newest lets every new device displace the owner's, and nothing is ever refused.

An address is live if a core reported it this pass, or heldElsewhere says another
node still has it; a persisted address that is neither has ended, whatever its
timestamp says — the core that held it answered, and it was not in the answer.
now and staleCutoff are the caller's clock: this package never reads one of its own.
*/
func Decide(persisted, observed []Observation, limit int, nowUnixMilli, staleCutoffUnixMilli int64, heldElsewhere map[string]bool) Verdict {
	live, retain := merge(persisted, observed, nowUnixMilli, staleCutoffUnixMilli, heldElsewhere)
	keep, over := overCap(live, limit)
	return Verdict{Keep: keep, Over: over, Retain: retain}
}

/*
merge folds this pass's observations into the persisted addresses and splits the
result into live and history. One address is one run: the later sighting and the
earlier start both win, and a run whose start nobody recorded starts at its
latest reading — now for an address seen here, its last sighting for one held on
another node — so a device that cannot say when it came is the one that yields.
Leaving the live set ends the run: history carries no start.
*/
func merge(persisted, observed []Observation, now, staleCutoff int64, heldElsewhere map[string]bool) (live, retain []Observation) {
	byIP := make(map[string]Observation, len(persisted)+len(observed))
	for _, o := range persisted {
		if o.LastSeenUnixMilli < staleCutoff {
			continue
		}
		byIP[o.IP] = o
	}
	seenThisPass := make(map[string]bool, len(observed))
	for _, o := range observed {
		seenThisPass[o.IP] = true
		prev, known := byIP[o.IP]
		if !known {
			byIP[o.IP] = o
			continue
		}
		prev.LastSeenUnixMilli = max(prev.LastSeenUnixMilli, o.LastSeenUnixMilli)
		prev.FirstSeenUnixMilli = earliest(prev.FirstSeenUnixMilli, o.FirstSeenUnixMilli)
		byIP[o.IP] = prev
	}
	for ip, o := range byIP {
		switch {
		case seenThisPass[ip]:
			if o.FirstSeenUnixMilli == 0 {
				o.FirstSeenUnixMilli = now
			}
			live = append(live, o)
		case heldElsewhere[ip]:
			if o.FirstSeenUnixMilli == 0 {
				o.FirstSeenUnixMilli = o.LastSeenUnixMilli
			}
			live = append(live, o)
		default:
			o.FirstSeenUnixMilli = 0
			retain = append(retain, o)
		}
	}
	sortByArrival(live)
	sortOldestFirst(retain)
	return live, retain
}

// earliest is the earlier of two starts, ignoring one nobody recorded.
func earliest(a, b int64) int64 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return min(a, b)
}

// sortByArrival orders by run start and breaks ties on the address: map order is
// random, and an arbitrary pick would name a different device over on every pass.
func sortByArrival(obs []Observation) {
	sort.Slice(obs, func(i, j int) bool {
		if obs[i].FirstSeenUnixMilli != obs[j].FirstSeenUnixMilli {
			return obs[i].FirstSeenUnixMilli < obs[j].FirstSeenUnixMilli
		}
		return obs[i].IP < obs[j].IP
	})
}

func sortOldestFirst(obs []Observation) {
	sort.Slice(obs, func(i, j int) bool {
		if obs[i].LastSeenUnixMilli != obs[j].LastSeenUnixMilli {
			return obs[i].LastSeenUnixMilli < obs[j].LastSeenUnixMilli
		}
		return obs[i].IP < obs[j].IP
	})
}

// overCap keeps the first limit arrivals and names the rest over. A limit of 0
// or less is UNLIMITED, never zero-allowed: no cap can never mean no access.
func overCap(live []Observation, limit int) (keep, over []Observation) {
	if limit <= 0 || len(live) <= limit {
		return live, nil
	}
	return live[:limit], live[limit:]
}

/*
Admit answers a login the way Decide answers a pass: a device already holding a
slot is let back in, a new one only while a slot is free. held is every source
the client is live from, anywhere; a limit of 0 or less is unlimited.
*/
func Admit(held []string, source string, limit int) bool {
	if limit <= 0 {
		return true
	}
	distinct := make(map[string]struct{}, len(held))
	for _, h := range held {
		if h == source {
			return true
		}
		distinct[h] = struct{}{}
	}
	return len(distinct) < limit
}

// AdvancedSince keeps only over-cap pairs whose lastSeen advanced since their last
// report: a frozen value is a connection the daemon has not reaped, not a reconnect.
func AdvancedSince(email string, over []Observation, seen map[string]int64) (actionable []Observation, next map[string]int64) {
	prefix := email + "|"
	current := make(map[string]struct{}, len(over))
	for _, o := range over {
		current[prefix+o.IP] = struct{}{}
	}

	// Copy rather than mutate, dropping this email's pairs that are no longer
	// over so the caller's map cannot grow without bound.
	next = make(map[string]int64, len(seen)+len(over))
	for key, ts := range seen {
		if _, still := current[key]; !still && strings.HasPrefix(key, prefix) {
			continue
		}
		next[key] = ts
	}

	actionable = make([]Observation, 0, len(over))
	for _, o := range over {
		key := prefix + o.IP
		if last, ok := next[key]; ok && o.LastSeenUnixMilli <= last {
			continue
		}
		next[key] = o.LastSeenUnixMilli
		actionable = append(actionable, o)
	}
	return actionable, next
}
