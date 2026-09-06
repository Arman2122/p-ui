package policy

import (
	"reflect"
	"testing"
)

const (
	nowMilli = int64(1_700_000_000_000)
	minuteMs = int64(60_000)
	// staleCutoff is the job's shipped value: a persisted entry ages out after 30m.
	staleCutoff = nowMilli - 30*minuteMs
)

// obs is an address seen and started at the same moment, the way a core that
// records when a connection opened reports one.
func obs(ip string, at int64) Observation {
	return Observation{IP: ip, LastSeenUnixMilli: at, FirstSeenUnixMilli: at}
}

// sighting is an address whose start nobody recorded.
func sighting(ip string, lastSeen int64) Observation {
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

/*
The rule itself. The devices that arrived first hold the slots; the latest
arrival is the one over. Addresses run against arrival order so a pass that
happened to sort by address could not satisfy this.
*/
func TestDecideKeepsTheFirstDevicesAndNamesTheLatestOver(t *testing.T) {
	first := obs("10.0.0.3", nowMilli-3*minuteMs)
	second := obs("10.0.0.2", nowMilli-2*minuteMs)
	third := obs("10.0.0.1", nowMilli-1*minuteMs)
	all := []Observation{third, first, second}
	byArrival := []Observation{first, second, third}

	tests := []struct {
		name     string
		limit    int
		wantKeep []Observation
		wantOver []Observation
	}{
		{"a cap of two names the latest of three", 2, []Observation{first, second}, []Observation{third}},
		{"a cap of one keeps only the first", 1, []Observation{first}, []Observation{second, third}},
		{"a cap equal to the count names nobody", 3, byArrival, nil},
		{"a cap above the count names nobody", 9, byArrival, nil},
		{"a cap of zero is unlimited, never zero-allowed", 0, byArrival, nil},
		{"a negative cap is unlimited too", -1, byArrival, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(nil, all, tt.limit, nowMilli, staleCutoff, nil)
			assertObs(t, "Keep", got.Keep, tt.wantKeep)
			assertObs(t, "Over", got.Over, tt.wantOver)
			assertObs(t, "Retain", got.Retain, nil)
		})
	}
}

func TestDecideStampsARunNobodyRecorded(t *testing.T) {
	t.Run("an address seen here without a start starts now", func(t *testing.T) {
		got := Decide(nil, []Observation{sighting("10.0.0.1", nowMilli-5*minuteMs)}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli - 5*minuteMs, FirstSeenUnixMilli: nowMilli}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})

	t.Run("a persisted start survives the next sighting", func(t *testing.T) {
		persisted := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli - 10*minuteMs, FirstSeenUnixMilli: nowMilli - 20*minuteMs}
		got := Decide([]Observation{persisted}, []Observation{sighting("10.0.0.1", nowMilli)}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli, FirstSeenUnixMilli: nowMilli - 20*minuteMs}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})

	t.Run("a core that knows an earlier start corrects the record", func(t *testing.T) {
		persisted := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 20*minuteMs}
		opened := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli, FirstSeenUnixMilli: nowMilli - 3*60*minuteMs}
		got := Decide([]Observation{persisted}, []Observation{opened}, 2, nowMilli, staleCutoff, nil)
		assertObs(t, "Keep", got.Keep, []Observation{opened})
	})

	t.Run("a later start from the core never moves the record forward", func(t *testing.T) {
		persisted := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 3*60*minuteMs}
		reported := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli, FirstSeenUnixMilli: nowMilli - 20*minuteMs}
		got := Decide([]Observation{persisted}, []Observation{reported}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.1", LastSeenUnixMilli: nowMilli, FirstSeenUnixMilli: nowMilli - 3*60*minuteMs}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})
}

/*
A persisted address no core reported this pass has ended, however recent its
timestamp: the core that held it answered and it was not in the answer. It goes
to history with its run closed, and its slot is free for the device arriving now.
*/
func TestDecideEndsARunThatLeftTheLiveSet(t *testing.T) {
	gone := Observation{IP: "10.0.0.5", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 10*minuteMs}

	got := Decide([]Observation{gone}, nil, 1, nowMilli, staleCutoff, nil)
	assertObs(t, "Keep", got.Keep, nil)
	assertObs(t, "Over", got.Over, nil)
	assertObs(t, "Retain", got.Retain, []Observation{{IP: "10.0.0.5", LastSeenUnixMilli: nowMilli - minuteMs}})

	arriving := obs("10.0.0.6", nowMilli)
	got = Decide([]Observation{gone}, []Observation{arriving}, 1, nowMilli, staleCutoff, nil)
	assertObs(t, "Keep", got.Keep, []Observation{arriving})
	assertObs(t, "Over", got.Over, nil)
}

func TestDecideCountsAnAddressAnotherNodeHolds(t *testing.T) {
	elsewhere := map[string]bool{"10.0.0.5": true}

	t.Run("held elsewhere, it keeps its slot ahead of a later arrival here", func(t *testing.T) {
		remote := Observation{IP: "10.0.0.5", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 10*minuteMs}
		here := obs("10.0.0.6", nowMilli)
		got := Decide([]Observation{remote}, []Observation{here}, 1, nowMilli, staleCutoff, elsewhere)
		assertObs(t, "Keep", got.Keep, []Observation{remote})
		assertObs(t, "Over", got.Over, []Observation{here})
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("a node that recorded no start is taken to have started at its last sighting", func(t *testing.T) {
		remote := sighting("10.0.0.5", nowMilli-minuteMs)
		here := obs("10.0.0.6", nowMilli-5*minuteMs)
		got := Decide([]Observation{remote}, []Observation{here}, 1, nowMilli, staleCutoff, elsewhere)
		assertObs(t, "Keep", got.Keep, []Observation{here})
		assertObs(t, "Over", got.Over, []Observation{obs("10.0.0.5", nowMilli-minuteMs)})
	})
}

func TestDecideTimestampRules(t *testing.T) {
	t.Run("a persisted entry below the stale cutoff is dropped from every bucket", func(t *testing.T) {
		stale := sighting("10.0.0.9", staleCutoff-1)
		got := Decide([]Observation{stale}, nil, 2, nowMilli, staleCutoff, nil)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Over", got.Over, nil)
		assertObs(t, "Retain", got.Retain, nil)
	})

	t.Run("a persisted entry exactly at the stale cutoff survives as history", func(t *testing.T) {
		edge := sighting("10.0.0.9", staleCutoff)
		got := Decide([]Observation{edge}, nil, 2, nowMilli, staleCutoff, nil)
		assertObs(t, "Keep", got.Keep, nil)
		assertObs(t, "Retain", got.Retain, []Observation{edge})
	})

	t.Run("an observation below the cutoff is live all the same: a core reported it", func(t *testing.T) {
		skewed := sighting("10.0.0.7", staleCutoff-1)
		got := Decide(nil, []Observation{skewed}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.7", LastSeenUnixMilli: staleCutoff - 1, FirstSeenUnixMilli: nowMilli}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})

	t.Run("the later sighting wins when both sides know an address", func(t *testing.T) {
		persisted := obs("10.0.0.8", nowMilli-10*minuteMs)
		fresher := obs("10.0.0.8", nowMilli-minuteMs)
		got := Decide([]Observation{persisted}, []Observation{fresher}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.8", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 10*minuteMs}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})

	t.Run("an older sighting never rewinds a fresher persisted entry", func(t *testing.T) {
		persisted := obs("10.0.0.8", nowMilli-minuteMs)
		staler := obs("10.0.0.8", nowMilli-10*minuteMs)
		got := Decide([]Observation{persisted}, []Observation{staler}, 2, nowMilli, staleCutoff, nil)
		want := Observation{IP: "10.0.0.8", LastSeenUnixMilli: nowMilli - minuteMs, FirstSeenUnixMilli: nowMilli - 10*minuteMs}
		assertObs(t, "Keep", got.Keep, []Observation{want})
	})
}

func TestDecideBreaksArrivalTiesOnTheAddress(t *testing.T) {
	higher := obs("10.0.0.2", nowMilli-minuteMs)
	lower := obs("10.0.0.1", nowMilli-minuteMs)
	for range 3 {
		got := Decide(nil, []Observation{higher, lower}, 1, nowMilli, staleCutoff, nil)
		assertObs(t, "Keep", got.Keep, []Observation{lower})
		assertObs(t, "Over", got.Over, []Observation{higher})
	}
}

func TestDecideIsIdempotentAndLeavesItsInputAlone(t *testing.T) {
	persisted := []Observation{
		obs("10.0.0.1", nowMilli-4*minuteMs),
		sighting("10.0.0.9", nowMilli-20*minuteMs),
	}
	observed := []Observation{
		sighting("10.0.0.1", nowMilli-minuteMs),
		obs("10.0.0.2", nowMilli-2*minuteMs),
		obs("10.0.0.3", nowMilli-3*minuteMs),
	}
	persistedBefore := append([]Observation(nil), persisted...)
	observedBefore := append([]Observation(nil), observed...)

	first := Decide(persisted, observed, 2, nowMilli, staleCutoff, nil)
	second := Decide(persisted, observed, 2, nowMilli, staleCutoff, nil)

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

func TestAdmitLetsAKnownDeviceBackAndANewOneIntoAFreeSlot(t *testing.T) {
	tests := []struct {
		name   string
		held   []string
		source string
		limit  int
		want   bool
	}{
		{"unlimited admits anyone", []string{"a", "b", "c"}, "d", 0, true},
		{"a negative cap is unlimited too", []string{"a", "b", "c"}, "d", -1, true},
		{"a free slot admits a new device", []string{"a"}, "d", 2, true},
		{"at the cap a new device is refused", []string{"a", "b"}, "d", 2, false},
		{"at the cap a device already holding a slot is let back in", []string{"a", "b"}, "a", 2, true},
		{"a device reported twice holds one slot", []string{"a", "a"}, "d", 2, true},
		{"nobody connected, the first device is admitted", nil, "d", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Admit(tt.held, tt.source, tt.limit); got != tt.want {
				t.Fatalf("Admit(%v, %q, %d) = %v, want %v", tt.held, tt.source, tt.limit, got, tt.want)
			}
		})
	}
}
