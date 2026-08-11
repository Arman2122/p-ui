package policy

import "testing"

// gib keeps the spec's ladder readable: 50*gib is the 53687091200 it stores.
const gib = int64(1) << 30

// exampleLadder is the spec's own example: 0-50 GB unlimited, 50-100 GB 10 Mbps,
// 100 GB and up 2 Mbps.
var exampleLadder = Plan{Tiers: []Tier{
	{FromBytes: 0, UpBps: 0, DownBps: 0},
	{FromBytes: 50 * gib, UpBps: 10_000_000, DownBps: 10_000_000},
	{FromBytes: 100 * gib, UpBps: 2_000_000, DownBps: 2_000_000},
}}

var (
	unlimited = Limits{}
	tenMbps   = Limits{UpBps: 10_000_000, DownBps: 10_000_000}
	twoMbps   = Limits{UpBps: 2_000_000, DownBps: 2_000_000}
)

func TestEvaluateTierBoundaries(t *testing.T) {
	tests := []struct {
		name string
		used int64
		want Limits
	}{
		{"a fresh client sits on the first tier", 0, unlimited},
		{"one byte below 50 GiB is still unlimited", 50*gib - 1, unlimited},
		{"50 GiB exactly binds the next tier", 50 * gib, tenMbps},
		{"99 GiB is still the 10 Mbps tier", 99 * gib, tenMbps},
		{"one byte below 100 GiB is still 10 Mbps", 100*gib - 1, tenMbps},
		{"100 GiB exactly binds the last tier", 100 * gib, twoMbps},
		{"10 TiB stays on the last tier", 10 * 1024 * gib, twoMbps},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Evaluate(exampleLadder, tt.used); got != tt.want {
				t.Fatalf("Evaluate(exampleLadder, %d) = %+v, want %+v", tt.used, got, tt.want)
			}
		})
	}
}

func TestEvaluateFailsOpenOnAMalformedPlan(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		used int64
	}{
		{"an empty plan never throttles", Plan{}, 10 * 1024 * gib},
		{"a nil tier list never throttles", Plan{Tiers: nil}, 10 * 1024 * gib},
		{
			"a ladder whose first tier starts above used never throttles",
			Plan{Tiers: []Tier{{FromBytes: 50 * gib, UpBps: 1, DownBps: 1}}},
			50*gib - 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Evaluate(tt.plan, tt.used); got != unlimited {
				t.Fatalf("Evaluate(%+v, %d) = %+v, want %+v", tt.plan, tt.used, got, unlimited)
			}
		})
	}
}

func TestEvaluateIsOrderInsensitive(t *testing.T) {
	shuffled := Plan{Tiers: []Tier{
		exampleLadder.Tiers[2],
		exampleLadder.Tiers[0],
		exampleLadder.Tiers[1],
	}}
	if got := Evaluate(shuffled, 100*gib); got != twoMbps {
		t.Fatalf("Evaluate(shuffled, 100 GiB) = %+v, want %+v", got, twoMbps)
	}
	for _, used := range []int64{0, 50*gib - 1, 50 * gib, 100*gib - 1, 100 * gib, 10 * 1024 * gib} {
		if got, want := Evaluate(shuffled, used), Evaluate(exampleLadder, used); got != want {
			t.Fatalf("used=%d: unsorted ladder gave %+v, sorted gave %+v", used, got, want)
		}
	}
}

func TestEvaluateZeroBpsIsUnlimitedNotBlocked(t *testing.T) {
	t.Run("an explicit zero tier answers exactly as having no plan at all", func(t *testing.T) {
		if got, want := Evaluate(exampleLadder, 0), Evaluate(Plan{}, 0); got != want {
			t.Fatalf("tier 0 gave %+v, no plan gave %+v — zero must mean unlimited", got, want)
		}
	})

	t.Run("a zero tier above a throttled one restores full speed", func(t *testing.T) {
		plan := Plan{Tiers: []Tier{
			{FromBytes: 0, UpBps: 2_000_000, DownBps: 2_000_000},
			{FromBytes: 100 * gib, UpBps: 0, DownBps: 0},
		}}
		if got := Evaluate(plan, 100*gib); got != unlimited {
			t.Fatalf("Evaluate(plan, 100 GiB) = %+v, want %+v", got, unlimited)
		}
	})

	t.Run("zero in one direction leaves only that direction unlimited", func(t *testing.T) {
		plan := Plan{Tiers: []Tier{{FromBytes: 0, UpBps: 1_000_000, DownBps: 0}}}
		want := Limits{UpBps: 1_000_000, DownBps: 0}
		if got := Evaluate(plan, 0); got != want {
			t.Fatalf("Evaluate(plan, 0) = %+v, want %+v", got, want)
		}
	})
}

func TestEvaluateDerivesTheTierAfterATrafficReset(t *testing.T) {
	if got := Evaluate(exampleLadder, 60*gib); got != tenMbps {
		t.Fatalf("before the reset = %+v, want %+v", got, tenMbps)
	}
	// A reset zeroes the counters and touches no policy state, so the very same
	// call re-derives tier 0 — and re-deriving it again is still tier 0.
	if got := Evaluate(exampleLadder, 0); got != unlimited {
		t.Fatalf("after the reset = %+v, want %+v", got, unlimited)
	}
	if got := Evaluate(exampleLadder, 60*gib); got != tenMbps {
		t.Fatalf("after re-accumulating = %+v, want %+v", got, tenMbps)
	}
}
