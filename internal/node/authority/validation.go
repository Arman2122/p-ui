package authority

import (
	"crypto/sha256"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

var ErrReport = errors.New("invalid forwarding-authority observation")

var runtimeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func ValidID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func ValidRuntime(journal *nodev1.RuntimeJournalIdentity) bool {
	return journal != nil && runtimeName.MatchString(journal.RuntimeId) && runtimeName.MatchString(journal.CoreId) && ValidID(journal.StateJournalId) && ValidID(journal.CreditJournalId) && journal.StateJournalId != journal.CreditJournalId
}

func NormalizeRegistration(request *nodev1.JournalRegistration) (*nodev1.JournalRegistration, []byte, error) {
	if request == nil || !ValidID(request.NodeId) || !ValidID(request.InstallationId) || !ValidID(request.AgentStateJournalId) || len(request.Runtimes) == 0 || len(request.Runtimes) > 32 {
		return nil, nil, ErrReport
	}
	seen := map[string]bool{request.AgentStateJournalId: true}
	names := make(map[string]bool)
	for _, runtime := range request.Runtimes {
		if !ValidRuntime(runtime) || names[runtime.RuntimeId] || seen[runtime.StateJournalId] || seen[runtime.CreditJournalId] {
			return nil, nil, ErrReport
		}
		names[runtime.RuntimeId], seen[runtime.StateJournalId], seen[runtime.CreditJournalId] = true, true, true
	}
	normalized := proto.Clone(request).(*nodev1.JournalRegistration)
	slices.SortFunc(normalized.Runtimes, func(a, b *nodev1.RuntimeJournalIdentity) int { return strings.Compare(a.RuntimeId, b.RuntimeId) })
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(data)
	return normalized, digest[:], nil
}

func ValidateGrant(grant *nodev1.GrantAuthority) error {
	if grant != nil && grant.AuthorityRevision > math.MaxInt64 {
		return ErrReport
	}
	if grant == nil || !ValidID(grant.GrantId) || !ValidID(grant.ClientId) || !ValidID(grant.TermId) || grant.PolicyRevision == 0 || grant.PolicyRevision > math.MaxInt64 || grant.CreditCeiling > math.MaxInt64 || grant.ReservedHighWater > math.MaxInt64 || grant.AdmissionHighWater > math.MaxInt64 || grant.SpendableBytes > math.MaxInt64 {
		return ErrReport
	}
	if grant.QuotaUnlimited && grant.CreditCeiling != 0 || !grant.AdmissionKnown && grant.AdmissionHighWater != 0 || !grant.SpendableKnown && grant.SpendableBytes != 0 {
		return ErrReport
	}
	if grant.AdmissionKnown && grant.AdmissionHighWater > grant.ReservedHighWater {
		return ErrReport
	}
	if grant.BurnedHighWater > grant.ReservedHighWater || grant.AdmissionKnown && grant.BurnedHighWater != 0 {
		return ErrReport
	}
	if !grant.QuotaUnlimited && grant.ReservedHighWater > grant.CreditCeiling {
		return ErrReport
	}
	if grant.SpendableKnown {
		if grant.QuotaUnlimited || grant.SpendableBytes > grant.CreditCeiling-grant.BurnedHighWater || !grant.AuthorizationActive && grant.SpendableBytes != 0 {
			return ErrReport
		}
		if grant.AdmissionKnown {
			remaining := uint64(0)
			if grant.AuthorizationActive && grant.CreditCeiling > grant.AdmissionHighWater {
				remaining = grant.CreditCeiling - grant.AdmissionHighWater
			}
			if remaining != grant.SpendableBytes {
				return ErrReport
			}
		}
	}
	return nil
}

func ValidateReport(report *nodev1.AuthorityReport, agent bool) error {
	if report == nil || !ValidID(report.NodeId) || !ValidID(report.InstallationId) || agent && !ValidID(report.AgentStateJournalId) || !runtimeName.MatchString(report.RuntimeId) || !ValidID(report.StateJournalId) || !ValidID(report.CreditJournalId) || report.Sequence == 0 || report.Sequence > math.MaxInt64 || report.ObservedUnixMilli <= 0 || len(report.Grants) > MaxEntries {
		return ErrReport
	}
	seen := make(map[string]bool, len(report.Grants))
	for _, grant := range report.Grants {
		if ValidateGrant(grant) != nil || seen[grant.GrantId] {
			return ErrReport
		}
		seen[grant.GrantId] = true
	}
	return nil
}

func ProtoGrant(grant Grant) *nodev1.GrantAuthority {
	return &nodev1.GrantAuthority{GrantId: grant.GrantID, ClientId: grant.ClientID, TermId: grant.TermID, PolicyRevision: grant.PolicyRevision, AuthorityRevision: grant.AuthorityRevision, CreditCeiling: grant.CreditCeiling, QuotaUnlimited: grant.QuotaUnlimited, ReservedHighWater: grant.ReservedHighWater, BurnedHighWater: grant.BurnedHighWater, AdmissionHighWater: grant.AdmissionHighWater, AdmissionKnown: grant.AdmissionKnown, SpendableBytes: grant.SpendableBytes, SpendableKnown: grant.SpendableKnown, AuthorizationActive: grant.AuthorizationActive}
}
