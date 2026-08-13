package egress

import (
	"strings"
	"testing"
)

/*
The ruleset is built as text and applied whole, so its exact shape is the
contract. These assert the three properties that decide whether a tunnel works
and whether anything else on the box survives.
*/
func TestMasqueradeRulesetTranslatesOnlyWhatLeaves(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2"})

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
	got := masqueradeRuleset([]string{"pwg2", "pwg7"})

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
	first := masqueradeRuleset([]string{"pwg7", "pwg2"})
	second := masqueradeRuleset([]string{"pwg2", "pwg7"})

	if first != second {
		t.Fatalf("the same devices in a different order must produce the same script:\n%s\n---\n%s", first, second)
	}
	if strings.Index(first, `"pwg2"`) > strings.Index(first, `"pwg7"`) {
		t.Fatal("devices must be emitted in a fixed order")
	}
}

// One rule per device, so a second inbound is covered without a second table.
func TestMasqueradeCoversEveryDevice(t *testing.T) {
	got := masqueradeRuleset([]string{"pwg2", "pwg7", "ptun3"})

	for _, device := range []string{"pwg2", "pwg7", "ptun3"} {
		if !strings.Contains(got, `iifname "`+device+`"`) {
			t.Errorf("device %s has no rule; a core that names a device must be covered:\n%s", device, got)
		}
	}
}
