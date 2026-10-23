package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestFleetPlacementRetirementPreservesManualNetworkDraftPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	f.applyBaseline(t)
	base := f.intent.AppliedRevision
	network, err := NewFleetNetworkService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(28001)
	draft, err := network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{
		NodeID: f.intent.NodeID, InstallationID: f.intent.InstallationID, ExpectedRevision: 1,
		Operations: []FleetNetworkOperation{{Section: "inbound", InboundID: f.f.inbounds[0].Id, Operation: "replace", Inbound: &FleetInboundNetwork{Port: &port}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.rule.Enabled, f.rule.ExpectedRevision = false, 1
	if _, err := f.service.PutRule(t.Context(), &f.f.admin, f.rule); err != nil {
		t.Fatal(err)
	}
	f.derive(t)
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", *f.intent.MaterializedPlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent); !errors.Is(err, ErrFleetNetworkApproval) {
		t.Fatalf("retirement implicitly approved pending network draft: %v", err)
	}
	var head model.FleetNodeNetworkHead
	var node model.FleetNode
	if err := f.f.db.First(&head, "node_id = ?", f.intent.NodeID).Error; err != nil || !head.Pending || head.CurrentRevisionID != draft.Revision.ID {
		t.Fatalf("retirement changed pending network approval: %+v %v", head, err)
	}
	if err := f.f.db.First(&node, "id = ?", f.intent.NodeID).Error; err != nil || node.DesiredRevision != base {
		t.Fatalf("held retirement published a manual draft: %+v %v", node, err)
	}
	preview, err := network.Preview(t.Context(), &f.f.admin, FleetNetworkPreviewRequest{
		NodeID: node.ID, InstallationID: node.InstallationID, ExpectedRevision: uint64(head.Revision), ExpectedDefaultsID: head.DesiredDefaultsID,
	})
	if err != nil || !preview.Ready {
		t.Fatalf("stopped retirement prevented explicit administrator preview: %+v %v", preview, err)
	}
	if _, err := network.Publish(t.Context(), &f.f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil || deployment == nil {
		t.Fatalf("explicit retirement publication=%+v %v", deployment, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: f.generation, PayloadSha256: deployment.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&f.intent, "id = ?", f.intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	settled, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || settled.Pending || settled.State != "retired" {
		t.Fatalf("explicit applied removal failed to settle held retirement: %+v %v", settled, err)
	}
}
