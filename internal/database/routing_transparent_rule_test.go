package database

import "testing"

/*
The migration must recognise the panel's own DNS-allow rules to leave them alone.

isTransparentRule looked for a `-dns-allow` suffix on inboundTag, and the rules
xray_setting_dns_routing.go writes carry `ruleTag: "pui-dns-allow"` and no
inboundTag at all -- so the guard never fired on one, and a migration pass could
strip the anchor those rules depend on. F162.
*/
func TestTransparentRuleRecognisesTheDNSAllowRuleTag(t *testing.T) {
	rule := map[string]any{
		"type": "field", "ruleTag": "pui-dns-allow",
		"ip": []any{"172.20.0.53"}, "port": "53", "outboundTag": "direct",
	}
	if !isTransparentRule(rule) {
		t.Fatal("a pui-dns-allow rule, keyed on ruleTag as the panel writes it, was not recognised as the panel's own")
	}
	if isTransparentRule(map[string]any{"type": "field", "ruleTag": "mine", "outboundTag": "direct"}) {
		t.Fatal("an operator's rule with an unrelated ruleTag was treated as the panel's")
	}
}
