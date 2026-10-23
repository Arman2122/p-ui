package agent

import (
	"context"
	"math"
	"sync"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
)

type ActivationSource interface {
	RunActivations(context.Context, func(*nodev1.ActivationRequest) error)
	ResolveActivation(context.Context, *nodev1.ActivationResult)
}

type pendingAdmission struct {
	worker  localRuntime
	journal *nodev1.RuntimeJournalIdentity
	client  string
	expires time.Time
}

func (l *LocalRuntimes) RunActivations(ctx context.Context, send func(*nodev1.ActivationRequest) error) {
	l.admissionMu.Lock()
	l.admissions = make(map[string]pendingAdmission)
	l.admissionMu.Unlock()
	var wg sync.WaitGroup
	for _, worker := range l.workers {
		wg.Go(func() { l.forwardActivations(ctx, worker, send) })
	}
	wg.Wait()
	l.admissionMu.Lock()
	l.admissions = nil
	l.admissionMu.Unlock()
}

func (l *LocalRuntimes) forwardActivations(ctx context.Context, worker localRuntime, send func(*nodev1.ActivationRequest) error) {
	var journal *nodev1.RuntimeJournalIdentity
	for ctx.Err() == nil {
		var err error
		if journal == nil {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			journal, err = l.bindWorkerJournal(call, worker)
			cancel()
		}
		if err == nil {
			call, cancel := context.WithTimeout(ctx, 25*time.Second)
			request := new(nodev1.ActivationRequest)
			var present bool
			present, err = worker.getJournal(call, "/v1/admission", request, 4096, journal)
			cancel()
			if err == nil && present && l.trackAdmission(worker, journal, request) {
				if send(request) != nil {
					return
				}
			}
		}
		if err != nil {
			journal = nil
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func (l *LocalRuntimes) trackAdmission(worker localRuntime, journal *nodev1.RuntimeJournalIdentity, request *nodev1.ActivationRequest) bool {
	if request == nil || request.RuntimeId != worker.endpoint.ID || !admission.ValidID(request.RequestId) || !admission.ValidID(request.ClientId) ||
		!admission.ValidID(request.PlacementId) || !admission.ValidID(request.InboundInstanceId) || request.DeadlineUnixMilli <= 0 ||
		request.CredentialGeneration == 0 || request.CredentialGeneration > math.MaxInt64 || request.PlacementRevision == 0 || request.PlacementRevision > math.MaxInt64 ||
		request.DeploymentRevision == 0 || request.DeploymentRevision > math.MaxInt64 {
		return false
	}
	cursors, err := l.store.Cursors()
	if err != nil || request.DeploymentRevision != cursors.Applied || cursors.Applied != cursors.Desired {
		return false
	}
	l.admissionMu.Lock()
	defer l.admissionMu.Unlock()
	now := time.Now()
	for id, pending := range l.admissions {
		if !now.Before(pending.expires) {
			delete(l.admissions, id)
		}
	}
	if l.admissions == nil || len(l.admissions) >= 1024 {
		return false
	}
	if _, exists := l.admissions[request.RequestId]; exists {
		return false
	}
	l.admissions[request.RequestId] = pendingAdmission{worker: worker, journal: journal, client: request.ClientId, expires: now.Add(5 * time.Second)}
	return true
}

func (l *LocalRuntimes) ResolveActivation(ctx context.Context, result *nodev1.ActivationResult) {
	if result == nil {
		return
	}
	l.admissionMu.Lock()
	pending, ok := l.admissions[result.RequestId]
	if ok && pending.client == result.ClientId {
		delete(l.admissions, result.RequestId)
	} else {
		ok = false
	}
	l.admissionMu.Unlock()
	if !ok || !time.Now().Before(pending.expires) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = pending.worker.postJournal(ctx, "/v1/admission", result, pending.journal)
}
