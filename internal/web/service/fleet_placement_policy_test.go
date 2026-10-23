package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPlacementPolicyRequiresExplicitTargetsAndRevisionPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	s, err := NewFleetPlacementService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.ReadDefault(t.Context(), &f.admin)
	if err != nil || view.NodeID != nil || view.Revision != 0 {
		t.Fatalf("enrollment invented default placement: %+v %v", view, err)
	}
	if _, err := s.ConfigureDefault(t.Context(), &f.admin, FleetPlacementDefaultRequest{NodeID: &f.nodes[0].NodeID, InstallationID: &f.nodes[0].InstallationID}); err == nil {
		t.Fatal("unapproved node became a default target")
	}
	request := FleetNodePlacementRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Enabled: true, Labels: map[string]string{"region": "eu"}, InboundIDs: []int{f.inbounds[0].Id}}
	configured, err := s.ConfigureNode(t.Context(), &f.admin, request)
	if err != nil || configured.Revision != 1 {
		t.Fatalf("node policy=%+v err=%v", configured, err)
	}
	replay, err := s.ConfigureNode(t.Context(), &f.admin, request)
	if err != nil || replay.Revision != configured.Revision {
		t.Fatalf("node policy retry changed revision: %+v %v", replay, err)
	}
	request.InboundIDs = []int{f.inbounds[1].Id}
	if _, err := s.ConfigureNode(t.Context(), &f.admin, request); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("stale node policy overwrote eligible templates: %v", err)
	}
	view, err = s.ReadDefault(t.Context(), &f.admin)
	if err != nil || view.NodeID != nil {
		t.Fatalf("node policy approval invented default assignment: %+v %v", view, err)
	}
	view, err = s.ConfigureDefault(t.Context(), &f.admin, FleetPlacementDefaultRequest{NodeID: &f.nodes[0].NodeID, InstallationID: &f.nodes[0].InstallationID})
	if err != nil || view.NodeID == nil || *view.NodeID != f.nodes[0].NodeID || view.Revision != 1 || !view.Pending {
		t.Fatalf("explicit approved default=%+v err=%v", view, err)
	}
	plan := model.Policy{Name: "placement plan", Tiers: `[]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	rule := FleetPlacementRuleRequest{ID: uuid.NewString(), Scope: "plan", PlanID: &plan.Id, Enabled: true, InboundIDs: []int{f.inbounds[0].Id}}
	if _, err := s.PutRule(t.Context(), &f.admin, rule); !errors.Is(err, controlplane.ErrInvalid) {
		t.Fatalf("wildcard rule copied clients to every node: %v", err)
	}
	rule.Labels = map[string]string{"region": "eu"}
	saved, err := s.PutRule(t.Context(), &f.admin, rule)
	if err != nil || saved.Revision != 1 || !saved.Pending {
		t.Fatalf("explicit plan rule=%+v err=%v", saved, err)
	}
	other := model.Policy{Name: "other placement plan", Tiers: `[]`}
	if err := f.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	rule.PlanID, rule.ExpectedRevision = &other.Id, 1
	if _, err := s.PutRule(t.Context(), &f.admin, rule); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("rule retargeting orphaned old members without fanout: %v", err)
	}
	rules, err := s.ListRules(t.Context(), &f.admin, "", 100)
	if err != nil || len(rules) != 1 || rules[0].PlanID == nil || *rules[0].PlanID != plan.Id || rules[0].Revision != 1 {
		t.Fatalf("refused mutation changed rule: %+v %v", rules, err)
	}
}
