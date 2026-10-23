package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetNetworkPublicationFundsAtomicallyAndRevalidatesPreview(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	service, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := service.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	previewRequest := FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID}
	preview, err := service.Preview(t.Context(), &f.admin, previewRequest)
	if err != nil || !preview.Ready {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	request := FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}
	funding := []FleetNetworkFunding{{PlacementID: placement.ID, Bytes: 200, Rates: controlplane.Rates{
		Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2000000, BurstBytes: 65536},
	}}}
	rollback := errors.New("restore changed source")
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update("security", "changed-after-preview").Error; err != nil {
			return err
		}
		changed, err := NewFleetNetworkService(tx)
		if err != nil {
			return err
		}
		if _, err := changed.PublishFunded(t.Context(), &f.admin, request, funding); !errors.Is(err, ErrFleetNetworkDependency) {
			t.Fatalf("stale normalized source published=%v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		var old model.FleetNetworkCompilation
		if err := tx.First(&old, "id = ?", preview.ID).Error; err != nil {
			return err
		}
		var definitions []nodeconfig.RuntimeDefinition
		if err := json.Unmarshal([]byte(old.RuntimeDefinitions), &definitions); err != nil {
			return err
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(definitions[0].Configuration, &config); err != nil {
			return err
		}
		var inbounds []map[string]json.RawMessage
		if err := json.Unmarshal(config["inbounds"], &inbounds); err != nil {
			return err
		}
		inbounds[0]["port"] = json.RawMessage(`24000`)
		config["inbounds"], err = json.Marshal(inbounds)
		if err != nil {
			return err
		}
		definitions[0].Configuration, err = json.Marshal(config)
		if err != nil {
			return err
		}
		encoded, digest, err := canonicalFleetArtifact(definitions, 32<<20)
		if err != nil {
			return err
		}
		if bytes.Equal(digest, old.Digest) {
			t.Fatal("stale compiler fixture did not change its listener")
		}
		if err := tx.Model(&old).Updates(map[string]any{"runtime_definitions": string(encoded), "digest": digest}).Error; err != nil {
			return err
		}
		changed, err := NewFleetNetworkService(tx)
		if err != nil {
			return err
		}
		if _, err := changed.PublishFunded(t.Context(), &f.admin, request, funding); !errors.Is(err, ErrFleetNetworkRevision) {
			t.Errorf("self-consistent stale compiler output published=%v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`CREATE FUNCTION reject_fleet_network_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'publication fault'; END $$`,
		`CREATE TRIGGER reject_fleet_network_publication BEFORE INSERT ON fleet_network_publications FOR EACH ROW EXECUTE FUNCTION reject_fleet_network_publication()`,
	} {
		if err := f.db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.PublishFunded(t.Context(), &f.admin, request, funding); err == nil || !bytes.Contains([]byte(err.Error()), []byte("publication fault")) {
		t.Fatalf("publication fault did not reach post-allocation commit=%v", err)
	}
	var grantCount, deploymentCount int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grantCount).Error; err != nil || grantCount != 0 {
		t.Fatalf("failed publication leaked credit grants=%d err=%v", grantCount, err)
	}
	if err := f.db.Model(&model.FleetDeployment{}).Count(&deploymentCount).Error; err != nil || deploymentCount != 0 {
		t.Fatalf("failed publication committed a deployment=%d err=%v", deploymentCount, err)
	}
	var budget model.FleetBudget
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 0 {
		t.Fatalf("failed publication leaked reserved bytes=%+v err=%v", budget, err)
	}
	if err := f.db.Exec(`DROP TRIGGER reject_fleet_network_publication ON fleet_network_publications`).Error; err != nil {
		t.Fatal(err)
	}
	published, err := service.PublishFunded(t.Context(), &f.admin, request, funding)
	if err != nil || published.Replayed || published.DeploymentRevision != 1 {
		t.Fatalf("publication=%+v err=%v", published, err)
	}
	replay, err := service.PublishFunded(t.Context(), &f.admin, request, funding)
	if err != nil || !replay.Replayed || replay.DeploymentRevision != published.DeploymentRevision {
		t.Fatalf("lost publication reply=%+v err=%v", replay, err)
	}
	changedFunding := append([]FleetNetworkFunding(nil), funding...)
	changedFunding[0].Bytes++
	if _, err := service.PublishFunded(t.Context(), &f.admin, request, changedFunding); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("same operation changed its funding=%v", err)
	}
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configuration.Decode(delivery.Payload)
	if err != nil || len(bundle.Runtimes) != 1 || len(bundle.Runtimes[0].Grants) != 1 || bundle.Runtimes[0].Grants[0].Policy.CreditCeiling != 200 || bundle.Runtimes[0].Grants[0].Policy.Upload != funding[0].Rates.Upload {
		t.Fatalf("published authority=%+v err=%v", bundle, err)
	}
	if err := f.store.Observe(t.Context(), f.nodes[0], generation, &nodev1.ApplyResult{
		Revision: delivery.Revision, AuthorityGeneration: generation, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Preview(t.Context(), &f.admin, previewRequest)
	if err != nil || !fresh.Ready {
		t.Fatalf("funded preview=%+v err=%v", fresh, err)
	}
	grant := bundle.Runtimes[0].Grants[0]
	fleetAuthorityReport(t, f, 1, time.Now(), &nodev1.GrantAuthority{
		GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.Policy.Term, PolicyRevision: delivery.Revision,
		CreditCeiling: 200, ReservedHighWater: 180, AdmissionHighWater: 180, AdmissionKnown: true,
		SpendableKnown: true, SpendableBytes: 20, AuthorizationActive: true,
	})
	if err := f.db.Where("grant_id = ?", grant.ID).Delete(&model.FleetCreditCandidate{}).Error; err != nil {
		t.Fatal(err)
	}
	unchanged, err := service.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: fresh.ID})
	if err != nil || unchanged.DeploymentRevision != published.DeploymentRevision {
		t.Fatalf("same settled graph changed authority=%+v err=%v", unchanged, err)
	}
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 200 {
		t.Fatalf("publication replay multiplied reservation=%+v err=%v", budget, err)
	}
	var candidates int64
	if err := f.db.Model(&model.FleetCreditCandidate{}).Where("grant_id = ?", grant.ID).Count(&candidates).Error; err != nil || candidates != 1 {
		t.Fatalf("unchanged published graph failed to wake credit reconciliation=%d err=%v", candidates, err)
	}
}
