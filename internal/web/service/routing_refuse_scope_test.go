package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A rule is refused for what IT cannot do, never for what another rule cannot do.

The save-time check compiles every stored rule together, because a rule's effect
depends on the others, and then refused on the first refusal in the whole plan
whoever owned it. One inbound whose bridge is off is enough to produce that
refusal for any rule scoped to all inbounds, and from then on NO routing rule
could be saved or edited at all: the operator is told a criterion is wrong on a
rule they are not editing, and the only way out is to find and delete the other
rule.

The blocked subject here is an mtproto inbound with routing switched off, which
is the shipped shape of that reason and needs no new core to reproduce.
*/
func TestASaveIsRefusedOnlyByItsOwnRule(t *testing.T) {
	initTestDB(t)
	bridged := seedRoutingInbound(t, "mt-off", 8461, model.MTProto, `{"routeThroughXray":false}`, true, nil)
	target := seedRoutingInbound(t, "vless-ok", 10461, model.VLESS, "", true, nil)

	// Stored directly: this rule covers every subject including the blocked
	// one, which is exactly the state an operator is left in after switching a
	// bridge off under a rule that was saved while it was on.
	stored := &model.RoutingRule{
		Enable: true, Remark: "everything direct", SortIndex: 0,
		IngressScope: model.RoutingScopeAll,
		DestKind:     model.RoutingDestDirect,
		Criteria:     `{"ip":["8.8.8.8"]}`,
	}
	if err := database.GetDB().Create(stored).Error; err != nil {
		t.Fatalf("seed the scope-all rule: %v", err)
	}

	fresh := &model.RoutingRule{
		Enable: true, Remark: "an unrelated rule",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   ingressIdsJSON(t, target.Id),
		DestKind:     model.RoutingDestDirect,
		Criteria:     `{"ip":["1.1.1.1"]}`,
	}
	/*
		Only the refusal is under test. Add goes on to converge the egress plane
		against this host's kernel, which a test box may refuse for reasons that
		have nothing to do with which rule owns a diagnostic, so any other error
		is left alone and the stored row is checked instead.
	*/
	_, err := (&RoutingService{}).Add(context.Background(), fresh)
	var refusal *RefusalError
	if errors.As(err, &refusal) {
		t.Fatalf("saving a rule about inbound %d was refused because inbound %d is blocked elsewhere: %s %v",
			target.Id, bridged.Id, refusal.Key, refusal.Args)
	}

	var count int64
	if err := database.GetDB().Model(&model.RoutingRule{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("stored %d rules, want the seeded one plus the new one", count)
	}
}
