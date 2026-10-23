package worker

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestBoundWorkerRefusesReplacementAgent(t *testing.T) {
	f := newAuthorityFixture(t)
	other := uuid.NewString()
	request := func(action, agent string, deployment *nodev1.Deployment) int {
		t.Helper()
		encoded, err := proto.Marshal(deployment)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/"+action, bytes.NewReader(encoded))
		r.Header.Set("X-Pui-State-Journal", f.store.JournalID())
		r.Header.Set("X-Pui-Credit-Journal", f.credit.JournalID())
		r.Header.Set("X-Pui-Agent-Journal", agent)
		response := httptest.NewRecorder()
		f.worker.Handler().ServeHTTP(response, r)
		return response.Code
	}
	for _, action := range []string{"validate", "apply"} {
		if got := request(action, other, f.deployment); got != http.StatusConflict {
			t.Fatalf("replacement agent reached %s: %d", action, got)
		}
	}
	if err := f.store.RequireAgentJournal(f.agent); err != nil {
		t.Fatal(err)
	}
	if got := request("apply", f.agent, f.deployment); got != http.StatusNoContent {
		t.Fatalf("exact full apply replay failed: %d", got)
	}
	if err := f.worker.ApplyCredit(t.Context(), &nodev1.CreditUpdateBatch{NodeId: f.id.NodeID, InstallationId: f.id.InstallationID, AgentStateJournalId: other, RuntimeId: f.runtime, StateJournalId: f.store.JournalID(), CreditJournalId: f.credit.JournalID(), BaseConfigurationRevision: 1, Updates: []*nodev1.CreditUpdate{{GrantId: f.batch.Updates[0].GrantId, ClientId: f.batch.Updates[0].ClientId, TermId: f.batch.Updates[0].TermId, CreditRevision: 2, CreditCeiling: 2000}}}); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("foreign agent submitted credit: %v", err)
	}
	if err := f.store.BindAgentJournal(other); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("durable agent binding replaced: %v", err)
	}
}

func TestFullValidationAndInvalidPayloadCannotPinAgentJournal(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	directory := t.TempDir()
	path := filepath.Join(directory, "state.db")
	store, err := state.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	credit, err := governor.Open(filepath.Join(directory, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer credit.Close()
	process, err := New(t.Context(), "xray", "xray", inactiveEngine{}, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	defer process.cancel()
	payload, _ := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "xray", Core: "xray", Configuration: json.RawMessage(`{}`)}}})
	digest := sha256.Sum256(payload)
	deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	call := func(action, agent string, d *nodev1.Deployment) int {
		t.Helper()
		encoded, _ := proto.Marshal(d)
		request := httptest.NewRequest(http.MethodPost, "/v1/"+action, bytes.NewReader(encoded))
		request.Header.Set("X-Pui-State-Journal", store.JournalID())
		request.Header.Set("X-Pui-Credit-Journal", credit.JournalID())
		request.Header.Set("X-Pui-Agent-Journal", agent)
		response := httptest.NewRecorder()
		process.Handler().ServeHTTP(response, request)
		return response.Code
	}
	first, second := uuid.NewString(), uuid.NewString()
	if got := call("validate", first, deployment); got != http.StatusNoContent {
		t.Fatalf("validate status=%d", got)
	}
	if err := store.RequireAgentJournal(first); !errors.Is(err, state.ErrIdentity) {
		t.Fatal("validation irreversibly pinned agent")
	}
	bad := proto.Clone(deployment).(*nodev1.Deployment)
	bad.PayloadSha256 = bytes.Repeat([]byte{1}, 32)
	if got := call("apply", first, bad); got != http.StatusConflict {
		t.Fatalf("invalid apply status=%d", got)
	}
	if err := store.RequireAgentJournal(first); !errors.Is(err, state.ErrIdentity) {
		t.Fatal("invalid payload pinned agent")
	}
	if got := call("apply", second, deployment); got != http.StatusNoContent {
		t.Fatalf("valid agent application failed: %d", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RequireAgentJournal(second); err != nil {
		t.Fatalf("applied agent binding was not durable: %v", err)
	}
	if err := store.RequireAgentJournal(first); !errors.Is(err, state.ErrIdentity) {
		t.Fatal("old validation authority replaced applied agent")
	}
}
