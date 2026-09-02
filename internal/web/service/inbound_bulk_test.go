package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestDelInboundsPrunesEveryDeletedInboundInOnePass(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	first := seedRoutingInbound(t, "gone-1", 24111, model.VLESS, `{"clients":[]}`, true, nil)
	second := seedRoutingInbound(t, "gone-2", 24112, model.VLESS, `{"clients":[]}`, true, nil)
	rule := seedRoutingRule(t, &model.RoutingRule{
		Enable: true, Remark: "office traffic",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   jsonIDs(t, first.Id, second.Id),
		DestKind:     model.RoutingDestOutbound, DestTag: "direct",
	})

	res, _, err := (&InboundService{}).DelInbounds([]int{first.Id, second.Id})
	if err != nil {
		t.Fatalf("DelInbounds: %v", err)
	}
	if res.Deleted != 2 || len(res.Skipped) != 0 {
		t.Fatalf("deleted=%d skipped=%+v, want both gone", res.Deleted, res.Skipped)
	}
	orphan := reloadRule(t, rule.Id)
	if orphan.IngressIds != "[]" || orphan.Enable {
		t.Fatalf("rule after the batch delete: ingress=%q enable=%v, want both inbounds pruned and the rule switched off", orphan.IngressIds, orphan.Enable)
	}
}

func TestSetInboundsEnableNamesTheOnesAlreadyThere(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	first := seedRoutingInbound(t, "flip-1", 24113, model.VLESS, `{"clients":[]}`, true, nil)
	second := seedRoutingInbound(t, "flip-2", 24114, model.VLESS, `{"clients":[]}`, true, nil)

	res, _, err := (&InboundService{}).SetInboundsEnable([]int{first.Id, second.Id, 999999}, false)
	if err != nil {
		t.Fatalf("SetInboundsEnable: %v", err)
	}
	if res.Changed != 2 || len(res.Skipped) != 1 || res.Skipped[0].Id != 999999 {
		t.Fatalf("changed=%d skipped=%+v, want both flipped and the unknown id named", res.Changed, res.Skipped)
	}
	for _, id := range []int{first.Id, second.Id} {
		ib, err := (&InboundService{}).GetInbound(id)
		if err != nil {
			t.Fatalf("reload %d: %v", id, err)
		}
		if ib.Enable {
			t.Errorf("inbound %d is still enabled", id)
		}
	}

	again, _, err := (&InboundService{}).SetInboundsEnable([]int{first.Id}, false)
	if err != nil {
		t.Fatalf("second SetInboundsEnable: %v", err)
	}
	if again.Changed != 0 || len(again.Skipped) != 1 || again.Skipped[0].Reason != "already disabled" {
		t.Fatalf("repeat = %+v, want the inbound reported as already disabled", again)
	}
}

func TestDelInboundRefusesAMissingId(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	if _, err := (&InboundService{}).DelInbound(999999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("deleting a missing inbound: err = %v, want not found", err)
	}
	res, _, err := (&InboundService{}).DelInbounds([]int{999999})
	if err != nil {
		t.Fatalf("DelInbounds: %v", err)
	}
	if res.Deleted != 0 || len(res.Skipped) != 1 || res.Skipped[0].Id != 999999 {
		t.Fatalf("bulk delete of a missing id = %+v, want it skipped, not counted", res)
	}
}
