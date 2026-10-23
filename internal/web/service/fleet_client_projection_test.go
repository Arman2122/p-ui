package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetPublishedAuthorityScopesRemainIndexedAndBounded(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	placement := f.approve(t, materialized)
	bundle := fleetAdmissionBundle(materialized)
	second := bundle.Runtimes[0].Admissions[0]
	second.InboundInstanceID, second.InboundTag = materialized.Inbounds[1].InstanceID, "node-"+materialized.Inbounds[1].InstanceID
	bundle.Runtimes[0].Admissions = append(bundle.Runtimes[0].Admissions, second)
	node := f.nodes[0]
	generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := fleetApplyProbe(t, f, node, generation, bundle)
	var deployment model.FleetDeployment
	if err := f.db.First(&deployment, "node_id = ? AND revision = ?", node.NodeID, firstRevision).Error; err != nil {
		t.Fatal(err)
	}
	projected, err := configuration.Project(deployment.Payload, "xray", "xray")
	if err != nil {
		t.Fatal(err)
	}
	projectedDigest := sha256.Sum256(projected)
	expected, err := configuration.DescriptorScopes(bundle.Runtimes[0])
	if err != nil {
		t.Fatal(err)
	}
	callback := "reject-deployment-blob-" + uuid.NewString()
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "fleet_deployments" && len(tx.Statement.Selects) == 0 {
			_ = tx.AddError(errors.New("authorization reread full deployment payload"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.db.Callback().Query().Remove(callback) }()
	scope, err := controlplane.PublishedAuthorityScopeTx(f.db, node.NodeID, firstRevision, "xray", materialized.Client.ID)
	if err != nil || scope.CoreID != "xray" || scope.BaseGrant != nil || len(scope.Bindings) != 2 || !bytes.Equal(scope.PayloadSHA256, projectedDigest[:]) ||
		!bytes.Equal(scope.Descriptor.SHA256, expected[materialized.Client.ID].SHA256) || scope.Descriptor.PlacementID != placement.ID {
		t.Fatalf("indexed descriptor scope=%+v err=%v", scope, err)
	}
	metadata, err := controlplane.PublishedAuthorityScopeTx(f.db, node.NodeID, firstRevision, "xray", "")
	if err != nil || metadata.CoreID != "xray" || len(metadata.Bindings) != 0 || metadata.BaseGrant != nil || !bytes.Equal(metadata.PayloadSHA256, projectedDigest[:]) {
		t.Fatalf("runtime-only indexed scope=%+v err=%v", metadata, err)
	}
	request := fleetDemand(materialized)
	request.DeploymentRevision = firstRevision
	if descriptor, digest, err := controlplane.DeployedAdmissionTx(f.db, placement, request); err != nil || descriptor.ClientID != materialized.Client.ID || !bytes.Equal(digest, deployment.Digest) {
		t.Fatalf("indexed authentication scope fetched deployment payload: %+v %v", descriptor, err)
	}
	if err := f.db.Callback().Query().Remove(callback); err != nil {
		t.Fatal(err)
	}
	active := f.activate(t, placement)
	credit, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle.Runtimes[0].Grants = []governor.Grant{{
		ID: credit.Grant.ID, ClientID: materialized.Client.ID, RateRevision: uint64(credit.Rates.Revision),
		Policy: limits.Policy{
			Term: active.Term.ID, Revision: 1, Enabled: true, CreditCeiling: 100, ExpiresUnixMilli: active.Term.ExpiresUnixMilli,
			Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
		},
	}}
	rollback := errors.New("restore projection fixture")
	for _, kind := range []string{"credit", "rate"} {
		if err := f.db.Transaction(func(tx *gorm.DB) error {
			candidate := bundle
			candidate.Runtimes = append([]configuration.Runtime(nil), bundle.Runtimes...)
			candidate.Runtimes[0].Grants = append([]governor.Grant(nil), bundle.Runtimes[0].Grants...)
			if kind == "credit" {
				candidate.Runtimes[0].Grants[0].Policy.CreditCeiling++
			} else {
				candidate.Runtimes[0].Grants[0].Policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				return err
			}
			store, err := controlplane.NewStore(tx)
			if err != nil {
				return err
			}
			if _, err := store.Publish(t.Context(), node.NodeID, encoded); !errors.Is(err, controlplane.ErrScope) {
				t.Errorf("raw typed publication bypassed %s ledger=%v", kind, err)
			}
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	bundle.Runtimes[0].Grants[0].Policy.Revision = 99
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	secondRevision, err := f.store.Publish(t.Context(), node.NodeID, raw)
	if err != nil {
		t.Fatal(err)
	}
	var canonical model.FleetDeployment
	if err := f.db.First(&canonical, "node_id = ? AND revision = ?", node.NodeID, secondRevision).Error; err != nil {
		t.Fatal(err)
	}
	stored, err := configuration.Decode(canonical.Payload)
	if err != nil || len(stored.Runtimes) != 1 || len(stored.Runtimes[0].Grants) != 1 {
		t.Fatalf("raw typed publication was not canonical: %+v %v", stored, err)
	}
	policy := stored.Runtimes[0].Grants[0].Policy
	if policy.Revision != secondRevision || policy.CreditRevision != uint64(credit.Grant.Revision) || policy.AuthorityRevision != uint64(credit.Rates.Revision) {
		t.Fatalf("raw publication stored noncanonical revisions=%+v", policy)
	}
	if replay, err := f.store.Publish(t.Context(), node.NodeID, raw); err != nil || replay != secondRevision {
		t.Fatalf("raw canonical replay changed deployment=%d err=%v", replay, err)
	}
	old, err := controlplane.PublishedAuthorityScopeTx(f.db, node.NodeID, firstRevision, "xray", materialized.Client.ID)
	if err != nil || old.BaseGrant != nil {
		t.Fatalf("new ledger grant rewrote immutable auth-only base=%+v err=%v", old, err)
	}
	funded, err := controlplane.PublishedAuthorityScopeTx(f.db, node.NodeID, secondRevision, "xray", materialized.Client.ID)
	if err != nil || funded.BaseGrant == nil || funded.BaseGrant.ID != credit.Grant.ID || funded.BaseGrant.Policy.Term != active.Term.ID || funded.BaseGrant.Policy.Revision != secondRevision {
		t.Fatalf("published base grant was not preserved=%+v err=%v", funded, err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetClient{}).Where("id = ?", materialized.Client.ID).Update("credential_generation", 2).Error; err != nil {
			return err
		}
		published, err := controlplane.PublishedAuthorityScopeTx(tx, node.NodeID, secondRevision, "xray", materialized.Client.ID)
		if err != nil || published.Descriptor.CredentialGeneration != 1 {
			t.Fatalf("old published scope was replaced by mutable credential state=%+v err=%v", published, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	bundle.Runtimes[0].Configuration = json.RawMessage(`{"changed":true}`)
	thirdRevision, err := f.store.PublishConfiguration(t.Context(), node, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlplane.PublishedAuthorityScopeTx(f.db, node.NodeID, secondRevision, "xray", materialized.Client.ID); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("superseded unsent projection was retained=%v", err)
	}
	delivery, err := f.store.Delivery(t.Context(), node, generation)
	if err != nil || delivery.Revision != thirdRevision {
		t.Fatalf("latest indexed delivery=%+v err=%v", delivery, err)
	}
	if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{
		Revision: delivery.Revision, AuthorityGeneration: generation, PayloadSha256: delivery.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []any{&model.FleetPublishedRuntime{}, &model.FleetPublishedClientAdmission{}, &model.FleetPublishedAdmissionBinding{}} {
		var stale int64
		if err := f.db.Model(table).Where("node_id = ? AND revision <> ?", node.NodeID, thirdRevision).Count(&stale).Error; err != nil || stale != 0 {
			t.Fatalf("old admission projection grew without bound %T=%d err=%v", table, stale, err)
		}
	}
}
