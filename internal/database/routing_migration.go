package database

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
migrateRoutingIntent turns today's two half-routers into one table of intent.

An egress attachment and a template routing rule both said "send this traffic
there", in two places that could not see each other. Both become rows here.

Nothing is imported that the compile could not realise, and nothing is imported
that would change the meaning of what stays behind — which is why the walk stops
at the first inexpressible rule rather than skipping past it.
*/
func migrateRoutingIntent() error {
	migrator := db.Migrator()
	if !migrator.HasTable(&model.RoutingRule{}) || !migrator.HasTable(&model.Egress{}) {
		return nil
	}
	if err := addConstraintOnce("egresses", "uq_egresses_ingress_inbound",
		`UNIQUE (ingress_inbound_id)`); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := adoptEgressAttachments(tx); err != nil {
			return err
		}
		if err := importTemplateRoutingRules(tx); err != nil {
			return err
		}
		return dropUnattachedFronts(tx)
	})
}

/*
adoptEgressAttachments turns every attachment into a rule and its egress row into
a panel-owned front.

The lowest-id inbound KEEPS the row, so its peg device, table, priority and
gateway are byte-identical afterwards and no kernel object moves. An egress that
served several inbounds is split — the model is one front per ingress — and the
others re-route through a fresh front on the first converge. That is contained
rather than leaked, because the converge installs the blackhole before the rule,
but it is a real re-route and the release notes have to say so.
*/
func adoptEgressAttachments(tx *gorm.DB) error {
	var inbounds []*model.Inbound
	if err := tx.Where("egress_id IS NOT NULL").Order("id").Find(&inbounds).Error; err != nil {
		return err
	}
	// Snapshot every row BEFORE mutating one: adopting the first inbound clears
	// the shared row's target, and a later inbound would then read it as empty.
	var rows []*model.Egress
	if err := tx.Find(&rows).Error; err != nil {
		return err
	}
	original := make(map[int]*model.Egress, len(rows))
	for _, row := range rows {
		original[row.Id] = row
	}

	claimed := map[int]bool{}
	for _, inbound := range inbounds {
		row, known := original[*inbound.EgressID]
		if !known {
			continue
		}
		target, kind := row.Target, model.RoutingDestOutbound
		if target == "" {
			continue
		}
		frontID := row.Id
		if claimed[row.Id] {
			// A second inbound on one row: it needs a front of its own.
			fresh := &model.Egress{Type: row.Type, Enable: true, Remark: row.Remark, Owner: model.EgressOwnerPanel}
			if err := tx.Create(fresh).Error; err != nil {
				return err
			}
			frontID = fresh.Id
		}
		claimed[row.Id] = true

		if err := createRoutingRule(tx, inbound.Id, kind, target); err != nil {
			return err
		}
		// Clear the reference BEFORE the front is owned: fk_inbounds_egress is ON
		// DELETE RESTRICT, and it would otherwise block the ingress cascade later.
		if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
			Update("egress_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Egress{}).Where("id = ?", frontID).Updates(map[string]any{
			"owner": model.EgressOwnerPanel, "ingress_inbound_id": inbound.Id, "target": "",
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// createRoutingRule appends one rule at the end of the current order.
func createRoutingRule(tx *gorm.DB, inboundID int, destKind, destTag string) error {
	var next int
	if err := tx.Model(&model.RoutingRule{}).
		Select("COALESCE(MAX(sort_index), -1) + 1").Scan(&next).Error; err != nil {
		return err
	}
	ids, err := json.Marshal([]int{inboundID})
	if err != nil {
		return err
	}
	return tx.Create(&model.RoutingRule{
		SortIndex: next, Enable: true, IngressScope: model.RoutingScopeSelected,
		IngressIds: string(ids), DestKind: destKind, DestTag: destTag, Criteria: "{}",
	}).Error
}

/*
importTemplateRoutingRules moves the template's own rules into intent rows,
MAXIMAL CONTIGUOUS PREFIX ONLY.

Stopping at the first inexpressible rule is correctness rather than tidiness:
intent rules compile above the template tail, so importing a rule that sat BELOW
a rule left behind would silently move it above that rule.
*/
func importTemplateRoutingRules(tx *gorm.DB) error {
	var setting model.Setting
	if err := tx.Where("key = ?", "xrayTemplateConfig").First(&setting).Error; err != nil {
		return nil
	}
	var template map[string]any
	if json.Unmarshal([]byte(setting.Value), &template) != nil {
		return nil
	}
	routing, _ := template["routing"].(map[string]any)
	rawRules, _ := routing["rules"].([]any)
	if len(rawRules) == 0 {
		return nil
	}
	tags, err := routableTagsByInbound(tx)
	if err != nil {
		return err
	}

	kept := make([]any, 0, len(rawRules))
	stopped := false
	for _, raw := range rawRules {
		rule, _ := raw.(map[string]any)
		if stopped || rule == nil {
			kept = append(kept, raw)
			continue
		}
		if isTransparentRule(rule) {
			kept = append(kept, raw)
			continue
		}
		inboundID, dest, tag, ok := expressibleRule(rule, tags)
		if !ok {
			stopped = true
			kept = append(kept, raw)
			continue
		}
		if err := createImportedRule(tx, inboundID, dest, tag, rule); err != nil {
			return err
		}
	}
	if len(kept) == len(rawRules) {
		return nil
	}
	routing["rules"] = kept
	rewritten, err := json.Marshal(template)
	if err != nil {
		return err
	}
	return tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
		Update("value", string(rewritten)).Error
}

/*
isTransparentRule reports the rules the panel maintains itself, which are left
exactly where they are.

The geoip:private block is in this set deliberately. It is the anchor
findPrivateBlockRule looks for; importing it would both invert its position
against the pui-dns-allow rules and remove the anchor, after which the next save
strips every allow rule and never reinserts one.
*/
func isTransparentRule(rule map[string]any) bool {
	if out, _ := rule["outboundTag"].(string); out == "api" {
		return true
	}
	if tag, _ := rule["balancerTag"].(string); strings.HasPrefix(tag, "_bl_") {
		return true
	}
	for _, tag := range ruleStrings(rule["inboundTag"]) {
		if strings.HasSuffix(tag, "-dns-allow") || strings.HasPrefix(tag, "_bl_") {
			return true
		}
	}
	for _, ip := range ruleStrings(rule["ip"]) {
		if strings.EqualFold(ip, "geoip:private") {
			return true
		}
	}
	return false
}

// expressibleRule reports whether one template rule can become intent, and what
// it becomes. Every clause is a way a rule could otherwise be imported and then
// never compile.
func expressibleRule(rule map[string]any, tags map[string]int) (int, string, string, bool) {
	inbounds := ruleStrings(rule["inboundTag"])
	if len(inbounds) != 1 {
		return 0, "", "", false
	}
	inboundID, known := tags[inbounds[0]]
	if !known {
		return 0, "", "", false
	}
	if tag, _ := rule["balancerTag"].(string); tag != "" {
		return inboundID, model.RoutingDestBalancer, tag, true
	}
	out, _ := rule["outboundTag"].(string)
	if out == "" {
		return 0, "", "", false
	}
	return inboundID, model.RoutingDestOutbound, out, true
}

// routableTagsByInbound maps a tag to its inbound id, excluding node inbounds:
// their tags resolve, but this host's rules array never reaches them, so an
// imported rule would be a first-class row that can never compile.
func routableTagsByInbound(tx *gorm.DB) (map[string]int, error) {
	var inbounds []*model.Inbound
	if err := tx.Where("node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(inbounds))
	for _, inbound := range inbounds {
		if inbound.Tag != "" {
			out[inbound.Tag] = inbound.Id
		}
	}
	return out, nil
}

func createImportedRule(tx *gorm.DB, inboundID int, destKind, destTag string, rule map[string]any) error {
	criteria := map[string]any{}
	for key, value := range rule {
		switch key {
		case "type", "inboundTag", "outboundTag", "balancerTag":
			continue
		}
		criteria[key] = value
	}
	body, err := json.Marshal(criteria)
	if err != nil {
		return err
	}
	var next int
	if err := tx.Model(&model.RoutingRule{}).
		Select("COALESCE(MAX(sort_index), -1) + 1").Scan(&next).Error; err != nil {
		return err
	}
	ids, err := json.Marshal([]int{inboundID})
	if err != nil {
		return err
	}
	return tx.Create(&model.RoutingRule{
		SortIndex: next, Enable: true, IngressScope: model.RoutingScopeSelected,
		IngressIds: string(ids), DestKind: destKind, DestTag: destTag, Criteria: string(body),
	}).Error
}

// dropUnattachedFronts removes an xray-tun row nothing was attached to: it routed
// nothing, and the first converge tears its kernel objects down.
func dropUnattachedFronts(tx *gorm.DB) error {
	return tx.Where("type = ? AND owner = ? AND ingress_inbound_id IS NULL",
		"xray-tun", model.EgressOwnerOperator).Delete(&model.Egress{}).Error
}

func ruleStrings(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// migratePanelEgressForeignKeys pins the cascade that makes a deleted inbound
// take its front with it, after the attachments above cleared egress_id.
func migratePanelEgressForeignKeys() error {
	if !db.Migrator().HasTable(&model.Egress{}) {
		return nil
	}
	if err := db.Exec(`UPDATE egresses SET owner = 'operator' WHERE owner IS NULL OR owner = ''`).Error; err != nil {
		return err
	}
	err := addConstraintOnce("egresses", "fk_egresses_ingress_inbound",
		`FOREIGN KEY (ingress_inbound_id) REFERENCES inbounds(id) ON DELETE CASCADE`)
	if err != nil {
		return fmt.Errorf("pin the front cascade: %w", err)
	}
	return nil
}
