package native

import (
	"context"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (c *Controller) ApplyCredit(ctx context.Context, credits []authority.Credit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.recovered {
		return ErrBusy
	}
	if err := authority.ValidateCredits(credits); err != nil {
		return err
	}
	now, err := authorizationTime(c.clock)
	if err != nil {
		return err
	}
	wanted := make(map[string]Assignment, len(credits))
	err = c.journal.db.Update(func(tx *bolt.Tx) error {
		network, err := readNetwork(tx)
		if err != nil {
			return err
		}
		if network.Pending != nil {
			return ErrBusy
		}
		for _, credit := range credits {
			if err := ctx.Err(); err != nil {
				return err
			}
			assignment, present := c.wanted[credit.GrantID]
			if !present {
				return limits.ErrBlocked
			}
			record, err := readGrant(tx, credit.GrantID)
			if err != nil {
				return err
			}
			policy := record.Grant.Policy
			if record.Grant.ClientID != credit.ClientID || policy.Term != credit.TermID || policy.Revision != credit.PolicyRevision || record.Grant != assignment.Grant {
				return limits.ErrRevision
			}
			if record.Suspended || record.Retired || record.SelectorsPending || record.PendingAuthority != nil || !policy.Enabled || policy.QuotaUnlimited || policy.Upload.Mode == limits.Blocked && policy.Download.Mode == limits.Blocked {
				return limits.ErrBlocked
			}
			if policy.ExpiresUnixMilli != 0 && policy.ExpiresUnixMilli <= now.UnixMilli() {
				return limits.ErrExpired
			}
			policy.CreditRevision, policy.CreditCeiling = credit.CreditRevision, credit.CreditCeiling
			if policy != record.Grant.Policy && !limits.CreditAdvance(record.Grant.Policy, policy) {
				return limits.ErrRevision
			}
			record.Grant.Policy = policy
			if err := saveGrant(tx, record); err != nil {
				return err
			}
			assignment.Grant = record.Grant
			wanted[credit.GrantID] = assignment
		}
		return nil
	})
	if err != nil {
		return err
	}
	for id, assignment := range wanted {
		c.wanted[id] = assignment
	}
	return c.refill(ctx)
}

func retainAssignmentCredit(assignment Assignment, record grantRecord) Assignment {
	grant := assignment.Grant
	if grant.ID == record.Grant.ID && grant.ClientID == record.Grant.ClientID && grant.RateRevision == record.Grant.RateRevision {
		assignment.Grant.Policy = limits.RetainCredit(grant.Policy, record.Grant.Policy)
	}
	return assignment
}
