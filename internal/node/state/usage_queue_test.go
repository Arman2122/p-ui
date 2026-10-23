package state

import (
	"bytes"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func TestOfflineUsageCoalescesWithoutRewritingExposedBatches(t *testing.T) {
	store := openStore(t, identity())
	checkpoints := make([]*nodev1.UsageCheckpoint, 5000)
	for i := range checkpoints {
		checkpoints[i] = checkpoint()
	}
	appendAll := func(multiplier uint64) {
		t.Helper()
		for _, checkpoint := range checkpoints {
			checkpoint.Uploaded = multiplier
			checkpoint.Downloaded = 2 * multiplier
		}
		for start := 0; start < len(checkpoints); start += 1000 {
			if _, err := store.AppendUsage(checkpoints[start:min(start+1000, len(checkpoints))]); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendAll(1)
	exposed, err := store.PendingUsage(1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := proto.MarshalOptions{Deterministic: true}.Marshal(exposed[0])
	if err != nil {
		t.Fatal(err)
	}
	for multiplier := uint64(2); multiplier <= 7; multiplier++ {
		appendAll(multiplier)
	}
	pending, err := store.PendingUsage(128)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) > 5 {
		t.Fatalf("offline queue grew with report count: %d batches", len(pending))
	}
	replay, err := proto.MarshalOptions{Deterministic: true}.Marshal(pending[0])
	if err != nil || !bytes.Equal(first, replay) {
		t.Fatalf("exposed batch changed: %v", err)
	}
	latest := make(map[string]*nodev1.UsageCheckpoint)
	for _, batch := range pending {
		for _, checkpoint := range batch.Checkpoints {
			latest[checkpoint.GrantId] = checkpoint
		}
	}
	if len(latest) != 5000 {
		t.Fatalf("coalescing lost meters: %d", len(latest))
	}
	for _, checkpoint := range latest {
		if checkpoint.Uploaded != 7 || checkpoint.Downloaded != 14 {
			t.Fatalf("coalescing lost latest usage: %v", checkpoint)
		}
	}
	last := pending[len(pending)-1].Sequence
	if err := store.Acknowledge(&nodev1.UsageReceipt{DurableSequence: last, AccountedSequence: last}); err != nil {
		t.Fatal(err)
	}
	if remaining, err := store.PendingUsage(128); err != nil || len(remaining) != 0 {
		t.Fatalf("acknowledged queue=%d,%v", len(remaining), err)
	}
}

func TestReceiptCannotEraseNeverExposedUsage(t *testing.T) {
	store := openStore(t, identity())
	sequence, err := store.AppendUsage([]*nodev1.UsageCheckpoint{checkpoint()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(&nodev1.UsageReceipt{DurableSequence: sequence}); !errors.Is(err, ErrUsage) {
		t.Fatalf("unsent receipt=%v", err)
	}
	if pending, err := store.PendingUsage(1); err != nil || len(pending) != 1 {
		t.Fatalf("unsent usage lost: %v,%v", pending, err)
	}
}
