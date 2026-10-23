package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type placementApplicationFixture struct {
	f          fleetClientFixture
	service    *FleetPlacementService
	rule       FleetPlacementRuleRequest
	intent     model.FleetPlacementIntent
	generation uint64
	approved   uint64
}

func newPlacementApplicationFixture(t *testing.T) placementApplicationFixture {
	t.Helper()
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	s, err := NewFleetPlacementService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	configurePlacementNode(t, f, s, 1, "target")
	plan := model.Policy{Name: "automatic-placement", Tiers: `[]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id}).Error; err != nil {
		t.Fatal(err)
	}
	rule := FleetPlacementRuleRequest{ID: uuid.NewString(), Scope: "plan", PlanID: &plan.Id, Enabled: true, NodeIDs: []string{f.nodes[1].NodeID}, InboundIDs: []int{f.inbounds[0].Id}}
	if _, err := s.PutRule(t.Context(), &f.admin, rule); err != nil {
		t.Fatal(err)
	}
	hello := f.hello(t, f.nodes[1])
	hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
	generation, err := f.store.BeginSession(t.Context(), f.nodes[1], hello)
	if err != nil {
		t.Fatal(err)
	}
	fixture := placementApplicationFixture{f: f, service: s, rule: rule, generation: generation}
	fixture.approveEmptyNetwork(t)
	fixture.derive(t)
	return fixture
}

func (f *placementApplicationFixture) approveEmptyNetwork(t *testing.T) {
	t.Helper()
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	var head model.FleetNodeNetworkHead
	if err := f.f.db.First(&head, "node_id = ?", f.f.nodes[1].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{
		NodeID: head.NodeID, InstallationID: head.InstallationID, ExpectedRevision: uint64(head.Revision), ExpectedDefaultsID: head.DesiredDefaultsID,
	})
	if err != nil || !preview.Ready || len(preview.Identities) != 0 {
		t.Fatalf("initial explicit empty network preview=%+v %v", preview, err)
	}
	if _, err := network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil || deployment == nil {
		t.Fatalf("explicit empty baseline delivery=%+v %v", deployment, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation, PayloadSha256: deployment.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	f.approved = deployment.Revision
}

func (f *placementApplicationFixture) derive(t *testing.T) {
	t.Helper()
	for range 8 {
		worked, err := ProcessFleetPlacementFanout(t.Context(), f.f.db, 32)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if err := f.f.db.First(&f.intent, "node_id = ? AND installation_id = ?", f.f.nodes[1].NodeID, f.f.nodes[1].InstallationID).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *placementApplicationFixture) applyBaseline(t *testing.T) FleetMaterialization {
	t.Helper()
	var err error
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.MaterializedPlacementID == nil || !f.intent.Pending {
		t.Fatalf("new target materialization=%+v err=%v", f.intent, err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil || deployment == nil {
		t.Fatalf("new baseline delivery=%+v err=%v", deployment, err)
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Grants) != 0 || len(bundle.Runtimes[0].Admissions) != 1 {
		t.Fatalf("automatic placement invented initial authority: %+v %v", bundle, err)
	}
	for _, row := range []any{&model.FleetGrant{}, &model.FleetTerm{}} {
		var count int64
		if err := f.f.db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("placement started billing before demand: %T count=%d err=%v", row, count, err)
		}
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation,
		PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_RECEIVED,
	}); err != nil {
		t.Fatal(err)
	}
	if current, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent); err != nil || !current.Pending {
		t.Fatalf("received baseline was mistaken for applied placement: %+v %v", current, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation,
		PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.Pending || f.intent.State != "applied" || f.intent.AppliedRevision != int64(deployment.Revision) {
		t.Fatalf("applied baseline did not settle placement: %+v %v", f.intent, err)
	}
	var materialized FleetMaterialization
	if err := f.f.db.First(&materialized.Placement, "id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&materialized.Client, "id = ?", f.intent.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Where("placement_id = ?", materialized.Placement.ID).Find(&materialized.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	return materialized
}

func (f *placementApplicationFixture) fundDemand(t *testing.T, materialized FleetMaterialization) (model.FleetGrant, *nodev1.AuthorityDeliveryRequest) {
	t.Helper()
	request := fleetDemand(materialized)
	request.DeploymentRevision = uint64(f.intent.AppliedRevision)
	if _, err := HandleFleetActivation(t.Context(), f.f.db, f.f.nodes[1], f.generation, request); err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReportAtNode(t, f.f, 1, 1, time.Now())
	var demand model.FleetAdmissionDemand
	if err := f.f.db.First(&demand, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	leases, err := f.f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("new target demand lease=%+v %v", leases, err)
	}
	result, err := ProcessFleetAdmissionDemand(t.Context(), f.f.db, leases[0], demand)
	if err != nil || result.Outcome != FleetAdmissionSatisfied {
		t.Fatalf("fresh target demand=%+v %v", result, err)
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	nodeFirst := f.f
	nodeFirst.nodes = append([]identity.Principal{f.f.nodes[1]}, f.f.nodes[:1]...)
	journal := fleetCreditRequest(t, nodeFirst)
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, f.f.nodes[1].NodeID, uint64(f.intent.AppliedRevision), "xray", f.intent.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	authority := &nodev1.AuthorityDeliveryRequest{
		NodeId: journal.NodeId, InstallationId: journal.InstallationId, AgentStateJournalId: journal.AgentStateJournalId,
		RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId,
		BaseConfigurationRevision: uint64(f.intent.AppliedRevision), BasePayloadSha256: scope.PayloadSHA256,
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[1], authority, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || !batch.Updates[0].Enabled {
		t.Fatalf("fresh demand did not deliver authority: %+v %v", batch, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[1], batch); err != nil {
		t.Fatal(err)
	}
	return grant, authority
}

func TestFleetPlacementApplicationRequiresAppliedBaselineAndFreshDemandPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	materialized := f.applyBaseline(t)
	grant, _ := f.fundDemand(t, materialized)
	if grant.IssuedBytes <= 0 || grant.IssuedBytes > f.f.client.TotalGB {
		t.Fatalf("new target demand exceeded shared quota: %+v", grant)
	}
}

func TestFleetPlacementRetirementWaitsForStopAndAppliedOmissionPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	materialized := f.applyBaseline(t)
	grant, authority := f.fundDemand(t, materialized)
	f.rule.Enabled, f.rule.ExpectedRevision = false, 1
	if _, err := f.service.PutRule(t.Context(), &f.f.admin, f.rule); err != nil {
		t.Fatal(err)
	}
	f.derive(t)
	if f.intent.Assigned || !f.intent.RequiresStop {
		t.Fatalf("removed rule did not retire its assignment: %+v", f.intent)
	}
	if _, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent); err != nil {
		t.Fatal(err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[1], authority, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].Enabled {
		t.Fatalf("retirement did not deliver frozen stop: %+v %v", batch, err)
	}
	if _, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent); err != nil {
		t.Fatal(err)
	}
	var before model.FleetNode
	if err := f.f.db.First(&before, "id = ?", f.f.nodes[1].NodeID).Error; err != nil || before.DesiredRevision != f.intent.AppliedRevision {
		t.Fatalf("queued stop removed configuration before actual ACK: %+v %v", before, err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[1], batch); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || !f.intent.Pending || f.intent.RequiresStop {
		t.Fatalf("stopped assignment removal=%+v %v", f.intent, err)
	}
	deployment, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil || deployment == nil || deployment.Revision <= uint64(before.AppliedRevision) {
		t.Fatalf("removal deployment=%+v %v", deployment, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation,
		PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.Pending || f.intent.State != "retired" || f.intent.AppliedRevision != int64(deployment.Revision) {
		t.Fatalf("applied omission did not settle retirement: %+v %v", f.intent, err)
	}
	var retained model.FleetGrant
	if err := f.f.db.First(&retained, "id = ?", grant.ID).Error; err != nil || retained.IssuedBytes != grant.IssuedBytes || retained.Revision != grant.Revision || retained.TermID != grant.TermID {
		t.Fatalf("retirement erased or rewrote historical authority: %+v %v", retained, err)
	}
}

func TestFleetPlacementAuthOnlyReattachPublishesNewDescriptorPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	materialized := f.applyBaseline(t)
	base := f.intent.AppliedRevision
	ids := []int{f.f.inbounds[0].Id}
	request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: f.intent.ClientID, ExpectedRevision: 1, Action: "detach", NodeID: f.f.nodes[1].NodeID, InboundIDs: &ids}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request); err != nil {
		t.Fatal(err)
	}
	f.derive(t)
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.ExpectedRevision, request.Action = uuid.NewString(), 2, "attach"
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request); err != nil {
		t.Fatal(err)
	}
	f.derive(t)
	if err := f.f.db.First(&work, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	var err error
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&work, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", f.f.nodes[1].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if node.DesiredRevision <= base {
		t.Fatalf("auth-only reattach replayed the stopped baseline: node=%+v base=%d", node, base)
	}
	deployment, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Admissions) != 1 || bundle.Runtimes[0].Admissions[0].PlacementRevision <= uint64(materialized.Placement.Revision) {
		t.Fatalf("reattach reused old admission scope: %+v %v", bundle, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		AuthorityGeneration: f.generation, Revision: deployment.Revision, PayloadSha256: deployment.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.Pending || f.intent.State != "applied" {
		t.Fatalf("fresh reattach did not converge: %+v %v", f.intent, err)
	}
	for _, row := range []any{&model.FleetGrant{}, &model.FleetTerm{}} {
		var count int64
		if err := f.f.db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("auth-only reattach started billing: %T count=%d err=%v", row, count, err)
		}
	}
}

func TestFleetPlacementSourcePublisherRechecksCurrentCapabilityPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	var err error
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.MaterializedPlacementID == nil {
		t.Fatalf("placement materialization=%+v %v", f.intent, err)
	}
	if _, err := f.f.store.BeginSession(t.Context(), f.f.nodes[1], f.f.hello(t, f.f.nodes[1])); err != nil {
		t.Fatal(err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); !errors.Is(err, ErrFleetNetworkDependency) {
		t.Fatalf("source publisher bypassed lost application capability: %v", err)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", f.f.nodes[1].NodeID).Error; err != nil || node.DesiredRevision != int64(f.approved) {
		t.Fatalf("unqualified source publication escaped rollback: %+v %v", node, err)
	}
	hello := f.f.hello(t, f.f.nodes[1])
	hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
	if _, err := f.f.store.BeginSession(t.Context(), f.f.nodes[1], hello); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&node, "id = ?", node.ID).Error; err != nil || node.DesiredRevision != int64(f.approved+1) {
		t.Fatalf("restored capability did not resume pending publication: %+v %v", node, err)
	}
}
