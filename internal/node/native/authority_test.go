package native

import (
	"errors"
	"net/netip"
	"testing"
)

func TestNativeAuthorityKeepsExhaustedAuthorizationAndPages(t *testing.T) {
	c, j, _, plane, first := selectorFixture(t)
	if c.AuthorityJournalID() != j.JournalID() || c.AuthorityJournalID() == "" {
		t.Fatal("missing empty-inventory journal identity")
	}
	first.Grant.ID = "00000000-0000-4000-8000-000000000001"
	first.Grant.Policy.CreditCeiling = 1000
	first.Grant.Policy.AuthorityRevision, first.Grant.RateRevision = 4, 4
	first.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	second := Assignment{Grant: testGrant(2000)}
	second.Grant.ID = "00000000-0000-4000-8000-000000000003"
	if err := c.Reconcile(t.Context(), []Assignment{first, second}); err != nil {
		t.Fatal(err)
	}
	rows, err := j.Records()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Grant.ID == first.Grant.ID {
			plane.counts[sliceName(row.Lease.ID)] = Counters{Uploaded: 1000}
		}
	}
	if err := c.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || page.Next != first.Grant.ID {
		t.Fatalf("first authority page: %+v %v", page, err)
	}
	grant := page.Grants[0]
	if grant.GrantID != first.Grant.ID || grant.AuthorityRevision != 4 || grant.PolicyRevision != first.Grant.Policy.Revision || grant.ReservedHighWater != 1000 || grant.CreditCeiling != 1000 || !grant.AuthorizationActive || grant.AdmissionKnown || !grant.SpendableKnown || grant.SpendableBytes != 0 {
		t.Fatalf("exhaustion invented surrender or live authority: %+v", grant)
	}
	page, err = c.Authority(t.Context(), page.Next, 1)
	if err != nil || len(page.Grants) != 1 || page.Grants[0].GrantID != second.Grant.ID || page.Next != "" || page.Grants[0].ReservedHighWater != 0 {
		t.Fatalf("dormant authority page: %+v %v", page, err)
	}
	if _, err := c.Authority(t.Context(), "not-a-cursor", 1); !errors.Is(err, ErrAuthority) {
		t.Fatalf("invalid cursor: %v", err)
	}
	if _, err := c.Authority(t.Context(), "", 0); !errors.Is(err, ErrAuthority) {
		t.Fatalf("invalid page limit: %v", err)
	}
}

func TestNativeAuthorityRejectsIncoherentKernelRemainder(t *testing.T) {
	c, j, _, plane, assignment := selectorFixture(t)
	assignment.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	rows, err := j.Records()
	if err != nil {
		t.Fatal(err)
	}
	lease := *rows[0].Lease
	plane.counts[sliceName(lease.ID)] = Counters{Uploaded: 1200, Downloaded: 1300}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	grant := page.Grants[0]
	if !grant.SpendableKnown || grant.SpendableBytes != assignment.Grant.Policy.CreditCeiling-2500 || grant.AdmissionKnown {
		t.Fatalf("coherent spendable projection: %+v", grant)
	}
	delete(plane.counts, sliceName(lease.ID))
	page, err = c.Authority(t.Context(), "", 1)
	if err != nil || page.Grants[0].SpendableKnown || !page.Grants[0].AuthorizationActive {
		t.Fatalf("missing kernel slice fabricated authority: %+v %v", page, err)
	}
	plane.counts[sliceName(lease.ID)] = Counters{Uploaded: 1200, Downloaded: 1300}
	plane.counts["unknown-owned-kernel-slice"] = Counters{}
	page, err = c.Authority(t.Context(), "", 1)
	if err != nil || page.Grants[0].SpendableKnown {
		t.Fatalf("foreign kernel slice fabricated coherence: %+v %v", page, err)
	}
}
