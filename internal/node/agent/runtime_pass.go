package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type partialRuntimeFailure struct{ error }

func (e partialRuntimeFailure) Unwrap() error { return e.error }

func (l *LocalRuntimes) bindWorkerJournal(ctx context.Context, worker localRuntime) (*nodev1.RuntimeJournalIdentity, error) {
	journal := new(nodev1.RuntimeJournalIdentity)
	present, err := worker.get(ctx, "/v1/journals", journal, 4096)
	if err != nil {
		return nil, err
	}
	if !present || journal.RuntimeId != worker.endpoint.ID || journal.CoreId != worker.endpoint.Core {
		return nil, state.ErrIdentity
	}
	id := l.store.Identity()
	registration, _, err := authority.NormalizeRegistration(&nodev1.JournalRegistration{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: l.store.JournalID(), Runtimes: []*nodev1.RuntimeJournalIdentity{journal}})
	if err != nil {
		return nil, state.ErrIdentity
	}
	if err := l.store.BindRuntimeJournals(registration.Runtimes); err != nil {
		return nil, err
	}
	return journal, nil
}

func (l *LocalRuntimes) runtimePass(ctx context.Context, run func(context.Context, localRuntime) error) error {
	failures := make([]error, len(l.workers))
	parallel := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, worker := range l.workers {
		wg.Go(func() {
			select {
			case parallel <- struct{}{}:
			case <-ctx.Done():
				failures[i] = ctx.Err()
				return
			}
			defer func() { <-parallel }()
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := run(call, worker); err != nil {
				failures[i] = fmt.Errorf("runtime %s: %w", worker.endpoint.ID, err)
			}
		})
	}
	wg.Wait()
	err := errors.Join(failures...)
	if err != nil {
		for _, failure := range failures {
			if failure == nil {
				return partialRuntimeFailure{err}
			}
		}
	}
	return err
}
