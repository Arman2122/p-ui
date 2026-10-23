package service

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

func RunFleetProviderSchedulers(ctx context.Context, db *gorm.DB) error {
	service, err := NewFleetProviderService(ctx, db)
	if err != nil {
		return err
	}
	group, lifecycle := errgroup.WithContext(ctx)
	runners := []func(context.Context) (int, error){
		func(ctx context.Context) (int, error) { return service.RefreshDue(ctx, 1) },
		func(ctx context.Context) (int, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			pages, err := service.FanoutDue(ctx, 4)
			if err != nil {
				return pages, err
			}
			nodes, err := service.WakeApplied(ctx, 32)
			return pages + nodes, err
		},
		func(ctx context.Context) (int, error) { return service.ProcessRollouts(ctx, 1) },
	}
	for _, run := range runners {
		group.Go(func() error {
			for lifecycle.Err() == nil {
				count, err := run(lifecycle)
				if err != nil {
					return err
				}
				if count != 0 {
					continue
				}
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-lifecycle.Done():
					timer.Stop()
					return lifecycle.Err()
				case <-timer.C:
				}
			}
			return lifecycle.Err()
		})
	}
	return group.Wait()
}
