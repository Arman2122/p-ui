package policy

import (
	"sort"
	"strings"
	"time"
)

// Observation is one address a client was seen at. LastSeenUnixMilli is
// load-bearing: a frozen value is a dead connection, not a reconnect.
type Observation struct {
	IP                string
	LastSeenUnixMilli int64
}

// Verdict splits one client's known addresses. Keep+Retain is what the caller
// persists; Ban is what it reports, oldest first.
type Verdict struct {
	Keep   []Observation
	Ban    []Observation
	Retain []Observation
}

// Decide answers which of a client's addresses are over their cap. staleCutoff
// and now are the caller's clock: this package never reads one of its own.
func Decide(
	persisted, observed []Observation,
	limit int,
	nowUnixMilli, staleCutoffUnixMilli int64,
	liveWindow time.Duration,
	observedAreLive bool,
) Verdict {
	merged := merge(persisted, observed, staleCutoffUnixMilli, observedAreLive)

	// Every observed address, including one the cutoff dropped from merged:
	// presence and recency are separate axes and collapsing them loses the first.
	seenThisPass := make(map[string]bool, len(observed))
	for _, o := range observed {
		seenThisPass[o.IP] = true
	}

	live, retain := partition(merged, seenThisPass, nowUnixMilli, liveWindow)
	keep, ban := overCap(live, limit)
	return Verdict{Keep: keep, Ban: ban, Retain: retain}
}

// merge folds observations into persisted, dropping entries below staleCutoff.
// observedAreLive exempts them: a live connection's lastSeen is its dispatch time.
func merge(persisted, observed []Observation, staleCutoff int64, observedAreLive bool) map[string]int64 {
	out := make(map[string]int64, len(persisted)+len(observed))
	for _, o := range persisted {
		if o.LastSeenUnixMilli < staleCutoff {
			continue
		}
		out[o.IP] = o.LastSeenUnixMilli
	}
	for _, o := range observed {
		if !observedAreLive && o.LastSeenUnixMilli < staleCutoff {
			continue
		}
		if prev, ok := out[o.IP]; !ok || o.LastSeenUnixMilli > prev {
			out[o.IP] = o.LastSeenUnixMilli
		}
	}
	return out
}

// partition splits merged into live and historical. An address this pass did not
// observe still counts live inside liveWindow — that is what makes the cap cluster-wide.
func partition(merged map[string]int64, seenThisPass map[string]bool, now int64, liveWindow time.Duration) (live, historical []Observation) {
	window := liveWindow.Milliseconds()
	live = make([]Observation, 0, len(seenThisPass))
	historical = make([]Observation, 0, len(merged))
	for ip, ts := range merged {
		entry := Observation{IP: ip, LastSeenUnixMilli: ts}
		if seenThisPass[ip] || now-ts < window {
			live = append(live, entry)
		} else {
			historical = append(historical, entry)
		}
	}
	sortOldestFirst(live)
	sortOldestFirst(historical)
	return live, historical
}

// sortOldestFirst breaks ties on the address deliberately: map order is random,
// so an arbitrary pick would ban a different client device on every pass.
func sortOldestFirst(obs []Observation) {
	sort.Slice(obs, func(i, j int) bool {
		if obs[i].LastSeenUnixMilli != obs[j].LastSeenUnixMilli {
			return obs[i].LastSeenUnixMilli < obs[j].LastSeenUnixMilli
		}
		return obs[i].IP < obs[j].IP
	})
}

// overCap keeps the newest limit addresses and bans the older remainder. A limit
// of 0 or less is UNLIMITED, never zero-allowed: no cap can never mean no access.
func overCap(live []Observation, limit int) (keep, ban []Observation) {
	if limit <= 0 || len(live) <= limit {
		return live, nil
	}
	cutoff := len(live) - limit
	return live[cutoff:], live[:cutoff]
}

// AdvancedSince keeps only banned pairs whose lastSeen advanced since their last
// ban: a frozen value is a connection the daemon has not reaped, not a reconnect.
func AdvancedSince(email string, banned []Observation, seen map[string]int64) (actionable []Observation, next map[string]int64) {
	prefix := email + "|"
	current := make(map[string]struct{}, len(banned))
	for _, o := range banned {
		current[prefix+o.IP] = struct{}{}
	}

	// Copy rather than mutate, dropping this email's pairs that are no longer
	// banned so the caller's map cannot grow without bound.
	next = make(map[string]int64, len(seen)+len(banned))
	for key, ts := range seen {
		if _, still := current[key]; !still && strings.HasPrefix(key, prefix) {
			continue
		}
		next[key] = ts
	}

	actionable = make([]Observation, 0, len(banned))
	for _, o := range banned {
		key := prefix + o.IP
		if last, ok := next[key]; ok && o.LastSeenUnixMilli <= last {
			continue
		}
		next[key] = o.LastSeenUnixMilli
		actionable = append(actionable, o)
	}
	return actionable, next
}
