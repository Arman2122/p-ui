package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type lostCreditReceipt struct {
	batch                          *nodev1.CreditUpdateBatch
	acknowledged, dropBeforeCommit bool
	calls                          int
}

type countingAuthorityReceipt struct{ reports atomic.Int32 }

func (*countingAuthorityReceipt) RegisterJournals(context.Context, *nodev1.JournalRegistration, ...grpc.CallOption) (*nodev1.JournalRegistrationReceipt, error) {
	return nil, authority.ErrReport
}

func (c *countingAuthorityReceipt) Report(_ context.Context, report *nodev1.AuthorityReport, _ ...grpc.CallOption) (*nodev1.AuthorityReceipt, error) {
	if err := authority.ValidateReport(report, true); err != nil {
		return nil, err
	}
	c.reports.Add(1)
	return &nodev1.AuthorityReceipt{RuntimeId: report.RuntimeId, StateJournalId: report.StateJournalId, CreditJournalId: report.CreditJournalId, Sequence: report.Sequence}, nil
}

func (c *lostCreditReceipt) Fetch(context.Context, *nodev1.CreditDeliveryRequest, ...grpc.CallOption) (*nodev1.CreditUpdateBatch, error) {
	batch := proto.Clone(c.batch).(*nodev1.CreditUpdateBatch)
	if c.acknowledged {
		batch.Updates = nil
		batch.BaseConfigurationRevision = 0
	}
	return batch, nil
}

func (c *lostCreditReceipt) Acknowledge(_ context.Context, batch *nodev1.CreditUpdateBatch, _ ...grpc.CallOption) (*nodev1.CreditUpdateReceipt, error) {
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
	digest, err := authority.CreditDigest(batch)
	return &nodev1.CreditUpdateReceipt{PayloadSha256: digest}, err
}

func TestCreditDeliveryRecoversLostRequestOrReceiptWithoutReissuing(t *testing.T) {
	for _, dropRequest := range []bool{false, true} {
		name := "lost response"
		if dropRequest {
			name = "lost request"
		}
		t.Run(name, func(t *testing.T) {
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
			grant := governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, CreditRevision: 1, Enabled: true, CreditCeiling: 1000, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
			payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{grant}}}})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(payload)
			deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
			if err := agentStore.Receive(deployment); err != nil {
				t.Fatal(err)
			}
			if err := agentStore.Applied(1, 1, digest[:]); err != nil {
				t.Fatal(err)
			}
			if err := process.Apply(t.Context(), deployment); err != nil {
				t.Fatal(err)
			}
			if err := runtimeStore.BindAgentJournal(agentStore.JournalID()); err != nil {
				t.Fatal(err)
			}
			account, _ := credit.Lookup(grant.ClientID)
			if err := account.Take(t.Context(), limits.Upload, 1000); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(process.Handler())
			defer server.Close()
			client := server.Client()
			client.Transport = runtimeTestTransport{target: server.URL, base: client.Transport}
			local := &LocalRuntimes{store: agentStore, workers: []localRuntime{{endpoint: RuntimeEndpoint{ID: "xray", Core: "xray"}, client: client}}}
			master := &lostCreditReceipt{dropBeforeCommit: dropRequest, batch: &nodev1.CreditUpdateBatch{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: agentStore.JournalID(), RuntimeId: "xray", StateJournalId: runtimeStore.JournalID(), CreditJournalId: credit.JournalID(), BaseConfigurationRevision: 1, Updates: []*nodev1.CreditUpdate{{GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.Policy.Term, CreditRevision: 2, CreditCeiling: 1500}}}}
			if err := local.receiveCredit(t.Context(), master); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost receipt=%v", err)
			}
			if err := local.receiveCredit(t.Context(), master); err != nil {
				t.Fatal(err)
			}
			if err := account.Take(t.Context(), limits.Download, 500); err != nil {
				t.Fatal(err)
			}
			if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
				t.Fatalf("retry multiplied credit: %v", err)
			}
			if engine.applications.Load() != 1 || !master.acknowledged {
				t.Fatal("credit replay restarted core or never acknowledged")
			}
			failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer failed.Close()
			failedClient := failed.Client()
			failedClient.Transport = runtimeTestTransport{target: failed.URL, base: failedClient.Transport}
			local.workers = append([]localRuntime{{endpoint: RuntimeEndpoint{ID: "failed", Core: "ssh"}, client: failedClient}}, local.workers...)
			master.batch.Updates[0].CreditRevision = 3
			master.batch.Updates[0].CreditCeiling = 2000
			master.calls = 1
			master.acknowledged = false
			var partial partialRuntimeFailure
			if err := local.receiveCredit(t.Context(), master); !errors.As(err, &partial) {
				t.Fatalf("missing isolated peer failure: %v", err)
			}
			if account.Snapshot().Policy.CreditCeiling != 2000 {
				t.Fatal("failed earlier runtime starved healthy credit")
			}
			reports := new(countingAuthorityReceipt)
			if err := local.forwardAuthority(t.Context(), reports); !errors.As(err, &partial) {
				t.Fatalf("missing isolated report failure: %v", err)
			}
			if reports.reports.Load() != 1 {
				t.Fatal("failed earlier runtime starved healthy authority report")
			}
		})
	}
}
