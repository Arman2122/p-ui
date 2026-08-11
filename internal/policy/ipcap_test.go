package policy

import (
	"maps"
	"reflect"
	"testing"
	"time"
)

const (
	nowMilli = int64(1_700_000_000_000)
	minuteMs = int64(60_000)
	// liveWindow and staleCutoff are the job's shipped values: an entry counts
	// live for 120s across nodes, and a persisted entry ages out after 30m.
	liveWindow  = 120 * time.Second
	staleCutoff = nowMilli - 30*minuteMs
)

func obs(ip string, lastSeen int64) Observation {
	return Observation{IP: ip, LastSeenUnixMilli: lastSeen}
}

// assertObs compares element-wise so a nil bucket and an empty one read alike:
// every caller ranges over these, and neither shape means anything different.
func assertObs(t *testing.T, label string, got, want []Observation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func TestDecideKeepsTheNewestAddressesUpToTheCap(t *testing.T) {
	oldest := obs("10.0.0.1", nowMilli-3*minuteMs)
	middle := obs("10.0.0.2", nowMilli-2*minuteMs)
	newest := obs("10.0.0.3", nowMilli-1*minuteMs)
	all := []Observation{oldest, middle, newest}

	tests := []struct {
		name     string
		limit    int
		wantKeep []Observation
		wantBan  []Observation
	}{
		{"a cap of two bans the oldest of three", 2, []Observation{middle, newest}, []Observation{oldest}},
		{"a cap of one keeps only the newest", 1, []Observation{newest}, []Observation{oldest, middle}},
		{"a cap equal to the count bans nobody", 3, all, nil},
		{"a cap above the count bans nobody", 9, all, nil},
		{"a cap of zero is unlimited, never zero-allowed", 0, all, nil},
		{"a negative cap is unlimited too", -1, all, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(nil, all, tt.limit, nowMilli, staleCutoff, liveWindow, true)
			assertObs(t, "Keep", got.Keep, tt.wantKeep)
			assertObs(t, "Ban", got.Ban, tt.wantBan)
			assertObs(t, "Retain", got.Retain, nil)
		})
	}
}

func TestDecideTimestampRules(t *testing.T) {
	t.Run("a persisted entry below the stale cutoff is dropped from every bucket", func(t *testing.T) {
		stale := obs("10.0.0.9", staleCutoff-1)
		got := Decide([]Observation{stale}, nil, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Ban", got.Ban, nil)
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("a persisted entry exactly at the stale cutoff survives as history", func(t *testing.T) {
		edge := obs("10.0.0.9", staleCutoff)
		got := Decide([]Observation{edge}, nil, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Retain", got.Retain, []Observation{edge})
	})

	t.Run("an observation hours old is live because observedAreLive", func(t *testing.T) {
		opened := obs("10.0.0.1", nowMilli-5*60*minuteMs)
		got := Decide(nil, []Observation{opened}, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, []Observation{opened})
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("without observedAreLive that same observation is dropped", func(t *testing.T) {
		opened := obs("10.0.0.1", nowMilli-5*60*minuteMs)
		got := Decide(nil, []Observation{opened}, 2, nowMilli, staleCutoff, liveWindow, false)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Ban", got.Ban, nil)
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("a persisted entry this node never observed is live inside the window", func(t *testing.T) {
		remote := obs("10.0.0.5", nowMilli-60_000)
		got := Decide([]Observation{remote}, nil, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, []Observation{remote})
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("exactly at the live window edge it is historical", func(t *testing.T) {
		edge := obs("10.0.0.5", nowMilli-liveWindow.Milliseconds())
		got := Decide([]Observation{edge}, nil, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Retain", got.Retain, []Observation{edge})
	})

	t.Run("an address past the window moves to Retain, never to Ban", func(t *testing.T) {
		past := obs("10.0.0.5", nowMilli-150_000)
		here := obs("10.0.0.6", nowMilli)
		got := Decide([]Observation{past}, []Observation{here}, 1, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, []Observation{here})
		assertObs(t, "Ban", got.Ban, nil)
		assertObs(t, "Retain", got.Retain, []Observation{past})
	})

	t.Run("an observation the cutoff dropped still marks its address live", func(t *testing.T) {
		persisted := obs("10.0.0.7", nowMilli-25*minuteMs)
		skewed := obs("10.0.0.7", staleCutoff-1)
		got := Decide([]Observation{persisted}, []Observation{skewed}, 2, nowMilli, staleCutoff, liveWindow, false)
		assertObs(t, "Keep", got.Keep, []Observation{persisted})
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("the later timestamp wins when both sides know an address", func(t *testing.T) {
		persisted := obs("10.0.0.8", nowMilli-10*minuteMs)
		fresher := obs("10.0.0.8", nowMilli-minuteMs)
		got := Decide([]Observation{persisted}, []Observation{fresher}, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, []Observation{fresher})
	})

	t.Run("an older observation never rewinds a fresher persisted entry", func(t *testing.T) {
		persisted := obs("10.0.0.8", nowMilli-minuteMs)
		staler := obs("10.0.0.8", nowMilli-10*minuteMs)
		got := Decide([]Observation{persisted}, []Observation{staler}, 2, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Keep", got.Keep, []Observation{persisted})
	})
}

func TestDecideBreaksTimestampTiesOnTheAddress(t *testing.T) {
	higher := obs("10.0.0.2", nowMilli-minuteMs)
	lower := obs("10.0.0.1", nowMilli-minuteMs)
	for range 3 {
		got := Decide(nil, []Observation{higher, lower}, 1, nowMilli, staleCutoff, liveWindow, true)
		assertObs(t, "Ban", got.Ban, []Observation{lower})
		assertObs(t, "Keep", got.Keep, []Observation{higher})
	}
}

func TestDecideIsIdempotentAndLeavesItsInputAlone(t *testing.T) {
	persisted := []Observation{
		obs("10.0.0.1", nowMilli-4*minuteMs),
		obs("10.0.0.9", nowMilli-20*minuteMs),
	}
	observed := []Observation{
		obs("10.0.0.1", nowMilli-minuteMs),
		obs("10.0.0.2", nowMilli-2*minuteMs),
		obs("10.0.0.3", nowMilli-3*minuteMs),
	}
	persistedBefore := append([]Observation(nil), persisted...)
	observedBefore := append([]Observation(nil), observed...)

	first := Decide(persisted, observed, 2, nowMilli, staleCutoff, liveWindow, true)
	second := Decide(persisted, observed, 2, nowMilli, staleCutoff, liveWindow, true)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second pass differs\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if !reflect.DeepEqual(persisted, persistedBefore) {
		t.Fatalf("Decide reordered its persisted input: %v, want %v", persisted, persistedBefore)
	}
	if !reflect.DeepEqual(observed, observedBefore) {
		t.Fatalf("Decide reordered its observed input: %v, want %v", observed, observedBefore)
	}
}

func TestAdvancedSinceBansAFrozenConnectionOnlyOnce(t *testing.T) {
	const email = "a@example.test"
	banned := []Observation{obs("10.0.0.1", nowMilli)}

	actionable, seen := AdvancedSince(email, banned, nil)
	assertObs(t, "first ban", actionable, banned)

	actionable, seen = AdvancedSince(email, banned, seen)
	assertObs(t, "frozen rescan", actionable, nil)

	reconnected := []Observation{obs("10.0.0.1", nowMilli+minuteMs)}
	actionable, seen = AdvancedSince(email, reconnected, seen)
	assertObs(t, "reconnect", actionable, reconnected)

	actionable, seen = AdvancedSince(email, nil, seen)
	assertObs(t, "no longer over the cap", actionable, nil)

	// Pruned above, so the pair is actionable again even at its original —
	// now older — timestamp.
	actionable, _ = AdvancedSince(email, banned, seen)
	assertObs(t, "banned again after clearing", actionable, banned)
}

func TestAdvancedSinceIsScopedPerEmail(t *testing.T) {
	t.Run("two emails banning one address are each actionable", func(t *testing.T) {
		banned := []Observation{obs("10.0.0.1", nowMilli)}
		first, seen := AdvancedSince("a@example.test", banned, nil)
		second, _ := AdvancedSince("b@example.test", banned, seen)
		assertObs(t, "a@example.test", first, banned)
		assertObs(t, "b@example.test", second, banned)
	})

	t.Run("clearing one email does not forget another's ban", func(t *testing.T) {
		aBan := []Observation{obs("10.0.0.1", nowMilli)}
		_, seen := AdvancedSince("a@example.test", aBan, nil)
		_, seen = AdvancedSince("b@example.test", []Observation{obs("10.0.0.2", nowMilli)}, seen)
		_, seen = AdvancedSince("b@example.test", nil, seen)

		again, _ := AdvancedSince("a@example.test", aBan, seen)
		assertObs(t, "a@example.test rescan", again, nil)
	})
}

func TestAdvancedSinceDoesNotMutateTheCallersMap(t *testing.T) {
	const email = "a@example.test"
	_, seen := AdvancedSince(email, []Observation{obs("10.0.0.1", nowMilli)}, nil)
	before := maps.Clone(seen)

	_, _ = AdvancedSince(email, []Observation{obs("10.0.0.1", nowMilli+minuteMs)}, seen)
	_, _ = AdvancedSince(email, nil, seen)

	if !reflect.DeepEqual(seen, before) {
		t.Fatalf("AdvancedSince mutated the map it was handed: %v, want %v", seen, before)
	}
}

func TestAdvancedSinceNeverReturnsANilMap(t *testing.T) {
	if _, next := AdvancedSince("a@example.test", nil, nil); next == nil {
		t.Fatal("next map is nil; the caller has nothing to carry into the next pass")
	}
}
