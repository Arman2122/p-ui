package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func projectFleetProviderBindings(tx *gorm.DB, publication model.FleetNetworkPublication, manifest FleetNetworkManifest) error {
	if err := tx.Where("node_id = ? AND installation_id = ? AND deployment_revision = ?", publication.NodeID, manifest.InstallationID, publication.DeploymentRevision).
		Delete(&model.FleetPublishedProviderBinding{}).Error; err != nil {
		return err
	}
	for _, pin := range manifest.Providers {
		digest, err := hex.DecodeString(pin.SHA256)
		if err != nil || len(digest) != 32 {
			return ErrFleetNetwork
		}
		row := model.FleetPublishedProviderBinding{
			NodeID: publication.NodeID, InstallationID: manifest.InstallationID, DeploymentRevision: publication.DeploymentRevision,
			RuntimeID: pin.RuntimeID, ProviderID: pin.ProviderID, CompilationID: publication.CompilationID, ProviderRevision: int64(pin.Revision),
			ConfigurationRevision: int64(pin.ConfigurationRevision), ProviderDigest: digest, FollowLatest: pin.FollowLatest, ScopeKnown: true, Pending: true,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

type fleetProviderBase struct {
	Compilation model.FleetNetworkCompilation
	Manifest    FleetNetworkManifest
	Deployment  model.FleetDeployment
}

func loadFleetProviderBase(tx *gorm.DB, node model.FleetNode) (fleetProviderBase, error) {
	var base fleetProviderBase
	var bindings []model.FleetPublishedProviderBinding
	if err := tx.Where("node_id = ? AND installation_id = ? AND deployment_revision = ?", node.ID, node.InstallationID, node.AppliedRevision).Find(&bindings).Error; err != nil {
		return base, err
	}
	compilationID := ""
	for _, binding := range bindings {
		if compilationID != "" && compilationID != binding.CompilationID {
			return base, ErrFleetNetworkRevision
		}
		compilationID = binding.CompilationID
	}
	if compilationID == "" && node.AppliedRevision == node.DesiredRevision {
		var head model.FleetNodeNetworkHead
		if err := tx.First(&head, "node_id = ? AND installation_id = ?", node.ID, node.InstallationID).Error; err != nil {
			return base, err
		}
		if head.PublishedCompilationID != nil {
			compilationID = *head.PublishedCompilationID
		}
	}
	var publications []model.FleetNetworkPublication
	query := tx.Where("node_id = ? AND deployment_revision = ?", node.ID, node.AppliedRevision)
	if compilationID != "" {
		query = query.Where("compilation_id = ?", compilationID)
	}
	if err := query.Limit(2).Find(&publications).Error; err != nil {
		return base, err
	}
	if len(publications) != 1 {
		return base, ErrFleetNetworkRevision
	}
	if err := tx.First(&base.Compilation, "id = ?", publications[0].CompilationID).Error; err != nil {
		return base, err
	}
	if base.Compilation.NodeID != node.ID || base.Compilation.InstallationID != node.InstallationID || !base.Compilation.Ready ||
		json.Unmarshal([]byte(base.Compilation.Manifest), &base.Manifest) != nil || base.Manifest.NodeID != node.ID || base.Manifest.InstallationID != node.InstallationID {
		return base, ErrFleetNetworkRevision
	}
	if err := tx.First(&base.Deployment, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
		return base, err
	}
	payloadDigest := sha256.Sum256(base.Deployment.Payload)
	if !bytes.Equal(payloadDigest[:], base.Deployment.Digest) {
		return base, ErrFleetNetworkRevision
	}
	if len(bindings) != len(base.Manifest.Providers) {
		return base, ErrFleetNetworkRevision
	}
	for _, pin := range base.Manifest.Providers {
		if !slices.ContainsFunc(bindings, func(row model.FleetPublishedProviderBinding) bool {
			return row.ScopeKnown && row.CompilationID == base.Compilation.ID && row.RuntimeID == pin.RuntimeID && row.ProviderID == pin.ProviderID && row.ProviderRevision == int64(pin.Revision) &&
				row.ConfigurationRevision == int64(pin.ConfigurationRevision) && row.FollowLatest == pin.FollowLatest && hex.EncodeToString(row.ProviderDigest) == pin.SHA256
		}) {
			return base, ErrFleetNetworkRevision
		}
	}
	return base, nil
}

func fleetProviderSourceDigest(manifest FleetNetworkManifest) ([]byte, error) {
	manifest.Providers = nil
	manifest.Version, manifest.BaseDeploymentRevision = 0, 0
	manifest.NodeRevisionID, manifest.DefaultsRevisionID, manifest.NodeRevision = "", "", 0
	manifest.Placements = slices.Clone(manifest.Placements)
	manifest.Identities = slices.Clone(manifest.Identities)
	for i := range manifest.Placements {
		manifest.Placements[i].GraphSHA256, manifest.Placements[i].TermID = "", ""
	}
	for i := range manifest.Identities {
		manifest.Identities[i].TermID, manifest.Identities[i].State = "", ""
	}
	_, digest, err := canonicalFleetArtifact(manifest, 32<<20)
	return digest, err
}

func providerPinKey(pin FleetNetworkProviderPin) string { return pin.RuntimeID + "/" + pin.ProviderID }

func equalProviderPins(a, b []FleetNetworkProviderPin) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	less := func(a, b FleetNetworkProviderPin) int { return strings.Compare(providerPinKey(a), providerPinKey(b)) }
	slices.SortFunc(a, less)
	slices.SortFunc(b, less)
	left, _, err := canonicalFleetArtifact(a, 1<<20)
	if err != nil {
		return false
	}
	right, _, err := canonicalFleetArtifact(b, 1<<20)
	return err == nil && bytes.Equal(left, right)
}
