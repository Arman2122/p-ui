package egress

import "testing"

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
