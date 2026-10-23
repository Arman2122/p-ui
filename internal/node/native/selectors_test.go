package native

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/state"
)

type selectorFaultPlane struct {
	faultPlane
	replace          func(Lease, Lease) error
	calls            int
	removedSelectors []Selector
}

func (p *selectorFaultPlane) ReplaceSelectors(_ context.Context, before, after Lease) error {
	p.calls++
	if p.replace != nil {
		return p.replace(before, after)
	}
	return nil
}

func (p *selectorFaultPlane) Remove(ctx context.Context, lease Lease) error {
	p.removedSelectors = slices.Clone(lease.Selectors)
	return p.faultPlane.Remove(ctx, lease)
}

func selectorFixture(t *testing.T) (*Controller, *Journal, *state.Store, *selectorFaultPlane, Assignment) {
	t.Helper()
	dir := t.TempDir()
	usage, err := state.Open(filepath.Join(dir, "usage.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = usage.Close() })
	journal, err := Open(filepath.Join(dir, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	plane := &selectorFaultPlane{faultPlane: faultPlane{counts: map[string]Counters{}, blocked: map[string]bool{}}}
	controller, err := NewController(journal, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	return controller, journal, usage, plane, Assignment{Grant: testGrant(4 * MaxSliceBytes)}
}

func TestNativeDynamicSelectorsPreserveQuotaAndDormancy(t *testing.T) {
	c, j, _, p, assignment := selectorFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err := j.Records()
	if err != nil || records[0].Reserved != 0 || records[0].Lease != nil {
		t.Fatalf("dormant grant consumed authority: %+v %v", records, err)
	}
	a := Selector{"phvtest1", netip.MustParseAddr("10.0.0.2")}
	b := Selector{"phvtest2", netip.MustParseAddr("10.0.0.3")}
	assignment.Selectors = []Selector{a}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err = j.Records()
	if err != nil {
		t.Fatal(err)
	}
	lease := *records[0].Lease
	p.counts[sliceName(lease.ID)] = Counters{Uploaded: 731, Downloaded: 942}
	for range 10 {
		for _, selectors := range [][]Selector{{a, b}, {b}, nil, {a}} {
			assignment.Selectors = selectors
			if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
				t.Fatal(err)
			}
		}
	}
	records, err = j.Records()
	if err != nil || records[0].Reserved != MaxSliceBytes || records[0].Lease.ID != lease.ID || len(p.counts) != 1 || p.counts[sliceName(lease.ID)].Uploaded != 731 {
		t.Fatalf("dynamic sessions reset or reissued credit: %+v %+v %v", records, p.counts, err)
	}
	assignment.Grant.Policy.Enabled = false
	assignment.Grant.Policy.Revision++
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	if !p.blocked[lease.ID] || len(p.counts) != 0 {
		t.Fatal("revoked dynamic grant stayed open")
	}
}

func TestNativeSelectorUpdateRetriesStagedIntent(t *testing.T) {
	c, j, _, p, assignment := selectorFixture(t)
	assignment.Selectors = []Selector{{"phvtest1", netip.MustParseAddr("10.0.0.2")}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	assignment.Selectors = append(assignment.Selectors, Selector{"phvtest2", netip.MustParseAddr("10.0.0.3")})
	failure := errors.New("kernel transaction did not commit")
	p.replace = func(Lease, Lease) error { return failure }
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	records, err := j.Records()
	if err != nil || !records[0].SelectorsPending {
		t.Fatalf("missing pending selector update: %+v %v", records, err)
	}
	p.replace = nil
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, err = j.Records()
	if err != nil || records[0].SelectorsPending || p.calls != 2 {
		t.Fatalf("staged intent mistaken for kernel state: %+v calls=%d %v", records, p.calls, err)
	}
}

func TestNativeSelectorRecoveryRetiresUnionWithoutInventingUsage(t *testing.T) {
	c, j, usage, p, assignment := selectorFixture(t)
	old := Selector{"phvtest1", netip.MustParseAddr("10.0.0.2")}
	next := Selector{"phvtest2", netip.MustParseAddr("10.0.0.3")}
	assignment.Selectors = []Selector{old}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	records, _ := j.Records()
	lease := *records[0].Lease
	p.counts[sliceName(lease.ID)] = Counters{Uploaded: 1200, Downloaded: 1900}
	failure := errors.New("lost selector commit result")
	p.replace = func(Lease, Lease) error { return failure }
	assignment.Selectors = []Selector{next}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	path := j.db.Path()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	j = reopened
	defer j.Close()
	c, err = NewController(j, usage, "native", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !p.blocked[lease.ID] || !slices.Contains(p.removedSelectors, old) || !slices.Contains(p.removedSelectors, next) {
		t.Fatalf("recovery lost possible selector ownership: %+v", p)
	}
	batches, err := usage.PendingUsage(2)
	if err != nil || len(batches) != 1 || len(batches[0].Checkpoints) != 1 {
		t.Fatalf("usage: %+v %v", batches, err)
	}
	checkpoint := batches[0].Checkpoints[0]
	if checkpoint.Uploaded != 1200 || checkpoint.Downloaded != 1900 {
		t.Fatalf("selector recovery invented usage: %+v", checkpoint)
	}
	records, err = j.Records()
	if err != nil || records[0].Reserved != MaxSliceBytes || records[0].Lease != nil {
		t.Fatalf("selector recovery replayed reservation: %+v %v", records, err)
	}
}
