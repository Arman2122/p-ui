package service

import (
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func completedFleetSourceDisables(tx *gorm.DB, node model.FleetNode) (map[string]model.FleetSourceWork, error) {
	var rows []model.FleetSourceWork
	if err := tx.Where("node_id = ? AND installation_id = ? AND NOT enabled AND NOT pending AND NOT requires_stop", node.ID, node.InstallationID).
		Order("placement_id").Limit(60001).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > 60000 {
		return nil, controlplane.ErrInvalid
	}
	result := make(map[string]model.FleetSourceWork, len(rows))
	for _, row := range rows {
		result[row.PlacementID] = row
	}
	return result, nil
}

func fleetSourceDisabledRetirement(tx *gorm.DB, node model.FleetNode, expected model.FleetSourceWork) (FleetNetworkAuthorityIdentity, bool, error) {
	var result FleetNetworkAuthorityIdentity
	client, source, err := lockFleetSourceClient(tx, expected.ClientID)
	if err != nil {
		return result, false, err
	}
	var work model.FleetSourceWork
	var placement model.FleetPlacement
	if err := tx.First(&work, "placement_id = ?", expected.PlacementID).Error; err != nil {
		return result, false, err
	}
	if err := tx.First(&placement, "id = ?", expected.PlacementID).Error; err != nil {
		return result, false, err
	}
	if source.Enable || work.Enabled || work.Pending || work.RequiresStop || work.Revision != expected.Revision || work.OperationID != expected.OperationID ||
		work.ClientID != client.ID || work.ClientID != placement.ClientID || work.NodeID != node.ID || placement.NodeID != node.ID ||
		work.InstallationID != node.InstallationID || placement.InstallationID != node.InstallationID || work.RuntimeID != placement.RuntimeID {
		return result, false, nil
	}
	stopped, _, err := fleetSourceWorkStopped(tx, work)
	if err != nil || !stopped {
		return result, false, err
	}
	result = FleetNetworkAuthorityIdentity{
		PlacementID: placement.ID, ClientID: client.ID, RuntimeID: placement.RuntimeID, PlacementRevision: uint64(placement.Revision),
		CredentialGeneration: uint64(client.CredentialGeneration), State: "retired",
	}
	if client.CurrentTermID != nil {
		result.TermID = *client.CurrentTermID
	}
	return result, true, nil
}
