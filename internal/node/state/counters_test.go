package state

import (
	"errors"
	"path/filepath"
	"testing"

	boltErrors "go.etcd.io/bbolt/errors"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestNativeCounterCheckpointCommitsWithUsage(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "node.db")
	store, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	prototype := checkpoint()
	prototype.Uploaded, prototype.Downloaded = 0, 0
	commit := func(previous, checkpoint core.CounterCheckpoint, deltas map[string]int64) error {
		var usage []MeterDelta
		if deltas["upload"] != 0 || deltas["download"] != 0 {
			usage = []MeterDelta{{Identity: prototype, Uploaded: uint64(deltas["upload"]), Downloaded: uint64(deltas["download"])}}
		}
		return store.CommitCounterUsage("native", previous, checkpoint, usage)
	}
	counter, err := store.LoadCounter("native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := counter.ObserveDurably("kernel-one", map[string]int64{}, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.ObserveDurably("kernel-one", map[string]int64{"upload": 100, "download": 200}, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingUsage(16); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	counter, err = store.LoadCounter("native")
	if err != nil {
		t.Fatal(err)
	}
	if delta, err := counter.ObserveDurably("kernel-one", map[string]int64{"upload": 100, "download": 200}, commit); err != nil || len(delta) != 0 {
		t.Fatalf("repeated native counters=%v,%v", delta, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.ObserveDurably("kernel-one", map[string]int64{"upload": 150, "download": 300}, commit); !errors.Is(err, boltErrors.ErrDatabaseNotOpen) {
		t.Fatalf("failed checkpoint commit=%v", err)
	}
	if snapshot := counter.Checkpoint(); snapshot.Last["upload"] != 100 || snapshot.Last["download"] != 200 {
		t.Fatalf("failed storage advanced baseline: %+v", snapshot)
	}
	store, err = Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	if delta, err := counter.ObserveDurably("kernel-one", map[string]int64{"upload": 150, "download": 300}, commit); err != nil || delta["upload"] != 50 || delta["download"] != 100 {
		t.Fatalf("retried native reading=%v,%v", delta, err)
	}
	pending, err := store.PendingUsage(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Sequence != 1 || pending[1].Sequence != 2 {
		t.Fatalf("native usage batches=%v", pending)
	}
	last := pending[1].Checkpoints[0]
	if last.Uploaded != 150 || last.Downloaded != 300 {
		t.Fatalf("native cumulative accounting=%v", last)
	}
	uncertain := errors.New("commit response lost")
	lost := func(previous, next core.CounterCheckpoint, deltas map[string]int64) error {
		if err := commit(previous, next, deltas); err != nil {
			return err
		}
		return uncertain
	}
	reading := map[string]int64{"upload": 175, "download": 350}
	if _, err := counter.ObserveDurably("kernel-one", reading, lost); !errors.Is(err, uncertain) {
		t.Fatalf("ambiguous commit=%v", err)
	}
	prototype.ObservedUnixMilli++
	if _, err := counter.ObserveDurably("kernel-one", reading, commit); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingUsage(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 || pending[2].Checkpoints[0].Uploaded != 175 || pending[2].Checkpoints[0].Downloaded != 350 {
		t.Fatalf("ambiguous commit charged again: %v", pending)
	}
}
