package service

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func fleetPlacementFanoutMembers(tx *gorm.DB, work model.FleetPlacementFanout, after string, limit int) ([]string, error) {
	var parts []string
	var args []any
	switch work.Scope {
	case "client":
		parts, args = append(parts, "SELECT id FROM fleet_clients WHERE id = ?"), append(args, work.ScopeID)
	case "rule":
		var rule model.FleetPlacementRule
		if err := tx.First(&rule, "id = ?", work.ScopeID).Error; err != nil {
			return nil, err
		}
		pins, _ := json.Marshal([]map[string]string{{"id": rule.ID}})
		parts, args = append(parts, "SELECT client_id FROM fleet_placement_intents WHERE rule_pins @> ?::jsonb"), append(args, string(pins))
		if rule.PlanID != nil {
			parts = append(parts, "SELECT f.id FROM client_policies p JOIN clients c ON c.email=p.email JOIN fleet_clients f ON f.panel_client_id=c.id WHERE p.policy_id = ?")
			args = append(args, *rule.PlanID)
		} else if rule.GroupID != nil {
			parts = append(parts, "SELECT f.id FROM client_groups g JOIN clients c ON c.group_name=g.name JOIN fleet_clients f ON f.panel_client_id=c.id WHERE g.id = ?")
			args = append(args, *rule.GroupID)
		} else {
			return nil, controlplane.ErrRecovery
		}
	case "default":
		parts = append(parts, standalonePlacementMembersSQL, "SELECT client_id FROM fleet_placement_intents WHERE default_revision > 0")
	case "node":
		nodeID, installationID, ok := strings.Cut(work.ScopeID, "/")
		if !ok || !fleetID(nodeID) || !fleetID(installationID) {
			return nil, controlplane.ErrRecovery
		}
		var node model.FleetNode
		query := tx.Where("id = ? AND installation_id = ?", nodeID, installationID).Find(&node)
		if query.Error != nil || query.RowsAffected == 0 {
			return nil, query.Error
		}
		parts = append(parts, "SELECT client_id FROM fleet_placement_intents WHERE node_id = ? AND installation_id = ?", "SELECT client_id FROM fleet_placements WHERE node_id = ? AND installation_id = ?")
		args = append(args, nodeID, installationID, nodeID, installationID)
		manual, _ := json.Marshal(map[string]any{"manualTargets": map[string][]int{nodeID: {}}})
		parts, args = append(parts, "SELECT id FROM fleet_clients WHERE placement_preferences @> ?::jsonb"), append(args, string(manual))
		var defaults model.FleetPlacementDefaults
		if err := tx.Where("id = 1").Find(&defaults).Error; err != nil {
			return nil, err
		}
		if defaults.NodeID != nil && *defaults.NodeID == nodeID && defaults.InstallationID != nil && *defaults.InstallationID == installationID {
			parts = append(parts, standalonePlacementMembersSQL)
		}
		var policy model.FleetNodePlacementPolicy
		if err := tx.Where("node_id = ? AND installation_id = ? AND enabled", nodeID, installationID).Find(&policy).Error; err != nil {
			return nil, err
		}
		if policy.NodeID != "" {
			encodedNode, _ := json.Marshal([]string{nodeID})
			var rules []model.FleetPlacementRule
			if err := tx.Where("enabled AND (node_ids = '[]'::jsonb OR node_ids @> ?::jsonb) AND ?::jsonb @> labels", string(encodedNode), policy.Labels).
				Order("id").Limit(4097).Find(&rules).Error; err != nil {
				return nil, err
			}
			if len(rules) > 4096 {
				return nil, controlplane.ErrInvalid
			}
			var plans, groups []int
			for _, rule := range rules {
				if rule.PlanID != nil {
					plans = append(plans, *rule.PlanID)
				}
				if rule.GroupID != nil {
					groups = append(groups, *rule.GroupID)
				}
			}
			if len(plans) > 0 {
				parts, args = append(parts, "SELECT f.id FROM client_policies p JOIN clients c ON c.email=p.email JOIN fleet_clients f ON f.panel_client_id=c.id WHERE p.policy_id IN ?"), append(args, plans)
			}
			if len(groups) > 0 {
				parts, args = append(parts, "SELECT f.id FROM client_groups g JOIN clients c ON c.group_name=g.name JOIN fleet_clients f ON f.panel_client_id=c.id WHERE g.id IN ?"), append(args, groups)
			}
		}
	default:
		return nil, controlplane.ErrRecovery
	}
	query := tx.Model(&model.FleetClient{}).Where("owned AND id IN (?)", tx.Raw(strings.Join(parts, " UNION "), args...)).Order("id").Limit(limit)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	var ids []string
	err := query.Pluck("id", &ids).Error
	return ids, err
}

const standalonePlacementMembersSQL = `SELECT f.id FROM fleet_clients f JOIN clients c ON c.id=f.panel_client_id
WHERE c.group_name='' AND NOT EXISTS (SELECT 1 FROM client_policies p WHERE p.email=c.email)
AND COALESCE(f.placement_preferences->'manualTargets','{}'::jsonb)='{}'::jsonb
AND NOT EXISTS (SELECT 1 FROM fleet_placements p WHERE p.client_id=f.id AND NOT EXISTS
(SELECT 1 FROM fleet_placement_intents i WHERE i.materialized_placement_id=p.id))`
