package worker

import (
	"bytes"
	"context"
	"net/http"
	"slices"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type AuthorityBackend interface {
	ValidateAuthority(context.Context, []authority.PolicyUpdate) error
	ApplyAuthority(context.Context, []authority.PolicyUpdate) error
}

type authorityScope struct {
	revision    uint64
	digest      []byte
	descriptors map[string]configuration.DescriptorScope
	grants      map[string]governor.Grant
}

func (s *streamBackend) ValidateAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	return s.governor.ValidateAuthority(ctx, updates)
}

func (s *streamBackend) ApplyAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	return s.governor.ApplyAuthority(ctx, updates)
}

func (scope *authorityScope) validate(update *nodev1.AuthorityUpdate) error {
	claim, ok := scope.descriptors[update.ClientId]
	if !ok || claim.CredentialGeneration != update.CredentialGeneration || claim.PlacementID != update.PlacementId || claim.PlacementRevision != update.PlacementRevision || !bytes.Equal(claim.SHA256, update.DescriptorSha256) {
		return state.ErrIdentity
	}
	if grant, known := scope.grants[update.ClientId]; known && (grant.ID != update.GrantId || grant.Policy.Term != update.TermId || grant.Policy.ExpiresUnixMilli != update.ExpiresUnixMilli || grant.Policy.QuotaUnlimited != update.QuotaUnlimited) {
		return state.ErrIdentity
	}
	return nil
}

func (w *Worker) prepareAuthority(deployment *nodev1.Deployment, runtime *configuration.Runtime) (*configuration.Runtime, *authorityScope, error) {
	if runtime == nil {
		return nil, nil, nil
	}
	if _, ok := w.backend.(AuthorityBackend); !ok {
		return runtime, nil, nil
	}
	descriptors, err := configuration.DescriptorScopes(*runtime)
	if err != nil {
		return nil, nil, err
	}
	scope := &authorityScope{revision: deployment.Revision, digest: bytes.Clone(deployment.PayloadSha256), descriptors: descriptors, grants: make(map[string]governor.Grant, len(runtime.Grants))}
	for _, grant := range runtime.Grants {
		if grant.Policy.AuthorityRevision != 0 && grant.Policy.Revision != deployment.Revision {
			return nil, nil, state.ErrRevision
		}
		scope.grants[grant.ClientID] = grant
	}
	intents, err := w.store.AuthorityIntents(deployment.Revision, deployment.PayloadSha256)
	if err != nil {
		return nil, nil, err
	}
	for _, intent := range intents {
		if intent.RuntimeId != w.id {
			return nil, nil, state.ErrIdentity
		}
		update := intent.Updates[0]
		if err := scope.validate(update); err != nil {
			return nil, nil, err
		}
		policy, err := authority.AuthorityPolicy(deployment.Revision, update)
		if err != nil {
			return nil, nil, err
		}
		if prior, ok := scope.grants[update.ClientId]; ok {
			policy, err = limits.MergeAuthority(prior.Policy, policy)
			if err != nil {
				return nil, nil, err
			}
		}
		scope.grants[update.ClientId] = governor.Grant{ID: update.GrantId, ClientID: update.ClientId, RateRevision: policy.AuthorityRevision, Policy: policy}
	}
	merged := *runtime
	merged.Grants = make([]governor.Grant, 0, len(scope.grants))
	for _, grant := range scope.grants {
		merged.Grants = append(merged.Grants, grant)
	}
	slices.SortFunc(merged.Grants, func(a, b governor.Grant) int { return bytes.Compare([]byte(a.ID), []byte(b.ID)) })
	return &merged, scope, nil
}

func (w *Worker) ApplyAuthority(ctx context.Context, batch *nodev1.AuthorityUpdateBatch) error {
	if err := authority.ValidateAuthorityBatch(batch, false); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.core == "native" {
		return authority.ErrReport
	}
	backend, ok := w.backend.(AuthorityBackend)
	if !ok {
		return authority.ErrReport
	}
	journal, err := w.journals()
	if err != nil {
		return err
	}
	id := w.store.Identity()
	if batch.NodeId != id.NodeID || batch.InstallationId != id.InstallationID || batch.RuntimeId != w.id || batch.StateJournalId != journal.StateJournalId || batch.CreditJournalId != journal.CreditJournalId {
		return state.ErrIdentity
	}
	if err := w.store.RequireAgentJournal(batch.AgentStateJournalId); err != nil {
		return err
	}
	cursors, err := w.store.Cursors()
	if err != nil {
		return err
	}
	if !w.available.Load() || w.applying.Load() || w.restoreFailed.Load() || cursors.Desired != cursors.Applied || cursors.Applied != batch.BaseConfigurationRevision || w.authorityScope == nil || w.authorityScope.revision != batch.BaseConfigurationRevision || !bytes.Equal(w.authorityScope.digest, batch.BasePayloadSha256) {
		return state.ErrRevision
	}
	for _, update := range batch.Updates {
		if err := w.authorityScope.validate(update); err != nil {
			return err
		}
	}
	updates, err := authority.AuthorityPolicies(batch)
	if err != nil {
		return err
	}
	if err := backend.ValidateAuthority(ctx, updates); err != nil {
		return err
	}
	if err := w.store.SaveAuthorityIntents(batch); err != nil {
		return err
	}
	if err := backend.ApplyAuthority(ctx, updates); err != nil {
		return err
	}
	for _, update := range updates {
		w.authorityScope.grants[update.ClientID] = governor.Grant{ID: update.GrantID, ClientID: update.ClientID, RateRevision: update.Policy.AuthorityRevision, Policy: update.Policy}
	}
	return nil
}

func (w *Worker) authorityUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/authority/update", func(response http.ResponseWriter, request *http.Request) {
		batch := new(nodev1.AuthorityUpdateBatch)
		if readMessage(response, request, batch, 1<<20) != nil {
			http.Error(response, "invalid authority update", http.StatusBadRequest)
			return
		}
		if err := w.ApplyAuthority(request.Context(), batch); err != nil {
			http.Error(response, "authority update rejected", http.StatusConflict)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
