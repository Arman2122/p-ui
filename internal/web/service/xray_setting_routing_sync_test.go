package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func seedXrayTemplate(t *testing.T, template string) {
	t.Helper()
	s := &SettingService{}
	if err := s.saveSetting("xrayTemplateConfig", template); err != nil {
		t.Fatalf("saveSetting: %v", err)
	}
}

func routingRulesFromTemplate(t *testing.T, template string) []map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(template), &cfg); err != nil {
		t.Fatalf("unmarshal template: %v", err)
	}
	return routingRulesFromCfg(cfg)
}

func TestPropagateInboundTagRename_UpdatesRoutingRule(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["in-21368-tcp"],"outboundTag":"direct"},
				{"type":"field","inboundTag":["api"],"outboundTag":"api"}
			]
		},
		"outbounds": [{"tag":"direct","protocol":"freedom"}]
	}`)

	svc := &XraySettingService{}
	changed, err := svc.PropagateInboundTagRename("in-21368-tcp", "in-33000-tcp")
	if err != nil {
		t.Fatalf("PropagateInboundTagRename: %v", err)
	}
	if !changed {
		t.Fatal("expected routing template to change")
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rules := routingRulesFromTemplate(t, got)
	if len(rules) != 2 {
		t.Fatalf("rules len = %d, want 2", len(rules))
	}
	if tags := readInboundTags(rules[1]["inboundTag"]); tags[0] != "in-33000-tcp" {
		t.Fatalf("renamed rule inboundTag = %v, want [in-33000-tcp]", tags)
	}
	if tags := readInboundTags(rules[0]["inboundTag"]); tags[0] != "api" {
		t.Fatalf("api rule should stay untouched, got %v", tags)
	}
}

func TestPropagateInboundTagRename_UpdatesLoopbackOutbound(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {"rules": []},
		"outbounds": [
			{"tag":"loop","protocol":"loopback","settings":{"inboundTag":"in-21368-tcp"}}
		]
	}`)

	svc := &XraySettingService{}
	if _, err := svc.PropagateInboundTagRename("in-21368-tcp", "in-33000-tcp"); err != nil {
		t.Fatalf("PropagateInboundTagRename: %v", err)
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	outbounds := outboundsFromCfg(cfg)
	settings := outbounds[0].(map[string]any)["settings"].(map[string]any)
	if settings["inboundTag"] != "in-33000-tcp" {
		t.Fatalf("loopback inboundTag = %v, want in-33000-tcp", settings["inboundTag"])
	}
}

func TestRemoveInboundTagReferences_DropsInboundOnlyRule(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["in-21368-tcp"],"outboundTag":"direct"},
				{"type":"field","inboundTag":["api"],"outboundTag":"api"}
			]
		}
	}`)

	svc := &XraySettingService{}
	changed, err := svc.RemoveInboundTagReferences("in-21368-tcp")
	if err != nil {
		t.Fatalf("RemoveInboundTagReferences: %v", err)
	}
	if !changed {
		t.Fatal("expected template to change")
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rules := routingRulesFromTemplate(t, got)
	if len(rules) != 1 {
		t.Fatalf("rules len = %d, want 1 (api rule only)", len(rules))
	}
	if tags := readInboundTags(rules[0]["inboundTag"]); tags[0] != "api" {
		t.Fatalf("remaining rule = %v, want api rule", tags)
	}
}

/*
Renegotiated deliberately: this used to assert that a rule losing its last
inboundTag keeps its other matchers and simply drops the tag.

That is a WIDENING. An untagged rule matches every inbound, including every
front the routing compile creates, so deleting one inbound re-pointed every
other inbound's matching traffic out that rule's outbound. The operator's rule
is kept -- they wrote it -- but switched off with the reason.
*/
func TestRemoveInboundTagReferences_DisablesRuleThatLostItsLastInboundTag(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["api"],"outboundTag":"api"},
				{
					"type":"field",
					"inboundTag":["in-21368-tcp"],
					"domain":["example.com"],
					"outboundTag":"direct"
				}
			]
		}
	}`)

	svc := &XraySettingService{}
	if _, err := svc.RemoveInboundTagReferences("in-21368-tcp"); err != nil {
		t.Fatalf("RemoveInboundTagReferences: %v", err)
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rule := findRuleByOutbound(t, got, "direct")
	if enabled, ok := rule["enabled"].(bool); !ok || enabled {
		t.Fatalf("a rule with no inbound left must not stay live, rule = %#v", rule)
	}
	if _, ok := rule["inboundTag"]; ok {
		t.Fatalf("the dead tag should be gone, rule = %#v", rule)
	}
	if domain, _ := rule["domain"].([]any); len(domain) != 1 {
		t.Fatalf("the operator's own matchers are kept, rule = %#v", rule)
	}
	if remark, _ := rule["remark"].(string); remark == "" {
		t.Fatalf("the rule must say why it was switched off, rule = %#v", rule)
	}
}

/*
Renegotiated for the same reason as the rule above: `source` matches the CLIENT's
address, not the inbound, so untagging a source-scoped rule widens it to every
inbound whose users share that prefix — including a fronted kernel inbound.
*/
func TestRemoveInboundTagReferences_DisablesSourceScopedRuleThatLostItsInbound(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{
					"type":"field",
					"inboundTag":["in-443-tcp"],
					"source":["10.0.0.0/8"],
					"outboundTag":"blocked"
				}
			]
		}
	}`)

	svc := &XraySettingService{}
	if _, err := svc.RemoveInboundTagReferences("in-443-tcp"); err != nil {
		t.Fatalf("RemoveInboundTagReferences: %v", err)
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rule := findRuleByOutbound(t, got, "blocked")
	if _, ok := rule["inboundTag"]; ok {
		t.Fatalf("the dead tag should be trimmed, rule = %#v", rule)
	}
	if enabled, ok := rule["enabled"].(bool); !ok || enabled {
		t.Fatalf("an untagged rule matches every inbound, so it must not stay live: %#v", rule)
	}
	if src, _ := rule["source"].([]any); len(src) != 1 {
		t.Fatalf("the operator's rule is kept, not dropped; rule = %#v", rule)
	}
}

func TestRemoveInboundTagReferences_RemovesOneTagFromMultiInboundRule(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["api"],"outboundTag":"api"},
				{
					"type":"field",
					"inboundTag":["in-21368-tcp","in-443-tcp"],
					"outboundTag":"direct"
				}
			]
		}
	}`)

	svc := &XraySettingService{}
	if _, err := svc.RemoveInboundTagReferences("in-21368-tcp"); err != nil {
		t.Fatalf("RemoveInboundTagReferences: %v", err)
	}

	got, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rule := findRuleByOutbound(t, got, "direct")
	if tags := readInboundTags(rule["inboundTag"]); len(tags) != 1 || tags[0] != "in-443-tcp" {
		t.Fatalf("inboundTag = %v, want [in-443-tcp]", tags)
	}
}

func findRuleByOutbound(t *testing.T, template, outbound string) map[string]any {
	t.Helper()
	for _, rule := range routingRulesFromTemplate(t, template) {
		if rule["outboundTag"] == outbound {
			return rule
		}
	}
	t.Fatalf("no rule with outboundTag %q in %s", outbound, template)
	return nil
}

func TestPropagateInboundTagRename_WorksWithConflictDB(t *testing.T) {
	setupConflictDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["in-22435-tcp"],"outboundTag":"direct"}
			]
		},
		"outbounds": [{"tag":"direct","protocol":"freedom"}]
	}`)

	svc := &XraySettingService{}
	changed, err := svc.PropagateInboundTagRename("in-22435-tcp", "in-33000-tcp")
	if err != nil {
		t.Fatalf("PropagateInboundTagRename: %v", err)
	}
	if !changed {
		t.Fatal("expected template to change")
	}
}

func TestUpdateInbound_PropagatesRoutingRuleOnPortChange(t *testing.T) {
	setupConflictDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["api"],"outboundTag":"api"},
				{"type":"field","inboundTag":["in-22435-tcp"],"outboundTag":"direct"}
			]
		},
		"outbounds": [{"tag":"direct","protocol":"freedom"}]
	}`)
	seedInboundConflict(t, "in-22435-tcp", "0.0.0.0", 22435, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)

	var existing model.Inbound
	if err := database.GetDB().Where("tag = ?", "in-22435-tcp").First(&existing).Error; err != nil {
		t.Fatalf("read seeded row: %v", err)
	}

	svc := &InboundService{}
	update := existing
	update.Port = 33000
	update.Tag = "in-22435-tcp"
	got, needRestart, err := svc.UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got.Tag != "in-33000-tcp" {
		t.Fatalf("returned tag = %q, want in-33000-tcp", got.Tag)
	}
	if !needRestart {
		t.Fatal("expected needRestart after routing template sync on tag rename")
	}

	xraySvc := &XraySettingService{}
	template, err := xraySvc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rule := findRuleByOutbound(t, template, "direct")
	if tags := readInboundTags(rule["inboundTag"]); tags[0] != "in-33000-tcp" {
		t.Fatalf("routing inboundTag = %v, want [in-33000-tcp]", tags)
	}
}

func TestDelInbound_RemovesInboundOnlyRoutingRule(t *testing.T) {
	setupConflictDB(t)
	seedXrayTemplate(t, `{
		"routing": {
			"rules": [
				{"type":"field","inboundTag":["api"],"outboundTag":"api"},
				{"type":"field","inboundTag":["in-22435-tcp"],"outboundTag":"direct"},
				{"type":"field","inboundTag":["in-443-tcp"],"outboundTag":"blocked"}
			]
		}
	}`)
	seedInboundConflict(t, "in-22435-tcp", "0.0.0.0", 22435, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)

	var existing model.Inbound
	if err := database.GetDB().Where("tag = ?", "in-22435-tcp").First(&existing).Error; err != nil {
		t.Fatalf("read seeded row: %v", err)
	}

	svc := &InboundService{}
	if _, err := svc.DelInbound(existing.Id); err != nil {
		t.Fatalf("DelInbound: %v", err)
	}

	xraySvc := &XraySettingService{}
	template, err := xraySvc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	rules := routingRulesFromTemplate(t, template)
	for _, rule := range rules {
		if rule["outboundTag"] == "direct" {
			t.Fatalf("direct rule should be removed, got %#v", rule)
		}
	}
	findRuleByOutbound(t, template, "blocked")
}
