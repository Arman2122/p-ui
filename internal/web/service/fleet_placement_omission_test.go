package service

import (
	"testing"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestFleetPlacementIndexedOmissionRejectsGrantWithoutDescriptorsPostgres(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			bundle := fleetCreditBundle(t, f.f.db, f.active.Term, f.credit)
			bundle.Runtimes[0].Admissions = nil
			bundle.Runtimes[0].Grants[0].Policy.Enabled = enabled
			generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
			if err != nil {
				t.Fatal(err)
			}
			revision := fleetApplyProbe(t, f.f, f.f.nodes[0], generation, bundle)
			var node model.FleetNode
			if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil {
				t.Fatal(err)
			}
			intent := model.FleetPlacementIntent{NodeID: node.ID, InstallationID: node.InstallationID, RuntimeID: "xray", CoreID: "xray", ClientID: f.materialized.Client.ID}
			known, omitted, err := indexedFleetPlacementOmission(f.f.db, node, intent)
			if err != nil || known || omitted {
				t.Fatalf("unindexed base grant was mistaken for omitted authority: known=%v omitted=%v err=%v", known, omitted, err)
			}
			var metadata model.FleetPublishedRuntime
			if err := f.f.db.First(&metadata, "node_id = ? AND revision = ? AND runtime_id = 'xray'", node.ID, revision).Error; err != nil || metadata.GrantScopesComplete {
				t.Fatalf("grant without descriptor marked complete: %+v %v", metadata, err)
			}
			bundle.Runtimes[0].Grants = nil
			revision = fleetApplyProbe(t, f.f, f.f.nodes[0], generation, bundle)
			if err := f.f.db.First(&node, "id = ?", node.ID).Error; err != nil {
				t.Fatal(err)
			}
			known, omitted, err = indexedFleetPlacementOmission(f.f.db, node, intent)
			if err != nil || !known || !omitted {
				t.Fatalf("digest-verified empty runtime omission=%v %v %v", known, omitted, err)
			}
			if err := f.f.db.Model(&model.FleetPublishedRuntime{}).Where("node_id = ? AND revision = ? AND runtime_id = 'xray'", node.ID, revision).Update("grant_scopes_complete", false).Error; err != nil {
				t.Fatal(err)
			}
			known, omitted, err = indexedFleetPlacementOmission(f.f.db, node, intent)
			if err != nil || known || omitted {
				t.Fatalf("unknown old projection skipped full-payload proof: %v %v %v", known, omitted, err)
			}
		})
	}
}

func TestFleetPlacementOmissionUnknownProjectionFallsBackToExactPayloadPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	f.applyBaseline(t)
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
	var err error
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.f.store.Delivery(t.Context(), f.f.nodes[1], f.generation)
	if err != nil || delivery == nil {
		t.Fatalf("omission delivery=%+v %v", delivery, err)
	}
	if err := f.f.store.Observe(t.Context(), f.f.nodes[1], f.generation, &nodev1.ApplyResult{AuthorityGeneration: f.generation, Revision: delivery.Revision, PayloadSha256: delivery.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED}); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Model(&model.FleetPublishedRuntime{}).Where("node_id = ? AND revision = ?", f.f.nodes[1].NodeID, delivery.Revision).Update("grant_scopes_complete", false).Error; err != nil {
		t.Fatal(err)
	}
	f.intent, err = ProcessFleetPlacementIntent(t.Context(), f.f.db, f.intent)
	if err != nil || f.intent.Pending || f.intent.State != "retired" {
		t.Fatalf("unknown projection failed bounded exact fallback: %+v %v", f.intent, err)
	}
}
