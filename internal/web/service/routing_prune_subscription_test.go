package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A rule may name a subscription outbound, and the prune must know that.

destResolves accepts a subscription tag when a rule is saved — the merged
config carries it — but the repair that runs after a template save judged rules
against the template's own outbounds only. So a rule aimed at a subscription
server was disabled, with a remark blaming a tag that was there all along, on
the next unrelated template edit. And when the subscription itself went away
nothing pruned at all. Found by the 2026-09-02 feature-gaps census.
*/
func seedSubscriptionWithTag(t *testing.T, tag string) *model.OutboundSubscription {
	t.Helper()
	sub := &model.OutboundSubscription{
		Remark: "sub", Url: "https://example.com/sub", Enabled: true, TagPrefix: "sub1", UpdateInterval: 600,
		LastFetchedOutbounds: `[{"tag":"` + tag + `","protocol":"freedom","settings":{}}]`,
	}
	if err := database.GetDB().Create(sub).Error; err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	return sub
}

func TestPresentDestinationsIncludeSubscriptionOutbounds(t *testing.T) {
	initTestDB(t)
	seedSubscriptionWithTag(t, "sub1-a")

	present := presentDestinations(`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)
	if !present["direct"] {
		t.Fatal("a template outbound is missing from the present set")
	}
	if !present["sub1-a"] {
		t.Fatal("a subscription outbound is missing from the present set: the next template save disables every rule that names it")
	}
}

func TestDeletingASubscriptionDisablesTheRulesThatNamedIt(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	sub := seedSubscriptionWithTag(t, "sub1-a")
	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "via the subscription",
		IngressScope: model.RoutingScopeAll,
		DestKind:     model.RoutingDestOutbound, DestTag: "sub1-a",
	})

	// While the subscription stands, a template save must leave the rule alone.
	if _, err := (&RoutingService{}).PruneOutbound(context.Background(), presentDestinations(`{"outbounds":[{"tag":"direct"}]}`)); err != nil {
		t.Fatalf("PruneOutbound: %v", err)
	}
	if !reloadRule(t, rule.Id).Enable {
		t.Fatal("a rule naming a live subscription outbound was disabled by a template save")
	}

	if err := (&OutboundSubscriptionService{}).Delete(sub.Id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if reloadRule(t, rule.Id).Enable {
		t.Fatal("the subscription is gone and the rule naming its outbound is still enabled: its traffic is dropped by the dispatcher")
	}
}
