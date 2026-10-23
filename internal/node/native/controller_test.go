package native

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	boltErrors "go.etcd.io/bbolt/errors"

	"github.com/Arman2122/p-ui/internal/node/state"
)

type faultPlane struct {
	counts      map[string]Counters
	blocked     map[string]bool
	removed     []string
	removeError error
}

func (p *faultPlane) Install(_ context.Context, l Lease) error {
	p.counts[sliceName(l.ID)] = Counters{}
	return nil
}

func (p *faultPlane) Snapshot(context.Context) (map[string]Counters, error) {
	return maps.Clone(p.counts), nil
}

func (p *faultPlane) Block(_ context.Context, l Lease) (Counters, error) {
	count, ok := p.counts[sliceName(l.ID)]
	if !ok {
		return Counters{}, ErrMissing
	}
	p.blocked[l.ID] = true
	return count, nil
}

func (p *faultPlane) Stop(ctx context.Context, l Lease) (Counters, error) { return p.Block(ctx, l) }

func (p *faultPlane) Remove(_ context.Context, l Lease) error {
	delete(p.counts, sliceName(l.ID))
	p.removed = append(p.removed, l.ID)
	return p.removeError
}

func TestNativeHandoverRequiresDurableFinalUsage(t *testing.T) {
	dir := t.TempDir()
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	usage, err := state.Open(filepath.Join(dir, "usage.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	j, err := Open(filepath.Join(dir, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	plane := &faultPlane{counts: make(map[string]Counters), blocked: make(map[string]bool)}
	c, err := NewController(j, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	assignment := Assignment{Grant: testGrant(2000), Selectors: []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err := j.Records()
	if err != nil || len(records) != 1 || records[0].Lease == nil {
		t.Fatalf("records = %+v %v", records, err)
	}
	lease := *records[0].Lease
	plane.counts[sliceName(lease.ID)] = Counters{Uploaded: 400, Downloaded: 600}
	if err := c.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := usage.Close(); err != nil {
		t.Fatal(err)
	}
	plane.counts[sliceName(lease.ID)] = Counters{Uploaded: 450, Downloaded: 650}
	if err := c.Reconcile(t.Context(), nil); !errors.Is(err, boltErrors.ErrDatabaseNotOpen) {
		t.Fatalf("closed spool handover: %v", err)
	}
	if len(plane.removed) != 0 || !plane.blocked[lease.ID] {
		t.Fatalf("lost counters or left forwarding open: %+v", plane)
	}
	usage, err = state.Open(filepath.Join(dir, "usage.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = usage.Close() }()
	c, err = NewController(j, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	plane.removeError = errors.New("lost delete acknowledgment")
	if err := c.Recover(t.Context()); !errors.Is(err, plane.removeError) {
		t.Fatalf("ambiguous removal = %v", err)
	}
	plane.removeError = nil
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	batches, err := usage.PendingUsage(8)
	if err != nil || len(batches) != 1 || len(batches[0].Checkpoints) != 1 {
		t.Fatalf("batches = %+v %v", batches, err)
	}
	checkpoint := batches[0].Checkpoints[0]
	if checkpoint.Uploaded != 450 || checkpoint.Downloaded != 650 || checkpoint.MeterIncarnation != lease.Grant.ID {
		t.Fatalf("replayed or fabricated accounting: %+v", checkpoint)
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	if len(plane.counts) != 0 {
		t.Fatal("reissued uncertain remainder")
	}
	assignment.Grant.Policy.Revision++
	assignment.Grant.Policy.CreditCeiling = 2500
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err = j.Records()
	if err != nil || records[0].Lease == nil || records[0].Lease.Bytes != 500 {
		t.Fatalf("refill authority = %+v %v", records, err)
	}
	if err := c.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, ErrAuthority) {
		t.Fatalf("resurrected revoked grant: %v", err)
	}
	assignment.Grant.ID, assignment.Grant.Policy.Term = uuid.NewString(), uuid.NewString()
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err = j.Records()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Grant.ID == assignment.Grant.ID && r.Class != lease.Class {
			t.Fatalf("new term allocated a second rate class: %+v", r)
		}
	}
}

func TestNativeMissingKernelNeverBecomesUsage(t *testing.T) {
	dir := t.TempDir()
	usage, err := state.Open(filepath.Join(dir, "usage.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = usage.Close() }()
	j, err := Open(filepath.Join(dir, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	plane := &faultPlane{counts: make(map[string]Counters), blocked: make(map[string]bool)}
	c, err := NewController(j, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	assignment := Assignment{Grant: testGrant(1000), Selectors: []Selector{{"phvtest", netip.MustParseAddr("10.0.0.2")}}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	clear(plane.counts)
	if err := c.Collect(t.Context()); !errors.Is(err, ErrMissing) {
		t.Fatalf("lost kernel state: %v", err)
	}
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	batches, err := usage.PendingUsage(8)
	if err != nil || len(batches) != 0 || len(plane.counts) != 0 {
		t.Fatalf("fabricated usage or replayed credit: %+v %+v %v", batches, plane, err)
	}
}

func TestNativeRefillsCoalesceWithoutLosingUsage(t *testing.T) {
	dir := t.TempDir()
	usage, err := state.Open(filepath.Join(dir, "usage.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = usage.Close() }()
	j, err := Open(filepath.Join(dir, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	plane := &faultPlane{counts: make(map[string]Counters), blocked: make(map[string]bool)}
	c, err := NewController(j, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	assignment := Assignment{Grant: testGrant(32 * MaxSliceBytes), Selectors: []Selector{{"phvtest", netip.MustParseAddr("10.0.0.2")}}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	for i := range 32 {
		records, err := j.Records()
		if err != nil || len(records) != 1 || records[0].Lease == nil {
			t.Fatalf("slice %d: %+v %v", i, records, err)
		}
		lease := *records[0].Lease
		plane.counts[sliceName(lease.ID)] = Counters{Uploaded: MaxSliceBytes / 2, Downloaded: MaxSliceBytes/2 - 128}
		if err := c.Collect(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	batches, err := usage.PendingUsage(8)
	if err != nil || len(batches) != 1 || len(batches[0].Checkpoints) != 1 {
		t.Fatalf("unbounded refill queue: %+v %v", batches, err)
	}
	checkpoint := batches[0].Checkpoints[0]
	if checkpoint.Uploaded != 32*MaxSliceBytes/2 || checkpoint.Downloaded != 32*(MaxSliceBytes/2-128) {
		t.Fatalf("refill lost or replayed usage: %+v", checkpoint)
	}
	if len(plane.counts) != 0 {
		t.Fatal("forwarding survived exhausted grant")
	}
}
