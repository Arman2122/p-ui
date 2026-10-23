package authority

import (
	"crypto/sha256"
	"math"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func ValidateCreditRequest(request *nodev1.CreditDeliveryRequest) error {
	if request == nil || !ValidID(request.NodeId) || !ValidID(request.InstallationId) || !ValidID(request.AgentStateJournalId) || !runtimeName.MatchString(request.RuntimeId) || !ValidID(request.StateJournalId) || !ValidID(request.CreditJournalId) || request.StateJournalId == request.CreditJournalId || request.AgentStateJournalId == request.StateJournalId || request.AgentStateJournalId == request.CreditJournalId {
		return ErrReport
	}
	return nil
}

func ValidateCredits(credits []Credit) error {
	if len(credits) == 0 || len(credits) > MaxCreditUpdates {
		return ErrReport
	}
	seen := make(map[string]bool, len(credits))
	for _, credit := range credits {
		if !ValidID(credit.GrantID) || !ValidID(credit.ClientID) || !ValidID(credit.TermID) || credit.PolicyRevision == 0 || credit.PolicyRevision > math.MaxInt64 || credit.CreditRevision == 0 || credit.CreditRevision > math.MaxInt64 || credit.CreditCeiling == 0 || credit.CreditCeiling > math.MaxInt64 || seen[credit.GrantID] {
			return ErrReport
		}
		seen[credit.GrantID] = true
	}
	return nil
}

func Credits(batch *nodev1.CreditUpdateBatch) []Credit {
	credits := make([]Credit, 0, len(batch.Updates))
	for _, credit := range batch.Updates {
		if credit == nil {
			return nil
		}
		credits = append(credits, Credit{GrantID: credit.GrantId, ClientID: credit.ClientId, TermID: credit.TermId, PolicyRevision: batch.BaseConfigurationRevision, CreditRevision: credit.CreditRevision, CreditCeiling: credit.CreditCeiling})
	}
	return credits
}

func ValidateCreditBatch(batch *nodev1.CreditUpdateBatch, allowEmpty bool) error {
	if batch == nil {
		return ErrReport
	}
	if err := ValidateCreditRequest(&nodev1.CreditDeliveryRequest{NodeId: batch.NodeId, InstallationId: batch.InstallationId, AgentStateJournalId: batch.AgentStateJournalId, RuntimeId: batch.RuntimeId, StateJournalId: batch.StateJournalId, CreditJournalId: batch.CreditJournalId}); err != nil {
		return err
	}
	if len(batch.Updates) == 0 && allowEmpty && batch.BaseConfigurationRevision == 0 {
		return nil
	}
	return ValidateCredits(Credits(batch))
}

func CreditDigest(batch *nodev1.CreditUpdateBatch) ([]byte, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	return digest[:], nil
}
