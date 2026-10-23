package service

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func RunFleetAuthoritySchedulers(ctx context.Context, db *gorm.DB) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runners := []func(context.Context, *gorm.DB) error{RunFleetCreditScheduler, RunFleetAdmissionScheduler, RunFleetSourceScheduler, RunFleetResetScheduler, RunFleetPlacementScheduler}
	results := make(chan error, len(runners))
	for _, run := range runners {
		go func() { results <- run(ctx, db) }()
	}
	first := <-results
	cancel()
	for range len(runners) - 1 {
		first = errors.Join(first, <-results)
	}
	return first
}

func RunFleetAdmissionScheduler(ctx context.Context, db *gorm.DB) error {
	store, err := controlplane.NewStore(db)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			results <- runFleetAdmissionWorker(ctx, db, store)
			cancel()
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return ctx.Err()
}

func runFleetAdmissionWorker(ctx context.Context, db *gorm.DB, store *controlplane.Store) error {
	for ctx.Err() == nil {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := store.WakeRateReconciliationNodes(query)
		var leases []model.FleetAdmissionNodeQueue
		if err == nil {
			leases, err = store.ClaimAdmissionNodes(query, 1)
		}
		cancel()
		if err != nil {
			return err
		}
		if len(leases) == 0 {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err := processFleetAdmissionNode(ctx, db, store, leases[0]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func processFleetAdmissionNode(ctx context.Context, db *gorm.DB, store *controlplane.Store, lease model.FleetAdmissionNodeQueue) error {
	work, cancel := context.WithTimeout(ctx, 2*time.Second)
	jobs, err := store.DueRateReconciliations(work, lease)
	if err == nil {
		for _, job := range jobs[:min(4, len(jobs))] {
			err = reconcileFleetAdmissionRate(work, db, lease, job)
			if transientFleetCandidate(err) && ctx.Err() == nil {
				retry, stop := context.WithTimeout(ctx, 2*time.Second)
				err = store.SettleRateReconciliation(retry, lease, job, true)
				stop()
				break
			}
			if err != nil || work.Err() != nil {
				break
			}
		}
	}
	if err == nil {
		var demands []model.FleetAdmissionDemand
		demands, err = store.DueAdmissionDemands(work, lease)
		for _, demand := range demands[:min(8-min(4, len(jobs)), len(demands))] {
			if work.Err() != nil {
				break
			}
			_, err = ProcessFleetAdmissionDemand(work, db, lease, demand)
			if transientFleetCandidate(err) && ctx.Err() == nil {
				retry, stop := context.WithTimeout(ctx, 2*time.Second)
				err = store.SettleAdmissionDemand(retry, lease, demand, true)
				stop()
				break
			}
			if err != nil {
				break
			}
		}
	}
	cancel()
	if transientFleetCandidate(err) && ctx.Err() == nil {
		err = nil
	}
	release, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	return errors.Join(err, store.ReleaseAdmissionNode(release, lease))
}

func ProcessFleetAdmissionDemand(ctx context.Context, db *gorm.DB, lease model.FleetAdmissionNodeQueue, demand model.FleetAdmissionDemand) (FleetAdmissionResult, error) {
	if db == nil || !fleetID(lease.NodeID) || lease.ClaimID == nil || !fleetID(*lease.ClaimID) || demand.NodeID != lease.NodeID || !fleetID(demand.PlacementID) {
		return FleetAdmissionResult{}, controlplane.ErrInvalid
	}
	result, err := WithFleetAdmissionDemandResult(ctx, db, lease, demand, allocateFleetAdmission)
	if err == nil || !settleFleetAdmissionError(err) {
		return result, err
	}
	retry := errors.Is(err, controlplane.ErrRecovery) || errors.Is(err, controlplane.ErrDrain) || errors.Is(err, ErrFleetRefill) || transientFleetCandidate(err)
	result = FleetAdmissionResult{Outcome: FleetAdmissionRejected, Diagnostic: err.Error()}
	if retry {
		result.Outcome = FleetAdmissionDeferred
	}
	return result, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetAdmissionDemand{}).Where(`placement_id = ? AND request_id = ? AND revision = ? AND pending
AND EXISTS (SELECT 1 FROM fleet_admission_node_queues q WHERE q.node_id = fleet_admission_demands.node_id AND q.claim_id = ? AND q.lease_until_unix_milli > ?)`,
			demand.PlacementID, demand.RequestID, demand.Revision, *lease.ClaimID, time.Now().UnixMilli()).
			Updates(map[string]any{"outcome": string(result.Outcome), "diagnostic": result.Diagnostic[:min(len(result.Diagnostic), 1024)]}).Error; err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		return store.SettleAdmissionDemand(ctx, lease, demand, retry)
	})
}

func settleFleetAdmissionError(err error) bool {
	return transientFleetCandidate(err) || errors.Is(err, controlplane.ErrRecovery) || errors.Is(err, controlplane.ErrDrain) || errors.Is(err, ErrFleetRefill) || errors.Is(err, ErrFleetSource) ||
		errors.Is(err, ErrFleetCredentialClaim) || errors.Is(err, ErrFleetSourceCredentials) || errors.Is(err, ErrFleetUsage) ||
		errors.Is(err, ErrFleetPhase) || errors.Is(err, ErrFleetPool) || errors.Is(err, controlplane.ErrBudget) || errors.Is(err, controlplane.ErrScope) ||
		errors.Is(err, controlplane.ErrRevision) || errors.Is(err, controlplane.ErrConflict) || errors.Is(err, controlplane.ErrInvalid) || errors.Is(err, gorm.ErrRecordNotFound) ||
		errors.Is(err, ErrResellerForbidden) || errors.Is(err, ErrResellerBudget)
}

func reconcileFleetAdmissionRate(ctx context.Context, db *gorm.DB, lease model.FleetAdmissionNodeQueue, expected model.FleetRateReconciliation) error {
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ? AND node_id = ?", expected.GrantID, lease.NodeID).Error; err != nil {
			return err
		}
		if grant.PlacementID == nil {
			return ErrFleetSource
		}
		snapshot, placement, _, err := fleetPlacementSnapshot(tx, *grant.PlacementID)
		if err != nil {
			return err
		}
		var job model.FleetRateReconciliation
		if err := tx.First(&job, "grant_id = ? AND pending AND request_id = ? AND demand_revision = ? AND deadline_unix_milli > ?", expected.GrantID, expected.RequestID, expected.DemandRevision, time.Now().UnixMilli()).Error; err != nil {
			return err
		}
		var trigger model.FleetAdmissionDemand
		if err := fleetLiveAdmissionDemands(tx).Where("d.placement_id = ? AND d.request_id = ? AND d.revision = ? AND d.deadline_unix_milli > ?", job.PlacementID, job.RequestID, job.DemandRevision, time.Now().UnixMilli()).Take(&trigger).Error; err != nil {
			return err
		}
		if trigger.ClientID != grant.ClientID || trigger.TermID != grant.TermID || trigger.CredentialGeneration != snapshot.Fleet.CredentialGeneration || !bytes.Equal(trigger.SourcePolicyDigest, snapshot.PolicyDigest) || fleetEntitlementMatches(snapshot) != nil {
			return ErrFleetSource
		}
		var claimed model.FleetAdmissionNodeQueue
		if lease.ClaimID == nil {
			return controlplane.ErrInvalid
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&claimed, "node_id = ? AND claim_id = ? AND lease_until_unix_milli > ?", lease.NodeID, *lease.ClaimID, time.Now().UnixMilli()).Error; err != nil {
			return err
		}
		deadline := min(job.DeadlineUnixMilli, trigger.DeadlineUnixMilli, claimed.LeaseUntilUnixMilli)
		live, cancel := context.WithDeadline(ctx, time.UnixMilli(deadline))
		defer cancel()
		tx = tx.WithContext(live)
		plan, err := fleetAdmissionRates(tx, snapshot)
		if err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", placement.NodeID).Error; err != nil {
			return err
		}
		for _, allocation := range plan.Allocations {
			member := plan.Members[allocation.ID]
			if allocation.ID == grant.ID && member.Eligible {
				if err := applyFleetAdmissionRate(live, tx, store, member, allocation.Next, trigger.RequestID, uint64(node.AppliedRevision)); err != nil {
					return err
				}
			}
		}
		if time.Now().UnixMilli() >= deadline {
			return ErrFleetSource
		}
		return store.SettleRateReconciliation(live, lease, expected, false)
	})
	if err == nil || !settleFleetAdmissionError(err) {
		return err
	}
	store, storeErr := controlplane.NewStore(db)
	if storeErr != nil {
		return storeErr
	}
	return store.SettleRateReconciliation(ctx, lease, expected, transientFleetCandidate(err) || errors.Is(err, controlplane.ErrRecovery))
}
