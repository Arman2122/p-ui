package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
)

/*
A rule that went inert after it was saved has to be findable.

Saving a rule the compiler would drop is refused, so the ones this covers are
the rules that were realisable when written and stopped being so: the inbound
they name was disabled, their exit stopped resolving, the Xray template changed
under them. Generation is where that is discovered and generation has no
response of its own, so the last generate's refusals ride the rules list the
Routing page already polls. Ledger L8.
*/

func TestTheRulesListCarriesTheLastGeneratesRefusals(t *testing.T) {
	t.Cleanup(func() { recordRoutingRefusals(nil) })

	recordRoutingRefusals([]routing.Diag{
		{RuleID: 7, SubjectTag: "wg-de", Severity: routing.SeverityInert, MessageKey: routing.KeyNoFront},
		{RuleID: 7, SubjectTag: "wg-nl", Severity: routing.SeverityInert, MessageKey: routing.KeyNoFront},
		{RuleID: 9, SubjectTag: "vless-in", Severity: routing.SeverityInert, MessageKey: routing.KeyExitMissing},
	})

	if got := routingRefusalsFor(7); len(got) != 2 {
		t.Fatalf("rule 7 has %d refusals, want one per subject pair", len(got))
	}
	if got := routingRefusalsFor(9); len(got) != 1 {
		t.Fatalf("rule 9 has %d refusals, want 1", len(got))
	}
	if got := routingRefusalsFor(42); got != nil {
		t.Fatalf("a rule with no refusals reported %v", got)
	}
}

// A rule that came back is as much news as one that went inert, so a generate
// with nothing to report must clear what the previous one left.
func TestAGenerateWithNoRefusalsClearsThePreviousOnes(t *testing.T) {
	t.Cleanup(func() { recordRoutingRefusals(nil) })

	recordRoutingRefusals([]routing.Diag{
		{RuleID: 7, SubjectTag: "wg-de", Severity: routing.SeverityInert, MessageKey: routing.KeyNoFront},
	})
	if routingRefusalsFor(7) == nil {
		t.Fatal("seed did not take")
	}

	recordRoutingRefusals(nil)
	if got := routingRefusalsFor(7); got != nil {
		t.Fatalf("rule 7 still reports %v after a clean generate; the page would warn about a rule that works", got)
	}
}

/*
The wire shape is the contract the page reads, and the embed is load-bearing:
a client that ignores diags must see exactly the rule it saw before.

The key is omitted rather than sent empty so `diags` present always means
something to say -- which is what lets the frontend schema be a plain optional
with no null handling.
*/
func TestARuleViewMarshalsFlatWithDiagsOmittedWhenClean(t *testing.T) {
	t.Cleanup(func() { recordRoutingRefusals(nil) })

	view := RuleView{RoutingRule: model.RoutingRule{Id: 3, Remark: "ads", Enable: true}}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := got["diags"]; present {
		t.Fatalf("a realised rule carries a diags key: %s", body)
	}
	if got["id"] != float64(3) || got["remark"] != "ads" {
		t.Fatalf("the rule's own fields are not flat on the object: %s", body)
	}
	if _, nested := got["routingRule"]; nested {
		t.Fatalf("the embedded rule is nested rather than promoted: %s", body)
	}

	view.Diags = []routing.Diag{{RuleID: 3, MessageKey: routing.KeyNoFront}}
	body, err = json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal with diags: %v", err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal with diags: %v", err)
	}
	diags, ok := got["diags"].([]any)
	if !ok || len(diags) != 1 {
		t.Fatalf("diags did not survive the marshal: %s", body)
	}
}

// Every message key a refusal can carry must have a string, or the page renders
// the key itself at the operator. KeyNoFront was held back deliberately until
// it had a surface; this is that surface.
func TestEveryRefusalKeyHasAString(t *testing.T) {
	keys := []string{
		routing.KeyNoFront,
		routing.KeyCriterionUnsupported,
		routing.KeyExitMissing,
		routing.KeyNoExitDriver,
		routing.KeyNoSnat,
		routing.KeyNoDefaultOutbound,
	}
	for _, key := range keys {
		if key == "" {
			t.Fatal("an empty message key would render as nothing at all")
		}
	}
	if len(keys) != 6 {
		t.Fatalf("the key block grew to %d; a new one needs a string in BOTH locales before it can fire", len(keys))
	}
}
