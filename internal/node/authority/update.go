package authority

import (
	"crypto/sha256"
	"math"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

const MaxAuthorityUpdates = 128

type PolicyUpdate struct {
	GrantID  string
	ClientID string
	Policy   limits.Policy
}

func ValidateAuthorityRequest(request *nodev1.AuthorityDeliveryRequest) error {
	if request == nil {
		return ErrReport
	}
	if ValidateCreditRequest(&nodev1.CreditDeliveryRequest{NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId, RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId}) != nil || request.BaseConfigurationRevision == 0 || request.BaseConfigurationRevision > math.MaxInt64 || len(request.BasePayloadSha256) != sha256.Size {
		return ErrReport
	}
	return nil
}

func authorityRate(value *nodev1.AuthorityRate) (limits.Rate, error) {
	if value == nil || value.Mode < nodev1.AuthorityRateMode_AUTHORITY_RATE_BLOCKED || value.Mode > nodev1.AuthorityRateMode_AUTHORITY_RATE_UNLIMITED || value.BurstBytes > 1<<20 {
		return limits.Rate{}, ErrReport
	}
	return limits.Rate{Mode: limits.Mode(value.Mode), BitsPerSecond: value.BitsPerSecond, BurstBytes: int(value.BurstBytes)}, nil
}

func AuthorityPolicy(base uint64, update *nodev1.AuthorityUpdate) (limits.Policy, error) {
	if update == nil || base == 0 || base > math.MaxInt64 || !ValidID(update.GrantId) || !ValidID(update.ClientId) || !ValidID(update.TermId) || !ValidID(update.PlacementId) || len(update.DescriptorSha256) != sha256.Size || update.AuthorityRevision == 0 || update.AuthorityRevision > math.MaxInt64 || update.CreditRevision == 0 || update.CreditRevision > math.MaxInt64 || update.CreditCeiling > math.MaxInt64 || update.CredentialGeneration == 0 || update.CredentialGeneration > math.MaxInt64 || update.PlacementRevision == 0 || update.PlacementRevision > math.MaxInt64 {
		return limits.Policy{}, ErrReport
	}
	up, err := authorityRate(update.Upload)
	if err != nil {
		return limits.Policy{}, err
	}
	down, err := authorityRate(update.Download)
	if err != nil {
		return limits.Policy{}, err
	}
	policy := limits.Policy{Term: update.TermId, Revision: base, AuthorityRevision: update.AuthorityRevision, Enabled: update.Enabled, ExpiresUnixMilli: update.ExpiresUnixMilli, QuotaUnlimited: update.QuotaUnlimited, CreditCeiling: update.CreditCeiling, CreditRevision: update.CreditRevision, Upload: up, Download: down}
	if _, err := limits.New(policy); err != nil || !policy.Enabled && (up.Mode != limits.Blocked || down.Mode != limits.Blocked) {
		return limits.Policy{}, ErrReport
	}
	return policy, nil
}

func ValidateAuthorityBatch(batch *nodev1.AuthorityUpdateBatch, allowEmpty bool) error {
	if batch == nil || len(batch.Updates) > MaxAuthorityUpdates {
		return ErrReport
	}
	request := &nodev1.AuthorityDeliveryRequest{NodeId: batch.NodeId, InstallationId: batch.InstallationId, AgentStateJournalId: batch.AgentStateJournalId, RuntimeId: batch.RuntimeId, StateJournalId: batch.StateJournalId, CreditJournalId: batch.CreditJournalId, BaseConfigurationRevision: batch.BaseConfigurationRevision, BasePayloadSha256: batch.BasePayloadSha256}
	if ValidateAuthorityRequest(request) != nil || len(batch.Updates) == 0 && !allowEmpty {
		return ErrReport
	}
	grants, clients := make(map[string]bool, len(batch.Updates)), make(map[string]bool, len(batch.Updates))
	for _, update := range batch.Updates {
		if _, err := AuthorityPolicy(batch.BaseConfigurationRevision, update); err != nil {
			return err
		}
		if grants[update.GrantId] || clients[update.ClientId] {
			return ErrReport
		}
		grants[update.GrantId], clients[update.ClientId] = true, true
	}
	return nil
}

func AuthorityPolicies(batch *nodev1.AuthorityUpdateBatch) ([]PolicyUpdate, error) {
	if err := ValidateAuthorityBatch(batch, false); err != nil {
		return nil, err
	}
	updates := make([]PolicyUpdate, 0, len(batch.Updates))
	for _, update := range batch.Updates {
		policy, err := AuthorityPolicy(batch.BaseConfigurationRevision, update)
		if err != nil {
			return nil, err
		}
		updates = append(updates, PolicyUpdate{GrantID: update.GrantId, ClientID: update.ClientId, Policy: policy})
	}
	return updates, nil
}

func AuthorityDigest(batch *nodev1.AuthorityUpdateBatch) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}
