package service

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A rule orphaned by an inbound deletion must stay repairable.

Deleting an inbound leaves the rule disabled, its ingress list empty and its
remark saying why — but with ingress_scope still "selected", which reads as a
contradiction: a scope that names nothing. Ledger L1 proposed either deleting
the rule or inventing a third scope value for it.

This decides that question by exercising the repair instead. The rule is the
operator's, the deletion is not their doing, and both proposals cost more than
the odd-looking row does: one destroys their work, the other adds a schema value
every reader must learn. What matters is that the row can be fixed from the UI,
and this pins that it can.
*/
func TestAnOrphanedRuleCanBeRepairedByNamingALiveInbound(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	doomed := seedRoutingInbound(t, "doomed", 24101, model.VLESS, `{"clients":[]}`, true, nil)
	survivor := seedRoutingInbound(t, "survivor", 24102, model.VLESS, `{"clients":[]}`, true, nil)

	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "office traffic",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   jsonIDs(t, doomed.Id),
		DestKind:     model.RoutingDestOutbound, DestTag: "direct",
	})

	if _, err := (&RoutingService{}).PruneInbound(context.Background(), doomed.Id); err != nil {
		t.Fatalf("PruneInbound: %v", err)
	}

	orphan := reloadRule(t, rule.Id)
	if orphan.Enable {
		t.Fatal("a rule naming nothing must not stay enabled")
	}
	if !strings.Contains(orphan.Remark, "deleted") {
		t.Fatalf("the remark must say why it was switched off, got %q", orphan.Remark)
	}
	if orphan.IngressIds != "[]" {
		t.Fatalf("the deleted inbound must be gone from the list, got %q", orphan.IngressIds)
	}

	// The repair an operator would perform: name a live inbound, switch it back
	// on, save. If this is refused the row really is unfixable and L1 needs code.
	orphan.IngressIds = jsonIDs(t, survivor.Id)
	orphan.Enable = true
	if _, err := (&RoutingService{}).Update(context.Background(), orphan); err != nil {
		t.Fatalf("an orphaned rule must be repairable by naming a live inbound: %v", err)
	}

	repaired := reloadRule(t, rule.Id)
	if !repaired.Enable || repaired.IngressIds != jsonIDs(t, survivor.Id) {
		t.Fatalf("the repair did not stick: enable=%v ids=%q", repaired.Enable, repaired.IngressIds)
	}
}

// And re-enabling WITHOUT naming anything is still refused — F048's guard, which
// is the reason the row is safe to leave as it is.
func TestAnOrphanedRuleCannotBeReEnabledEmpty(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	doomed := seedRoutingInbound(t, "doomed", 24103, model.VLESS, `{"clients":[]}`, true, nil)
	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "office traffic",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   jsonIDs(t, doomed.Id),
		DestKind:     model.RoutingDestOutbound, DestTag: "direct",
	})
	if _, err := (&RoutingService{}).PruneInbound(context.Background(), doomed.Id); err != nil {
		t.Fatalf("PruneInbound: %v", err)
	}

	orphan := reloadRule(t, rule.Id)
	orphan.Enable = true
	if _, err := (&RoutingService{}).Update(context.Background(), orphan); err == nil {
		t.Fatal("a selected rule naming nothing was re-enabled: that is the widening F048 closed")
	}
}

func jsonIDs(t *testing.T, ids ...int) string {
	t.Helper()
	out := "["
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(id)
	}
	return out + "]"
}
