package governor

import (
	"context"
	"encoding/json"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (s *Store) ValidateAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateAuthorityLocked(ctx, updates)
}

func (s *Store) validateAuthorityLocked(ctx context.Context, updates []authority.PolicyUpdate) error {
	if len(updates) == 0 || len(updates) > authority.MaxAuthorityUpdates {
		return ErrGrant
	}
	seen, clients := make(map[string]bool, len(updates)), make(map[string]bool, len(updates))
	saved := make([]record, len(updates))
	if err := s.db.View(func(tx *bolt.Tx) error {
		for i, update := range updates {
			if data := tx.Bucket(grantsBucket).Get([]byte(update.GrantID)); data != nil && json.Unmarshal(data, &saved[i]) != nil {
				return ErrGrant
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for i, update := range updates {
		if err := ctx.Err(); err != nil {
			return err
		}
		grant := Grant{ID: update.GrantID, ClientID: update.ClientID, RateRevision: update.Policy.AuthorityRevision, Policy: update.Policy}
		if Validate(grant) != nil || seen[grant.ID] || clients[grant.ClientID] || !grant.Policy.Enabled && (grant.Policy.Upload.Mode != limits.Blocked || grant.Policy.Download.Mode != limits.Blocked) {
			return ErrGrant
		}
		seen[grant.ID], clients[grant.ClientID] = true, true
		if saved[i].Grant.ID != "" {
			if saved[i].Suspended || saved[i].Grant.ClientID != grant.ClientID || saved[i].Grant.Policy.Term != grant.Policy.Term || s.accounts[grant.ID] == nil {
				return ErrGrant
			}
			if err := s.accounts[grant.ID].ValidateAuthority(grant.Policy); err != nil {
				return err
			}
		} else {
			if s.clients[grant.ClientID] != "" {
				return ErrGrant
			}
			account, err := limits.New(grant.Policy)
			if err != nil {
				return err
			}
			account.SetClock(s.clock)
			if err := account.ValidateAuthority(grant.Policy); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ApplyAuthority(ctx context.Context, updates []authority.PolicyUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateAuthorityLocked(ctx, updates); err != nil {
		return err
	}
	for _, update := range updates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if account := s.accounts[update.GrantID]; account != nil {
			if err := account.ApplyAuthority(update.Policy); err != nil {
				return err
			}
		} else {
			record := record{Grant: Grant{ID: update.GrantID, ClientID: update.ClientID, RateRevision: update.Policy.AuthorityRevision, Policy: update.Policy}}
			if err := s.db.Update(func(tx *bolt.Tx) error { return save(tx, record) }); err != nil {
				return err
			}
			if err := s.restore(record); err != nil {
				return err
			}
		}
		if update.Policy.Enabled {
			s.clients[update.ClientID] = update.GrantID
		} else if s.clients[update.ClientID] == update.GrantID {
			delete(s.clients, update.ClientID)
		}
	}
	return nil
}
