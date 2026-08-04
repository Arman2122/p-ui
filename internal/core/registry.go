package core

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps a Kind to the bound core that serves it. Registration is
// explicit — see internal/cores/cores.go — never init(): with init() the
// registered set depends on the import graph of whichever main links it, so a
// dropped blank import silently shrinks generated output and gen-check passes.
type Registry struct {
	mu     sync.RWMutex
	byKind map[Kind]*Bound
	cores  []*Bound
}

func NewRegistry() *Registry {
	return &Registry{byKind: make(map[Kind]*Bound)}
}

// Register binds c and claims each of its kinds. A duplicate kind is an error
// rather than a silent overwrite, which is how database/sql treats it and how
// sing-box's registry gets it wrong.
func (r *Registry) Register(c Core) error {
	if c == nil {
		return fmt.Errorf("core: Register(nil)")
	}
	kinds := c.Kinds()
	if len(kinds) == 0 {
		return fmt.Errorf("core: %q registers no kinds", c.Describe().ID)
	}
	bound := Bind(c)

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, kind := range kinds {
		if kind == "" {
			return fmt.Errorf("core: %q registers an empty kind", c.Describe().ID)
		}
		if existing, taken := r.byKind[kind]; taken {
			return fmt.Errorf("core: kind %q already registered by %q", kind, existing.Core.Describe().ID)
		}
	}
	for _, kind := range kinds {
		r.byKind[kind] = bound
	}
	r.cores = append(r.cores, bound)
	return nil
}

// For returns the core serving kind. A false second result means the kind is
// unknown to this build, which callers must treat as "quarantine the inbound",
// never as "delete it".
func (r *Registry) For(kind Kind) (*Bound, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byKind[kind]
	return b, ok
}

// Kinds returns every registered kind, sorted. Deterministic order matters:
// codegen reads it, and an unstable order makes gen-check flap.
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Kind, 0, len(r.byKind))
	for kind := range r.byKind {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Cores returns each registered core once, in registration order.
func (r *Registry) Cores() []*Bound {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Bound, len(r.cores))
	copy(out, r.cores)
	return out
}
