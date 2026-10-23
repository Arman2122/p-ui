package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type RuntimeEndpoint struct {
	ID     string `json:"id"`
	Core   string `json:"core"`
	Socket string `json:"socket"`
	UID    uint32 `json:"uid"`
}

type localRuntime struct {
	endpoint RuntimeEndpoint
	client   *http.Client
}

type LocalRuntimes struct {
	workers      []localRuntime
	store        *state.Store
	admissionMu  sync.Mutex
	admissions   map[string]pendingAdmission
	projectionMu sync.Mutex
	projections  *projectionCache
}

func NewLocalRuntimes(store *state.Store, endpoints []RuntimeEndpoint) (*LocalRuntimes, error) {
	if store == nil || len(endpoints) == 0 || len(endpoints) > 32 {
		return nil, configuration.ErrConfiguration
	}
	local := &LocalRuntimes{store: store}
	seen := make(map[string]bool)
	for _, endpoint := range endpoints {
		if endpoint.ID == "" || endpoint.Core == "" || seen[endpoint.ID] {
			local.Close()
			return nil, configuration.ErrConfiguration
		}
		seen[endpoint.ID] = true
		client, err := localrpc.Client(endpoint.Socket, endpoint.UID)
		if err != nil {
			local.Close()
			return nil, err
		}
		local.workers = append(local.workers, localRuntime{endpoint: endpoint, client: client})
	}
	slices.SortFunc(local.workers, func(a, b localRuntime) int {
		return bytes.Compare([]byte(a.endpoint.ID), []byte(b.endpoint.ID))
	})
	return local, nil
}

func (l *LocalRuntimes) Close() {
	for _, worker := range l.workers {
		worker.client.CloseIdleConnections()
	}
}

func (l *LocalRuntimes) Apply(ctx context.Context, deployment *nodev1.Deployment) error {
	if deployment == nil {
		return configuration.ErrConfiguration
	}
	masterDigest := sha256.Sum256(deployment.Payload)
	if !bytes.Equal(masterDigest[:], deployment.PayloadSha256) {
		return state.ErrDigest
	}
	journals, err := l.Journals(ctx)
	if err != nil {
		return err
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil {
		return err
	}
	for _, runtime := range bundle.Runtimes {
		if !slices.ContainsFunc(l.workers, func(worker localRuntime) bool {
			return worker.endpoint.ID == runtime.ID && worker.endpoint.Core == runtime.Core
		}) {
			return fmt.Errorf("runtime %s is not installed", runtime.ID)
		}
	}
	projected := make([]*nodev1.Deployment, len(l.workers))
	manifests := make([][]byte, len(l.workers))
	for i, worker := range l.workers {
		own, err := configuration.ProjectBundle(bundle, worker.endpoint.ID, worker.endpoint.Core)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(own)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		projected[i] = &nodev1.Deployment{NodeId: deployment.NodeId, ControlPlaneId: deployment.ControlPlaneId, AuthorityGeneration: deployment.AuthorityGeneration, Revision: deployment.Revision, Payload: payload, PayloadSha256: digest[:]}
		manifests[i] = configuration.AssetManifest(own)
	}
	for _, action := range []string{"validate", "apply"} {
		for i, worker := range l.workers {
			var manifest []byte
			if action == "apply" {
				manifest = manifests[i]
			}
			if err := worker.postVerified(ctx, "/v1/"+action, projected[i], journals[i], manifest, l.store.JournalID()); err != nil {
				return fmt.Errorf("runtime %s %s: %w", worker.endpoint.ID, action, err)
			}
		}
	}
	cache := &projectionCache{revision: deployment.Revision, masterDigest: bytes.Clone(deployment.PayloadSha256), hashes: make(map[string][]byte, len(l.workers))}
	for i, worker := range l.workers {
		if slices.ContainsFunc(bundle.Runtimes, func(runtime configuration.Runtime) bool { return runtime.ID == worker.endpoint.ID }) {
			cache.hashes[worker.endpoint.ID] = bytes.Clone(projected[i].PayloadSha256)
		}
	}
	l.projectionMu.Lock()
	l.projections = cache
	l.projectionMu.Unlock()
	return nil
}

func (w localRuntime) post(ctx context.Context, path string, message proto.Message) error {
	return w.postJournal(ctx, path, message, nil)
}

func (w localRuntime) postJournal(ctx context.Context, path string, message proto.Message, journal *nodev1.RuntimeJournalIdentity) error {
	return w.postVerified(ctx, path, message, journal, nil, "")
}

func (w localRuntime) postVerified(ctx context.Context, path string, message proto.Message, journal *nodev1.RuntimeJournalIdentity, manifest []byte, agentJournal string) error {
	encoded, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://runtime"+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/protobuf")
	if agentJournal != "" {
		request.Header.Set("X-Pui-Agent-Journal", agentJournal)
	}
	if journal != nil {
		request.Header.Set("X-Pui-State-Journal", journal.StateJournalId)
		request.Header.Set("X-Pui-Credit-Journal", journal.CreditJournalId)
	}
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("local runtime returned HTTP %d", response.StatusCode)
	}
	if manifest != nil && response.Header.Get(configuration.AssetManifestHeader) != hex.EncodeToString(manifest) {
		return errors.New("local runtime asset manifest differs from desired configuration")
	}
	return nil
}

func (l *LocalRuntimes) receiveUsage(ctx context.Context, w localRuntime) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://runtime/v1/usage", nil)
	if err != nil {
		return false, err
	}
	response, err := w.client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("runtime usage HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, state.MaxBatchBytes+1))
	if err != nil {
		return false, err
	}
	if len(encoded) > state.MaxBatchBytes {
		return false, state.ErrUsage
	}
	batch := new(nodev1.UsageBatch)
	if err := proto.Unmarshal(encoded, batch); err != nil {
		return false, err
	}
	id := l.store.Identity()
	if batch.NodeId != id.NodeID || batch.InstallationId != id.InstallationID || batch.Sequence == 0 {
		return false, state.ErrIdentity
	}
	for _, checkpoint := range batch.Checkpoints {
		if checkpoint.MeterId != w.endpoint.ID {
			return false, state.ErrIdentity
		}
	}
	if _, err := l.store.AppendUsage(batch.Checkpoints); err != nil {
		return false, err
	}
	return true, w.post(ctx, "/v1/ack", &nodev1.UsageReceipt{DurableSequence: batch.Sequence})
}

func (l *LocalRuntimes) Collect(ctx context.Context, onError func(string, error)) {
	var wg sync.WaitGroup
	var reportMu sync.Mutex
	for _, worker := range l.workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				pending, err := l.receiveUsage(ctx, worker)
				if err != nil && !errors.Is(err, context.Canceled) && onError != nil {
					reportMu.Lock()
					onError(worker.endpoint.ID, err)
					reportMu.Unlock()
				}
				if pending && err == nil {
					continue
				}
				delay := time.Second
				if err != nil {
					delay = 5 * time.Second
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}
