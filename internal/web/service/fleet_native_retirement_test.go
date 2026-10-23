package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func nativeObserveDeployment(t *testing.T, f fleetClientFixture) configuration.Bundle {
	t.Helper()
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configuration.Decode(delivery.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), f.nodes[0], generation, &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: delivery.Revision, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestFleetNativeAddressReuseRequiresAppliedRemovalAndEveryStoppedGrant(t *testing.T) {
	f, service, preview := fleetNativePublicationFixture(t, model.OCServ)
	request := FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}
	funding := []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2000000, BurstBytes: 65536}}}}
	if _, err := service.PublishFunded(t.Context(), &f.admin, request, funding); err != nil {
		t.Fatal(err)
	}
	bundle := nativeObserveDeployment(t, f)
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{OperationID: uuid.NewString(), PlacementID: funding[0].PlacementID, Kind: controlplane.TrafficReset, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("native drain=%+v %v", pending, err)
	}
	previewRequest := FleetNetworkPreviewRequest{NodeID: preview.NodeID, InstallationID: preview.InstallationID, ExpectedRevision: preview.NodeRevision.Revision, ExpectedDefaultsID: preview.Defaults.ID}
	blocked, err := service.Preview(t.Context(), &f.admin, previewRequest)
	if err != nil || blocked.Ready {
		t.Fatalf("unstopped native removal ready=%+v %v", blocked, err)
	}
	grant := bundle.Runtimes[0].Grants[0]
	share, err := f.store.RequestRateShare(t.Context(), controlplane.RateRequest{OperationID: uuid.NewString(), GrantID: grant.ID, BudgetID: grant.ClientID, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, RuntimeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	grant.Policy.Enabled, grant.Policy.Upload, grant.Policy.Download = false, limits.Rate{}, limits.Rate{}
	grant.RateRevision, grant.Policy.AuthorityRevision = uint64(share.Revision), uint64(share.Revision)
	bundle.Runtimes[0].Grants[0] = grant
	if _, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], bundle); err != nil {
		t.Fatal(err)
	}
	nativeObserveDeployment(t, f)
	removal, err := service.Preview(t.Context(), &f.admin, previewRequest)
	if err != nil || !removal.Ready || len(removal.NativeBindings) != 0 || len(removal.Runtimes) != 0 {
		t.Fatalf("stopped native removal=%+v %v", removal, err)
	}
	if _, err := service.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: removal.ID}); err != nil {
		t.Fatal(err)
	}
	var node model.FleetNode
	if err := f.db.First(&node, "id = ?", f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if active, retired, err := loadFleetNativeBindings(f.db, node); err != nil || len(active) != 1 || len(retired) != 0 {
		t.Fatalf("unapplied removal released address=%v %v %v", active, retired, err)
	}
	if removed := nativeObserveDeployment(t, f); len(removed.Runtimes) != 0 {
		t.Fatal("removal retained runtime")
	}
	if err := f.db.First(&node, "id = ?", node.ID).Error; err != nil {
		t.Fatal(err)
	}
	var compilation model.FleetNetworkCompilation
	if err := f.db.First(&compilation, "id = ?", removal.ID).Error; err != nil {
		t.Fatal(err)
	}
	var manifest FleetNetworkManifest
	if err := json.Unmarshal([]byte(compilation.Manifest), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*gorm.DB, *model.FleetNode) error
	}{
		{"node-offline-before-removal", func(_ *gorm.DB, node *model.FleetNode) error { node.AppliedRevision = 1; return nil }},
		{"another-config-pending", func(_ *gorm.DB, node *model.FleetNode) error { node.DesiredRevision++; return nil }},
		{"missing-stop", func(tx *gorm.DB, _ *model.FleetNode) error {
			return tx.Delete(&model.FleetGrantStop{}, "grant_id = ?", grant.ID).Error
		}},
		{"unapplied-stop", func(tx *gorm.DB, _ *model.FleetNode) error {
			return tx.Model(&model.FleetRateShare{}).Where("id = ?", grant.ID).Update("applied_revision", 1).Error
		}},
		{"future-stop", func(tx *gorm.DB, _ *model.FleetNode) error {
			return tx.Model(&model.FleetGrantStop{}).Where("grant_id = ?", grant.ID).Update("deployment_revision", node.AppliedRevision+1).Error
		}},
		{"rate-still-reserved", func(tx *gorm.DB, _ *model.FleetNode) error {
			data, _ := json.Marshal(funding[0].Rates)
			return tx.Model(&model.FleetRateShare{}).Where("id = ?", grant.ID).Update("outstanding", string(data)).Error
		}},
		{"old-owner-still-present", func(tx *gorm.DB, _ *model.FleetNode) error {
			manifest.NativeBindings = preview.NativeBindings
			data, _ := json.Marshal(manifest)
			return tx.Model(&compilation).Update("manifest", string(data)).Error
		}},
		{"foreign-installation", func(tx *gorm.DB, _ *model.FleetNode) error {
			return tx.Model(&model.FleetRateShare{}).Where("id = ?", grant.ID).Update("installation_id", uuid.NewString()).Error
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rollback := errors.New("restore retirement fixture")
			err := f.db.Transaction(func(tx *gorm.DB) error {
				current := node
				if err := test.change(tx, &current); err != nil {
					return err
				}
				active, retired, err := loadFleetNativeBindings(tx, current)
				if err != nil || len(active) != 1 || len(retired) != 0 {
					t.Fatalf("unsafe address release=%d %v %v", len(active), retired, err)
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	active, retired, err := loadFleetNativeBindings(f.db, node)
	if err != nil || len(active) != 0 || len(retired) != 1 || retired[0] != preview.NativeBindings[0].ID {
		t.Fatalf("proved address retirement=%+v %v %v", active, retired, err)
	}
	var count int64
	if err := f.db.Model(&model.FleetNativeBinding{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("preview deleted claims=%d %v", count, err)
	}
	rollback := errors.New("publication rolled back")
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := retireFleetNativeBindings(tx, node); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.FleetNativeBinding{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rollback released claim=%d %v", count, err)
	}
	cleanup, err := service.Preview(t.Context(), &f.admin, previewRequest)
	if err != nil || !cleanup.Ready {
		t.Fatalf("retired address cleanup preview=%+v %v", cleanup, err)
	}
	if _, err := service.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: cleanup.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.FleetNativeBinding{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("completed publication kept retired claim=%d %v", count, err)
	}
}
