package controlplane

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func deliverStop(t *testing.T, f stopFixture) (uint64, *nodev1.Deployment) {
	t.Helper()
	if _, err := f.store.StopConfiguration(t.Context(), f.node, f.request, stopValidators()); err != nil {
		t.Fatal(err)
	}
	generation, err := f.store.BeginSession(t.Context(), f.node, journalHello(t, f.store, f.node))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.store.Delivery(t.Context(), f.node, generation)
	if err != nil {
		t.Fatal(err)
	}
	return generation, deployment
}

func appliedStop(generation uint64, deployment *nodev1.Deployment) *nodev1.ApplyResult {
	return &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: deployment.Revision, PayloadSha256: deployment.PayloadSha256,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}
}

func TestAppliedStopCleanupKeepsNewerQueuedRowsPostgres(t *testing.T) {
	for _, table := range []string{"fleet_authority_updates", "fleet_credit_updates"} {
		t.Run(table, func(t *testing.T) {
			f := newStopFixture(t, false)
			generation, deployment := deliverStop(t, f)
			callback := "test:replace_stop_outbox_" + uuid.NewString()
			changed := false
			if err := f.db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
				if changed || tx.Statement.Table != table {
					return
				}
				changed = true
				// Clear callback predicates, then restore cloning for each nested query chain.
				mutation := tx.Session(&gorm.Session{NewDB: true, Initialized: true}).WithContext(tx.Statement.Context)
				_ = tx.AddError(replaceStopOutbox(mutation, f, table))
			}); err != nil {
				t.Fatal(err)
			}
			err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, deployment))
			_ = f.db.Callback().Delete().Remove(callback)
			if err != nil || !changed {
				t.Fatalf("queue replacement was not exercised: changed=%v err=%v", changed, err)
			}
			var row struct {
				Revision                  int64
				BaseConfigurationRevision int64
			}
			if err := f.db.Table(table).Where("grant_id = ?", f.delta.grant.ID).Take(&row).Error; err != nil || row.Revision != 3 || row.BaseConfigurationRevision != 2 {
				t.Fatalf("cleanup erased newer queued authority: %+v %v", row, err)
			}
			if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, deployment)); err != nil {
				t.Fatalf("lost applied response did not replay: %v", err)
			}
			var count int64
			if err := f.db.Table(table).Where("grant_id = ? AND revision = 3", f.delta.grant.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("replayed full ACK erased current-base work: %d %v", count, err)
			}
		})
	}
}

func replaceStopOutbox(tx *gorm.DB, f stopFixture, table string) error {
	if table == "fleet_credit_updates" {
		store := &Store{db: tx}
		grant, err := store.ReserveClientCredit(context.Background(), ClientCreditRequest{
			OperationID: uuid.NewString(), PlacementID: f.delta.placement.ID, ExpectedPlacementRevision: 1,
			ExpectedRevision: uint64(f.delta.client.Revision), ExpectedCredentialGeneration: 1, TermID: f.delta.term.ID, SourcePolicyDigest: f.delta.placement.SourcePolicyDigest, Bytes: 25,
		})
		if err != nil {
			return err
		}
		return tx.Model(&model.FleetCreditUpdate{}).Where("grant_id = ?", grant.ID).Updates(map[string]any{"base_configuration_revision": 2, "revision": grant.Revision, "credit_ceiling": grant.IssuedBytes, "acknowledged_revision": 0, "acknowledged_ceiling": 0}).Error
	}
	var row model.FleetAuthorityUpdate
	if err := tx.First(&row, "grant_id = ?", f.delta.grant.ID).Error; err != nil {
		return err
	}
	update, err := decodeAuthorityUpdate(row)
	if err != nil {
		return err
	}
	scope, err := publishedAuthorityMetadataTx(tx, f.node.NodeID, 2, "xray", f.delta.client.ID)
	if err != nil {
		return err
	}
	update.AuthorityRevision, update.CreditRevision, update.CreditCeiling = 3, 2, 175
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(update)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if err := tx.Model(&model.FleetRateShare{}).Where("id = ?", f.delta.grant.ID).Update("revision", 3).Error; err != nil {
		return err
	}
	return tx.Model(&row).Updates(map[string]any{
		"base_configuration_revision": 2, "base_payload_sha256": scope.PayloadSHA256, "revision": 3,
		"payload": encoded, "payload_sha256": digest[:], "acknowledged_revision": 0, "acknowledged_sha256": nil,
	}).Error
}

func TestAppliedStopCleanupRetainsUnprovenEqualRevisionPayloadPostgres(t *testing.T) {
	f := newStopFixture(t, false)
	generation, deployment := deliverStop(t, f)
	var row model.FleetAuthorityUpdate
	if err := f.db.First(&row, "grant_id = ?", f.delta.grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	update, err := decodeAuthorityUpdate(row)
	if err != nil {
		t.Fatal(err)
	}
	update.Upload.BitsPerSecond++
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if err := f.db.Model(&row).Updates(map[string]any{"payload": encoded, "payload_sha256": digest[:]}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, deployment)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.FleetAuthorityUpdate{}).Where("grant_id = ?", row.GrantID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("unproven equal-revision policy was discarded: %d %v", count, err)
	}
	if _, err := f.store.AcknowledgeAuthorityUpdates(t.Context(), f.node, f.batch); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale ACK adopted unproven policy: %v", err)
	}
}
