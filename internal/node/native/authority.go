package native

import (
	"context"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (c *Controller) AuthorityJournalID() string { return c.journal.JournalID() }

func (c *Controller) Authority(ctx context.Context, after string, limit int) (authority.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	page := authority.Page{Grants: []authority.Grant{}}
	if limit < 1 || limit > authority.MaxEntries || after != "" && !validID(after) {
		return page, ErrAuthority
	}
	stamp, err := authorizationTime(c.clock)
	if err != nil {
		return page, err
	}
	now := stamp.UnixMilli()
	counts, kernelErr := c.plane.Snapshot(ctx)
	network, networkErr := c.journal.Network()
	if networkErr != nil {
		return page, networkErr
	}
	records, recordErr := c.journal.Records()
	if recordErr != nil {
		return page, recordErr
	}
	known := kernelErr == nil && network.Pending == nil
	leases := make(map[string]bool, len(records))
	for _, record := range records {
		if record.Lease != nil {
			leases[sliceName(record.Lease.ID)] = true
		}
	}
	for id := range counts {
		if !leases[id] {
			known = false
		}
	}
	err = c.journal.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(grantsBucket).Cursor()
		key, _ := cursor.First()
		if after != "" {
			key, _ = cursor.Seek([]byte(after))
			if string(key) == after {
				key, _ = cursor.Next()
			}
		}
		for ; key != nil; key, _ = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(page.Grants) == limit {
				page.Next = page.Grants[len(page.Grants)-1].GrantID
				break
			}
			record, err := readGrant(tx, string(key))
			if err != nil {
				return err
			}
			policy := record.Grant.Policy
			active := policy.Enabled && !record.Suspended && record.PendingAuthority == nil && (policy.ExpiresUnixMilli == 0 || policy.ExpiresUnixMilli > now) && (policy.Upload.Mode != limits.Blocked || policy.Download.Mode != limits.Blocked)
			grant := authority.Grant{GrantID: record.Grant.ID, ClientID: record.Grant.ClientID, TermID: policy.Term, PolicyRevision: policy.Revision, AuthorityRevision: policy.AuthorityRevision, CreditCeiling: policy.CreditCeiling, QuotaUnlimited: policy.QuotaUnlimited, ReservedHighWater: record.Reserved, AuthorizationActive: active}
			if record.PendingAuthority != nil {
				grant.CreditCeiling = max(grant.CreditCeiling, record.PendingAuthority.Grant.Policy.CreditCeiling)
			}
			if record.PendingAuthority != nil && record.PendingAuthority.Initial {
				grant.AuthorityRevision = 0
			}
			if known && !record.SelectorsPending && record.PendingAuthority == nil && !policy.QuotaUnlimited {
				grant.SpendableKnown = true
				if active && policy.CreditCeiling > record.Reserved {
					grant.SpendableBytes = policy.CreditCeiling - record.Reserved
				}
				if active && record.Lease != nil {
					count, present := counts[sliceName(record.Lease.ID)]
					if !present || record.Retired || record.Lease.ExpiresUnixMilli <= now || count.Uploaded > record.Lease.Bytes || count.Downloaded > record.Lease.Bytes-count.Uploaded {
						grant.SpendableKnown = false
						grant.SpendableBytes = 0
					} else {
						grant.SpendableBytes += record.Lease.Bytes - count.Uploaded - count.Downloaded
					}
				}
			}
			page.Grants = append(page.Grants, grant)
		}
		return nil
	})
	return page, err
}
