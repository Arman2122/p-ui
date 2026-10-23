package native

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	boltErrors "go.etcd.io/bbolt/errors"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type authorityFaultPlane struct {
	*selectorFaultPlane
	replacements int
	failure      error
	before       Lease
	after        Lease
	afterChange  func() error
}

func (p *authorityFaultPlane) ReplacePolicy(_ context.Context, before, after Lease) error {
	p.replacements++
	p.before, p.after = before, after
	if p.failure != nil {
		return p.failure
	}
	if _, present := p.counts[sliceName(before.ID)]; !present {
		return ErrMissing
	}
	p.blocked[before.ID] = false
	if p.afterChange != nil {
		return p.afterChange()
	}
	return nil
}

func authorityFixture(t *testing.T) (*Controller, *Journal, *authorityFaultPlane, Assignment, Assignment) {
	t.Helper()
	c, journal, _, base, a := selectorFixture(t)
	plane := &authorityFaultPlane{selectorFaultPlane: base}
	c.plane = plane
	a.Grant.RateRevision, a.Grant.Policy.AuthorityRevision, a.Grant.Policy.CreditRevision = 1, 1, 1
	a.Grant.Policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 800000, BurstBytes: 65536}
	a.Grant.Policy.Download = a.Grant.Policy.Upload
	a.Selectors = []Selector{{Device: "phvtest1", Address: netip.MustParseAddr("10.0.0.2")}}
	b := a
	b.Grant.ID, b.Grant.ClientID = uuid.NewString(), uuid.NewString()
	b.Selectors = []Selector{{Device: "phvtest2", Address: netip.MustParseAddr("10.0.0.3")}}
	return c, journal, plane, a, b
}

func authorityRecordFor(t *testing.T, c *Controller, id string) grantRecord {
	t.Helper()
	record, err := c.authorityRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func lowerAuthority(assignment Assignment) Assignment {
	assignment.Grant.RateRevision++
	assignment.Grant.Policy.AuthorityRevision++
	assignment.Grant.Policy.Upload.BitsPerSecond /= 2
	assignment.Grant.Policy.Download.BitsPerSecond /= 2
	return assignment
}

func TestNativeAuthorityPreservesLeaseCountersCreditAndNeighbor(t *testing.T) {
	c, journal, plane, a, b := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	before, neighbor := authorityRecordFor(t, c, a.Grant.ID), authorityRecordFor(t, c, b.Grant.ID)
	count := Counters{Uploaded: 300, Downloaded: 500}
	plane.counts[sliceName(before.Lease.ID)] = count
	if err := c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(a, 2, a.Grant.Policy.CreditCeiling+1000)}); err != nil {
		t.Fatal(err)
	}
	desired := lowerAuthority(a)
	applied, err := c.ApplyAuthority(t.Context(), []Assignment{desired})
	if err != nil || len(applied) != 1 || applied[0].Policy.CreditRevision != 2 || applied[0].Policy.CreditCeiling != a.Grant.Policy.CreditCeiling+1000 {
		t.Fatalf("authority lost newer credit: %+v %v", applied, err)
	}
	after := authorityRecordFor(t, c, a.Grant.ID)
	if after.PendingAuthority != nil || after.Reserved != before.Reserved || after.Class != before.Class || after.Lease == nil || after.Lease.ID != before.Lease.ID || after.Lease.Bytes != before.Lease.Bytes || after.Lease.ExpiresUnixMilli != before.Lease.ExpiresUnixMilli || !slices.Equal(after.Lease.Selectors, before.Lease.Selectors) || plane.counts[sliceName(before.Lease.ID)] != count || len(plane.removed) != 0 {
		t.Fatalf("same-base authority replaced lease, quota or counters: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(neighbor, authorityRecordFor(t, c, b.Grant.ID)) || plane.blocked[neighbor.Lease.ID] {
		t.Fatal("authority update disturbed unrelated client")
	}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{desired}); err != nil || plane.replacements != 1 {
		t.Fatalf("authority replay performed kernel work: calls=%d error=%v", plane.replacements, err)
	}
	if err := c.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	usage, err := c.usage.PendingUsage(8)
	if err != nil || len(usage) != 1 || len(usage[0].Checkpoints) != 1 || usage[0].Checkpoints[0].Uploaded != count.Uploaded || usage[0].Checkpoints[0].Downloaded != count.Downloaded {
		t.Fatalf("authority changed cumulative usage: %+v %v", usage, err)
	}
	if err := journal.Apply(a.Grant); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("old same-base policy rewrote applied authority: %v", err)
	}
}

func TestNativeAuthorityInitialGrantAndDormantSelectors(t *testing.T) {
	c, _, plane, a, b := authorityFixture(t)
	b.Selectors = nil
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	active, dormant := authorityRecordFor(t, c, a.Grant.ID), authorityRecordFor(t, c, b.Grant.ID)
	if active.Lease == nil || active.Reserved != MaxSliceBytes || dormant.Lease != nil || dormant.Reserved != 0 || len(plane.counts) != 1 {
		t.Fatalf("initial authority fabricated selectors or missed install: %+v %+v", active, dormant)
	}
	b.Selectors = []Selector{{Device: "phvtest2", Address: netip.MustParseAddr("10.0.0.3")}}
	if err := c.Reconcile(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	if after := authorityRecordFor(t, c, b.Grant.ID); after.Lease == nil || after.Reserved != MaxSliceBytes || len(plane.counts) != 2 {
		t.Fatalf("authenticated selector did not install original grant: %+v", after)
	}
}

func TestNativePendingAuthorityFencesOldPathsAndResumesWithoutReservation(t *testing.T) {
	c, journal, plane, a, b := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	desired := lowerAuthority(a)
	plane.failure = errors.New("interrupted class update")
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{desired}); !errors.Is(err, plane.failure) {
		t.Fatalf("failed kernel update was acknowledged: %v", err)
	}
	pending := authorityRecordFor(t, c, a.Grant.ID)
	if pending.PendingAuthority == nil || pending.Grant != a.Grant || !plane.blocked[before.Lease.ID] || pending.Reserved != before.Reserved {
		t.Fatalf("failed authority lost its durable closed transition: %+v", pending)
	}
	page, err := c.Authority(t.Context(), "", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range page.Grants {
		if grant.GrantID == a.Grant.ID && (grant.AuthorizationActive || grant.SpendableKnown || grant.AuthorityRevision != 1) {
			t.Fatalf("pending authority advertised applied credit: %+v", grant)
		}
	}
	if err := c.Reconcile(t.Context(), []Assignment{b}); !errors.Is(err, ErrBusy) {
		t.Fatalf("old configuration bypassed pending authority: %v", err)
	}
	if err := journal.Apply(a.Grant); !errors.Is(err, ErrBusy) {
		t.Fatalf("old journal policy bypassed pending authority: %v", err)
	}
	if err := c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(a, 2, a.Grant.Policy.CreditCeiling+1000)}); !errors.Is(err, limits.ErrBlocked) {
		t.Fatalf("credit bypassed pending authority: %v", err)
	}
	if err := c.ValidateAuthority(t.Context(), []Assignment{lowerAuthority(desired)}); !errors.Is(err, ErrBusy) {
		t.Fatalf("newer delivery overwrote unsettled intent: %v", err)
	}
	if err := c.Collect(t.Context()); err != nil || !plane.blocked[before.Lease.ID] {
		t.Fatalf("collection reopened failed authority: %v", err)
	}
	plane.failure = nil
	applied, err := c.ResumeAuthority(t.Context())
	after := authorityRecordFor(t, c, a.Grant.ID)
	if err != nil || len(applied) != 1 || after.PendingAuthority != nil || after.Grant != desired.Grant || after.Reserved != before.Reserved || after.Lease.ID != before.Lease.ID || plane.blocked[before.Lease.ID] {
		t.Fatalf("authority retry reset reservation or failed to converge: %+v %v", after, err)
	}
}

func TestNativeAuthorityStopRequiresKernelProofAndCannotRevive(t *testing.T) {
	c, _, plane, a, _ := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	stop := lowerAuthority(a)
	stop.Grant.Policy.Enabled = false
	delete(plane.counts, sliceName(before.Lease.ID))
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{stop}); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing kernel state acknowledged stop: %v", err)
	}
	if authorityRecordFor(t, c, a.Grant.ID).PendingAuthority == nil {
		t.Fatal("missing kernel state cleared stop intent")
	}
	plane.counts[sliceName(before.Lease.ID)] = Counters{Uploaded: 100, Downloaded: 200}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{stop}); err != nil {
		t.Fatal(err)
	}
	after := authorityRecordFor(t, c, a.Grant.ID)
	if after.Lease != nil || after.Reserved != before.Reserved || after.Grant.Policy.Enabled || after.PendingAuthority != nil || !plane.blocked[before.Lease.ID] {
		t.Fatalf("stop reset credit or retained forwarding: %+v", after)
	}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{stop}); err != nil || len(plane.removed) != 1 {
		t.Fatalf("stop replay was not idempotent: %v", err)
	}
	revive := lowerAuthority(stop)
	revive.Grant.Policy.Enabled = true
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{revive}); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("same-base stopped grant revived: %v", err)
	}
}

func TestNativeAuthorityBatchRejectsBeforeAnyIntentOrKernelMutation(t *testing.T) {
	c, journal, plane, a, b := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	before, err := journal.Records()
	if err != nil {
		t.Fatal(err)
	}
	bad := lowerAuthority(b)
	bad.Grant.Policy.Term = uuid.NewString()
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{lowerAuthority(a), bad}); !errors.Is(err, limits.ErrTerm) {
		t.Fatalf("wrong-term batch accepted: %v", err)
	}
	after, err := journal.Records()
	if err != nil || !reflect.DeepEqual(before, after) || plane.replacements != 0 {
		t.Fatalf("rejected batch changed authority: %v", err)
	}
	foreign := lowerAuthority(a)
	foreign.Selectors = slices.Clone(b.Selectors)
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{foreign}); !errors.Is(err, ErrAuthority) && !errors.Is(err, ErrOwnership) {
		t.Fatalf("authority reassigned another client's selectors: %v", err)
	}
	mode := lowerAuthority(a)
	mode.Grant.Policy.Upload = limits.Rate{Mode: limits.Unlimited}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{mode}); !errors.Is(err, ErrAuthority) {
		t.Fatalf("unqualified rate mode transition accepted: %v", err)
	}
}

func TestNativeAuthorityCrashIntentRetainsChargedReservationAndRejectsOldBasePolicy(t *testing.T) {
	c, journal, plane, a, b := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a, b}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	count := Counters{Uploaded: 120, Downloaded: 230}
	plane.counts[sliceName(before.Lease.ID)] = count
	desired := lowerAuthority(a)
	if _, err := journal.stageAuthority(t.Context(), []Assignment{desired}, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := journal.db.Path()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, err := NewController(reopened, c.usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := authorityRecordFor(t, restarted, a.Grant.ID)
	if after.PendingAuthority != nil || after.Lease != nil || after.Reserved != before.Reserved || after.Grant != desired.Grant || len(plane.counts) != 0 {
		t.Fatalf("recovery forgot authority or refunded uncertain reservation: %+v", after)
	}
	if err := restarted.Reconcile(t.Context(), []Assignment{a, b}); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("crash recovery reinstalled old same-base authority: %v", err)
	}
	if err := restarted.Reconcile(t.Context(), []Assignment{desired, b}); err != nil {
		t.Fatal(err)
	}
	after = authorityRecordFor(t, restarted, a.Grant.ID)
	if after.Lease == nil || after.Lease.ID == before.Lease.ID || after.Reserved != before.Reserved+MaxSliceBytes {
		t.Fatalf("recovery reused uncertain lease credit: %+v", after)
	}
	usage, err := c.usage.PendingUsage(8)
	if err != nil || len(usage) != 1 || len(usage[0].Checkpoints) != 1 || usage[0].Checkpoints[0].Uploaded != count.Uploaded || usage[0].Checkpoints[0].Downloaded != count.Downloaded {
		t.Fatalf("recovery invented or lost measured usage: %+v %v", usage, err)
	}
}

func TestNativePendingAuthorityExpiresBlockedWithoutReissuingCredit(t *testing.T) {
	c, _, plane, a, _ := authorityFixture(t)
	a.Grant.Policy.ExpiresUnixMilli = time.Now().Add(time.Hour).UnixMilli()
	if err := c.Reconcile(t.Context(), []Assignment{a}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	plane.failure = errors.New("interrupted before expiry")
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{lowerAuthority(a)}); !errors.Is(err, plane.failure) {
		t.Fatal(err)
	}
	c.SetClock(shiftedNativeClock{offset: 2 * time.Hour})
	applied, err := c.ResumeAuthority(t.Context())
	after := authorityRecordFor(t, c, a.Grant.ID)
	if err != nil || len(applied) != 1 || after.PendingAuthority != nil || after.Lease != nil || after.Reserved != before.Reserved || len(plane.counts) != 0 {
		t.Fatalf("expired pending authority reopened or refunded credit: %+v %v", after, err)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || page.Grants[0].AuthorizationActive || page.Grants[0].SpendableBytes != 0 {
		t.Fatalf("expired amendment advertised forwarding: %+v %v", page, err)
	}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{lowerAuthority(a)}); err != nil {
		t.Fatalf("retired expired intent rejected its exact replay: %v", err)
	}
}

func TestNativeAuthorityExhaustedQuotaRemainsBlockedAfterRateChange(t *testing.T) {
	c, _, plane, a, _ := authorityFixture(t)
	a.Grant.Policy.CreditCeiling = MaxSliceBytes
	if err := c.Reconcile(t.Context(), []Assignment{a}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	plane.counts[sliceName(before.Lease.ID)] = Counters{Uploaded: before.Lease.Bytes}
	if err := c.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{lowerAuthority(a)}); err != nil {
		t.Fatal(err)
	}
	after := authorityRecordFor(t, c, a.Grant.ID)
	if after.PendingAuthority != nil || after.Lease != nil || after.Reserved != MaxSliceBytes || len(plane.counts) != 0 {
		t.Fatalf("rate update refilled exhausted credit: %+v", after)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || !page.Grants[0].SpendableKnown || page.Grants[0].SpendableBytes != 0 {
		t.Fatalf("exhausted authority advertised fresh credit: %+v %v", page, err)
	}
}

func TestNativeAuthorityExpiredReplayAcknowledgesOnlyCommittedRevision(t *testing.T) {
	c, _, plane, a, _ := authorityFixture(t)
	a.Grant.Policy.ExpiresUnixMilli = time.Now().Add(time.Hour).UnixMilli()
	if err := c.Reconcile(t.Context(), []Assignment{a}); err != nil {
		t.Fatal(err)
	}
	desired := lowerAuthority(a)
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{desired}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	c.SetClock(shiftedNativeClock{offset: 2 * time.Hour})
	grant, err := c.ApplyAuthority(t.Context(), []Assignment{desired})
	if err != nil || len(grant) != 1 || grant[0] != desired.Grant {
		t.Fatalf("committed expired authority rejected exact replay: %+v %v", grant, err)
	}
	if after := authorityRecordFor(t, c, a.Grant.ID); !reflect.DeepEqual(before, after) || plane.replacements != 1 {
		t.Fatalf("expired replay changed credit or kernel state: %+v", after)
	}
	if _, err := c.ApplyAuthority(t.Context(), []Assignment{lowerAuthority(desired)}); !errors.Is(err, limits.ErrExpired) {
		t.Fatalf("new expired authority revision accepted: %v", err)
	}
}

func TestNativeAuthorityCommitFailureBlocksKernelAndRetainsDurableIntent(t *testing.T) {
	c, journal, plane, a, _ := authorityFixture(t)
	if err := c.Reconcile(t.Context(), []Assignment{a}); err != nil {
		t.Fatal(err)
	}
	before := authorityRecordFor(t, c, a.Grant.ID)
	path := journal.db.Path()
	desired := lowerAuthority(a)
	plane.afterChange = journal.Close
	applied, err := c.ApplyAuthority(t.Context(), []Assignment{desired})
	if !errors.Is(err, boltErrors.ErrDatabaseNotOpen) || len(applied) != 0 || !plane.blocked[before.Lease.ID] || plane.replacements != 1 {
		t.Fatalf("failed durable commit acknowledged or left kernel enabled: %+v %v", applied, err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, err := NewController(reopened, c.usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	pending := authorityRecordFor(t, restarted, a.Grant.ID)
	if pending.PendingAuthority == nil || pending.PendingAuthority.Grant != desired.Grant || pending.Grant != a.Grant || pending.Reserved != before.Reserved || pending.Lease.ID != before.Lease.ID {
		t.Fatalf("commit failure lost pending authority or charged reservation: %+v", pending)
	}
	plane.afterChange = nil
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := authorityRecordFor(t, restarted, a.Grant.ID)
	if after.PendingAuthority != nil || after.Lease != nil || after.Grant != desired.Grant || after.Reserved != before.Reserved {
		t.Fatalf("commit-failure recovery reset authority: %+v", after)
	}
}
