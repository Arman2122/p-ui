package controlplane

import (
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func bootstrapTestRuntime(t *testing.T, store *Store, p identity.Principal, runtimeID string) *nodev1.JournalRegistration {
	t.Helper()
	var node model.FleetNodeJournal
	if err := store.db.Find(&node, "node_id = ?", p.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	agentID := uuid.NewString()
	if node.StateJournalID != nil {
		agentID = *node.StateJournalID
	}
	var saved model.FleetRuntimeJournal
	result := store.db.Find(&saved, "node_id = ? AND runtime_id = ?", p.NodeID, runtimeID)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	runtime := &nodev1.RuntimeJournalIdentity{RuntimeId: runtimeID, CoreId: runtimeID, StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}
	if result.RowsAffected != 0 {
		runtime = &nodev1.RuntimeJournalIdentity{RuntimeId: saved.RuntimeID, CoreId: saved.CoreID, StateJournalId: saved.StateJournalID, CreditJournalId: saved.CreditJournalID}
	}
	request := &nodev1.JournalRegistration{NodeId: p.NodeID, InstallationId: p.InstallationID, AgentStateJournalId: agentID, Runtimes: []*nodev1.RuntimeJournalIdentity{runtime}}
	if _, err := store.RegisterJournals(t.Context(), p, request); err != nil {
		t.Fatal(err)
	}
	return request
}

func journalHello(t *testing.T, store *Store, p identity.Principal) *nodev1.NodeHello {
	t.Helper()
	request := bootstrapTestRuntime(t, store, p, "xray")
	return &nodev1.NodeHello{NodeId: p.NodeID, InstallationId: p.InstallationID, ProtocolVersion: compatibility.Protocol, AgentStateJournalId: request.AgentStateJournalId}
}
