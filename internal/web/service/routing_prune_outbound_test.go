package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Deleting or renaming an outbound must not leave a rule aimed at nothing.

The rule-save direction has always been guarded: destResolves refuses a rule
naming a tag that resolves to nothing. The outbound-CRUD direction had no guard,
so an operator renaming an outbound in the Xray settings left every rule that
named it pointing at a tag the config no longer contains — and the pinned core
does not fall back for an unknown outboundTag, it logs "non existing outTag",
closes the writer and interrupts the reader. The rule keeps matching and
everything it matches is dropped, silently, for as long as it stands.

Same repair and the same marker as PruneInbound: disabled, with a remark saying
why. No new column, and the operator can find it by reading the list.
*/
func TestARuleWhoseOutboundLeftTheTemplateIsDisabled(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "office traffic",
		IngressScope: model.RoutingScopeAll,
		DestKind:     model.RoutingDestOutbound, DestTag: "warp",
	})
	survivor := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "everything else",
		IngressScope: model.RoutingScopeAll,
		DestKind:     model.RoutingDestOutbound, DestTag: "direct",
	})

	// The template after the operator renamed warp away.
	changed, err := (&RoutingService{}).PruneOutbound(context.Background(),
		map[string]bool{"direct": true, "blocked": true})
	if err != nil {
		t.Fatalf("PruneOutbound: %v", err)
	}
	if !changed {
		t.Fatal("nothing was repaired; the rule still names an outbound the config does not have")
	}

	orphan := reloadRule(t, rule.Id)
	if orphan.Enable {
		t.Fatal("the rule is still enabled, so everything it matches is dropped by the dispatcher")
	}
	if !strings.Contains(orphan.Remark, "warp") {
		t.Fatalf("remark %q does not name the outbound that went away", orphan.Remark)
	}
	if !strings.Contains(orphan.Remark, "office traffic") {
		t.Fatalf("remark %q lost what the operator wrote", orphan.Remark)
	}

	kept := reloadRule(t, survivor.Id)
	if !kept.Enable {
		t.Fatal("a rule whose outbound is still in the template was disabled")
	}
	if kept.Remark != "everything else" {
		t.Fatalf("an untouched rule's remark was rewritten to %q", kept.Remark)
	}
}

// A balancer tag is a destination too, and lives in a different part of the
// template — so the presence set has to read both or half the rules are missed.
func TestABalancerThatLeftTheTemplateIsAlsoRepaired(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeAll,
		DestKind: model.RoutingDestBalancer, DestTag: "bal-eu",
	})

	present := TemplateDestinations(`{"outbounds":[{"tag":"direct"}],"routing":{"balancers":[{"tag":"bal-us"}]}}`)
	if !present["direct"] || !present["bal-us"] {
		t.Fatalf("the presence set missed a tag: %v", present)
	}
	if _, err := (&RoutingService{}).PruneOutbound(context.Background(), present); err != nil {
		t.Fatalf("PruneOutbound: %v", err)
	}
	if reloadRule(t, rule.Id).Enable {
		t.Fatal("a rule aimed at a balancer the template no longer declares is still enabled")
	}
}

/*
A rule the operator disabled themselves is left alone, and so is one aimed at an
exit — an exit is a row in egresses, not a tag in the template, so judging it
against the template's tags would disable every uplink rule on the box.
*/
func TestPruneOutboundLeavesWhatIsNotItsToJudge(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	exitID := 4
	exitRule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeAll,
		DestKind: model.RoutingDestExit, DestExitId: &exitID,
	})
	offRule := seedRoutingRule(t, &model.RoutingRule{
		Remark: "off on purpose", IngressScope: model.RoutingScopeAll,
		DestKind: model.RoutingDestOutbound, DestTag: "warp",
	})
	// Written after the insert on purpose: the column defaults to true and GORM
	// omits a zero value, so Create(Enable: false) stores an enabled rule (L9).
	if err := database.GetDB().Model(&model.RoutingRule{}).
		Where("id = ?", offRule.Id).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := (&RoutingService{}).PruneOutbound(context.Background(), map[string]bool{"direct": true}); err != nil {
		t.Fatalf("PruneOutbound: %v", err)
	}
	if !reloadRule(t, exitRule.Id).Enable {
		t.Fatal("an exit rule was disabled by a check that only knows about template tags")
	}
	if got := reloadRule(t, offRule.Id).Remark; got != "off on purpose" {
		t.Fatalf("an already-disabled rule's remark was rewritten to %q", got)
	}
}

func seedRoutingRule(t *testing.T, rule *model.RoutingRule) *model.RoutingRule {
	t.Helper()
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed routing rule: %v", err)
	}
	return rule
}

func reloadRule(t *testing.T, id int) *model.RoutingRule {
	t.Helper()
	var out model.RoutingRule
	if err := database.GetDB().First(&out, id).Error; err != nil {
		t.Fatalf("reload rule %d: %v", id, err)
	}
	return &out
}
