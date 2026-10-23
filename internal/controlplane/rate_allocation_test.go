package controlplane

import (
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestSharedRateAllocatorWaitsForActualReleaseAcrossNodes(t *testing.T) {
	budget := limitedRates(100_000_000, 1<<20)
	a := RateParticipant{ID: uuid.NewString(), Outstanding: budget, Eligible: true, MinimumBurst: 65536}
	b := RateParticipant{ID: uuid.NewString(), Eligible: true, MinimumBurst: 65536}
	allocation, err := AllocateSharedRates(budget, []RateParticipant{b, a})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]RateAllocation)
	for _, item := range allocation {
		byID[item.ID] = item
	}
	half := limitedRates(50_000_000, 1<<19)
	if byID[a.ID].Target != half || byID[b.ID].Target != half || byID[a.ID].Next != half || byID[b.ID].Next != (Rates{}) {
		t.Fatalf("unconfirmed reduction reused=%+v", allocation)
	}
	a.Outstanding = half
	allocation, err = AllocateSharedRates(budget, []RateParticipant{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range allocation {
		if item.Next != half {
			t.Fatalf("confirmed release not allocated=%+v", allocation)
		}
	}
	a.Outstanding, a.Frozen = budget, true
	allocation, err = AllocateSharedRates(budget, []RateParticipant{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range allocation {
		if item.ID == a.ID && item.Next != budget || item.ID == b.ID && item.Next != (Rates{}) {
			t.Fatalf("offline authority reused=%+v", allocation)
		}
	}
	a.Outstanding = limitedRates(40_000_000, 1<<19)
	allocation, err = AllocateSharedRates(budget, []RateParticipant{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range allocation {
		if item.ID == b.ID && item.Next != limitedRates(60_000_000, 1<<19) {
			t.Fatalf("unassigned capacity incorrectly withheld=%+v", allocation)
		}
	}
}

func TestSharedRateAllocatorPreservesPacketGranularityAndUnlimitedSemantics(t *testing.T) {
	participants := []RateParticipant{{ID: uuid.NewString(), Eligible: true, MinimumBurst: 65536}, {ID: uuid.NewString(), Eligible: true, MinimumBurst: 65536}}
	allocation, err := AllocateSharedRates(limitedRates(1_000_000, 65536), participants)
	var constraint *RateCapacityError
	if !errors.As(err, &constraint) || constraint.RequiredBurst != 131072 || constraint.AvailableBurst != 65536 || constraint.Participants != 2 {
		t.Fatalf("missing explicit packet concurrency constraint=%v", err)
	}
	for _, item := range allocation {
		if item.Next != (Rates{}) || item.Target != (Rates{}) {
			t.Fatalf("insufficient packet burst silently rounded upward=%+v", allocation)
		}
	}
	participants[0].Outstanding = limitedRates(1_000_000, 65536)
	allocation, err = AllocateSharedRates(limitedRates(1_000_000, 65536), participants)
	if !errors.As(err, &constraint) {
		t.Fatalf("infeasible new participant reported success=%v", err)
	}
	for _, item := range allocation {
		if item.ID == participants[0].ID && item.Next != participants[0].Outstanding {
			t.Fatalf("packet capacity shortage interrupted feasible service=%+v", allocation)
		}
	}
	participants[0].Outstanding = Rates{}
	budget := Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 101, BurstBytes: 131073}}
	allocation, err = AllocateSharedRates(budget, participants)
	if err != nil {
		t.Fatal(err)
	}
	var bits uint64
	var burst int
	for _, item := range allocation {
		if item.Next.Upload.Mode != limits.Unlimited || item.Next.Download.BurstBytes < 65536 {
			t.Fatalf("incorrect directional allocation=%+v", allocation)
		}
		bits += item.Next.Download.BitsPerSecond
		burst += item.Next.Download.BurstBytes
	}
	if bits != 101 || burst != 131073 {
		t.Fatalf("rounding multiplied authority: bits=%d burst=%d", bits, burst)
	}
}

func TestSharedRateAllocationCannotOversubscribeAnyApplicationOrder(t *testing.T) {
	random := rand.New(rand.NewPCG(128, 512))
	for range 100 {
		count := 2 + random.IntN(5)
		budget := limitedRates(uint64(1000+random.IntN(1000)), 4096)
		participants := make([]RateParticipant, count)
		for i := range participants {
			participants[i] = RateParticipant{ID: uuid.NewString(), Eligible: true, MinimumBurst: 64}
		}
		participants[random.IntN(count)].Outstanding = budget
		allocation, err := AllocateSharedRates(budget, participants)
		if err != nil {
			t.Fatal(err)
		}
		byID := make(map[string]Rates)
		for _, item := range participants {
			byID[item.ID] = item.Outstanding
		}
		for mask := 0; mask < 1<<count; mask++ {
			var uploads, downloads []limits.Rate
			for i, item := range allocation {
				value := byID[item.ID]
				if mask&(1<<i) != 0 {
					value = item.Next
				}
				uploads, downloads = append(uploads, value.Upload), append(downloads, value.Download)
			}
			if !fitsRates(budget.Upload, uploads) || !fitsRates(budget.Download, downloads) {
				t.Fatalf("unsafe application subset=%d allocation=%+v", mask, allocation)
			}
		}
	}
}

func TestRateAllocationDoesNotWrapOutstandingCapacity(t *testing.T) {
	participants := make([]RateParticipant, 2049)
	for i := range participants {
		participants[i] = RateParticipant{ID: uuid.NewString(), Eligible: true, MinimumBurst: 1}
		if i < 2048 {
			participants[i].Outstanding = limitedRates(1<<53, 1)
		}
	}
	newID := participants[2048].ID
	allocation, err := AllocateSharedRates(limitedRates(1<<53, 1<<20), participants)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range allocation {
		if item.ID == newID && item.Next != (Rates{}) {
			t.Fatalf("wrapped outstanding rate created capacity=%+v", item)
		}
	}
}
