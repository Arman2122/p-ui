//go:build linux

package native

import (
	"context"
	"slices"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (p *Linux) ReplacePolicy(ctx context.Context, before, after Lease) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if before.Grant.ID != after.Grant.ID || before.Grant.ClientID != after.Grant.ClientID || governor.Validate(after.Grant) != nil || !slices.Equal(before.Selectors, after.Selectors) || !after.Grant.Policy.Enabled || before.Grant.Policy.Upload.Mode != after.Grant.Policy.Upload.Mode || before.Grant.Policy.Download.Mode != after.Grant.Policy.Download.Mode {
		return ErrAuthority
	}
	merged, err := limits.MergeAuthority(before.Grant.Policy, after.Grant.Policy)
	if err != nil || merged != after.Grant.Policy {
		return ErrAuthority
	}
	return p.replaceLease(ctx, before, after, true)
}

var _ policyPlane = (*Linux)(nil)
