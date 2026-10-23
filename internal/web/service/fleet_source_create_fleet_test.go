package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

func createdFleetAdmissionFixture(t *testing.T) admissionAllocationFixture {
	t.Helper()
	f := fleetFixture(t, true)
	if err := f.db.Where("client_id = ?", f.client.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("email = ?", f.client.Email).Delete(&core.ClientTraffic{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Delete(&f.client).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.Policy{Name: "created-shared-plan", Rev: 1, OnDepletion: "disable", ResetCycle: "none", Tiers: `[{"fromBytes":0,"upBps":100000000,"downBps":100000000}]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	ids := []int{f.inbounds[0].Id, f.inbounds[1].Id}
	if err := f.db.Model(&model.Inbound{}).Where("id IN ?", ids).Update("enable", true).Error; err != nil {
		t.Fatal(err)
	}
	created, err := CreateFleetClientTx(t.Context(), f.db, &f.admin, entity.ClientCreatePayload{
		OperationID: uuid.NewString(), PolicyID: &plan.Id, InboundIds: ids,
		Client: model.Client{Email: "created-shared-source", Enable: true, TotalGB: 1 << 30, ExpiryTime: -3_600_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.client = model.ClientRecord{}
	if err := f.db.First(&f.client, created.PanelClientID).Error; err != nil {
		t.Fatal(err)
	}
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1, Bases: map[string]FleetRuntimeBase{
		"xray": {Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := admissionAllocationFixture{f: f}
	for index, node := range f.nodes {
		materialized := f.materialize(t, index)
		hello := f.hello(t, node)
		hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
		generation, err := f.store.BeginSession(t.Context(), node, hello)
		if err != nil {
			t.Fatal(err)
		}
		configured, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: node.NodeID, InstallationID: node.InstallationID})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: configured.Revision.Revision, ExpectedDefaultsID: defaults.ID})
		if err != nil || !preview.Ready {
			t.Fatalf("created source could not compile: %+v %v", preview, err)
		}
		if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
			t.Fatal(err)
		}
		deployment, err := f.store.Delivery(t.Context(), node, generation)
		if err != nil || deployment == nil || deployment.Revision != 1 {
			t.Fatalf("created source deployment unavailable: %+v %v", deployment, err)
		}
		bundle, err := configuration.Decode(deployment.Payload)
		if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Admissions) != 2 || len(bundle.Runtimes[0].Grants) != 0 {
			t.Fatalf("created source was billed before demand or lost bindings: %+v %v", bundle, err)
		}
		if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{
			Revision: deployment.Revision, AuthorityGeneration: generation, PayloadSha256: deployment.PayloadSha256,
			AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.db.First(&materialized.Placement, "id = ?", materialized.Placement.ID).Error; err != nil {
			t.Fatal(err)
		}
		fleetAuthorityReportAtNode(t, f, index, 1, time.Now())
		fixture.materialized = append(fixture.materialized, materialized)
		fixture.generations = append(fixture.generations, generation)
	}
	requireCreateCount(t, f.db, &model.FleetTerm{}, 0)
	requireCreateCount(t, f.db, &model.FleetGrant{}, 0)
	return fixture
}

func TestFleetCreatedSourceSharesRateQuotaAndMeasuredUsageAcrossTwoNodesPostgres(t *testing.T) {
	f := createdFleetAdmissionFixture(t)
	assertFleetSharedRateAdmission(t, f)
	var grants []model.FleetGrant
	if err := f.f.db.Order("node_id").Find(&grants).Error; err != nil || len(grants) != 2 || grants[0].ClientID != grants[1].ClientID || grants[0].TermID != grants[1].TermID {
		t.Fatalf("created source split its billing identity: %+v %v", grants, err)
	}
	for index, node := range f.f.nodes {
		var grant model.FleetGrant
		if err := f.f.db.First(&grant, "node_id = ?", node.NodeID).Error; err != nil {
			t.Fatal(err)
		}
		batch := &nodev1.UsageBatch{NodeId: node.NodeID, InstallationId: node.InstallationID, Sequence: 1, Checkpoints: []*nodev1.UsageCheckpoint{{
			ClientId: grant.ClientID, TermId: grant.TermID, GrantId: grant.ID, MeterId: grant.RuntimeID, MeterIncarnation: uuid.NewString(),
			Uploaded: uint64(100 + index*200), Downloaded: uint64(200 + index*200), ObservedUnixMilli: time.Now().UnixMilli(),
		}}}
		for range 2 {
			if _, err := f.f.store.Ingest(t.Context(), node, batch); err != nil {
				t.Fatalf("ingest created source node %d usage: %v", index, err)
			}
		}
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = (SELECT budget_id FROM fleet_terms WHERE id = ?)", grants[0].TermID).Error; err != nil || budget.UsedBytes != 1000 || budget.ReservedBytes != (128<<20)-1000 {
		t.Fatalf("two-node usage/replay violated shared conservation: %+v %v", budget, err)
	}
	traffic, err := (&InboundService{}).GetClientTrafficByEmail(f.f.client.Email)
	if err != nil || traffic == nil || traffic.Up != 400 || traffic.Down != 600 {
		t.Fatalf("two-node client traffic did not read measured ledger usage: %+v %v", traffic, err)
	}
}
