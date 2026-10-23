package agent

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/grpc"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

type timeSynchronizer interface {
	SynchronizeTime(context.Context, nodev1.TimeServiceClient) error
}

func (a *Agent) synchronizeTime(ctx context.Context, conn grpc.ClientConnInterface) error {
	if local, ok := a.applier.(timeSynchronizer); ok {
		return local.SynchronizeTime(ctx, nodev1.NewTimeServiceClient(conn))
	}
	return nil
}

func (l *LocalRuntimes) SynchronizeTime(ctx context.Context, master nodev1.TimeServiceClient) error {
	var wg sync.WaitGroup
	results := make(chan error, len(l.workers))
	slots := make(chan struct{}, 4)
	for _, worker := range l.workers {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				results <- ctx.Err()
				return
			}
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(ctx, trustedtime.MaxRoundTrip)
			defer cancel()
			request := new(nodev1.TimeRequest)
			found, err := worker.get(ctx, "/v1/time", request, 1024)
			if err != nil {
				results <- err
				return
			}
			id := l.store.Identity()
			if !found || request.NodeId != id.NodeID || request.InstallationId != id.InstallationID {
				results <- trustedtime.ErrUntrusted
				return
			}
			response, err := master.Synchronize(ctx, request)
			if err != nil {
				results <- err
				return
			}
			if response == nil || response.Nonce != request.Nonce || response.NodeId != id.NodeID || response.InstallationId != id.InstallationID {
				results <- trustedtime.ErrUntrusted
				return
			}
			results <- worker.post(ctx, "/v1/time", response)
		})
	}
	wg.Wait()
	close(results)
	var failures []error
	for err := range results {
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
