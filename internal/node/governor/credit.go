package governor

import (
	"context"
	"encoding/json"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
)

func (s *Store) ApplyCredit(ctx context.Context, credits []authority.Credit) error {
	if authority.ValidateCredits(credits) != nil {
		return ErrGrant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.View(func(tx *bolt.Tx) error {
		for _, credit := range credits {
			var saved record
			if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(credit.GrantID)), &saved); err != nil {
				return ErrGrant
			}
			if saved.Suspended || saved.Grant.ClientID != credit.ClientID || saved.Grant.Policy.Term != credit.TermID || saved.Grant.Policy.Revision != credit.PolicyRevision || s.accounts[credit.GrantID] == nil {
				return ErrGrant
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, credit := range credits {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.accounts[credit.GrantID].ApplyCredit(credit.TermID, credit.PolicyRevision, credit.CreditRevision, credit.CreditCeiling); err != nil {
			return err
		}
	}
	return nil
}
