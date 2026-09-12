package policy

import (
	"testing"
	"time"
)

func TestMonthlyResetClampsShortMonth(t *testing.T) {
	start := time.Date(2028, 1, 31, 10, 0, 0, 0, time.UTC)
	want := time.Date(2028, 2, 29, 10, 0, 0, 0, time.UTC)
	if got := NextReset(start, "monthly"); !got.Equal(want) {
		t.Fatalf("monthly reset: %s, want %s", got, want)
	}
}

func TestDepletionLadderOnlyCapsAfterQuota(t *testing.T) {
	original := Plan{Tiers: []Tier{{FromBytes: 0, DownBps: 2000000}, {FromBytes: 200, DownBps: 50000000}}}
	plan := DepletionLadder(original, 100)
	if got := Evaluate(plan, 99); got != (Limits{DownBps: 2000000}) {
		t.Fatalf("pre-quota changed: %+v", got)
	}
	if got := Evaluate(plan, 500); got != (Limits{UpBps: 8388608, DownBps: 2000000}) {
		t.Fatalf("post-quota cap lost: %+v", got)
	}
	if len(original.Tiers) != 2 || original.Tiers[1].FromBytes != 200 {
		t.Fatalf("depletion mutated source ladder: %+v", original)
	}
}
