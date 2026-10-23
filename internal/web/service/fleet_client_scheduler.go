package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func RunFleetCreditScheduler(ctx context.Context, db *gorm.DB) error {
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
			results <- runFleetCreditWorker(ctx, db, store)
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

func runFleetCreditWorker(ctx context.Context, db *gorm.DB, store *controlplane.Store) error {
	for ctx.Err() == nil {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		leases, err := store.ClaimCreditNodes(query, 1)
		cancel()
		if err != nil {
			return err
		}
		if len(leases) == 0 {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err := processFleetCreditNode(ctx, db, store, leases[0]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func processFleetCreditNode(ctx context.Context, db *gorm.DB, store *controlplane.Store, lease model.FleetCreditNodeQueue) error {
	work, cancel := context.WithTimeout(ctx, 2*time.Second)
	candidates, err := store.DueCreditCandidates(work, lease)
	if err == nil {
		for _, candidate := range candidates {
			if work.Err() != nil {
				break
			}
			err = processFleetCreditCandidate(work, db, lease, candidate)
			if transientFleetCandidate(err) && ctx.Err() == nil {
				retry, stop := context.WithTimeout(ctx, 5*time.Second)
				err = store.SettleCreditCandidate(retry, lease, candidate, true)
				stop()
				break
			}
			if err != nil {
				break
			}
		}
	}
	cancel()
	release, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	return errors.Join(err, store.ReleaseCreditNode(release, lease))
}

func transientFleetCandidate(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var state interface{ SQLState() string }
	if !errors.As(err, &state) {
		return false
	}
	switch state.SQLState() {
	case "40001", "40P01", "55P03", "57014":
		return true
	default:
		return false
	}
}

func processFleetCreditCandidate(ctx context.Context, db *gorm.DB, lease model.FleetCreditNodeQueue, candidate model.FleetCreditCandidate) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ?", candidate.GrantID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return store.SettleCreditCandidate(ctx, lease, candidate, false)
			}
			return err
		}
		if grant.PlacementID == nil || grant.NodeID != lease.NodeID {
			return store.SettleCreditCandidate(ctx, lease, candidate, false)
		}
		var placement model.FleetPlacement
		if err := tx.First(&placement, "id = ?", *grant.PlacementID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return store.SettleCreditCandidate(ctx, lease, candidate, false)
			}
			return err
		}
		_, err = RefillCreditTx(ctx, tx, FleetRefillRequest{
			PlacementID: placement.ID, GrantID: grant.ID, TermID: grant.TermID,
			ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
			AuthoritySequence: uint64(candidate.AuthoritySequence), LowWaterBytes: controlplane.CreditRefillLowWaterBytes, TargetBytes: controlplane.CreditRefillTargetBytes,
		})
		retry := errors.Is(err, ErrFleetRefill) || errors.Is(err, controlplane.ErrRecovery) || errors.Is(err, controlplane.ErrDrain)
		if err != nil && !retry && !errors.Is(err, ErrFleetSource) && !errors.Is(err, ErrFleetPhase) && !errors.Is(err, ErrFleetPool) && !errors.Is(err, controlplane.ErrBudget) &&
			!errors.Is(err, controlplane.ErrScope) && !errors.Is(err, controlplane.ErrRevision) && !errors.Is(err, controlplane.ErrConflict) && !errors.Is(err, controlplane.ErrInvalid) &&
			!errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, ErrResellerForbidden) && !errors.Is(err, ErrResellerBudget) {
			return err
		}
		return store.SettleCreditCandidate(ctx, lease, candidate, retry)
	})
}
