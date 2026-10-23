package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

type fleetNativeCreditPublication struct {
	pins       map[string]FleetNetworkPlacementPin
	identities map[string]FleetNetworkAuthorityIdentity
	grants     map[string]governor.Grant
}

func currentFleetCreditPublication(tx *gorm.DB, snapshot fleetClientSnapshot, placement model.FleetPlacement, base int64, grant model.FleetGrant, native map[int64]fleetNativeCreditPublication) (bool, error) {
	if base <= 0 || snapshot.Term == nil {
		return false, nil
	}
	if !nodeadmission.SupportsRuntime(placement.CoreID) {
		published, exists := native[base]
		if !exists {
			var err error
			published, err = loadFleetNativeCreditPublication(tx, placement, base)
			if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, controlplane.ErrScope) {
				native[base] = fleetNativeCreditPublication{}
				return false, nil
			}
			if err != nil {
				return false, err
			}
			native[base] = published
		}
		pin, identity, baseline := published.pins[placement.ID], published.identities[placement.ID], published.grants[grant.ID]
		return pin.Owned && pin.PlacementID == placement.ID && pin.ClientID == snapshot.Fleet.ID && pin.Revision == uint64(placement.Revision) && pin.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration) &&
			pin.TermID == snapshot.Term.ID && pin.PolicySHA256 == hex.EncodeToString(snapshot.PolicyDigest) && pin.CredentialSHA256 == hex.EncodeToString(snapshot.CredentialDigest) && pin.DefinitionSHA256 == hex.EncodeToString(snapshot.DefinitionDigest) &&
			identity.PlacementID == placement.ID && identity.ClientID == snapshot.Fleet.ID && identity.RuntimeID == placement.RuntimeID && identity.TermID == snapshot.Term.ID &&
			(identity.State == "active" || identity.State == "authority_required") && baseline.ID == grant.ID && baseline.ClientID == grant.ClientID && baseline.Policy.Enabled &&
			baseline.Policy.Term == grant.TermID && !baseline.Policy.QuotaUnlimited && baseline.Policy.CreditCeiling <= uint64(grant.IssuedBytes), nil
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(tx, placement.NodeID, uint64(base), placement.RuntimeID, snapshot.Fleet.ID)
	if errors.Is(err, controlplane.ErrScope) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if scope.CoreID != placement.CoreID || scope.Descriptor.PlacementID != placement.ID || scope.Descriptor.PlacementRevision != uint64(placement.Revision) || scope.Descriptor.CredentialGeneration != uint64(snapshot.Fleet.CredentialGeneration) {
		return false, nil
	}
	if baseline := scope.BaseGrant; baseline != nil {
		return baseline.ID == grant.ID && baseline.ClientID == grant.ClientID && baseline.Policy.Term == grant.TermID && baseline.Policy.Enabled && !baseline.Policy.QuotaUnlimited && baseline.Policy.CreditCeiling <= uint64(grant.IssuedBytes), nil
	}
	var row model.FleetAuthorityUpdate
	found := tx.Where("grant_id = ? AND base_configuration_revision = ?", grant.ID, base).Find(&row)
	if found.Error != nil || found.RowsAffected == 0 {
		return false, found.Error
	}
	message := new(nodev1.AuthorityUpdate)
	digest := sha256.Sum256(row.Payload)
	if !bytes.Equal(digest[:], row.PayloadSHA256) || proto.Unmarshal(row.Payload, message) != nil {
		return false, controlplane.ErrRecovery
	}
	if _, err := authority.AuthorityPolicy(uint64(base), message); err != nil {
		return false, controlplane.ErrRecovery
	}
	return !row.DeliveryBlocked && row.AcknowledgedRevision == row.Revision && bytes.Equal(row.AcknowledgedSHA256, row.PayloadSHA256) &&
		row.NodeID == placement.NodeID && row.InstallationID == placement.InstallationID && row.RuntimeID == placement.RuntimeID && row.PlacementID == placement.ID &&
		message.Enabled && !message.QuotaUnlimited && message.GrantId == grant.ID && message.ClientId == grant.ClientID && message.TermId == grant.TermID &&
		message.PlacementId == placement.ID && message.PlacementRevision == uint64(placement.Revision) && message.CredentialGeneration == uint64(snapshot.Fleet.CredentialGeneration) &&
		bytes.Equal(row.BasePayloadSHA256, scope.PayloadSHA256) && bytes.Equal(message.DescriptorSha256, scope.Descriptor.SHA256) && bytes.Equal(row.DescriptorSHA256, scope.Descriptor.SHA256), nil
}

func loadFleetNativeCreditPublication(tx *gorm.DB, placement model.FleetPlacement, base int64) (fleetNativeCreditPublication, error) {
	result := fleetNativeCreditPublication{pins: map[string]FleetNetworkPlacementPin{}, identities: map[string]FleetNetworkAuthorityIdentity{}, grants: map[string]governor.Grant{}}
	if placement.CoreID != "native" || placement.RuntimeID != "native" {
		return result, controlplane.ErrScope
	}
	var compilationIDs []string
	if err := tx.Model(&model.FleetNetworkPublication{}).Where("node_id = ? AND deployment_revision = ?", placement.NodeID, base).
		Distinct("compilation_id").Limit(2).Pluck("compilation_id", &compilationIDs).Error; err != nil {
		return result, err
	}
	if len(compilationIDs) != 1 {
		return result, controlplane.ErrScope
	}
	var compilation model.FleetNetworkCompilation
	if err := tx.Select("node_id,installation_id,manifest").Where("id = ? AND node_id = ? AND installation_id = ?", compilationIDs[0], placement.NodeID, placement.InstallationID).Take(&compilation).Error; err != nil {
		return result, err
	}
	var manifest FleetNetworkManifest
	if len(compilation.Manifest) > 32<<20 || json.Unmarshal([]byte(compilation.Manifest), &manifest) != nil || manifest.NodeID != placement.NodeID || manifest.InstallationID != placement.InstallationID {
		return result, controlplane.ErrRecovery
	}
	for _, pin := range manifest.Placements {
		if result.pins[pin.PlacementID].PlacementID != "" {
			return result, controlplane.ErrRecovery
		}
		result.pins[pin.PlacementID] = pin
	}
	for _, identity := range manifest.Identities {
		if result.identities[identity.PlacementID].PlacementID != "" {
			return result, controlplane.ErrRecovery
		}
		result.identities[identity.PlacementID] = identity
	}
	var deployment model.FleetDeployment
	if err := tx.First(&deployment, "node_id = ? AND revision = ?", placement.NodeID, base).Error; err != nil {
		return result, err
	}
	digest := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(digest[:], deployment.Digest) {
		return result, controlplane.ErrRecovery
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return result, err
	}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID == placement.RuntimeID && runtime.Core == placement.CoreID {
			for _, grant := range runtime.Grants {
				result.grants[grant.ID] = grant
			}
		}
	}
	return result, nil
}
