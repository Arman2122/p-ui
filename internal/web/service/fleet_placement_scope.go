package service

import (
	"encoding/json"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetPlacementPreferences struct {
	InboundIDs        *[]int           `json:"inboundIds"`
	SuppressedTargets map[string][]int `json:"suppressedTargets,omitempty"`
	ManualTargets     map[string][]int `json:"manualTargets,omitempty"`
}

type FleetPlacementRulePin struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

func fleetPlacementIntentTx(tx *gorm.DB, placement model.FleetPlacement) (model.FleetPlacementIntent, bool, error) {
	var intent model.FleetPlacementIntent
	query := tx.Where("client_id = ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", placement.ClientID, placement.NodeID, placement.InstallationID, placement.RuntimeID).Find(&intent)
	if query.Error != nil || query.RowsAffected == 0 {
		return intent, false, query.Error
	}
	if intent.CoreID != placement.CoreID || intent.MaterializedPlacementID != nil && *intent.MaterializedPlacementID != placement.ID {
		return intent, true, controlplane.ErrScope
	}
	return intent, true, nil
}

func placementInboundIDs(tx *gorm.DB, placementID string) ([]int, error) {
	var rows []model.FleetPlacementInbound
	if err := tx.Where("placement_id = ?", placementID).Order("panel_inbound").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.PanelInbound == nil || *row.PanelInbound <= 0 {
			return nil, ErrFleetSource
		}
		ids = append(ids, *row.PanelInbound)
	}
	return ids, nil
}

func fleetPlacementPreferencesTx(tx *gorm.DB, clientID string) (model.FleetClient, FleetPlacementPreferences, error) {
	var client model.FleetClient
	var preferences FleetPlacementPreferences
	if err := tx.Select("id,panel_client_id,pending_term_id,credential_generation,placement_revision,placement_preferences").First(&client, "id = ?", clientID).Error; err != nil {
		return client, preferences, err
	}
	if len(client.PlacementPreferences) > 1<<20 || json.Unmarshal([]byte(client.PlacementPreferences), &preferences) != nil {
		return client, preferences, controlplane.ErrRecovery
	}
	return client, preferences, nil
}

func placementInboundAllowed(preferences FleetPlacementPreferences, nodeID string, inboundID int) bool {
	return (preferences.InboundIDs == nil || slices.Contains(*preferences.InboundIDs, inboundID)) && !slices.Contains(preferences.SuppressedTargets[nodeID], inboundID)
}

func fleetPlacementAssignedTx(tx *gorm.DB, placement model.FleetPlacement) (bool, error) {
	intent, exists, err := fleetPlacementIntentTx(tx, placement)
	if err != nil || exists && (!intent.Assigned || intent.State == "retiring" || intent.State == "retired") {
		return false, err
	}
	_, preferences, err := fleetPlacementPreferencesTx(tx, placement.ClientID)
	if err != nil {
		return false, err
	}
	var ids []int
	if exists {
		if json.Unmarshal([]byte(intent.InboundIDs), &ids) != nil {
			return false, controlplane.ErrRecovery
		}
	} else {
		ids, err = placementInboundIDs(tx, placement.ID)
		if err != nil {
			return false, err
		}
	}
	return slices.ContainsFunc(ids, func(id int) bool { return placementInboundAllowed(preferences, placement.NodeID, id) }), nil
}

func requireFleetPlacementSourceReadyTx(tx *gorm.DB, placement model.FleetPlacement) error {
	stopping, err := fleetPlacementStopPending(tx, placement.ID)
	if err != nil {
		return err
	}
	if stopping {
		return controlplane.ErrDrain
	}
	intent, exists, err := fleetPlacementIntentTx(tx, placement)
	if err != nil {
		return err
	}
	client, preferences, err := fleetPlacementPreferencesTx(tx, placement.ClientID)
	if err != nil {
		return err
	}
	if client.PendingTermID != nil {
		return controlplane.ErrDrain
	}
	actual, err := placementInboundIDs(tx, placement.ID)
	if err != nil {
		return err
	}
	if len(actual) == 0 || slices.ContainsFunc(actual, func(id int) bool { return !placementInboundAllowed(preferences, placement.NodeID, id) }) {
		return controlplane.ErrDrain
	}
	var policy model.FleetNodePlacementPolicy
	query := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("node_id = ?", placement.NodeID).Find(&policy)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected != 0 {
		var eligible []int
		if json.Unmarshal([]byte(policy.InboundIDs), &eligible) != nil {
			return controlplane.ErrRecovery
		}
		if !policy.Enabled || policy.InstallationID != placement.InstallationID || slices.ContainsFunc(actual, func(id int) bool { return !slices.Contains(eligible, id) }) {
			return controlplane.ErrDrain
		}
	}
	if !exists {
		return nil
	}
	var desired []int
	if json.Unmarshal([]byte(intent.InboundIDs), &desired) != nil {
		return controlplane.ErrRecovery
	}
	slices.Sort(desired)
	if !intent.Assigned || intent.RequiresStop || intent.State == "retiring" || intent.State == "retired" || intent.SourceRevision != client.PlacementRevision || intent.CredentialGeneration != client.CredentialGeneration || !slices.Equal(actual, desired) {
		return controlplane.ErrDrain
	}
	if !policy.Enabled || policy.InstallationID != placement.InstallationID || policy.Revision != intent.NodePolicyRevision {
		return controlplane.ErrDrain
	}
	if intent.DefaultRevision > 0 {
		var defaults model.FleetPlacementDefaults
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&defaults, "id = 1").Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return controlplane.ErrDrain
			}
			return err
		}
		if defaults.Revision != intent.DefaultRevision || defaults.NodeID == nil || *defaults.NodeID != placement.NodeID || defaults.InstallationID == nil || *defaults.InstallationID != placement.InstallationID {
			return controlplane.ErrDrain
		}
	}
	var pins []FleetPlacementRulePin
	if json.Unmarshal([]byte(intent.RulePins), &pins) != nil || len(pins) > 128 {
		return controlplane.ErrRecovery
	}
	ids := make([]string, 0, len(pins))
	revisions := make(map[string]uint64, len(pins))
	for _, pin := range pins {
		if !fleetID(pin.ID) || pin.Revision == 0 || revisions[pin.ID] != 0 {
			return controlplane.ErrRecovery
		}
		ids, revisions[pin.ID] = append(ids, pin.ID), pin.Revision
	}
	if len(ids) == 0 {
		if intent.DefaultRevision == 0 && len(preferences.ManualTargets[placement.NodeID]) == 0 {
			return controlplane.ErrDrain
		}
		return nil
	}
	membership, err := lockFleetPlacementMembership(tx, client)
	if err != nil {
		return err
	}
	var rules []model.FleetPlacementRule
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", ids).Order("id").Find(&rules).Error; err != nil {
		return err
	}
	if len(rules) != len(ids) {
		return controlplane.ErrDrain
	}
	for _, rule := range rules {
		if !rule.Enabled || uint64(rule.Revision) != revisions[rule.ID] {
			return controlplane.ErrDrain
		}
		if rule.Scope == "plan" && !equalPlacementScopeID(membership.PlanID, rule.PlanID) || rule.Scope == "group" && !equalPlacementScopeID(membership.GroupID, rule.GroupID) {
			return controlplane.ErrDrain
		}
	}
	return nil
}

type fleetPlacementMembership struct{ PlanID, GroupID *int }

func lockFleetPlacementMembership(tx *gorm.DB, client model.FleetClient) (fleetPlacementMembership, error) {
	var membership fleetPlacementMembership
	if client.PanelClientID == nil {
		return membership, controlplane.ErrDrain
	}
	var source model.ClientRecord
	if err := tx.First(&source, *client.PanelClientID).Error; err != nil {
		return membership, err
	}
	var assignment model.ClientPolicy
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("email = ?", source.Email).Find(&assignment).Error; err != nil {
		return membership, err
	}
	membership.PlanID = assignment.PolicyId
	if membership.PlanID != nil {
		var plan model.Policy
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&plan, *membership.PlanID).Error; err != nil {
			return membership, err
		}
	}
	if source.Group != "" {
		var group model.ClientGroup
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&group, "name = ?", source.Group).Error; err != nil {
			return membership, err
		}
		membership.GroupID = &group.Id
	}
	return membership, nil
}
