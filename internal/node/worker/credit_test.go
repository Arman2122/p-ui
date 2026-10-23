package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type creditEngine struct {
	inactiveEngine
	applications int
}

func (e *creditEngine) Apply(context.Context, []byte, func(string) (*limits.Account, bool)) error {
	e.applications++
	return nil
}

func TestWorkerCreditUpdateFencesJournalsAndPendingConfiguration(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer credit.Close()
	engine := new(creditEngine)
	w, err := New(t.Context(), "xray", "xray", engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	defer w.cancel()
	grant := governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, CreditRevision: 1, Enabled: true, CreditCeiling: 1000, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
	payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{grant}}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := w.Apply(t.Context(), deployment); err != nil {
		t.Fatal(err)
	}
	batch := &nodev1.CreditUpdateBatch{NodeId: id.NodeID, InstallationId: id.InstallationID, AgentStateJournalId: uuid.NewString(), RuntimeId: "xray", StateJournalId: store.JournalID(), CreditJournalId: credit.JournalID(), BaseConfigurationRevision: 1, Updates: []*nodev1.CreditUpdate{{GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.Policy.Term, CreditRevision: 2, CreditCeiling: 1500}}}
	if err := store.BindAgentJournal(batch.AgentStateJournalId); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.ApplyCredit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if engine.applications != 1 {
		t.Fatal("credit-only update restarted core")
	}
	account, _ := credit.Lookup(grant.ClientID)
	if account.Snapshot().Policy.CreditCeiling != 1500 {
		t.Fatal("credit update not applied")
	}
	bad := proto.Clone(batch).(*nodev1.CreditUpdateBatch)
	bad.CreditJournalId = uuid.NewString()
	if err := w.ApplyCredit(t.Context(), bad); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("replacement journal accepted credit=%v", err)
	}
	deployment.Revision = 2
	if err := store.Receive(deployment); err != nil {
		t.Fatal(err)
	}
	batch.Updates[0].CreditRevision = 3
	batch.Updates[0].CreditCeiling = 2000
	if err := w.ApplyCredit(t.Context(), batch); !errors.Is(err, state.ErrRevision) {
		t.Fatalf("pending configuration allowed credit expansion=%v", err)
	}
	if account.Snapshot().Policy.CreditCeiling != 1500 {
		t.Fatal("rejected credit changed ceiling")
	}
}
