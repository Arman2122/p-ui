package egress

import (
	"net/netip"
	"strings"
	"testing"
)

/*
The ruleset is built as text and applied whole, so its exact shape is the
contract. These assert the three properties that decide whether a tunnel works
and whether anything else on the box survives.
*/
func TestMasqueradeRulesetTranslatesOnlyWhatLeaves(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2"}, nil, nil)

	if !strings.Contains(got, `iifname "pwg2" oifname != "pwg2" masquerade`) {
		t.Fatalf("the rule must translate what ARRIVED on the device and is LEAVING elsewhere:\n%s", got)
	}
	// A client reaching another client on the same device never leaves the host,
	// and rewriting its source would break the return path.
	if strings.Contains(got, `oifname "pwg2" masquerade`) {
		t.Fatalf("traffic staying on the device must not be translated:\n%s", got)
	}
}

/*
Every write is confined to the panel's own table and is preceded by a flush of
that table alone. `flush ruleset` would take ufw, docker and fail2ban with it —
fail2ban owns table inet f2b-table on the very host this was measured on.
*/
func TestMasqueradeNeverTouchesAnotherWritersRules(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2", "pwg7"}, nil, nil)

	if strings.Contains(got, "flush ruleset") {
		t.Fatal("flush ruleset would delete every other writer's rules on the host")
	}
	if !strings.Contains(got, "flush table inet "+masqueradeTable) {
		t.Fatalf("the panel's own table must be flushed before it is rewritten:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "table ") && !strings.Contains(trimmed, masqueradeTable) {
			t.Fatalf("the script names a table that is not ours: %q", trimmed)
		}
	}
}

// Deterministic output: the reconcile job applies this every ten seconds, and a
// ruleset whose text depends on map order would rewrite the table forever.
func TestMasqueradeRulesetIsStable(t *testing.T) {
	first := masqueradeRuleset([]string{"pwg7", "pwg2"}, nil, nil)
	second := masqueradeRuleset([]string{"pwg2", "pwg7"}, nil, nil)

	if first != second {
		t.Fatalf("the same devices in a different order must produce the same script:\n%s\n---\n%s", first, second)
	}
	if strings.Index(first, `"pwg2"`) > strings.Index(first, `"pwg7"`) {
		t.Fatal("devices must be emitted in a fixed order")
	}
}

// One rule per device, so a second inbound is covered without a second table.
func TestMasqueradeCoversEveryDevice(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2", "pwg7", "ptun3"}, nil, nil)

	for _, device := range []string{"pwg2", "pwg7", "ptun3"} {
		if !strings.Contains(got, `iifname "`+device+`"`) {
			t.Errorf("device %s has no rule; a core that names a device must be covered:\n%s", device, got)
		}
	}
}

/*
Tearing the table down must work on the oldest nft this project supports.

`destroy` landed in nft 1.0.8. Ubuntu 22.04 ships 1.0.2 and Debian 12 ships
1.0.6, and on the 1.0.2 binary the command does not parse at all — measured as
exit 1 with "syntax error, unexpected table, expecting string". So on two of
five supported targets the masquerade table was left on the box forever, with
its rules still translating.
*/
func TestMasqueradeTeardownRunsOnEveryTarget(t *testing.T) {
	got := masqueradeTeardown()

	if strings.Contains(got, "destroy") {
		t.Fatalf("teardown uses `destroy`, which is a syntax error on nft < 1.0.8 (Ubuntu 22.04, Debian 12):\n%s", got)
	}
	add := strings.Index(got, "add table inet "+masqueradeTable)
	del := strings.Index(got, "delete table inet "+masqueradeTable)
	if add < 0 || del < 0 {
		t.Fatalf("teardown must add-then-delete so it is idempotent without `destroy`:\n%s", got)
	}
	if add > del {
		t.Fatalf("the add must come first, or deleting an absent table is an error:\n%s", got)
	}
	// And it must still confine itself to the panel's own table.
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.Contains(line, masqueradeTable) {
			t.Fatalf("teardown touches something other than the panel's table: %q", line)
		}
	}
}

/*
An inbound with no interface to name is translated by its pool instead.

Without this an OpenConnect client connects, is given an address, and reaches
nothing: forwarding alone keeps the client's in-tunnel source and every upstream
drops it. The device rules cannot cover it -- ocserv makes one tun per session,
so there is no interface name that is true for longer than one connection.
*/
func TestMasqueradeTranslatesAPoolThatHasNoDevice(t *testing.T) {
	pool := netip.MustParsePrefix("10.218.0.0/24")
	got := masqueradeRuleset(nil, []netip.Prefix{pool}, []string{"peg9"})

	if !strings.Contains(got, "ip saddr 10.218.0.0/24 ip daddr != 10.218.0.0/24") {
		t.Fatalf("the rule must translate what CAME FROM the pool and is going elsewhere:\n%s", got)
	}
	// The front exemption is why a source criterion can match at all: postrouting
	// runs before the tun hands the packet up, so a translated one reads as the
	// gateway. Same reason as the device rules, same clause.
	if !strings.Contains(got, `oifname != "peg9" masquerade`) {
		t.Fatalf("a front must be exempted from a pool rule too:\n%s", got)
	}
	// And with neither devices nor pools there is nothing of ours to translate.
	if inet := masqueradeRuleset(nil, nil, nil); strings.Contains(inet, "masquerade") {
		t.Fatalf("an empty host produced a translation rule:\n%s", inet)
	}
}

// Stable across pass order, like the devices: this is reapplied every ten
// seconds and text that moved would rewrite the table forever.
func TestMasqueradePoolsAreEmittedInAFixedOrder(t *testing.T) {
	a, b := netip.MustParsePrefix("10.9.0.0/24"), netip.MustParsePrefix("10.218.0.0/24")
	first := masqueradeRuleset(nil, []netip.Prefix{a, b}, nil)
	if second := masqueradeRuleset(nil, []netip.Prefix{b, a}, nil); first != second {
		t.Fatalf("the same pools in a different order produced different scripts:\n%s\n---\n%s", first, second)
	}
}
