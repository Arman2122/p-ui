package database

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func seedTemplate(t *testing.T, rules []any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"routing": map[string]any{"rules": rules}})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	setting := model.Setting{Key: "xrayTemplateConfig", Value: string(body)}
	if err := db.Where("key = ?", setting.Key).Delete(&model.Setting{}).Error; err != nil {
		t.Fatalf("clear template: %v", err)
	}
	if err := db.Create(&setting).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}
}

func templateRules(t *testing.T) []any {
	t.Helper()
	var setting model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&setting).Error; err != nil {
		t.Fatalf("reload template: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(setting.Value), &parsed); err != nil {
		t.Fatalf("template is unparsable after migration: %v", err)
	}
	routing, _ := parsed["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	return rules
}

func seedRoutedInbound(t *testing.T, tag string, port int, protocol model.Protocol, nodeID *int) *model.Inbound {
	t.Helper()
	in := &model.Inbound{UserId: 1, Remark: tag, Tag: tag, Port: port, Protocol: protocol, Enable: true, NodeID: nodeID}
	if err := db.Create(in).Error; err != nil {
		t.Fatalf("seed inbound %s: %v", tag, err)
	}
	return in
}

func rulesInOrder(t *testing.T) []model.RoutingRule {
	t.Helper()
	var out []model.RoutingRule
	if err := db.Order("sort_index").Find(&out).Error; err != nil {
		t.Fatalf("read routing rules: %v", err)
	}
	return out
}

/*
The geoip:private block is the anchor findPrivateBlockRule looks for. Importing
it would put the block above the allow rules AND remove the anchor, after which
the next SaveXraySetting strips every pui-dns-allow rule and never puts one back.
*/
func TestMigrationKeepsThePrivateBlockAnchor(t *testing.T) {
	initTestDB(t)
	seedTemplate(t, []any{
		map[string]any{"type": "field", "inboundTag": []any{"api"}, "outboundTag": "api"},
		map[string]any{"type": "field", "inboundTag": []any{"pui-dns-allow"}, "outboundTag": "direct"},
		map[string]any{"type": "field", "ip": []any{"geoip:private"}, "outboundTag": "blocked"},
		map[string]any{"type": "field", "protocol": []any{"bittorrent"}, "outboundTag": "blocked"},
	})

	if err := migrateRoutingIntent(); err != nil {
		t.Fatalf("migrateRoutingIntent: %v", err)
	}

	kept := templateRules(t)
	var anchor, allow int = -1, -1
	for i, raw := range kept {
		rule, _ := raw.(map[string]any)
		if out, _ := rule["outboundTag"].(string); out == "blocked" {
			if ips, _ := rule["ip"].([]any); len(ips) == 1 && ips[0] == "geoip:private" {
				anchor = i
			}
		}
		if tags, _ := rule["inboundTag"].([]any); len(tags) == 1 && tags[0] == "pui-dns-allow" {
			allow = i
		}
	}
	if anchor < 0 {
		t.Fatal("the geoip:private anchor must stay in the template, or dns-allow rules are stripped forever")
	}
	if allow < 0 || allow > anchor {
		t.Fatalf("the allow rule must stay ABOVE the block rule, got allow=%d anchor=%d", allow, anchor)
	}
}

/*
An egress row may serve several inbounds today; the new model is one front per
ingress. The lowest-id inbound keeps the row so its kernel objects do not move,
and the others get fresh rows — a real re-route the release notes must state.
*/
func TestMigrationPreservesFirstAttachedEgressId(t *testing.T) {
	initTestDB(t)
	seedTemplate(t, []any{})

	row := &model.Egress{Type: "xray-tun", Enable: true, Target: "warp", Remark: "shared"}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed egress: %v", err)
	}
	first := seedRoutedInbound(t, "wg-a", 51820, model.WGKernel, nil)
	second := seedRoutedInbound(t, "wg-b", 51821, model.WGKernel, nil)
	for _, in := range []*model.Inbound{first, second} {
		if err := db.Model(&model.Inbound{}).Where("id = ?", in.Id).Update("egress_id", row.Id).Error; err != nil {
			t.Fatalf("attach %s: %v", in.Tag, err)
		}
	}

	if err := migrateRoutingIntent(); err != nil {
		t.Fatalf("migrateRoutingIntent: %v", err)
	}

	var keptRow model.Egress
	if err := db.First(&keptRow, row.Id).Error; err != nil {
		t.Fatalf("the original row must survive for the lowest-id inbound: %v", err)
	}
	if keptRow.IngressInboundId == nil || *keptRow.IngressInboundId != first.Id {
		t.Fatalf("row %d must belong to inbound %d, got %v", row.Id, first.Id, keptRow.IngressInboundId)
	}
	if keptRow.Owner != model.EgressOwnerPanel || keptRow.Target != "" {
		t.Fatalf("an adopted front is panel-owned with no target, got owner=%q target=%q", keptRow.Owner, keptRow.Target)
	}

	var fresh model.Egress
	if err := db.Where("ingress_inbound_id = ?", second.Id).First(&fresh).Error; err != nil {
		t.Fatalf("the second inbound must get a front of its own: %v", err)
	}
	if fresh.Id == row.Id {
		t.Fatal("two inbounds cannot share one front under the new model")
	}

	rules := rulesInOrder(t)
	if len(rules) != 2 {
		t.Fatalf("each attachment becomes one rule, got %d", len(rules))
	}
	for _, rule := range rules {
		if rule.DestKind != model.RoutingDestOutbound || rule.DestTag != "warp" {
			t.Errorf("rule %d = %s/%s, want outbound/warp", rule.Id, rule.DestKind, rule.DestTag)
		}
	}
	// The reference has to be cleared, or fk_inbounds_egress ON DELETE RESTRICT
	// blocks the ingress cascade the new FK installs.
	var stillAttached int64
	if err := db.Model(&model.Inbound{}).Where("egress_id IS NOT NULL").Count(&stillAttached).Error; err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if stillAttached != 0 {
		t.Fatalf("egress_id must stop being referenced, %d left", stillAttached)
	}
}

/*
Intent rules compile ABOVE the template tail, so importing a rule that sat below
a rule left behind would silently move it above that rule. The walk therefore
stops at the first rule it cannot express rather than skipping past it.
*/
func TestMigrationStopsAtTheFirstInexpressibleRule(t *testing.T) {
	initTestDB(t)
	node := 2
	seedRoutedInbound(t, "vless-in", 10443, model.VLESS, nil)
	seedRoutedInbound(t, "node-in", 10444, model.VLESS, &node)

	seedTemplate(t, []any{
		map[string]any{"type": "field", "inboundTag": []any{"vless-in"}, "outboundTag": "warp"},
		map[string]any{"type": "field", "inboundTag": []any{"node-in"}, "outboundTag": "warp"},
		map[string]any{"type": "field", "inboundTag": []any{"vless-in"}, "outboundTag": "direct"},
	})

	if err := migrateRoutingIntent(); err != nil {
		t.Fatalf("migrateRoutingIntent: %v", err)
	}

	rules := rulesInOrder(t)
	if len(rules) != 1 {
		t.Fatalf("only the contiguous prefix imports, got %d rules", len(rules))
	}
	if rules[0].DestTag != "warp" {
		t.Fatalf("the imported rule = %q, want the first one", rules[0].DestTag)
	}
	kept := templateRules(t)
	if len(kept) != 2 {
		t.Fatalf("the node rule and everything below it stay in the tail, got %d", len(kept))
	}
	first, _ := kept[0].(map[string]any)
	if tags, _ := first["inboundTag"].([]any); len(tags) != 1 || tags[0] != "node-in" {
		t.Fatalf("the tail must start at the node rule, got %v", kept[0])
	}
}

// A front nothing was attached to routed nothing; the first converge tears its
// kernel objects down, so leaving the row would only burn a band id.
func TestMigrationDropsAnUnattachedFront(t *testing.T) {
	initTestDB(t)
	seedTemplate(t, []any{})
	orphan := &model.Egress{Type: "xray-tun", Enable: true, Target: "blocked", Remark: "orphan"}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	if err := migrateRoutingIntent(); err != nil {
		t.Fatalf("migrateRoutingIntent: %v", err)
	}

	var left int64
	if err := db.Model(&model.Egress{}).Where("id = ?", orphan.Id).Count(&left).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Fatal("an xray-tun row with no attachment routed nothing and must not survive")
	}
}
