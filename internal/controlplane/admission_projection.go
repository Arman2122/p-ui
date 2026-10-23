package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

func indexDeploymentAdmissions(tx *gorm.DB, node model.FleetNode, deployment model.FleetDeployment) error {
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(digest[:], deployment.Digest) {
		return ErrScope
	}
	var existing int64
	if err := tx.Model(&model.FleetPublishedRuntime{}).Where("node_id = ? AND revision = ?", node.ID, deployment.Revision).Count(&existing).Error; err != nil || existing != 0 {
		return err
	}
	var registered []model.FleetRuntimeJournal
	if err := tx.Where("node_id = ? AND installation_id = ?", node.ID, node.InstallationID).Find(&registered).Error; err != nil {
		return err
	}
	runtimes := map[string]configuration.Runtime{}
	for _, runtime := range registered {
		runtimes[runtime.RuntimeID] = configuration.Runtime{ID: runtime.RuntimeID, Core: runtime.CoreID}
	}
	for _, runtime := range bundle.Runtimes {
		runtimes[runtime.ID] = runtime
	}
	ids := make([]string, 0, len(runtimes))
	for id := range runtimes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		runtime := runtimes[id]
		projection, err := configuration.ProjectBundle(bundle, runtime.ID, runtime.Core)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(projection)
		if err != nil {
			return err
		}
		projectedDigest := sha256.Sum256(encoded)
		scopes, err := configuration.DescriptorScopes(runtime)
		if err != nil {
			return err
		}
		complete := true
		for _, grant := range runtime.Grants {
			if _, present := scopes[grant.ClientID]; !present {
				complete = false
			}
		}
		metadata := model.FleetPublishedRuntime{NodeID: node.ID, Revision: deployment.Revision, RuntimeID: id, CoreID: runtime.Core, DeploymentDigest: digest[:], PayloadDigest: projectedDigest[:], GrantScopesComplete: complete}
		if err := tx.Create(&metadata).Error; err != nil {
			return err
		}
		byGrant := make(map[string]governor.Grant, len(runtime.Grants))
		for _, grant := range runtime.Grants {
			byGrant[grant.ClientID] = grant
		}
		var clients []model.FleetPublishedClientAdmission
		for clientID, scope := range scopes {
			row := model.FleetPublishedClientAdmission{
				NodeID: node.ID, Revision: deployment.Revision, RuntimeID: id, ClientID: clientID,
				PlacementID: scope.PlacementID, CredentialGeneration: int64(scope.CredentialGeneration), PlacementRevision: int64(scope.PlacementRevision), DescriptorDigest: scope.SHA256,
			}
			if grant, exists := byGrant[clientID]; exists {
				row.BaseGrant, err = json.Marshal(grant)
				if err != nil {
					return err
				}
			}
			clients = append(clients, row)
		}
		slices.SortFunc(clients, func(a, b model.FleetPublishedClientAdmission) int { return strings.Compare(a.ClientID, b.ClientID) })
		if len(clients) != 0 {
			if err := tx.CreateInBatches(clients, 512).Error; err != nil {
				return err
			}
		}
		var bindings []model.FleetPublishedAdmissionBinding
		for _, descriptor := range runtime.Admissions {
			bindings = append(bindings, model.FleetPublishedAdmissionBinding{
				NodeID: node.ID, Revision: deployment.Revision, RuntimeID: id, ClientID: descriptor.ClientID,
				InboundInstanceID: descriptor.InboundInstanceID, InboundTag: descriptor.InboundTag,
			})
		}
		if len(bindings) != 0 {
			if err := tx.CreateInBatches(bindings, 512).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func pruneAdmissionProjections(tx *gorm.DB, node model.FleetNode) error {
	keep := []int64{node.DesiredRevision, node.AppliedRevision, node.SentRevision}
	for _, table := range []any{&model.FleetPublishedAdmissionBinding{}, &model.FleetPublishedClientAdmission{}, &model.FleetPublishedRuntime{}} {
		if err := tx.Where("node_id = ? AND revision NOT IN ?", node.ID, keep).Delete(table).Error; err != nil {
			return err
		}
	}
	return nil
}
