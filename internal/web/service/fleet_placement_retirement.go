package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func completedFleetPlacementRetirements(tx *gorm.DB, node model.FleetNode) (map[string]model.FleetPlacementIntent, error) {
	var rows []model.FleetPlacementIntent
	if err := tx.Where("node_id = ? AND installation_id = ? AND NOT assigned AND NOT requires_stop AND materialized_placement_id IS NOT NULL", node.ID, node.InstallationID).
		Order("id").Limit(60001).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > 60000 {
		return nil, controlplane.ErrInvalid
	}
	result := make(map[string]model.FleetPlacementIntent, len(rows))
	for _, row := range rows {
		result[*row.MaterializedPlacementID] = row
	}
	return result, nil
}

func fleetPlacementRetirementIdentity(tx *gorm.DB, node model.FleetNode, expected model.FleetPlacementIntent) (FleetNetworkAuthorityIdentity, bool, error) {
	var identity FleetNetworkAuthorityIdentity
	source, err := loadFleetPlacementSource(tx, expected.ClientID)
	if err != nil {
		return identity, false, err
	}
	var current model.FleetPlacementIntent
	if err := tx.First(&current, "id = ?", expected.ID).Error; err != nil {
		return identity, false, err
	}
	if current.Revision != expected.Revision || current.Assigned || current.RequiresStop || current.MaterializedPlacementID == nil ||
		current.SourceRevision != source.client.PlacementRevision || current.NodeID != node.ID || current.InstallationID != node.InstallationID {
		return identity, false, nil
	}
	projection, err := projectFleetPlacementTx(tx, source)
	if err != nil {
		return identity, false, err
	}
	for _, target := range projection.Targets {
		if target.NodeID == current.NodeID && target.InstallationID == current.InstallationID && target.CoreID == current.CoreID && (target.RuntimeID == "" || target.RuntimeID == current.RuntimeID) {
			return identity, false, nil
		}
	}
	var placement model.FleetPlacement
	if err := tx.First(&placement, "id = ?", *current.MaterializedPlacementID).Error; err != nil {
		return identity, false, err
	}
	work, err := fleetPlacementSourceWork(tx, current, placement)
	if err != nil {
		return identity, false, err
	}
	stopped, _, err := fleetSourceWorkStopped(tx, work)
	if err != nil || !stopped || work.RequiresStop || work.Enabled != source.source.Enable {
		return identity, false, err
	}
	identity = FleetNetworkAuthorityIdentity{PlacementID: placement.ID, ClientID: current.ClientID, RuntimeID: current.RuntimeID, PlacementRevision: uint64(placement.Revision), CredentialGeneration: uint64(source.client.CredentialGeneration), State: "retired"}
	if source.client.CurrentTermID != nil {
		identity.TermID = *source.client.CurrentTermID
	}
	return identity, true, nil
}

func fleetPlacementSourceWork(tx *gorm.DB, intent model.FleetPlacementIntent, placement model.FleetPlacement) (model.FleetSourceWork, error) {
	var work model.FleetSourceWork
	if intent.MaterializedPlacementID == nil || *intent.MaterializedPlacementID != placement.ID || placement.ClientID != intent.ClientID || placement.NodeID != intent.NodeID || placement.InstallationID != intent.InstallationID || placement.RuntimeID != intent.RuntimeID || placement.CoreID != intent.CoreID {
		return work, controlplane.ErrScope
	}
	if err := tx.First(&work, "placement_id = ?", placement.ID).Error; err != nil {
		return work, err
	}
	if work.ClientID != intent.ClientID || work.NodeID != intent.NodeID || work.InstallationID != intent.InstallationID || work.RuntimeID != intent.RuntimeID {
		return work, controlplane.ErrScope
	}
	return work, nil
}

func fleetPlacementOmissionApplied(tx *gorm.DB, node model.FleetNode, intent model.FleetPlacementIntent) (bool, error) {
	if node.AppliedRevision != node.DesiredRevision || node.AppliedRevision < intent.AppliedStopRevision || intent.MaterializedPlacementID == nil {
		return false, nil
	}
	var compilationIDs []string
	if err := tx.Model(&model.FleetNetworkPublication{}).Where("node_id = ? AND deployment_revision = ?", node.ID, node.AppliedRevision).
		Distinct("compilation_id").Order("compilation_id").Limit(2).Pluck("compilation_id", &compilationIDs).Error; err != nil {
		return false, err
	}
	if len(compilationIDs) != 1 {
		return false, nil
	}
	var compilation model.FleetNetworkCompilation
	if err := tx.Select("id,node_id,installation_id").First(&compilation, "id = ?", compilationIDs[0]).Error; err != nil {
		return false, err
	}
	if compilation.NodeID != node.ID || compilation.InstallationID != node.InstallationID {
		return false, controlplane.ErrRecovery
	}
	known, omitted, err := indexedFleetPlacementOmission(tx, node, intent)
	if err != nil || known {
		return omitted, err
	}
	if err := tx.Select("id,node_id,installation_id,manifest,runtime_definitions").First(&compilation, "id = ?", compilationIDs[0]).Error; err != nil {
		return false, err
	}
	var manifest FleetNetworkManifest
	if len(compilation.Manifest) > 32<<20 || len(compilation.RuntimeDefinitions) > 32<<20 || json.Unmarshal([]byte(compilation.Manifest), &manifest) != nil || manifest.NodeID != node.ID || manifest.InstallationID != node.InstallationID {
		return false, controlplane.ErrRecovery
	}
	retired := false
	for _, identity := range manifest.Identities {
		if identity.PlacementID == *intent.MaterializedPlacementID && identity.ClientID == intent.ClientID && identity.RuntimeID == intent.RuntimeID && identity.State == "retired" {
			retired = true
		}
	}
	if !retired {
		return false, nil
	}
	for _, pin := range manifest.Placements {
		if pin.PlacementID == *intent.MaterializedPlacementID {
			return false, nil
		}
	}
	var deployment model.FleetDeployment
	if err := tx.First(&deployment, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
		return false, err
	}
	digest := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(digest[:], deployment.Digest) {
		return false, controlplane.ErrRecovery
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return false, err
	}
	var definitions []nodeconfig.RuntimeDefinition
	if json.Unmarshal([]byte(compilation.RuntimeDefinitions), &definitions) != nil {
		return false, controlplane.ErrRecovery
	}
	var definition *nodeconfig.RuntimeDefinition
	for i := range definitions {
		if definitions[i].ID == intent.RuntimeID {
			if definition != nil || definitions[i].Core != intent.CoreID || slices.Contains(definitions[i].ClientIDs, intent.ClientID) {
				return false, nil
			}
			definition = &definitions[i]
		}
	}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID != intent.RuntimeID {
			continue
		}
		if definition == nil || runtime.Core != intent.CoreID {
			return false, nil
		}
		actual, _, err := canonicalFleetArtifact(runtime.Configuration, 32<<20)
		if err != nil {
			return false, err
		}
		compiled, _, err := canonicalFleetArtifact(definition.Configuration, 32<<20)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(actual, compiled) {
			return false, nil
		}
		for _, admission := range runtime.Admissions {
			if admission.ClientID == intent.ClientID {
				return false, nil
			}
		}
		for _, grant := range runtime.Grants {
			if grant.ClientID == intent.ClientID {
				return false, nil
			}
		}
		return true, nil
	}
	return definition == nil, nil
}

func indexedFleetPlacementOmission(tx *gorm.DB, node model.FleetNode, intent model.FleetPlacementIntent) (bool, bool, error) {
	if !nodeadmission.SupportsRuntime(intent.CoreID) || node.ID != intent.NodeID || node.InstallationID != intent.InstallationID || node.DesiredRevision != node.AppliedRevision || node.AppliedRevision <= 0 {
		return false, false, nil
	}
	var runtime model.FleetPublishedRuntime
	query := tx.Where("node_id = ? AND revision = ? AND runtime_id = ?", node.ID, node.AppliedRevision, intent.RuntimeID).Find(&runtime)
	if query.Error != nil || query.RowsAffected == 0 {
		return false, false, query.Error
	}
	var deployment model.FleetDeployment
	if err := tx.Select("node_id,revision,digest").First(&deployment, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
		return false, false, err
	}
	if runtime.CoreID != intent.CoreID || len(runtime.PayloadDigest) != sha256.Size || !bytes.Equal(runtime.DeploymentDigest, deployment.Digest) {
		return false, false, controlplane.ErrRecovery
	}
	if !runtime.GrantScopesComplete {
		return false, false, nil
	}
	var client model.FleetPublishedClientAdmission
	err := tx.Select("client_id").Where("node_id = ? AND revision = ? AND runtime_id = ? AND client_id = ?", node.ID, node.AppliedRevision, intent.RuntimeID, intent.ClientID).Take(&client).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, true, nil
	}
	return true, false, err
}
