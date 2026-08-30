package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"

	json_util "github.com/Arman2122/p-ui/internal/util/json_util"
)

/*
A template whose outbound array the panel cannot read must not be rewritten.

applyRoutingPlan is the only one of the four config injectors that both writes
cfg.OutboundConfigs and skipped this check — mergeSubscriptionOutbounds spells
the contract out in its own doc comment and the other three are pinned by
*_BadOutboundsSkips tests.

What made it urgent: the failed parse and the trigger for writing are the same
event. An unreadable array leaves `outbounds` nil, so outboundsByProtocol
answers "" for blackhole, so the planner synthesizes one — and `merged` then
starts from that same nil slice. The operator's direct, their proxies and their
subscription outbounds are replaced by a single blackhole. That config is
VALID, so the pre-flight passes and the running core is stopped and swapped for
it; every rule left naming a real outbound now names a dangling tag, and the
dispatcher closes those connections rather than falling back.
*/
func TestAnUnreadableOutboundArrayIsLeftAlone(t *testing.T) {
	initTestDB(t)

	inbound := &model.Inbound{
		UserId: 1, Tag: "vless-in", Enable: true, Port: 46310,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	rule := &model.RoutingRule{
		Enable: true, Remark: "block everything",
		IngressScope: model.RoutingScopeAll, DestKind: model.RoutingDestBlock,
	}
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed routing rule: %v", err)
	}

	const unreadable = `{"not":"an array"}`
	cfg := &xray.Config{
		RouterConfig:    json_util.RawMessage(`{"rules":[]}`),
		OutboundConfigs: json_util.RawMessage(unreadable),
	}
	if err := applyRoutingPlan(context.Background(), cfg, false); err != nil {
		t.Fatalf("applyRoutingPlan: %v", err)
	}

	if got := string(cfg.OutboundConfigs); got != unreadable {
		t.Fatalf("the template's outbounds were rewritten to %s\nfrom %s — every outbound the operator configured is gone, and the config is valid enough that the pre-flight will let it through", got, unreadable)
	}
}

// The readable path still gets its synthesized outbound appended, which is what
// makes the guard above a guard rather than a switch-off.
func TestAReadableOutboundArrayStillGetsTheSynthesizedBlackhole(t *testing.T) {
	initTestDB(t)

	inbound := &model.Inbound{
		UserId: 1, Tag: "vless-in2", Enable: true, Port: 46311,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	rule := &model.RoutingRule{
		Enable: true, Remark: "block everything",
		IngressScope: model.RoutingScopeAll, DestKind: model.RoutingDestBlock,
	}
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed routing rule: %v", err)
	}

	cfg := &xray.Config{
		RouterConfig:    json_util.RawMessage(`{"rules":[]}`),
		OutboundConfigs: json_util.RawMessage(`[{"protocol":"freedom","tag":"direct"}]`),
	}
	if err := applyRoutingPlan(context.Background(), cfg, false); err != nil {
		t.Fatalf("applyRoutingPlan: %v", err)
	}

	got := string(cfg.OutboundConfigs)
	if !strings.Contains(got, `"tag":"direct"`) {
		t.Fatalf("the operator's own outbound was dropped: %s", got)
	}
	if !strings.Contains(got, "blackhole") {
		t.Fatalf("no blackhole was synthesized for the block rule: %s", got)
	}
	if strings.Index(got, "direct") > strings.Index(got, "blackhole") {
		t.Fatalf("the synthesized outbound sorts before the operator's; the first outbound is xray's default handler: %s", got)
	}
}
