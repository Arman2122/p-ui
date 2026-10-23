package native

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type authorityChange struct {
	Grant     governor.Grant
	Selectors []Selector
	Initial   bool
}

func authorityAssignment(tx *bolt.Tx, assignment Assignment, now time.Time) (Assignment, bool, error) {
	grant := assignment.Grant
	if grant.Policy.AuthorityRevision == 0 || grant.Policy.CreditRevision == 0 {
		return Assignment{}, false, limits.ErrRevision
	}
	if tx.Bucket(grantsBucket).Get([]byte(grant.ID)) == nil {
		if grant.Policy.Enabled && grant.Policy.ExpiresUnixMilli != 0 && grant.Policy.ExpiresUnixMilli <= now.UnixMilli() {
			return Assignment{}, false, limits.ErrExpired
		}
		if data := tx.Bucket(clientsBucket).Get([]byte(grant.ClientID)); data != nil {
			var client clientRecord
			if json.Unmarshal(data, &client) != nil || client.ActiveGrant != "" || client.Class == 0 || client.Class > MaxClasses {
				return Assignment{}, false, ErrAuthority
			}
		}
		if _, err := limits.MergeAuthority(grant.Policy, grant.Policy); err != nil {
			return Assignment{}, false, err
		}
		return assignment, true, nil
	}
	record, err := readGrant(tx, grant.ID)
	if err != nil {
		return Assignment{}, false, err
	}
	if record.Suspended || record.SelectorsPending || record.Grant.ClientID != grant.ClientID {
		return Assignment{}, false, ErrAuthority
	}
	if record.PendingAuthority != nil {
		pending := record.PendingAuthority
		merged, err := limits.MergeAuthority(pending.Grant.Policy, grant.Policy)
		if err != nil || merged != pending.Grant.Policy || !slices.Equal(pending.Selectors, assignment.Selectors) {
			return Assignment{}, false, ErrBusy
		}
		assignment.Grant = pending.Grant
		return assignment, pending.Initial, nil
	}
	policy, err := limits.MergeAuthority(record.Grant.Policy, grant.Policy)
	if err != nil {
		return Assignment{}, false, err
	}
	assignment.Grant.Policy, assignment.Grant.RateRevision = policy, policy.AuthorityRevision
	if policy != record.Grant.Policy && policy.Enabled && policy.ExpiresUnixMilli != 0 && policy.ExpiresUnixMilli <= now.UnixMilli() {
		return Assignment{}, false, limits.ErrExpired
	}
	if record.Lease != nil && (!slices.Equal(record.Lease.Selectors, assignment.Selectors) || record.Retired) {
		return Assignment{}, false, ErrBusy
	}
	if policy.Enabled && (policy.Upload.Mode != record.Grant.Policy.Upload.Mode || policy.Download.Mode != record.Grant.Policy.Download.Mode) {
		return Assignment{}, false, ErrAuthority
	}
	return assignment, false, nil
}

func validateAuthorityChange(record grantRecord) error {
	pending := record.PendingAuthority
	if pending == nil {
		return nil
	}
	if governor.Validate(pending.Grant) != nil || pending.Grant.ID != record.Grant.ID || pending.Grant.ClientID != record.Grant.ClientID || validateSelectorList(pending.Selectors, MaxSelectors) != nil || record.Suspended || record.SelectorsPending {
		return ErrAuthority
	}
	merged, err := limits.MergeAuthority(record.Grant.Policy, pending.Grant.Policy)
	if err != nil || merged != pending.Grant.Policy || pending.Initial && record.Grant != pending.Grant {
		return ErrAuthority
	}
	return nil
}

func authorityAssignments(ctx context.Context, tx *bolt.Tx, assignments []Assignment, now time.Time) ([]Assignment, []bool, error) {
	network, err := readNetwork(tx)
	if err != nil {
		return nil, nil, err
	}
	if network.Pending != nil {
		return nil, nil, ErrBusy
	}
	normalized, initial := make([]Assignment, len(assignments)), make([]bool, len(assignments))
	for i, assignment := range assignments {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		normalized[i], initial[i], err = authorityAssignment(tx, assignment, now)
		if err != nil {
			return nil, nil, err
		}
	}
	return normalized, initial, nil
}

func (j *Journal) stageAuthority(ctx context.Context, assignments []Assignment, now time.Time) ([]Assignment, error) {
	var normalized []Assignment
	err := j.db.Update(func(tx *bolt.Tx) error {
		var initial []bool
		var err error
		normalized, initial, err = authorityAssignments(ctx, tx, assignments, now)
		if err != nil {
			return err
		}
		for i, assignment := range normalized {
			if initial[i] && tx.Bucket(grantsBucket).Get([]byte(assignment.Grant.ID)) == nil {
				if err := applyGrant(tx, assignment.Grant); err != nil {
					return err
				}
			}
			record, err := readGrant(tx, assignment.Grant.ID)
			if err != nil {
				return err
			}
			if record.PendingAuthority != nil || !initial[i] && record.Grant == assignment.Grant {
				continue
			}
			record.PendingAuthority = &authorityChange{Grant: assignment.Grant, Selectors: slices.Clone(assignment.Selectors), Initial: initial[i]}
			if err := saveGrant(tx, record); err != nil {
				return err
			}
		}
		return nil
	})
	return normalized, err
}

func (j *Journal) commitAuthority(id string, grant governor.Grant) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		record, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if record.PendingAuthority == nil || record.PendingAuthority.Grant != grant || record.Retired {
			return ErrAuthority
		}
		record.Grant, record.PendingAuthority = grant, nil
		if record.Lease != nil {
			record.Lease.Grant = grant
		}
		return saveGrant(tx, record)
	})
}
