package native

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func nativeCredit(assignment Assignment, revision, ceiling uint64) authority.Credit {
	return authority.Credit{GrantID: assignment.Grant.ID, ClientID: assignment.Grant.ClientID, TermID: assignment.Grant.Policy.Term, PolicyRevision: assignment.Grant.Policy.Revision, CreditRevision: revision, CreditCeiling: ceiling}
}

func TestNativeCreditPreservesLiveSliceAndRestoresRaisedCeiling(t *testing.T) {
	c, journal, usage, plane, assignment := selectorFixture(t)
	assignment.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	assignment.Grant.Policy.CreditCeiling, assignment.Grant.Policy.CreditRevision = 1000, 1
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	before, err := journal.Records()
	if err != nil || len(before) != 1 || before[0].Lease == nil {
		t.Fatalf("initial slice: %+v %v", before, err)
	}
	lease := *before[0].Lease
	plane.counts[sliceName(lease.ID)] = Counters{Uploaded: 25}
	for range 2 {
		if err := c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(assignment, 2, 2000)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatalf("same base lost durable topup: %v", err)
	}
	after, err := journal.Records()
	if err != nil || len(after) != 1 || after[0].Reserved != 1000 || after[0].Lease == nil || after[0].Lease.ID != lease.ID || after[0].Lease.Grant != lease.Grant || after[0].Grant.Policy.CreditCeiling != 2000 || len(plane.removed) != 0 || plane.calls != 0 {
		t.Fatalf("credit update disturbed live authority: %+v %v", after, err)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || !page.Grants[0].SpendableKnown || page.Grants[0].SpendableBytes != 1975 {
		t.Fatalf("wrong spendable projection after topup: %+v %v", page, err)
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
	restarted, err := NewController(reopened, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	rows, err := reopened.Records()
	if err != nil || len(rows) != 1 || rows[0].Reserved != 2000 || rows[0].Lease == nil || rows[0].Lease.Bytes != 1000 || rows[0].Lease.ID == lease.ID || rows[0].Grant.Policy.CreditRevision != 2 {
		t.Fatalf("restart replayed or lost raised credit: %+v %v", rows, err)
	}
}

func TestNativeCreditBatchRejectsMismatchAtomically(t *testing.T) {
	c, journal, _, _, first := selectorFixture(t)
	first.Grant.Policy.CreditCeiling, first.Grant.Policy.CreditRevision = 1000, 1
	second := Assignment{Grant: testGrant(1000)}
	if err := c.Reconcile(t.Context(), []Assignment{first, second}); err != nil {
		t.Fatal(err)
	}
	bad := nativeCredit(second, 2, 2000)
	bad.TermID = uuid.NewString()
	if err := c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(first, 2, 2000), bad}); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("accepted wrong term: %v", err)
	}
	rows, err := journal.Records()
	if err != nil || len(rows) != 2 {
		t.Fatalf("credit rows: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.Grant.Policy.CreditCeiling != 1000 || row.Reserved != 0 {
			t.Fatalf("partial credit batch committed: %+v", row)
		}
	}
}

func TestNativeRejectsCreditVersionRollbackAcrossConfiguration(t *testing.T) {
	c, journal, _, _, assignment := selectorFixture(t)
	assignment.Grant.Policy.CreditCeiling, assignment.Grant.Policy.CreditRevision = 2000, 2
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	assignment.Grant.Policy.Revision++
	assignment.Grant.Policy.CreditRevision = 1
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("new configuration rolled durable credit backward: %v", err)
	}
	rows, err := journal.Records()
	if err != nil || len(rows) != 1 || rows[0].Grant.Policy.CreditRevision != 2 || rows[0].Grant.Policy.Revision != 1 {
		t.Fatalf("failed configuration changed durable version: %+v %v", rows, err)
	}
}

func TestNativeCreditRefillsExhaustionButNotRevocationOrExpiry(t *testing.T) {
	for _, mode := range []string{"exhausted", "disabled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			c, journal, _, plane, assignment := selectorFixture(t)
			assignment.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
			assignment.Grant.Policy.CreditCeiling, assignment.Grant.Policy.CreditRevision = 1000, 1
			clock := &fixedNativeClock{now: time.Now()}
			c.SetClock(clock)
			if mode == "expired" {
				assignment.Grant.Policy.ExpiresUnixMilli = clock.now.Add(time.Minute).UnixMilli()
			}
			if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
				t.Fatal(err)
			}
			rows, err := journal.Records()
			if err != nil {
				t.Fatal(err)
			}
			plane.counts[sliceName(rows[0].Lease.ID)] = Counters{Uploaded: 1000}
			if err := c.Collect(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				assignment.Grant.Policy.Enabled = false
				assignment.Grant.Policy.Revision++
				if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
					t.Fatal(err)
				}
			case "expired":
				clock.now = clock.now.Add(2 * time.Minute)
			}
			err = c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(assignment, 2, 2000)})
			wanted := map[string]error{"disabled": limits.ErrBlocked, "expired": limits.ErrExpired}[mode]
			if !errors.Is(err, wanted) {
				t.Fatalf("%s credit result: %v want %v", mode, err, wanted)
			}
			rows, err = journal.Records()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "exhausted" {
				if rows[0].Reserved != 2000 || rows[0].Lease == nil || rows[0].Lease.Bytes != 1000 {
					t.Fatalf("exhaustion topup did not refill exactly: %+v", rows[0])
				}
			} else if rows[0].Reserved != 1000 || rows[0].Lease != nil || rows[0].Grant.Policy.CreditCeiling != 1000 {
				t.Fatalf("inactive topup changed forwarding authority: %+v", rows[0])
			}
		})
	}
}
