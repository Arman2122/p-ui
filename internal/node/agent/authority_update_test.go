package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type lostAuthorityReceipt struct {
	mu                             sync.Mutex
	batch                          *nodev1.AuthorityUpdateBatch
	last                           *nodev1.AuthorityDeliveryRequest
	acknowledged, dropBeforeCommit bool
	calls, fetches                 int
}

func (c *lostAuthorityReceipt) Fetch(_ context.Context, request *nodev1.AuthorityDeliveryRequest, _ ...grpc.CallOption) (*nodev1.AuthorityUpdateBatch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetches++
	if request.RuntimeId != c.batch.RuntimeId {
		return &nodev1.AuthorityUpdateBatch{NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId, RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId, BaseConfigurationRevision: request.BaseConfigurationRevision, BasePayloadSha256: bytes.Clone(request.BasePayloadSha256)}, nil
	}
	c.last = proto.Clone(request).(*nodev1.AuthorityDeliveryRequest)
	batch := proto.Clone(c.batch).(*nodev1.AuthorityUpdateBatch)
	if c.acknowledged {
		batch.Updates = nil
	}
	return batch, nil
}

func (c *lostAuthorityReceipt) Acknowledge(_ context.Context, batch *nodev1.AuthorityUpdateBatch, _ ...grpc.CallOption) (*nodev1.AuthorityUpdateReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if !proto.Equal(batch, c.batch) {
		return nil, authority.ErrReport
	}
	if c.calls != 1 || !c.dropBeforeCommit {
		c.acknowledged = true
	}
	if c.calls == 1 {
		return nil, context.DeadlineExceeded
	}
	digest, err := authority.AuthorityDigest(batch)
	return &nodev1.AuthorityUpdateReceipt{PayloadSha256: digest}, err
}

func TestAuthorityDeliveryRecoversLostACKAndFencesProjectedCache(t *testing.T) {
	for _, dropBefore := range []bool{false, true} {
		name := "lost response"
		if dropBefore {
			name = "lost request"
		}
		t.Run(name, func(t *testing.T) {
			id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
			open := func() *state.Store {
				t.Helper()
				store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), id)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				return store
			}
			agentStore, runtimeStore := open(), open()
			credit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer credit.Close()
			engine := new(journalEngine)
			process, err := worker.New(t.Context(), "xray", "xray", engine, runtimeStore, credit)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(process.Handler())
			defer server.Close()
			client := server.Client()
			client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
			local := &LocalRuntimes{store: agentStore, workers: []localRuntime{{endpoint: RuntimeEndpoint{ID: "xray", Core: "xray"}, client: client}}}
			sshStore := open()
			sshCredit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sshCredit.Close()
			sshWorker, err := worker.New(t.Context(), "ssh", "ssh", new(journalEngine), sshStore, sshCredit)
			if err != nil {
				t.Fatal(err)
			}
			var failedSSH atomic.Bool
			sshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failedSSH.Load() {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				sshWorker.Handler().ServeHTTP(w, r)
			}))
			defer sshServer.Close()
			sshClient := sshServer.Client()
			sshClient.Transport = runtimeTestTransport{target: sshServer.URL, base: sshClient.Transport}
			local.workers = append([]localRuntime{{endpoint: RuntimeEndpoint{ID: "ssh", Core: "ssh"}, client: sshClient}}, local.workers...)
			descriptor := admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 7, PlacementID: uuid.NewString(), PlacementRevision: 11, InboundInstanceID: uuid.NewString(), InboundTag: "inbound"}
			runtime := configuration.Runtime{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Admissions: []admission.Descriptor{descriptor}}
			deploy := func(revision uint64) *nodev1.Deployment {
				t.Helper()
				payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "ssh", Core: "ssh", Configuration: json.RawMessage(`{}`)}, runtime}})
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(payload)
				return &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: revision, Payload: payload, PayloadSha256: digest[:]}
			}
			deployment := deploy(1)
			if err := agentStore.Receive(deployment); err != nil {
				t.Fatal(err)
			}
			if err := local.Apply(t.Context(), deployment); err != nil {
				t.Fatal(err)
			}
			if err := agentStore.Applied(1, 1, deployment.PayloadSha256); err != nil {
				t.Fatal(err)
			}
			private, err := runtimeStore.Desired()
			if err != nil {
				t.Fatal(err)
			}
			scopes, err := configuration.DescriptorScopes(runtime)
			if err != nil {
				t.Fatal(err)
			}
			update := &nodev1.AuthorityUpdate{GrantId: uuid.NewString(), ClientId: descriptor.ClientID, TermId: uuid.NewString(), AuthorityRevision: 1, CreditRevision: 1, CreditCeiling: 1000, Enabled: true, Upload: &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_UNLIMITED}, Download: &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_UNLIMITED}, CredentialGeneration: 7, PlacementId: descriptor.PlacementID, PlacementRevision: 11, DescriptorSha256: scopes[descriptor.ClientID].SHA256}
			master := &lostAuthorityReceipt{dropBeforeCommit: dropBefore, batch: &nodev1.AuthorityUpdateBatch{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: agentStore.JournalID(), RuntimeId: "xray", StateJournalId: runtimeStore.JournalID(), CreditJournalId: credit.JournalID(), BaseConfigurationRevision: 1, BasePayloadSha256: private.PayloadSha256, Updates: []*nodev1.AuthorityUpdate{update}}}
			if err := local.receiveAuthorityUpdates(t.Context(), master); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost authority ACK=%v", err)
			}
			if err := local.receiveAuthorityUpdates(t.Context(), master); err != nil {
				t.Fatal(err)
			}
			account, ok := credit.Lookup(descriptor.ClientID)
			if !ok || !master.acknowledged || engine.applications.Load() != 1 {
				t.Fatal("authority ACK replay rebuilt core or failed to apply")
			}
			if err := account.Take(t.Context(), limits.Upload, 500); err != nil {
				t.Fatal(err)
			}
			local.projectionMu.Lock()
			local.projections = nil
			local.projectionMu.Unlock()
			if err := local.receiveAuthorityUpdates(t.Context(), master); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(master.last.BasePayloadSha256, private.PayloadSha256) {
				t.Fatal("cold authority cache lost projected hash")
			}
			runtime.Configuration = json.RawMessage(`{"changed":true}`)
			current := account.Snapshot().Policy
			current.Revision = 2
			runtime.Grants = []governor.Grant{{ID: update.GrantId, ClientID: update.ClientId, RateRevision: current.AuthorityRevision, Policy: current}}
			next := deploy(2)
			if err := agentStore.Receive(next); err != nil {
				t.Fatal(err)
			}
			fetches := master.fetches
			if err := local.receiveAuthorityUpdates(t.Context(), master); err != nil || master.fetches != fetches {
				t.Fatalf("pending master config fetched authority: %v", err)
			}
			if err := local.Apply(t.Context(), next); err != nil {
				t.Fatal(err)
			}
			if err := agentStore.Applied(1, 2, next.PayloadSha256); err != nil {
				t.Fatal(err)
			}
			private, err = runtimeStore.Desired()
			if err != nil {
				t.Fatal(err)
			}
			master.batch.BaseConfigurationRevision, master.batch.BasePayloadSha256 = 2, private.PayloadSha256
			update.AuthorityRevision = 2
			update.Upload = &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_LIMITED, BitsPerSecond: 8000, BurstBytes: 100}
			master.acknowledged, master.calls = false, 1
			failedSSH.Store(true)
			var partial partialRuntimeFailure
			if err := local.receiveAuthorityUpdates(t.Context(), master); !errors.As(err, &partial) {
				t.Fatalf("runtime failure isolation=%v", err)
			}
			if account.Snapshot().Policy.AuthorityRevision != 2 || account.Snapshot().AdmittedUpload != 500 || master.last.BaseConfigurationRevision != 2 || !bytes.Equal(master.last.BasePayloadSha256, private.PayloadSha256) || engine.applications.Load() != 2 {
				t.Fatal("failed peer or stale cache blocked healthy incremental authority")
			}
			master.batch.BasePayloadSha256 = bytes.Repeat([]byte{1}, 32)
			local.workers = local.workers[1:]
			if err := local.receiveAuthorityUpdates(t.Context(), master); !errors.Is(err, state.ErrIdentity) {
				t.Fatalf("idle response omitted exact projected scope: %v", err)
			}
		})
	}
}

func TestAuthorityDeliverySkipsInstalledUnconfiguredRuntimes(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	open := func() *state.Store {
		t.Helper()
		s, err := state.Open(filepath.Join(t.TempDir(), "state.db"), id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	agentStore := open()
	local := &LocalRuntimes{store: agentStore}
	var polling atomic.Bool
	var absentCalls atomic.Int32
	var activeJournal *nodev1.RuntimeJournalIdentity
	for _, name := range []string{"mtproto", "native", "singbox", "ssh", "xray"} {
		store := open()
		credit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = credit.Close() })
		process, err := worker.New(t.Context(), name, name, new(journalEngine), store, credit)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if polling.Load() && name != "xray" {
				absentCalls.Add(1)
				http.Error(w, "unconfigured", http.StatusServiceUnavailable)
				return
			}
			process.Handler().ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		client := server.Client()
		client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
		local.workers = append(local.workers, localRuntime{endpoint: RuntimeEndpoint{ID: name, Core: name}, client: client})
		if name == "xray" {
			activeJournal = &nodev1.RuntimeJournalIdentity{RuntimeId: name, CoreId: name, StateJournalId: store.JournalID(), CreditJournalId: credit.JournalID()}
		}
	}
	payload, _ := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`)}}})
	digest := sha256.Sum256(payload)
	d := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := agentStore.Receive(d); err != nil {
		t.Fatal(err)
	}
	if err := local.Apply(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err := agentStore.Applied(1, 1, d.PayloadSha256); err != nil {
		t.Fatal(err)
	}
	projected, err := configuration.Project(payload, "xray", "xray")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(projected)
	master := &lostAuthorityReceipt{acknowledged: true, batch: &nodev1.AuthorityUpdateBatch{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: agentStore.JournalID(), RuntimeId: "xray", StateJournalId: activeJournal.StateJournalId, CreditJournalId: activeJournal.CreditJournalId, BaseConfigurationRevision: 1, BasePayloadSha256: hash[:]}}
	polling.Store(true)
	for i := 1; i <= 2; i++ {
		if err := local.receiveAuthorityUpdates(t.Context(), master); err != nil || master.fetches != i || absentCalls.Load() != 0 {
			t.Fatalf("unconfigured runtime was polled: fetches=%d absent=%d err=%v", master.fetches, absentCalls.Load(), err)
		}
		local.projectionMu.Lock()
		local.projections = nil
		local.projectionMu.Unlock()
	}
}
