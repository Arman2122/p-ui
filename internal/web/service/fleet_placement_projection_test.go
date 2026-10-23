package service

import (
	"bytes"
	"slices"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func configurePlacementNode(t *testing.T, f fleetClientFixture, s *FleetPlacementService, index int, region string) {
	t.Helper()
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := network.ApproveDefaults(t.Context(), &f.admin, 0, networkSpec(25000, f.inbounds[0].Id)); err != nil {
		t.Fatal(err)
	}
	if _, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[index].NodeID, InstallationID: f.nodes[index].InstallationID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigureNode(t.Context(), &f.admin, FleetNodePlacementRequest{
		NodeID: f.nodes[index].NodeID, InstallationID: f.nodes[index].InstallationID, Enabled: true,
		Labels: map[string]string{"region": region}, InboundIDs: []int{f.inbounds[0].Id, f.inbounds[1].Id},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetPlacementProjectionUnionsRulesAndIntersectsRestrictionsPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	s, err := NewFleetPlacementService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	configurePlacementNode(t, f, s, 0, "manual")
	configurePlacementNode(t, f, s, 1, "eu")
	group := model.ClientGroup{Name: "placement-group"}
	plan := model.Policy{Name: "placement-plan", Tiers: `[]`}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: f.inbounds[0].Id}).Error; err != nil {
		t.Fatal(err)
	}
	ids := []int{f.inbounds[0].Id, f.inbounds[1].Id}
	rules := []FleetPlacementRuleRequest{
		{ID: uuid.NewString(), Scope: "plan", PlanID: &plan.Id, Enabled: true, NodeIDs: []string{f.nodes[1].NodeID}, InboundIDs: ids},
		{ID: uuid.NewString(), Scope: "group", GroupID: &group.Id, Enabled: true, Labels: map[string]string{"region": "eu"}, InboundIDs: ids},
	}
	for _, rule := range rules {
		if _, err := s.PutRule(t.Context(), &f.admin, rule); err != nil {
			t.Fatal(err)
		}
	}
	hello := f.hello(t, f.nodes[1])
	hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
	if _, err := f.store.BeginSession(t.Context(), f.nodes[1], hello); err != nil {
		t.Fatal(err)
	}
	projection, err := s.PreviewClient(t.Context(), &f.admin, materialized.Client.ID)
	if err != nil || len(projection.Targets) != 2 {
		t.Fatalf("rule projection=%+v err=%v", projection, err)
	}
	for _, target := range projection.Targets {
		if !slices.Equal(target.InboundIDs, ids[:1]) {
			t.Fatalf("plan restriction lost to rule/group union: %+v", target)
		}
		if target.NodeID == f.nodes[1].NodeID && (len(target.Rules) != 2 || !target.ApplicationEligible) {
			t.Fatalf("matching rules duplicated or lost a qualified target: %+v", target)
		}
	}
	hello.Capabilities = nil
	if _, err := f.store.BeginSession(t.Context(), f.nodes[1], hello); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, materialized.Client.ID)
	if err != nil || len(projection.Targets) != 2 {
		t.Fatalf("missing inventory retired a master-selected target: %+v %v", projection, err)
	}
	for _, target := range projection.Targets {
		if target.NodeID == f.nodes[1].NodeID && (target.ApplicationEligible || target.Diagnostic != "CAPABILITIES_UNAVAILABLE") {
			t.Fatalf("missing inventory authorized new application: %+v", target)
		}
	}
	one := ids[:1]
	request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: materialized.Client.ID, ExpectedRevision: 1, Action: "detach", NodeID: f.nodes[1].NodeID, InboundIDs: &one}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.db, &f.admin, request); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, materialized.Client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].NodeID != f.nodes[0].NodeID {
		t.Fatalf("rules overrode explicit target suppression: %+v %v", projection, err)
	}
	request.OperationID, request.ExpectedRevision, request.Action = uuid.NewString(), 2, "attach"
	if _, err := SetFleetClientPlacementTx(t.Context(), f.db, &f.admin, request); err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		rule.ExpectedRevision, rule.Enabled = 1, false
		if _, err := s.PutRule(t.Context(), &f.admin, rule); err != nil {
			t.Fatal(err)
		}
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, materialized.Client.ID)
	if err != nil || len(projection.Targets) != 2 {
		t.Fatalf("rule removal deleted an explicit manual target: %+v %v", projection, err)
	}
	var grants int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("placement preview funded authority: grants=%d err=%v", grants, err)
	}
}

func TestFleetPlacementDefaultOnlyAppliesToUnplacedStandaloneClientPostgres(t *testing.T) {
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
	configurePlacementNode(t, f, s, 1, "manual")
	projection, err := s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 0 {
		t.Fatalf("approved nodes received unassigned standalone client: %+v %v", projection, err)
	}
	if _, err := s.ConfigureDefault(t.Context(), &f.admin, FleetPlacementDefaultRequest{NodeID: &f.nodes[0].NodeID, InstallationID: &f.nodes[0].InstallationID}); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].NodeID != f.nodes[0].NodeID || projection.Targets[0].DefaultRevision != 1 {
		t.Fatalf("explicit standalone default failed: %+v %v", projection, err)
	}
	ids := []int{f.inbounds[0].Id}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.db, &f.admin, FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: 1, Action: "attach", NodeID: f.nodes[1].NodeID, InboundIDs: &ids}); err != nil {
		t.Fatal(err)
	}
	projection, err = s.PreviewClient(t.Context(), &f.admin, client.ID)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].NodeID != f.nodes[1].NodeID || projection.Targets[0].DefaultRevision != 0 {
		t.Fatalf("default duplicated an explicit standalone placement: %+v %v", projection, err)
	}
}
