//go:build linux

package nativeruntime

import (
	"context"
	"slices"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/native"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

func (r *Runtime) AuthorityJournalID() string { return r.control.AuthorityJournalID() }
func (r *Runtime) Authority(ctx context.Context, after string, limit int) (authority.Page, error) {
	return r.control.Authority(ctx, after, limit)
}

func (r *Runtime) authorityAssignments(updates []authority.PolicyUpdate) ([]native.Assignment, error) {
	if !r.configured || !r.initialized {
		return nil, native.ErrAuthority
	}
	assignments, err := nativeAuthorityAssignments(updates, r.selectors, r.authentication)
	if err != nil {
		return nil, err
	}
	for i := range assignments {
		for _, binding := range r.ocserv.bindings {
			if binding.ClientID == assignments[i].Grant.ClientID {
				assignments[i].Selectors = append(assignments[i].Selectors, native.Selector{Device: binding.Device, Address: binding.Address})
			}
		}
		sortNativeSelectors(assignments[i].Selectors)
	}
	return assignments, nil
}

func (r *Runtime) ValidateAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	assignments, err := r.authorityAssignments(updates)
	if err != nil {
		return err
	}
	return r.control.ValidateAuthority(ctx, assignments)
}

func (r *Runtime) ApplyAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	assignments, err := r.authorityAssignments(updates)
	if err != nil {
		return err
	}
	applied, err := r.control.ApplyAuthority(ctx, assignments)
	r.cacheAuthority(applied)
	return err
}

func (r *Runtime) cacheAuthority(applied []governor.Grant) {
	if len(applied) == 0 {
		return
	}
	grants, assignments := make(map[string]int, len(r.grants)), make(map[string]int, len(r.assignments))
	for i, grant := range r.grants {
		grants[grant.ClientID] = i
	}
	for i, assignment := range r.assignments {
		assignments[assignment.Grant.ClientID] = i
	}
	for _, grant := range applied {
		r.byClient[grant.ClientID] = grant
		if index, exists := grants[grant.ClientID]; exists {
			r.grants[index] = grant
		} else {
			r.grants = append(r.grants, grant)
		}
		if index, exists := assignments[grant.ClientID]; exists {
			r.assignments[index].Grant = grant
		} else {
			r.assignments = append(r.assignments, native.Assignment{Grant: grant, Selectors: slices.Clone(r.selectors[grant.ClientID])})
		}
	}
	for _, process := range r.openvpn.processes {
		process.auth.Grants(r.grants)
	}
	for _, process := range r.ocserv.processes {
		process.auth.Grants(r.grants)
	}
}

var (
	_ authority.Source        = (*Runtime)(nil)
	_ worker.AuthorityBackend = (*Runtime)(nil)
)
