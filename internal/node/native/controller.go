package native

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type Plane interface {
	Install(context.Context, Lease) error
	Snapshot(context.Context) (map[string]Counters, error)
	Block(context.Context, Lease) (Counters, error)
	Stop(context.Context, Lease) (Counters, error)
	Remove(context.Context, Lease) error
}

type selectorPlane interface {
	ReplaceSelectors(context.Context, Lease, Lease) error
}

type Assignment struct {
	Grant     governor.Grant
	Selectors []Selector
}

func ValidateAssignments(assignments []Assignment) error {
	clients, grants, selectors := make(map[string]bool), make(map[string]bool), make(map[Selector]bool)
	for _, assignment := range assignments {
		grant := assignment.Grant
		if governor.Validate(grant) != nil || len(assignment.Selectors) > 0 && validateSelectors(assignment.Selectors) != nil || clients[grant.ClientID] || grants[grant.ID] {
			return ErrAuthority
		}
		clients[grant.ClientID], grants[grant.ID] = true, true
		for _, rate := range []limits.Rate{grant.Policy.Upload, grant.Policy.Download} {
			if rate.Mode == limits.Limited && (rate.BitsPerSecond < 8 || rate.BurstBytes < 65536) {
				return ErrAuthority
			}
		}
		for _, selector := range assignment.Selectors {
			if selectors[selector] {
				return ErrAuthority
			}
			selectors[selector] = true
		}
	}
	return nil
}

type Controller struct {
	mu        sync.Mutex
	journal   *Journal
	usage     *state.Store
	meter     string
	plane     Plane
	wanted    map[string]Assignment
	recovered bool
	clock     limits.Clock
}

func NewController(journal *Journal, usage *state.Store, meter string, plane Plane) (*Controller, error) {
	if journal == nil || usage == nil || plane == nil {
		return nil, ErrAuthority
	}
	if err := journal.Bind(usage.Identity(), meter); err != nil {
		return nil, err
	}
	if err := usage.BindRuntimeJournal(meter, journal.journalID); err != nil {
		return nil, err
	}
	return &Controller{journal: journal, usage: usage, meter: meter, plane: plane, wanted: make(map[string]Assignment)}, nil
}

func (c *Controller) checkpoint(lease Lease, counters Counters) (*nodev1.UsageCheckpoint, error) {
	if counters.Uploaded > lease.Bytes || counters.Downloaded > lease.Bytes-counters.Uploaded {
		return nil, ErrAuthority
	}
	return &nodev1.UsageCheckpoint{
		ClientId: lease.Grant.ClientID, TermId: lease.Grant.Policy.Term, GrantId: lease.Grant.ID,
		MeterId: c.meter, MeterIncarnation: lease.Grant.ID,
		ObservedUnixMilli: time.Now().UnixMilli(),
	}, nil
}

func (c *Controller) observe(lease Lease, count Counters, prime bool) error {
	identity, err := c.checkpoint(lease, count)
	if err != nil {
		return err
	}
	source := "native:" + lease.Grant.ID
	counter, err := c.usage.LoadCounter(source)
	if err != nil {
		return err
	}
	if prime && counter.Primed() {
		return nil
	}
	if !prime && !counter.Primed() {
		return core.ErrCounterCheckpoint
	}
	readings := map[string]int64{"upload": int64(count.Uploaded), "download": int64(count.Downloaded)}
	_, err = counter.ObserveDurably(lease.ID, readings, func(before, after core.CounterCheckpoint, delta map[string]int64) error {
		var usage []state.MeterDelta
		if delta["upload"] != 0 || delta["download"] != 0 {
			usage = []state.MeterDelta{{Identity: identity, Uploaded: uint64(delta["upload"]), Downloaded: uint64(delta["download"])}}
		}
		return c.usage.CommitCounterUsage(source, before, after, usage)
	})
	return err
}

func (c *Controller) retire(ctx context.Context, r grantRecord, stop bool) error {
	if r.Lease == nil {
		return nil
	}
	lease := *r.Lease
	block := c.plane.Block
	if stop {
		block = c.plane.Stop
	}
	count, err := block(ctx, lease)
	if err != nil && !errors.Is(err, ErrMissing) {
		return err
	}
	present := err == nil
	if !r.Retired {
		if present {
			if err := c.observe(lease, count, false); err != nil {
				return err
			}
		}
		if err := c.journal.Finalize(lease.Grant.ID, lease.ID); err != nil {
			return err
		}
	}
	if present {
		if err := c.plane.Remove(ctx, lease); err != nil {
			return err
		}
	}
	return c.journal.Forget(lease.Grant.ID, lease.ID)
}

func (c *Controller) Recover(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	records, err := c.journal.Records()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.PendingAuthority != nil {
			if err := c.retirePendingAuthority(ctx, r); err != nil {
				return err
			}
			continue
		}
		if err := c.retire(ctx, r, true); err != nil {
			return err
		}
	}
	c.recovered = true
	return nil
}

// Quiesce closes forwarding before changing authenticated address ownership.
// Grants remain valid for a subsequent reconcile on the same installation.
func (c *Controller) Quiesce(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wanted = make(map[string]Assignment)
	records, err := c.journal.Records()
	if err != nil {
		return err
	}
	var failures []error
	for _, r := range records {
		if r.PendingAuthority != nil {
			failures = append(failures, c.retirePendingAuthority(ctx, r))
		} else {
			failures = append(failures, c.retire(ctx, r, true))
		}
	}
	return errors.Join(failures...)
}

func (c *Controller) Reconcile(ctx context.Context, assignments []Assignment) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.recovered {
		return ErrBusy
	}
	if err := ValidateAssignments(assignments); err != nil {
		return err
	}
	now, err := authorizationTime(c.clock)
	if err != nil && len(assignments) != 0 {
		return err
	}
	records, err := c.journal.Records()
	if err != nil {
		return err
	}
	persisted := make(map[string]grantRecord, len(records))
	for _, record := range records {
		if record.PendingAuthority != nil {
			return ErrBusy
		}
		persisted[record.Grant.ID] = record
	}
	assignments = slices.Clone(assignments)
	wanted := make(map[string]Assignment, len(assignments))
	for i, assignment := range assignments {
		if record, ok := persisted[assignment.Grant.ID]; ok {
			assignment = retainAssignmentCredit(assignment, record)
		}
		assignment.Selectors = slices.Clone(assignment.Selectors)
		assignments[i] = assignment
		wanted[assignment.Grant.ID] = assignment
	}
	for _, r := range records {
		assignment, keep := wanted[r.Grant.ID]
		if keep && r.Grant == assignment.Grant && r.Lease != nil && !r.Retired {
			if !r.SelectorsPending && slices.Equal(r.Lease.Selectors, assignment.Selectors) {
				continue
			}
			if plane, ok := c.plane.(selectorPlane); ok && r.Lease.ExpiresUnixMilli > now.UnixMilli() {
				if err := c.replaceSelectors(ctx, plane, *r.Lease, assignment.Selectors); err != nil {
					return err
				}
				continue
			}
		}
		if err := c.retire(ctx, r, true); err != nil {
			return err
		}
		if !keep {
			if err := c.journal.Suspend(r.Grant.ID); err != nil {
				return err
			}
		}
	}
	for _, assignment := range assignments {
		if err := c.journal.Apply(assignment.Grant); err != nil {
			return err
		}
	}
	c.wanted = wanted
	return c.refill(ctx)
}

func (c *Controller) refill(ctx context.Context) error {
	records, err := c.journal.Records()
	if err != nil {
		return err
	}
	for _, r := range records {
		assignment, ok := c.wanted[r.Grant.ID]
		if !ok || r.Suspended || r.PendingAuthority != nil || r.Lease != nil || !r.Grant.Policy.Enabled || len(assignment.Selectors) == 0 {
			continue
		}
		now, err := authorizationTime(c.clock)
		if err != nil {
			return err
		}
		lease, err := c.journal.Reserve(r.Grant.ID, assignment.Selectors, MaxSliceBytes, now)
		if errors.Is(err, limits.ErrQuota) || errors.Is(err, limits.ErrExpired) {
			continue
		}
		if err != nil {
			return err
		}
		if err := c.observe(lease, Counters{}, true); err != nil {
			return err
		}
		if err := c.plane.Install(ctx, lease); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) replaceSelectors(ctx context.Context, plane selectorPlane, before Lease, selectors []Selector) error {
	union := slices.Clone(before.Selectors)
	for _, selector := range selectors {
		if !slices.Contains(union, selector) {
			union = append(union, selector)
		}
	}
	if len(union) > 512 {
		return ErrAuthority
	}
	if err := c.journal.selectors(before.Grant.ID, before.ID, union, true); err != nil {
		return err
	}
	after := before
	after.Selectors = slices.Clone(selectors)
	if err := plane.ReplaceSelectors(ctx, before, after); err != nil {
		return err
	}
	return c.journal.selectors(before.Grant.ID, before.ID, selectors, false)
}

func (c *Controller) Collect(ctx context.Context) (result error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.recovered {
		return ErrBusy
	}
	defer func() {
		if result != nil {
			if plane, ok := c.plane.(interface{ Quarantine(context.Context) error }); ok {
				c.recovered = false
				result = errors.Join(result, plane.Quarantine(ctx))
			}
		}
	}()
	records, err := c.journal.Records()
	if err != nil {
		return err
	}
	counts, err := c.plane.Snapshot(ctx)
	if err != nil {
		return err
	}
	now, clockErr := authorizationTime(c.clock)
	if clockErr != nil {
		if len(c.wanted) == 0 {
			return nil
		}
		return clockErr
	}
	for _, r := range records {
		if r.Lease == nil || r.Retired || r.PendingAuthority != nil {
			continue
		}
		count, present := counts[sliceName(r.Lease.ID)]
		if !present {
			return ErrMissing
		}
		if err := c.observe(*r.Lease, count, false); err != nil {
			for _, r := range records {
				if r.Lease != nil {
					_, _ = c.plane.Block(ctx, *r.Lease)
				}
			}
			return err
		}
	}
	for _, r := range records {
		if r.Lease == nil || r.PendingAuthority != nil {
			continue
		}
		count := counts[sliceName(r.Lease.ID)]
		remaining := r.Lease.Bytes - count.Uploaded - count.Downloaded
		if r.Retired || remaining < min(uint64(65536), r.Lease.Bytes/16+1) || r.Lease.ExpiresUnixMilli <= now.UnixMilli() {
			if err := c.retire(ctx, r, r.Lease.ExpiresUnixMilli <= now.UnixMilli()); err != nil {
				return err
			}
		}
	}
	return c.refill(ctx)
}
