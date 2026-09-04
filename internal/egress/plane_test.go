package egress

import (
	"net/netip"
	"testing"
)

/*
A rule selects by ingress device OR by mark, never by neither.

An iif rule with an empty device and a mark of zero matches every packet on the
box and steals it into an egress table, which is the one outcome this band must
never produce. The String form is what an operator reads in a log, so it names
whichever selector is actually in force.
*/
func TestRuleSpecNamesExactlyOneSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec RuleSpec
		want string
	}{
		{
			"an ingress rule reads by device",
			RuleSpec{Family: FamilyV4, Priority: 31002, Iif: "pwg2", Table: 30002},
			"v4 prio 31002 iif pwg2 lookup 30002",
		},
		{
			"a marked rule reads by mark, because it has no device to name",
			RuleSpec{Family: FamilyV4, Priority: 30002, Mark: 0x0e000002, Table: 30002},
			"v4 prio 30002 fwmark 0xe000002 lookup 30002",
		},
		{
			"the v6 twin is not an afterthought",
			RuleSpec{Family: FamilyV6, Priority: 30002, Mark: 0x0e000002, Table: 30002},
			"v6 prio 30002 fwmark 0xe000002 lookup 30002",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two rules differing only by mark are different rules, or reconcile would leave
// a stale selector installed while believing it had converged.
func TestRuleSpecMarkIsPartOfIdentity(t *testing.T) {
	base := RuleSpec{Family: FamilyV4, Priority: 30002, Table: 30002}
	marked := base
	marked.Mark = 0x0e000002

	if base == marked {
		t.Fatal("a marked rule must not compare equal to an unmarked one")
	}
	if base.String() == marked.String() {
		t.Fatal("the two must not render the same either, or an op log cannot tell them apart")
	}
}

/*
A source-selected rule reads by source, and is a different rule from its
unselected self.

The shape exists for an inbound whose sessions each get a transient device: the
pool is the only stable handle, so `from <prefix>` is the only selector.
*/
func TestRuleSpecReadsAndComparesBySource(t *testing.T) {
	spec := RuleSpec{
		Family: FamilyV4, Priority: 31007, Src: netip.MustParsePrefix("10.218.0.0/24"), Table: 30007,
	}
	if got, want := spec.String(), "v4 prio 31007 from 10.218.0.0/24 lookup 30007"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	bare := spec
	bare.Src = netip.Prefix{}
	if bare == spec {
		t.Fatal("a source rule must not compare equal to one with no source")
	}
	wider := spec
	wider.Src = netip.MustParsePrefix("10.218.0.0/16")
	if wider == spec || wider.String() == spec.String() {
		t.Fatal("two rules differing only by prefix length are different rules")
	}
}

// The throw route names itself as a throw. It is read out of an op log by an
// operator asking why one destination escapes a table that blackholes the rest.
func TestThrowRouteNamesItself(t *testing.T) {
	spec := RouteSpec{
		Family: FamilyV4, Table: 30007, Type: RouteThrow, Dst: netip.MustParsePrefix("10.218.0.0/24"),
	}
	if got, want := spec.String(), "v4 table 30007 throw 10.218.0.0/24 metric 0"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
