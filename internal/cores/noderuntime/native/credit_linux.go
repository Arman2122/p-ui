//go:build linux

package nativeruntime

import (
	"context"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/native"
)

func (r *Runtime) ApplyCredit(ctx context.Context, credits []authority.Credit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.configured {
		return native.ErrAuthority
	}
	if err := r.control.ApplyCredit(ctx, credits); err != nil {
		return err
	}
	applied := make([]governor.Grant, 0, len(credits))
	for _, credit := range credits {
		grant := r.byClient[credit.ClientID]
		grant.Policy.CreditRevision, grant.Policy.CreditCeiling = credit.CreditRevision, credit.CreditCeiling
		applied = append(applied, grant)
	}
	r.cacheAuthority(applied)
	return nil
}
