package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type projectionCache struct {
	revision     uint64
	masterDigest []byte
	hashes       map[string][]byte
}

func (l *LocalRuntimes) appliedProjections() (*projectionCache, error) {
	revision, digest, err := l.store.AppliedBase()
	if err != nil {
		return nil, err
	}
	l.projectionMu.Lock()
	defer l.projectionMu.Unlock()
	if l.projections != nil && l.projections.revision == revision && bytes.Equal(l.projections.masterDigest, digest) {
		return l.projections, nil
	}
	deployment, err := l.store.Desired()
	if err != nil {
		return nil, err
	}
	if deployment == nil || deployment.Revision != revision || !bytes.Equal(deployment.PayloadSha256, digest) {
		return nil, state.ErrRevision
	}
	actual := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(actual[:], digest) {
		return nil, state.ErrDigest
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return nil, err
	}
	cache := &projectionCache{revision: revision, masterDigest: digest, hashes: make(map[string][]byte, len(l.workers))}
	for _, worker := range l.workers {
		own, err := configuration.ProjectBundle(bundle, worker.endpoint.ID, worker.endpoint.Core)
		if err != nil {
			return nil, err
		}
		if len(own.Runtimes) == 0 {
			continue
		}
		encoded, err := json.Marshal(own)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(encoded)
		cache.hashes[worker.endpoint.ID] = hash[:]
	}
	l.projections = cache
	return cache, nil
}

func (l *LocalRuntimes) receiveAuthorityUpdates(ctx context.Context, client nodev1.AuthorityDeliveryServiceClient) error {
	cache, err := l.appliedProjections()
	if errors.Is(err, state.ErrRevision) {
		return nil
	}
	if err != nil {
		return err
	}
	id := l.store.Identity()
	return l.runtimePass(ctx, func(ctx context.Context, worker localRuntime) error {
		projected, configured := cache.hashes[worker.endpoint.ID]
		if worker.endpoint.Core == "native" || !configured {
			return nil
		}
		journal, err := l.bindWorkerJournal(ctx, worker)
		if err != nil {
			return err
		}
		request := &nodev1.AuthorityDeliveryRequest{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: l.store.JournalID(), RuntimeId: worker.endpoint.ID, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId, BaseConfigurationRevision: cache.revision, BasePayloadSha256: bytes.Clone(projected)}
		if err := authority.ValidateAuthorityRequest(request); err != nil {
			return err
		}
		batch, err := client.Fetch(ctx, request)
		if err != nil {
			return err
		}
		if authority.ValidateAuthorityBatch(batch, true) != nil || batch.NodeId != request.NodeId || batch.InstallationId != request.InstallationId || batch.AgentStateJournalId != request.AgentStateJournalId || batch.RuntimeId != request.RuntimeId || batch.StateJournalId != request.StateJournalId || batch.CreditJournalId != request.CreditJournalId || batch.BaseConfigurationRevision != request.BaseConfigurationRevision || !bytes.Equal(batch.BasePayloadSha256, request.BasePayloadSha256) {
			return state.ErrIdentity
		}
		if len(batch.Updates) == 0 {
			return nil
		}
		revision, digest, err := l.store.AppliedBase()
		if err != nil {
			return err
		}
		if revision != cache.revision || !bytes.Equal(digest, cache.masterDigest) {
			return state.ErrRevision
		}
		if err := worker.post(ctx, "/v1/authority/update", batch); err != nil {
			return err
		}
		digest, err = authority.AuthorityDigest(batch)
		if err != nil {
			return err
		}
		receipt, err := client.Acknowledge(ctx, batch)
		if err != nil {
			return err
		}
		if receipt == nil || !bytes.Equal(receipt.PayloadSha256, digest) {
			return authority.ErrReport
		}
		return nil
	})
}

func (a *Agent) AuthorityUpdates(ctx context.Context) error {
	local, ok := a.applier.(*LocalRuntimes)
	if !ok {
		return authority.ErrReport
	}
	connection, err := a.connection()
	if err != nil {
		return err
	}
	defer connection.Close()
	client := nodev1.NewAuthorityDeliveryServiceClient(connection)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := local.receiveAuthorityUpdates(ctx, client); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
