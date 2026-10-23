package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type fleetClientFixture struct {
	db       *gorm.DB
	admin    model.User
	client   model.ClientRecord
	inbounds []model.Inbound
	nodes    []identity.Principal
	store    *controlplane.Store
}

func fleetFixture(t *testing.T, delayed bool) fleetClientFixture {
	t.Helper()
	initTestDB(t)
	f := fleetClientFixture{db: database.GetDB()}
	if err := f.db.First(&f.admin).Error; err != nil {
		t.Fatal(err)
	}
	f.client = model.ClientRecord{Email: "fleet-client", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Reset: 1}
	if delayed {
		f.client.ExpiryTime = -3_600_000
	}
	if err := f.db.Create(&f.client).Error; err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{"clients": []*model.Client{f.client.ToClient()}, "decryption": "none"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		inbound := model.Inbound{UserId: f.admin.Id, Protocol: model.VLESS, Tag: uuid.NewString(), Port: 23000 + i, Settings: string(settings), StreamSettings: `{}`, Sniffing: `{}`}
		if err := f.db.Create(&inbound).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.ClientInbound{ClientId: f.client.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
		f.inbounds = append(f.inbounds, inbound)
		node := identity.Principal{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
		if err := f.db.Create(&model.FleetNode{ID: node.NodeID, InstallationID: node.InstallationID, Name: "test", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	if err := f.db.Create(&core.ClientTraffic{Email: f.client.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: f.client.TotalGB, ExpiryTime: f.client.ExpiryTime, Reset: 1}).Error; err != nil {
		t.Fatal(err)
	}
	f.store, err = controlplane.NewStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range f.nodes {
		if _, err := f.store.RegisterJournals(t.Context(), node, &nodev1.JournalRegistration{
			NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: uuid.NewString(),
			Runtimes: []*nodev1.RuntimeJournalIdentity{{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f fleetClientFixture) materialize(t *testing.T, node int) FleetMaterialization {
	t.Helper()
	ids := []int{f.inbounds[0].Id, f.inbounds[1].Id}
	result, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{
		PanelClientID: f.client.Id,
		NodeID:        f.nodes[node].NodeID, InstallationID: f.nodes[node].InstallationID, RuntimeID: "xray", CoreID: "xray", InboundIDs: ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f fleetClientFixture) approve(t *testing.T, materialized FleetMaterialization) model.FleetPlacement {
	t.Helper()
	result, err := (FleetClientService{}).Approve(t.Context(), &f.admin, FleetApproveRequest{
		PlacementID:      materialized.Placement.ID,
		ExpectedRevision: uint64(materialized.Placement.Revision), DefinitionDigest: materialized.Placement.DefinitionDigest,
		SourcePolicyDigest: materialized.Placement.SourcePolicyDigest, GraphDigest: make([]byte, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f fleetClientFixture) activate(t *testing.T, placement model.FleetPlacement) controlplane.TransitionResult {
	t.Helper()
	result, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID,
		Kind: controlplane.Activation, ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readFleetTraffic(t *testing.T, f fleetClientFixture) core.ClientTraffic {
	t.Helper()
	var traffic core.ClientTraffic
	if err := f.db.Where("email = ?", f.client.Email).First(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	return traffic
}

func (f fleetClientFixture) hello(t *testing.T, node identity.Principal) *nodev1.NodeHello {
	t.Helper()
	var journal model.FleetNodeJournal
	if err := f.db.First(&journal, "node_id = ?", node.NodeID).Error; err != nil || journal.StateJournalID == nil {
		t.Fatalf("registered agent journal unavailable: %+v %v", journal, err)
	}
	return &nodev1.NodeHello{NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: *journal.StateJournalID}
}

func fleetDemand(materialized FleetMaterialization) *nodev1.ActivationRequest {
	return &nodev1.ActivationRequest{
		RequestId: uuid.NewString(), ClientId: materialized.Client.ID, CredentialGeneration: uint64(materialized.Client.CredentialGeneration),
		DeadlineUnixMilli: time.Now().Add(10 * time.Second).UnixMilli(), RuntimeId: materialized.Placement.RuntimeID, PlacementId: materialized.Placement.ID,
		PlacementRevision: uint64(materialized.Placement.Revision), InboundInstanceId: materialized.Inbounds[0].InstanceID, DeploymentRevision: 1,
	}
}

func fleetAdmissionBundle(materialized FleetMaterialization) configuration.Bundle {
	return configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{
		ID: materialized.Placement.RuntimeID, Core: materialized.Placement.CoreID, Configuration: json.RawMessage(`{}`),
		Admissions: []nodeadmission.Descriptor{{
			ClientID: materialized.Client.ID, CredentialGeneration: uint64(materialized.Client.CredentialGeneration),
			PlacementID: materialized.Placement.ID, PlacementRevision: uint64(materialized.Placement.Revision), InboundInstanceID: materialized.Inbounds[0].InstanceID, InboundTag: "node-" + materialized.Inbounds[0].InstanceID,
		}},
	}}}
}

func fleetApplyProbe(t *testing.T, f fleetClientFixture, node identity.Principal, generation uint64, bundle configuration.Bundle) uint64 {
	t.Helper()
	if _, err := f.store.PublishConfiguration(t.Context(), node, bundle); err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{Revision: delivery.Revision, AuthorityGeneration: generation, PayloadSha256: delivery.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED}); err != nil {
		t.Fatal(err)
	}
	return delivery.Revision
}

func TestFleetMaterializationDoesNotActivateUntilFreshAuthenticatedDemand(t *testing.T) {
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	if materialized.Client.Owned || materialized.Client.CurrentTermID != nil || materialized.Placement.Enabled {
		t.Fatal("materialization changed ownership or activated a term")
	}
	placement := f.approve(t, materialized)
	if got := readFleetTraffic(t, f); got.ExpiryTime != f.client.ExpiryTime {
		t.Fatalf("approval activated duration: %+v", got)
	}
	if err := submitTrafficWrite(func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			return (&InboundService{}).addClientTraffic(tx, []*core.ClientTraffic{{Email: f.client.Email, Up: 50, Down: 70}})
		})
	}); err != nil {
		t.Fatal(err)
	}
	if got := readFleetTraffic(t, f); got.ExpiryTime != f.client.ExpiryTime || got.Up != 0 || got.Down != 0 {
		t.Fatalf("legacy poll controlled fleet first use: %+v", got)
	}
	if _, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID,
		Kind: controlplane.Activation, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1,
	}); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("first use without demand=%v", err)
	}
	node := f.nodes[0]
	generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
	if err != nil {
		t.Fatal(err)
	}
	demand := fleetDemand(materialized)
	demand.DeploymentRevision = fleetApplyProbe(t, f, node, generation, configuration.Bundle{Version: 1})
	if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("undeployed dormant identity activated billing=%v", err)
	}
	demand.DeploymentRevision = fleetApplyProbe(t, f, node, generation, fleetAdmissionBundle(materialized))
	for _, tc := range []struct {
		name   string
		change func(*nodev1.ActivationRequest)
	}{
		{"runtime", func(value *nodev1.ActivationRequest) { value.RuntimeId = "ssh" }},
		{"deployment", func(value *nodev1.ActivationRequest) { value.DeploymentRevision++ }},
		{"placement", func(value *nodev1.ActivationRequest) { value.PlacementId = uuid.NewString() }},
		{"placement revision", func(value *nodev1.ActivationRequest) { value.PlacementRevision++ }},
		{"inbound instance", func(value *nodev1.ActivationRequest) { value.InboundInstanceId = uuid.NewString() }},
	} {
		bad := proto.Clone(demand).(*nodev1.ActivationRequest)
		tc.change(bad)
		if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, bad); !errors.Is(err, ErrFleetSource) {
			t.Fatalf("foreign %s demand=%v", tc.name, err)
		}
	}
	nextBundle := fleetAdmissionBundle(materialized)
	nextBundle.Runtimes[0].Configuration = json.RawMessage(`{"changed":true}`)
	if _, err := f.store.PublishConfiguration(t.Context(), node, nextBundle); err != nil {
		t.Fatal(err)
	}
	if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("pending deployment activated first use=%v", err)
	}
	demand.DeploymentRevision = fleetApplyProbe(t, f, node, generation, nextBundle)
	if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), f.nodes[1], generation, placement.ID, demand); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("foreign demand=%v", err)
	}
	demand.DeadlineUnixMilli = time.Now().Add(time.Minute).UnixMilli()
	if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("unbounded demand lifetime=%v", err)
	}
	demand.DeadlineUnixMilli = time.Now().Add(-time.Second).UnixMilli()
	if _, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("historical demand=%v", err)
	}
	demand.DeadlineUnixMilli = time.Now().Add(10 * time.Second).UnixMilli()
	active, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand)
	if err != nil || active.Replayed || active.Term.ExpiresUnixMilli-active.Term.StartedUnixMilli != 3_600_000 {
		t.Fatalf("first activation=%+v err=%v", active, err)
	}
	if got := readFleetTraffic(t, f); got.ExpiryTime != active.Term.ExpiresUnixMilli {
		t.Fatalf("master projection not atomic: %+v", got)
	}
	if replay, err := (FleetClientService{}).ActivateFromDemand(t.Context(), node, generation, placement.ID, demand); err != nil || !replay.Replayed || replay.Term.ID != active.Term.ID {
		t.Fatalf("lost activation reply=%+v err=%v", replay, err)
	}
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID,
		TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil || credit.Grant.ClientID != materialized.Client.ID {
		t.Fatalf("activation invalidated its own approved source=%+v err=%v", credit, err)
	}
}

func TestFleetBoundAllocationSharesInboundsAndRefusesPolicyDrift(t *testing.T) {
	f := fleetFixture(t, false)
	a := f.materialize(t, 0)
	b := f.materialize(t, 1)
	if a.Client.ID != b.Client.ID || len(a.Inbounds) != 2 || a.Inbounds[0].InstanceID == b.Inbounds[0].InstanceID {
		t.Fatal("node instances and logical client identity were aliased")
	}
	first, second := f.approve(t, a), f.approve(t, b)
	active := f.activate(t, first)
	unlimited := controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
	request := FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: first.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 600, Rates: unlimited,
	}
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), request)
	if err != nil || credit.Grant.PlacementID == nil || *credit.Grant.PlacementID != first.ID {
		t.Fatalf("bound allocation=%+v err=%v", credit, err)
	}
	if replay, err := (FleetClientService{}).AllocateCredit(t.Context(), request); err != nil || !replay.Replayed || replay.Grant.ID != credit.Grant.ID {
		t.Fatalf("credit replay=%+v err=%v", replay, err)
	}
	request.OperationID, request.PlacementID, request.Bytes = uuid.NewString(), second.ID, 401
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, controlplane.ErrBudget) {
		t.Fatalf("second node duplicated allowance=%v", err)
	}
	request.Bytes = 400
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	var grants int64
	if err := f.db.Model(&model.FleetGrant{}).Where("client_id = ?", a.Client.ID).Count(&grants).Error; err != nil || grants != 2 {
		t.Fatalf("grant per inbound multiplied authority: %d %v", grants, err)
	}
	if _, err := f.store.Reserve(t.Context(), controlplane.ReserveRequest{
		OperationID: uuid.NewString(), GrantID: uuid.NewString(), NodeID: f.nodes[0].NodeID,
		InstallationID: f.nodes[0].InstallationID, RuntimeID: "xray", ClientID: a.Client.ID, TermID: active.Term.ID, BudgetIDs: []string{active.Term.BudgetID}, Bytes: 1,
	}); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("raw primitive bypassed placement=%v", err)
	}
	if err := f.db.Model(&f.client).Update("uuid", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.Bytes = uuid.NewString(), 1
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), request); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("credential edit bypassed generation fencing=%v", err)
	}
	rematerialized := f.materialize(t, 0)
	if rematerialized.Client.ID != a.Client.ID || rematerialized.Client.CredentialGeneration != 2 || rematerialized.Placement.Enabled {
		t.Fatalf("credential reconciliation=%+v", rematerialized)
	}
}

func fleetAppliedStop(t *testing.T, f fleetClientFixture, node identity.Principal, term model.FleetTerm, credit FleetCredit) {
	t.Helper()
	share, err := f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: credit.Grant.ID, BudgetID: credit.Grant.ClientID,
		NodeID: node.NodeID, InstallationID: node.InstallationID, RuntimeID: credit.Grant.RuntimeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
	if err != nil {
		t.Fatal(err)
	}
	bundle := configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{
		ID: credit.Grant.RuntimeID, Core: "xray", Configuration: json.RawMessage(`{}`),
		Grants: []governor.Grant{{ID: credit.Grant.ID, ClientID: credit.Grant.ClientID, RateRevision: uint64(share.Revision), Policy: limits.Policy{
			Term: term.ID, Revision: 1, CreditCeiling: uint64(credit.Grant.IssuedBytes), ExpiresUnixMilli: term.ExpiresUnixMilli,
		}}},
	}}}
	if _, err := f.store.PublishConfiguration(t.Context(), node, bundle); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.store.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: deployment.Revision,
		PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetResetWaitsForStopAndExcludesLegacyWriters(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	placement := f.approve(t, materialized)
	active := f.activate(t, placement)
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": 10, "down": 20}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&ClientService{}).ResetTrafficByEmail(&InboundService{}, f.client.Email); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("legacy reset touched fleet counters=%v", err)
	}
	if _, err := (&ClientService{}).BulkResetTraffic(&InboundService{}, []string{f.client.Email}); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("legacy bulk reset touched fleet counters=%v", err)
	}
	request := FleetTransitionRequest{OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.TrafficReset, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1}
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, request)
	if err != nil || !pending.PendingDrain {
		t.Fatalf("reset intent=%+v err=%v", pending, err)
	}
	finish := FleetFinishRequest{OperationID: uuid.NewString(), PlacementID: placement.ID, PendingTermID: pending.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1}
	if _, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, finish); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("reset committed before stop=%v", err)
	}
	if got := readFleetTraffic(t, f); got.Up != 10 || got.Down != 20 || got.ExpiryTime != active.Term.ExpiresUnixMilli {
		t.Fatalf("pending reset changed panel state: %+v", got)
	}
	fleetAppliedStop(t, f, f.nodes[0], active.Term, credit)
	completed, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, finish)
	if err != nil || completed.Term.ExpiresUnixMilli != active.Term.ExpiresUnixMilli {
		t.Fatalf("completed reset=%+v err=%v", completed, err)
	}
	if got := readFleetTraffic(t, f); got.Up != 0 || got.Down != 0 {
		t.Fatalf("completed reset did not update panel atomically: %+v", got)
	}
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Update("up", 25).Error; err != nil {
		t.Fatal(err)
	}
	if replay, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, finish); err != nil || !replay.Replayed {
		t.Fatalf("reset replay=%+v err=%v", replay, err)
	}
	if got := readFleetTraffic(t, f); got.Up != 25 {
		t.Fatalf("lost reset response zeroed later usage: %+v", got)
	}
}
