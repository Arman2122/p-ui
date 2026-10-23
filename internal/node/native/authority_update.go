package native

import (
	"context"
	"errors"
	"slices"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type policyPlane interface {
	ReplacePolicy(context.Context, Lease, Lease) error
}

func (c *Controller) validateAuthority(ctx context.Context, assignments []Assignment) (time.Time, error) {
	if !c.recovered || len(assignments) == 0 || len(assignments) > authority.MaxAuthorityUpdates {
		return time.Time{}, ErrBusy
	}
	if err := ValidateAssignments(assignments); err != nil {
		return time.Time{}, err
	}
	now, err := authorizationTime(c.clock)
	if err != nil {
		for _, assignment := range assignments {
			if assignment.Grant.Policy.Enabled {
				return time.Time{}, err
			}
		}
	}
	for _, assignment := range assignments {
		for id, current := range c.wanted {
			if id == assignment.Grant.ID {
				if current.Grant.ClientID != assignment.Grant.ClientID || !slices.Equal(current.Selectors, assignment.Selectors) {
					return time.Time{}, ErrAuthority
				}
				continue
			}
			if current.Grant.ClientID == assignment.Grant.ClientID {
				return time.Time{}, ErrAuthority
			}
			for _, selector := range assignment.Selectors {
				if slices.Contains(current.Selectors, selector) {
					return time.Time{}, ErrOwnership
				}
			}
		}
	}
	err = c.journal.db.View(func(tx *bolt.Tx) error {
		_, _, err := authorityAssignments(ctx, tx, assignments, now)
		return err
	})
	return now, err
}

func (c *Controller) ValidateAuthority(ctx context.Context, assignments []Assignment) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.validateAuthority(ctx, assignments)
	return err
}

func (c *Controller) ApplyAuthority(ctx context.Context, assignments []Assignment) ([]governor.Grant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.validateAuthority(ctx, assignments)
	if err != nil {
		return nil, err
	}
	normalized, err := c.journal.stageAuthority(ctx, assignments, now)
	if err != nil {
		return nil, err
	}
	var applied []governor.Grant
	for _, assignment := range normalized {
		if err := c.finishAuthority(ctx, assignment.Grant.ID); err != nil {
			return applied, err
		}
		applied = append(applied, assignment.Grant)
		c.wanted[assignment.Grant.ID] = Assignment{Grant: assignment.Grant, Selectors: slices.Clone(assignment.Selectors)}
	}
	return applied, nil
}

func (c *Controller) ResumeAuthority(ctx context.Context) ([]governor.Grant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.recovered {
		return nil, ErrBusy
	}
	records, err := c.journal.Records()
	if err != nil {
		return nil, err
	}
	var applied []governor.Grant
	var failures []error
	attempts := 0
	for _, record := range records {
		pending := record.PendingAuthority
		if pending == nil {
			continue
		}
		if attempts == authority.MaxAuthorityUpdates {
			break
		}
		attempts++
		if err := c.finishAuthority(ctx, record.Grant.ID); err != nil {
			failures = append(failures, err)
			continue
		}
		c.wanted[record.Grant.ID] = Assignment{Grant: pending.Grant, Selectors: slices.Clone(pending.Selectors)}
		applied = append(applied, pending.Grant)
	}
	return applied, errors.Join(failures...)
}

func (c *Controller) authorityRecord(id string) (grantRecord, error) {
	var record grantRecord
	err := c.journal.db.View(func(tx *bolt.Tx) error {
		var err error
		record, err = readGrant(tx, id)
		return err
	})
	return record, err
}

func (c *Controller) closeFailedAuthority(lease Lease) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.plane.Stop(ctx, lease)
	if err != nil {
		if plane, ok := c.plane.(interface{ Quarantine(context.Context) error }); ok {
			c.recovered = false
			return errors.Join(err, plane.Quarantine(ctx))
		}
	}
	return err
}

func (c *Controller) finishAuthority(ctx context.Context, id string) (result error) {
	record, err := c.authorityRecord(id)
	if err != nil || record.PendingAuthority == nil {
		return err
	}
	pending := record.PendingAuthority
	defer func() {
		if result != nil && record.Lease != nil {
			result = errors.Join(result, c.closeFailedAuthority(*record.Lease))
		}
	}()
	if !pending.Grant.Policy.Enabled {
		if err := c.retireAuthority(ctx, record); err != nil {
			return err
		}
		return c.journal.commitAuthority(id, pending.Grant)
	}
	now, err := authorizationTime(c.clock)
	if err != nil {
		return err
	}
	if pending.Grant.Policy.ExpiresUnixMilli != 0 && pending.Grant.Policy.ExpiresUnixMilli <= now.UnixMilli() {
		return c.retirePendingAuthority(ctx, record)
	}
	created := false
	if record.Lease == nil && len(pending.Selectors) > 0 {
		lease, err := c.journal.reserve(id, pending.Selectors, MaxSliceBytes, now, true)
		if errors.Is(err, limits.ErrQuota) {
			return c.journal.commitAuthority(id, pending.Grant)
		}
		if err != nil {
			return err
		}
		record.Lease, created = &lease, true
	}
	if record.Lease != nil {
		before, after := *record.Lease, *record.Lease
		after.Grant = pending.Grant
		if created {
			if err := c.plane.Install(ctx, after); err != nil {
				return err
			}
		} else {
			plane, ok := c.plane.(policyPlane)
			if !ok {
				return ErrAuthority
			}
			if err := plane.ReplacePolicy(ctx, before, after); err != nil {
				return err
			}
		}
		if pending.Initial || created {
			if err := c.observe(after, Counters{}, true); err != nil {
				return err
			}
		}
	}
	return c.journal.commitAuthority(id, pending.Grant)
}

func (c *Controller) retirePendingAuthority(ctx context.Context, record grantRecord) error {
	if record.Lease != nil && record.PendingAuthority.Initial {
		if err := c.observe(*record.Lease, Counters{}, true); err != nil {
			return err
		}
	}
	if err := c.retireAuthority(ctx, record); err != nil {
		return err
	}
	return c.journal.commitAuthority(record.Grant.ID, record.PendingAuthority.Grant)
}

func (c *Controller) retireAuthority(ctx context.Context, record grantRecord) error {
	if record.Lease == nil {
		return nil
	}
	lease := *record.Lease
	if !record.Retired {
		count, err := c.plane.Stop(ctx, lease)
		if err != nil {
			return err
		}
		if err := c.observe(lease, count, false); err != nil {
			return err
		}
		if err := c.journal.Finalize(lease.Grant.ID, lease.ID); err != nil {
			return err
		}
	}
	counts, err := c.plane.Snapshot(ctx)
	if err != nil {
		return err
	}
	if _, exists := counts[sliceName(lease.ID)]; exists {
		if err := c.plane.Remove(ctx, lease); err != nil {
			return err
		}
	}
	return c.journal.Forget(lease.Grant.ID, lease.ID)
}
