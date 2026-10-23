package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func loadFleetNativeBindings(tx *gorm.DB, node model.FleetNode) ([]model.FleetNativeBinding, []string, error) {
	var saved []model.FleetNativeBinding
	if err := tx.Where("node_id = ? AND installation_id = ?", node.ID, node.InstallationID).Order("id").Limit(60001).Find(&saved).Error; err != nil {
		return nil, nil, err
	}
	if len(saved) > 60000 {
		return nil, nil, ErrFleetNativeBinding
	}
	for _, row := range saved {
		if !fleetID(row.ID) || !fleetID(row.PlacementID) || !fleetID(row.ClientID) || !fleetID(row.InboundInstanceID) || row.TargetID == 0 || len(row.PoolDigest) != sha256.Size || row.PublishedRevision <= 0 {
			return nil, nil, ErrFleetNativeBinding
		}
	}
	if len(saved) == 0 || node.AppliedRevision == 0 || node.AppliedRevision != node.DesiredRevision {
		return saved, nil, nil
	}
	var publication model.FleetNetworkPublication
	err := tx.Where("node_id = ? AND deployment_revision = ?", node.ID, node.AppliedRevision).First(&publication).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return saved, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var compilation model.FleetNetworkCompilation
	if err := tx.First(&compilation, "id = ? AND node_id = ? AND installation_id = ?", publication.CompilationID, node.ID, node.InstallationID).Error; err != nil {
		return nil, nil, err
	}
	var manifest FleetNetworkManifest
	if json.Unmarshal([]byte(compilation.Manifest), &manifest) != nil || manifest.NodeID != node.ID || manifest.InstallationID != node.InstallationID {
		return nil, nil, ErrFleetNativeBinding
	}
	present := make(map[string]bool, len(manifest.NativeBindings))
	for _, pin := range manifest.NativeBindings {
		present[pin.ID] = true
	}
	placements := make(map[string]string)
	for _, row := range saved {
		if !present[row.ID] && row.PublishedRevision < node.AppliedRevision {
			placements[row.PlacementID] = row.ClientID
		}
	}
	stopped, err := stoppedNativePlacements(tx, node, placements)
	if err != nil {
		return nil, nil, err
	}
	var active []model.FleetNativeBinding
	var retired []string
	for _, row := range saved {
		if !present[row.ID] && row.PublishedRevision < node.AppliedRevision && stopped[row.PlacementID] {
			retired = append(retired, row.ID)
		} else {
			active = append(active, row)
		}
	}
	return active, retired, nil
}

func stoppedNativePlacements(tx *gorm.DB, node model.FleetNode, placements map[string]string) (map[string]bool, error) {
	ids := make([]string, 0, len(placements))
	for id := range placements {
		ids = append(ids, id)
	}
	stopped, observed := map[string]bool{}, map[string]bool{}
	for offset := 0; offset < len(ids); offset += 512 {
		var rows []struct {
			model.FleetGrant
			ShareNodeID, ShareInstallationID, ShareRuntimeID, BudgetID   string
			ShareRevision, AppliedRevision, StopRevision, StopDeployment int64
			Desired, Outstanding                                         string
		}
		if err := tx.Table("fleet_grants g").Select("g.*, s.node_id AS share_node_id, s.installation_id AS share_installation_id, s.runtime_id AS share_runtime_id, s.budget_id, s.revision AS share_revision, s.applied_revision, s.desired, s.outstanding, stop.rate_revision AS stop_revision, stop.deployment_revision AS stop_deployment").
			Joins("LEFT JOIN fleet_rate_shares s ON s.id = g.id").Joins("LEFT JOIN fleet_grant_stops stop ON stop.grant_id = g.id").
			Where("g.placement_id IN ?", ids[offset:min(offset+512, len(ids))]).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			id := *row.PlacementID
			if !observed[id] {
				stopped[id], observed[id] = true, true
			}
			var desired, outstanding controlplane.Rates
			valid := json.Unmarshal([]byte(row.Desired), &desired) == nil && json.Unmarshal([]byte(row.Outstanding), &outstanding) == nil
			if !valid || row.ClientID != placements[id] || row.NodeID != node.ID || row.InstallationID != node.InstallationID || row.RuntimeID != "native" ||
				row.ShareNodeID != node.ID || row.ShareInstallationID != node.InstallationID || row.ShareRuntimeID != "native" || row.BudgetID != row.ClientID ||
				row.StopRevision <= 0 || row.StopRevision > row.AppliedRevision || row.AppliedRevision != row.ShareRevision || row.StopDeployment <= 0 || row.StopDeployment > node.AppliedRevision ||
				desired != (controlplane.Rates{}) || outstanding != (controlplane.Rates{}) {
				stopped[id] = false
			}
		}
	}
	return stopped, nil
}

func retireFleetNativeBindings(tx *gorm.DB, node model.FleetNode) error {
	_, retired, err := loadFleetNativeBindings(tx, node)
	if err != nil {
		return err
	}
	for offset := 0; offset < len(retired); offset += 512 {
		if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", retired[offset:min(offset+512, len(retired))], node.ID, node.InstallationID).Delete(&model.FleetNativeBinding{}).Error; err != nil {
			return err
		}
	}
	return nil
}
