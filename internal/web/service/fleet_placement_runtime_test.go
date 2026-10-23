package service

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPlacementRuntimeUsesApprovedBaseAndExactJournalPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	client := model.FleetClient{ID: uuid.NewString(), PanelClientID: &f.client.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: bytes.Repeat([]byte{1}, 32), Owned: true}
	if err := f.db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	s, err := NewFleetPlacementService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	configurePlacementNode(t, f, s, 0, "local")
	if _, err := s.ConfigureDefault(t.Context(), &f.admin, FleetPlacementDefaultRequest{NodeID: &f.nodes[0].NodeID, InstallationID: &f.nodes[0].InstallationID}); err != nil {
		t.Fatal(err)
	}
	hello := f.hello(t, f.nodes[0])
	custom := &nodev1.RuntimeJournalIdentity{RuntimeId: "stream-eu", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}
	if _, err := f.store.RegisterJournals(t.Context(), f.nodes[0], &nodev1.JournalRegistration{NodeId: f.nodes[0].NodeID, InstallationId: f.nodes[0].InstallationID, AgentStateJournalId: hello.AgentStateJournalId, Runtimes: []*nodev1.RuntimeJournalIdentity{custom}}); err != nil {
		t.Fatal(err)
	}
	hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
	if _, err := f.store.BeginSession(t.Context(), f.nodes[0], hello); err != nil {
		t.Fatal(err)
	}
	projection, err := s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].RuntimeID != "xray" || !projection.Targets[0].ApplicationEligible {
		t.Fatalf("unconfigured journal selected a worker: %+v %v", projection, err)
	}
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	base := networkSpec(25000, f.inbounds[0].Id).Bases["xray"]
	request := FleetNodeNetworkRequest{
		NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, ExpectedRevision: 1,
		Operations: []FleetNetworkOperation{{Section: "base", Key: "stream-eu", Operation: "replace", Base: &base}},
	}
	if _, err := network.ConfigureNode(t.Context(), &f.admin, request); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].RuntimeID != "" || projection.Targets[0].ApplicationEligible || projection.Targets[0].Diagnostic != "RUNTIME_AMBIGUOUS" {
		t.Fatalf("ambiguous same-core workers silently selected: %+v %v", projection, err)
	}
	request.ExpectedRevision, request.Operations = 2, []FleetNetworkOperation{{Section: "base", Key: "xray", Operation: "remove"}}
	if _, err := network.ConfigureNode(t.Context(), &f.admin, request); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].RuntimeID != "stream-eu" || projection.Targets[0].ApplicationEligible || projection.Targets[0].Diagnostic != "RUNTIME_ALIAS_UNQUALIFIED" {
		t.Fatalf("arbitrary runtime ID was replaced by core-name guess: %+v %v", projection, err)
	}
}
