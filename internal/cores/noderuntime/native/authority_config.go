package nativeruntime

import (
	"slices"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/native"
)

func nativeAuthorityAssignments(updates []authority.PolicyUpdate, selectors map[string][]native.Selector, proofs map[admission.Subject]*admission.BoundProof) ([]native.Assignment, error) {
	if len(updates) == 0 || len(updates) > authority.MaxAuthorityUpdates {
		return nil, native.ErrAuthority
	}
	current := make(map[string]bool)
	for subject, proof := range proofs {
		if proof == nil || !proof.Current() {
			current[subject.ClientID] = false
			continue
		}
		if _, seen := current[subject.ClientID]; !seen {
			current[subject.ClientID] = true
		}
	}
	assignments := make([]native.Assignment, len(updates))
	for i, update := range updates {
		selected, known := selectors[update.ClientID]
		if !known || !current[update.ClientID] {
			return nil, native.ErrAuthority
		}
		assignments[i] = native.Assignment{Grant: governor.Grant{ID: update.GrantID, ClientID: update.ClientID, RateRevision: update.Policy.AuthorityRevision, Policy: update.Policy}, Selectors: slices.Clone(selected)}
	}
	if err := native.ValidateAssignments(assignments); err != nil {
		return nil, err
	}
	return assignments, nil
}
