package service

import (
	"encoding/json"
	"slices"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func resolveFleetPlacementRuntime(tx *gorm.DB, source fleetPlacementSource, target *FleetPlacementTarget) (bool, error) {
	target.RuntimeID, target.Diagnostic = "", "NETWORK_POLICY_REQUIRED"
	var head model.FleetNodeNetworkHead
	query := tx.Where("node_id = ? AND installation_id = ?", target.NodeID, target.InstallationID).Find(&head)
	if query.Error != nil || query.RowsAffected == 0 {
		return false, query.Error
	}
	revision, err := loadNetworkRevision(tx, head.CurrentRevisionID, "node")
	if err != nil {
		return false, err
	}
	defaults, err := loadNetworkRevision(tx, head.DesiredDefaultsID, "defaults")
	if err != nil {
		return false, err
	}
	if revision.NodeID == nil || *revision.NodeID != target.NodeID || revision.InstallationID == nil || *revision.InstallationID != target.InstallationID {
		return false, controlplane.ErrScope
	}
	var overrides FleetNetworkOverrides
	var base FleetNetworkSpec
	if json.Unmarshal([]byte(revision.Specification), &overrides) != nil || json.Unmarshal([]byte(defaults.Specification), &base) != nil {
		return false, controlplane.ErrRecovery
	}
	resolved, _, err := resolveFleetNetwork(base, overrides)
	if err != nil {
		return false, err
	}
	var journals []model.FleetRuntimeJournal
	if err := tx.Where("node_id = ? AND installation_id = ? AND core_id = ?", target.NodeID, target.InstallationID, target.CoreID).Order("runtime_id").Limit(129).Find(&journals).Error; err != nil {
		return false, err
	}
	if len(journals) > 128 {
		return false, controlplane.ErrInvalid
	}
	var candidates []string
	baseCount := 0
	for _, base := range resolved.Bases {
		if base.Core == target.CoreID {
			baseCount++
		}
	}
	for _, journal := range journals {
		if base, exists := resolved.Bases[journal.RuntimeID]; exists && base.Core == target.CoreID {
			candidates = append(candidates, journal.RuntimeID)
		}
	}
	var existing []model.FleetPlacement
	if err := tx.Where("client_id = ? AND node_id = ? AND installation_id = ? AND core_id = ?", source.client.ID, target.NodeID, target.InstallationID, target.CoreID).Order("runtime_id").Limit(129).Find(&existing).Error; err != nil {
		return false, err
	}
	if len(existing) > 128 {
		return false, controlplane.ErrInvalid
	}
	var retained []string
	for _, placement := range existing {
		if slices.Contains(candidates, placement.RuntimeID) {
			retained = append(retained, placement.RuntimeID)
		}
	}
	if len(retained) == 1 {
		target.RuntimeID = retained[0]
		if baseCount > 1 {
			target.Diagnostic = "MULTIPLE_CORE_RUNTIMES_UNQUALIFIED"
			return false, nil
		}
		return true, nil
	}
	if len(retained) == 0 && len(candidates) == 1 && baseCount == 1 {
		target.RuntimeID = candidates[0]
		return true, nil
	}
	target.Diagnostic = "RUNTIME_UNAVAILABLE"
	if len(retained) > 1 || len(candidates) > 1 {
		target.Diagnostic = "RUNTIME_AMBIGUOUS"
	}
	return false, nil
}
