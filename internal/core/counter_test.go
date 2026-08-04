package core

import (
	"sync"
	"testing"
)

func TestCounterBaselinePassAttributesNothing(t *testing.T) {
	c := NewCounter()
	got := c.Observe("boot-1", map[string]int64{"a": 500, "b": 900})
	if len(got) != 0 {
		t.Fatalf("first Observe returned %v, want nothing — the counters may already hold traffic a previous panel process billed", got)
	}
	if !c.Primed() {
		t.Error("counter did not prime on the first Observe")
	}
}

func TestCounterDeltas(t *testing.T) {
	tests := []struct {
		name     string
		epochs   []string
		readings []map[string]int64
		want     map[string]int64
	}{
		{
			name:     "steady growth",
			epochs:   []string{"b1", "b1"},
			readings: []map[string]int64{{"a": 100}, {"a": 175}},
			want:     map[string]int64{"a": 75},
		},
		{
			name:     "idle subject emits no delta",
			epochs:   []string{"b1", "b1"},
			readings: []map[string]int64{{"a": 100}, {"a": 100}},
			want:     map[string]int64{},
		},
		{
			name:     "subject first seen after priming counts from zero",
			epochs:   []string{"b1", "b1"},
			readings: []map[string]int64{{"a": 100}, {"a": 100, "b": 42}},
			want:     map[string]int64{"b": 42},
		},
		{
			// The bug in internal/mtproto/manager.go: it clamps the negative
			// difference away and loses everything moved since the restart.
			name:     "source restart counts the whole new reading",
			epochs:   []string{"b1", "b2"},
			readings: []map[string]int64{{"a": 9_000}, {"a": 120}},
			want:     map[string]int64{"a": 120},
		},
		{
			name:     "counter reset without an epoch change still counts from zero",
			epochs:   []string{"b1", "b1"},
			readings: []map[string]int64{{"a": 9_000}, {"a": 120}},
			want:     map[string]int64{"a": 120},
		},
		{
			name:     "restart to zero attributes nothing",
			epochs:   []string{"b1", "b2"},
			readings: []map[string]int64{{"a": 9_000}, {"a": 0}},
			want:     map[string]int64{},
		},
		{
			name:     "negative reading is refused rather than guessed",
			epochs:   []string{"b1", "b1"},
			readings: []map[string]int64{{"a": 100}, {"a": -5}},
			want:     map[string]int64{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCounter()
			var got map[string]int64
			for i, reading := range tc.readings {
				got = c.Observe(tc.epochs[i], reading)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("key %q: got %d, want %d", key, got[key], want)
				}
			}
		})
	}
}

func TestCounterRestartDoesNotDoubleCountAcrossThreePolls(t *testing.T) {
	c := NewCounter()
	c.Observe("b1", map[string]int64{"a": 1_000})
	if d := c.Observe("b1", map[string]int64{"a": 1_500}); d["a"] != 500 {
		t.Fatalf("pre-restart delta = %d, want 500", d["a"])
	}
	// Daemon restarts; its counter is back near zero.
	if d := c.Observe("b2", map[string]int64{"a": 200}); d["a"] != 200 {
		t.Fatalf("restart delta = %d, want 200 — the whole post-restart reading is new traffic", d["a"])
	}
	if d := c.Observe("b2", map[string]int64{"a": 260}); d["a"] != 60 {
		t.Fatalf("post-restart delta = %d, want 60", d["a"])
	}
}

func TestCounterForgetMakesASubjectCountFromZero(t *testing.T) {
	c := NewCounter()
	c.Observe("b1", map[string]int64{"a": 100})
	c.Observe("b1", map[string]int64{"a": 400})

	c.Forget("a")
	if d := c.Observe("b1", map[string]int64{"a": 30}); d["a"] != 30 {
		t.Fatalf("after Forget delta = %d, want 30 — a re-added subject counts from zero", d["a"])
	}
}

// TestCounterKeepsBaselinesThroughAPartialReading covers a scrape landing during
// a reload. Dropping those baselines re-bills each returning subject in full.
func TestCounterKeepsBaselinesThroughAPartialReading(t *testing.T) {
	c := NewCounter()
	full := map[string]int64{"a": 10, "b": 10, "c": 10, "d": 10, "e": 10, "f": 10}
	c.Observe("b1", full)
	c.Observe("b1", full)

	c.Observe("b1", map[string]int64{"a": 10})

	if d := c.Observe("b1", full); len(d) != 0 {
		t.Fatalf("a subject that missed one reading was re-billed: %v", d)
	}
}

// TestCounterTreatsAnEmptyEpochAsUnknown covers a source answering without its
// start token: read as new, the epoch flips twice and each flip wipes baselines.
func TestCounterTreatsAnEmptyEpochAsUnknown(t *testing.T) {
	c := NewCounter()
	c.Observe("b1", map[string]int64{"a": 100})
	if d := c.Observe("b1", map[string]int64{"a": 500}); d["a"] != 400 {
		t.Fatalf("delta = %d, want 400", d["a"])
	}

	if d := c.Observe("", map[string]int64{}); len(d) != 0 {
		t.Fatalf("a reading with no subjects bills nothing, got %v", d)
	}
	if d := c.Observe("b1", map[string]int64{"a": 600}); d["a"] != 100 {
		t.Fatalf("delta = %d, want 100 — an unknown epoch must not re-baseline a live counter", d["a"])
	}
}

func TestCounterIsSafeUnderConcurrentObserve(t *testing.T) {
	c := NewCounter()
	c.Observe("b1", map[string]int64{"a": 0})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Observe("b1", map[string]int64{"a": int64(n)})
		}(i)
	}
	wg.Wait()
}
