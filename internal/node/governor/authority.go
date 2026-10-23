package governor

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (s *Store) AuthorityJournalID() string { return s.journalID }

func (s *Store) Authority(ctx context.Context, after string, limit int) (authority.Page, error) {
	if after != "" && !validID(after) || limit <= 0 || limit > authority.MaxEntries {
		return authority.Page{}, ErrGrant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.clock != nil {
		var err error
		now, err = s.clock.Now()
		if err != nil {
			return authority.Page{}, err
		}
	}
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(grantsBucket).Cursor()
		key, _ := cursor.First()
		if after != "" {
			key, _ = cursor.Seek([]byte(after))
			if bytes.Equal(key, []byte(after)) {
				key, _ = cursor.Next()
			}
		}
		for key != nil && len(ids) <= limit {
			ids = append(ids, string(key))
			key, _ = cursor.Next()
		}
		return nil
	})
	if err != nil {
		return authority.Page{}, err
	}
	page := authority.Page{}
	if len(ids) > limit {
		ids = ids[:limit]
		page.Next = ids[len(ids)-1]
	}
	snapshots := make(map[string]limits.Snapshot, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return authority.Page{}, err
		}
		account := s.accounts[id]
		if account == nil {
			return authority.Page{}, ErrGrant
		}
		snapshots[id] = account.Snapshot()
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		for _, id := range ids {
			var saved record
			if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(id)), &saved); err != nil {
				return err
			}
			snapshot := snapshots[id]
			if saved.Grant.Policy != snapshot.Policy || snapshot.PreviouslyReserved > math.MaxUint64-snapshot.AdmittedUpload || snapshot.PreviouslyReserved+snapshot.AdmittedUpload > math.MaxUint64-snapshot.AdmittedDownload {
				return ErrGrant
			}
			admitted := snapshot.PreviouslyReserved + snapshot.AdmittedUpload + snapshot.AdmittedDownload
			if admitted > saved.Reserved {
				return ErrGrant
			}
			policy := snapshot.Policy
			active := policy.Enabled && !saved.Suspended && !snapshot.Quarantined && (policy.ExpiresUnixMilli == 0 || now.UnixMilli() < policy.ExpiresUnixMilli) && (policy.Upload.Mode != limits.Blocked || policy.Download.Mode != limits.Blocked)
			remaining := uint64(0)
			if active && !policy.QuotaUnlimited && policy.CreditCeiling > admitted {
				remaining = policy.CreditCeiling - admitted
			}
			observation := authority.Grant{GrantID: id, ClientID: saved.Grant.ClientID, TermID: policy.Term, PolicyRevision: policy.Revision, AuthorityRevision: policy.AuthorityRevision, CreditCeiling: policy.CreditCeiling, QuotaUnlimited: policy.QuotaUnlimited, ReservedHighWater: saved.Reserved, BurnedHighWater: snapshot.PreviouslyReserved, SpendableBytes: remaining, SpendableKnown: !policy.QuotaUnlimited, AuthorizationActive: active}
			if snapshot.PreviouslyReserved == 0 {
				observation.AdmissionKnown = true
				observation.AdmissionHighWater = admitted
			}
			page.Grants = append(page.Grants, observation)
		}
		return nil
	})
	return page, err
}
