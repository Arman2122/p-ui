package controlplane

import (
	"crypto/sha256"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

// The caller holds the current node and placement locks through its authorization transaction.
func DeployedAdmissionTx(tx *gorm.DB, placement model.FleetPlacement, demand *nodev1.ActivationRequest) (nodeadmission.Descriptor, []byte, error) {
	if demand == nil || tx == nil {
		return nodeadmission.Descriptor{}, nil, ErrInvalid
	}
	if demand.RuntimeId != placement.RuntimeID || demand.ClientId != placement.ClientID || demand.PlacementId != placement.ID {
		return nodeadmission.Descriptor{}, nil, ErrScope
	}
	scope, err := publishedAuthorityMetadataTx(tx, placement.NodeID, demand.DeploymentRevision, placement.RuntimeID, demand.ClientId)
	if err != nil {
		return nodeadmission.Descriptor{}, nil, err
	}
	if scope.CoreID != placement.CoreID || scope.Descriptor.PlacementID != demand.PlacementId || scope.Descriptor.PlacementRevision != demand.PlacementRevision || scope.Descriptor.CredentialGeneration != demand.CredentialGeneration {
		return nodeadmission.Descriptor{}, nil, ErrScope
	}
	var binding model.FleetPublishedAdmissionBinding
	err = tx.Where("node_id = ? AND revision = ? AND runtime_id = ? AND client_id = ? AND inbound_instance_id = ?", placement.NodeID, demand.DeploymentRevision, placement.RuntimeID, demand.ClientId, demand.InboundInstanceId).
		Order("inbound_tag").Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nodeadmission.Descriptor{}, nil, ErrScope
	}
	if err != nil {
		return nodeadmission.Descriptor{}, nil, err
	}
	return nodeadmission.Descriptor{
		ClientID: demand.ClientId, CredentialGeneration: scope.Descriptor.CredentialGeneration, PlacementID: scope.Descriptor.PlacementID,
		PlacementRevision: scope.Descriptor.PlacementRevision, InboundInstanceID: binding.InboundInstanceID, InboundTag: binding.InboundTag,
	}, scope.DeploymentSHA256, nil
}

func validateRuntimeAdmissions(tx *gorm.DB, principal identity.Principal, bundle configuration.Bundle) error {
	var clients, placements []string
	for _, runtime := range bundle.Runtimes {
		if len(runtime.Admissions) != 0 {
			if !nodeadmission.SupportsRuntime(runtime.Core) {
				return ErrScope
			}
			if err := RequireRuntimeJournalTx(tx, principal.NodeID, principal.InstallationID, runtime.ID); err != nil {
				return err
			}
		}
		for _, descriptor := range runtime.Admissions {
			clients = append(clients, descriptor.ClientID)
			placements = append(placements, descriptor.PlacementID)
		}
	}
	slices.Sort(clients)
	clients = slices.Compact(clients)
	slices.Sort(placements)
	placements = slices.Compact(placements)
	byClient := make(map[string]model.FleetClient, len(clients))
	byPlacement := make(map[string]model.FleetPlacement, len(placements))
	instances := make(map[string]map[string]bool, len(placements))
	for offset := 0; offset < len(clients); offset += 1024 {
		chunk := clients[offset:min(offset+1024, len(clients))]
		var rows []model.FleetClient
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrScope
		}
		for _, row := range rows {
			byClient[row.ID] = row
		}
	}
	for offset := 0; offset < len(placements); offset += 1024 {
		chunk := placements[offset:min(offset+1024, len(placements))]
		var rows []model.FleetPlacement
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrScope
		}
		for _, row := range rows {
			byPlacement[row.ID] = row
			instances[row.ID] = map[string]bool{}
		}
		var references []model.FleetPlacementInbound
		if err := tx.Where("placement_id IN ?", chunk).Find(&references).Error; err != nil {
			return err
		}
		for _, reference := range references {
			instances[reference.PlacementID][reference.InstanceID] = reference.PanelInbound != nil
		}
	}
	for _, runtime := range bundle.Runtimes {
		for _, descriptor := range runtime.Admissions {
			client, placement := byClient[descriptor.ClientID], byPlacement[descriptor.PlacementID]
			if !client.Owned || client.PanelClientID == nil || uint64(client.CredentialGeneration) != descriptor.CredentialGeneration ||
				placement.ClientID != client.ID || placement.NodeID != principal.NodeID || placement.InstallationID != principal.InstallationID || placement.RuntimeID != runtime.ID ||
				placement.CoreID != runtime.Core || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || uint64(placement.Revision) != descriptor.PlacementRevision ||
				placement.CredentialGeneration != client.CredentialGeneration || !instances[placement.ID][descriptor.InboundInstanceID] {
				return ErrScope
			}
		}
	}
	return nil
}
