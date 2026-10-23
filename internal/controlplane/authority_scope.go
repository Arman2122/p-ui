package controlplane

import (
	"encoding/json"
	"errors"
	"math"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

type PublishedAuthorityScope struct {
	CoreID           string
	PayloadSHA256    []byte
	DeploymentSHA256 []byte
	Descriptor       configuration.DescriptorScope
	Bindings         []admission.Descriptor
	BaseGrant        *governor.Grant
}

// Indexed projection is required; missing scope cannot fall back to repeated full-payload scans.
func PublishedAuthorityScopeTx(tx *gorm.DB, nodeID string, baseRevision uint64, runtimeID, clientID string) (PublishedAuthorityScope, error) {
	scope, err := publishedAuthorityMetadataTx(tx, nodeID, baseRevision, runtimeID, clientID)
	if err != nil || clientID == "" {
		return scope, err
	}
	var rows []model.FleetPublishedAdmissionBinding
	if err := tx.Where("node_id = ? AND revision = ? AND runtime_id = ? AND client_id = ?", nodeID, baseRevision, runtimeID, clientID).
		Order("inbound_instance_id,inbound_tag").Find(&rows).Error; err != nil {
		return scope, err
	}
	if len(rows) == 0 {
		return scope, ErrScope
	}
	for _, row := range rows {
		scope.Bindings = append(scope.Bindings, admission.Descriptor{
			ClientID: clientID, CredentialGeneration: scope.Descriptor.CredentialGeneration, PlacementID: scope.Descriptor.PlacementID,
			PlacementRevision: scope.Descriptor.PlacementRevision, InboundInstanceID: row.InboundInstanceID, InboundTag: row.InboundTag,
		})
	}
	return scope, nil
}

func publishedAuthorityMetadataTx(tx *gorm.DB, nodeID string, baseRevision uint64, runtimeID, clientID string) (PublishedAuthorityScope, error) {
	var scope PublishedAuthorityScope
	if tx == nil || !validID(nodeID) || baseRevision == 0 || baseRevision > math.MaxInt64 || !runtimeName.MatchString(runtimeID) || clientID != "" && !validID(clientID) {
		return scope, ErrInvalid
	}
	var runtime model.FleetPublishedRuntime
	err := tx.Table("fleet_published_runtimes r").Select("r.*").Joins("JOIN fleet_deployments d ON d.node_id = r.node_id AND d.revision = r.revision AND d.digest = r.deployment_digest").
		Where("r.node_id = ? AND r.revision = ? AND r.runtime_id = ?", nodeID, baseRevision, runtimeID).Take(&runtime).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return scope, ErrScope
	}
	if err != nil {
		return scope, err
	}
	scope.CoreID, scope.PayloadSHA256, scope.DeploymentSHA256 = runtime.CoreID, runtime.PayloadDigest, runtime.DeploymentDigest
	if clientID == "" {
		return scope, nil
	}
	var client model.FleetPublishedClientAdmission
	err = tx.Where("node_id = ? AND revision = ? AND runtime_id = ? AND client_id = ?", nodeID, baseRevision, runtimeID, clientID).Take(&client).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return scope, ErrScope
	}
	if err != nil {
		return scope, err
	}
	scope.Descriptor = configuration.DescriptorScope{CredentialGeneration: uint64(client.CredentialGeneration), PlacementID: client.PlacementID, PlacementRevision: uint64(client.PlacementRevision), SHA256: client.DescriptorDigest}
	if len(client.BaseGrant) != 0 {
		scope.BaseGrant = new(governor.Grant)
		if err := json.Unmarshal(client.BaseGrant, scope.BaseGrant); err != nil || governor.Validate(*scope.BaseGrant) != nil || scope.BaseGrant.ClientID != clientID {
			return PublishedAuthorityScope{}, ErrScope
		}
	}
	return scope, nil
}
