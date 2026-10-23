package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type stopSubject struct {
	client    model.FleetClient
	term      model.FleetTerm
	placement model.FleetPlacement
	instance  string
	grant     model.FleetGrant
	share     model.FleetRateShare
}

type stopFixture struct {
	store                 *Store
	db                    *gorm.DB
	node                  identity.Principal
	native, stream, delta stopSubject
	base                  model.FleetDeployment
	batch                 *nodev1.AuthorityUpdateBatch
	creditBatch           *nodev1.CreditUpdateBatch
	request               StopConfigurationRequest
}

func newStopSubject(t *testing.T, store *Store, db *gorm.DB, node identity.Principal, runtime string, sourceID int) stopSubject {
	t.Helper()
	client := managedClient(t, store, db)
	active, err := store.Transition(t.Context(), activation(client))
	if err != nil {
		t.Fatal(err)
	}
	client = active.Client
	client.Owned = true
	if err := db.Model(&client).Update("owned", true).Error; err != nil {
		t.Fatal(err)
	}
	placement := model.FleetPlacement{
		ID: uuid.NewString(), ClientID: client.ID, NodeID: node.NodeID, InstallationID: node.InstallationID, RuntimeID: runtime, CoreID: runtime, Revision: 1, Enabled: true, CredentialGeneration: 1,
		SourcePolicyDigest: sourceDigest("activation snapshot"), DefinitionDigest: sourceDigest("definition"), GraphDigest: sourceDigest("graph"),
	}
	if err := db.Create(&placement).Error; err != nil {
		t.Fatal(err)
	}
	instance := uuid.NewString()
	if err := db.Create(&model.FleetPlacementInbound{PlacementID: placement.ID, InstanceID: instance, PanelInbound: &sourceID}).Error; err != nil {
		t.Fatal(err)
	}
	subject := stopSubject{client: client, term: active.Term, placement: placement, instance: instance}
	subject.grant = reserveStopCredit(t, store, subject, 100)
	subject.share, err = store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: subject.grant.ID, BudgetID: client.ID,
		NodeID: node.NodeID, InstallationID: node.InstallationID, RuntimeID: runtime, Desired: limitedRates(10000, 1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

func reserveStopCredit(t *testing.T, store *Store, subject stopSubject, amount int64) model.FleetGrant {
	t.Helper()
	grant, err := store.ReserveClientCredit(t.Context(), ClientCreditRequest{
		OperationID: uuid.NewString(), PlacementID: subject.placement.ID, ExpectedPlacementRevision: 1,
		ExpectedRevision: uint64(subject.client.Revision), ExpectedCredentialGeneration: 1, TermID: subject.term.ID, SourcePolicyDigest: subject.placement.SourcePolicyDigest, Bytes: amount,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (s stopSubject) descriptor() admission.Descriptor {
	return admission.Descriptor{ClientID: s.client.ID, CredentialGeneration: 1, PlacementID: s.placement.ID, PlacementRevision: 1, InboundInstanceID: s.instance, InboundTag: "frozen-" + s.instance}
}

func (s stopSubject) authority() governor.Grant {
	rates, _ := decodeRates(s.share.Desired)
	return governor.Grant{ID: s.grant.ID, ClientID: s.client.ID, RateRevision: uint64(s.share.Revision), Policy: limits.Policy{
		Term: s.term.ID, Revision: 1, AuthorityRevision: uint64(s.share.Revision), CreditRevision: uint64(s.grant.Revision), CreditCeiling: uint64(s.grant.IssuedBytes), Enabled: true,
		ExpiresUnixMilli: s.term.ExpiresUnixMilli, Upload: rates.Upload, Download: rates.Download,
	}}
}

func newStopFixture(t *testing.T, acknowledged bool) stopFixture {
	t.Helper()
	store, db, nodes, _, _, _ := setupLedger(t)
	f := stopFixture{store: store, db: db, node: nodes[0]}
	bootstrapTestRuntime(t, store, f.node, "native")
	bootstrapTestRuntime(t, store, f.node, "xray")
	f.native, f.stream, f.delta = newStopSubject(t, store, db, f.node, "native", 1), newStopSubject(t, store, db, f.node, "xray", 2), newStopSubject(t, store, db, f.node, "xray", 3)
	asset := []byte("frozen private asset")
	hash := sha256.Sum256(asset)
	bundle := configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{
		{ID: "native", Core: "native", Configuration: json.RawMessage(`{"network":"frozen-native"}`), Grants: []governor.Grant{f.native.authority()}},
		{
			ID: "xray", Core: "xray", Configuration: json.RawMessage(`{"network":"frozen-stream"}`), Grants: []governor.Grant{f.stream.authority()}, Admissions: []admission.Descriptor{f.stream.descriptor(), f.delta.descriptor()},
			FileAssets: []configuration.FileAsset{{ID: "certificate", SHA256: hex.EncodeToString(hash[:]), Data: asset}},
		},
	}}
	applyBundle(t, store, f.node, bundle)
	if err := db.First(&f.base, "node_id = ? AND revision = 1", f.node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAuthorityUpdate(t.Context(), f.delta.grant.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	f.batch = stopAuthorityBatch(t, f, f.delta.grant.ID)
	if _, err := store.AcknowledgeAuthorityUpdates(t.Context(), f.node, f.batch); err != nil {
		t.Fatal(err)
	}
	var err error
	f.delta.share, err = store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: f.delta.grant.ID, BudgetID: f.delta.client.ID,
		NodeID: f.node.NodeID, InstallationID: f.node.InstallationID, RuntimeID: "xray", Desired: limitedRates(8000, 800),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAuthorityUpdate(t.Context(), f.delta.grant.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	f.batch = stopAuthorityBatch(t, f, f.delta.grant.ID)
	f.delta.grant = reserveStopCredit(t, store, f.delta, 75)
	if err := store.QueueCreditUpdate(t.Context(), f.delta.grant.ID, 1); err != nil {
		t.Fatal(err)
	}
	f.creditBatch = &nodev1.CreditUpdateBatch{
		NodeId: f.node.NodeID, InstallationId: f.node.InstallationID, AgentStateJournalId: f.batch.AgentStateJournalId,
		RuntimeId: "xray", StateJournalId: f.batch.StateJournalId, CreditJournalId: f.batch.CreditJournalId, BaseConfigurationRevision: 1,
		Updates: []*nodev1.CreditUpdate{{GrantId: f.delta.grant.ID, ClientId: f.delta.client.ID, TermId: f.delta.term.ID, CreditRevision: uint64(f.delta.grant.Revision), CreditCeiling: uint64(f.delta.grant.IssuedBytes)}},
	}
	if acknowledged {
		if _, err := store.AcknowledgeAuthorityUpdates(t.Context(), f.node, f.batch); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcknowledgeCredit(t.Context(), f.node, f.creditBatch); err != nil {
			t.Fatal(err)
		}
	}
	f.request = StopConfigurationRequest{OperationID: uuid.NewString(), ExpectedBaseRevision: 1, ExpectedBaseSHA256: f.base.Digest, GrantIDs: []string{f.native.grant.ID, f.stream.grant.ID}}
	return f
}

func stopAuthorityBatch(t *testing.T, f stopFixture, id string) *nodev1.AuthorityUpdateBatch {
	t.Helper()
	var row model.FleetAuthorityUpdate
	if err := f.db.First(&row, "grant_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	update, err := decodeAuthorityUpdate(row)
	if err != nil {
		t.Fatal(err)
	}
	batch := authorityBatch(row)
	batch.Updates = []*nodev1.AuthorityUpdate{update}
	return batch
}

func stopValidators() StopConfigurationValidators {
	return StopConfigurationValidators{
		Authority: func(_ context.Context, _ *gorm.DB, rows []model.FleetAuthorityUpdate) (map[string]bool, error) {
			allowed := map[string]bool{}
			for _, row := range rows {
				allowed[row.GrantID] = true
			}
			return allowed, nil
		},
		Credit: func(_ context.Context, _ *gorm.DB, rows []model.FleetCreditUpdate) (map[string]bool, error) {
			allowed := map[string]bool{}
			for _, row := range rows {
				allowed[row.GrantID] = true
			}
			return allowed, nil
		},
	}
}

func stopOutboxCounts(t *testing.T, f stopFixture, want int64) {
	t.Helper()
	for _, table := range []any{&model.FleetAuthorityUpdate{}, &model.FleetCreditUpdate{}} {
		var count int64
		if err := f.db.Model(table).Where("node_id = ?", f.node.NodeID).Count(&count).Error; err != nil || count != want {
			t.Fatalf("%T outbox count=%d want=%d: %v", table, count, want, err)
		}
	}
}

func stopTestRates(t *testing.T, encoded string) Rates {
	t.Helper()
	rates, err := decodeRates(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestStopConfigurationPreservesDeltaAuthorityAndFrozenGraphPostgres(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "acknowledged"}[acknowledged], func(t *testing.T) {
			f := newStopFixture(t, acknowledged)
			measured := ReserveRequest{GrantID: f.native.grant.ID, ClientID: f.native.client.ID, TermID: f.native.term.ID, RuntimeID: "native"}
			if _, err := f.store.Ingest(t.Context(), f.node, batch(f.node, measured, 1, 10, 20)); err != nil {
				t.Fatal(err)
			}
			for _, target := range []stopSubject{f.native, f.stream} {
				if err := f.db.Model(&target.placement).Updates(map[string]any{"enabled": false, "revision": 2}).Error; err != nil {
					t.Fatal(err)
				}
				if acknowledged {
					if err := f.db.Model(&target.client).Update("panel_client_id", nil).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			observation := model.FleetGrantAuthority{GrantID: f.delta.grant.ID, NodeID: f.node.NodeID, RuntimeID: "xray", ReportSequence: 1, PolicyRevision: 1, AuthorityRevision: f.delta.share.Revision, CreditCeiling: 175, ReservedHighWater: 165, AdmissionHighWater: 165, AdmissionKnown: true, SpendableBytes: 10, SpendableKnown: true, AuthorizationActive: true, ReceivedUnixMilli: time.Now().UnixMilli()}
			if err := f.db.Create(&observation).Error; err != nil {
				t.Fatal(err)
			}
			var budgets []model.FleetBudget
			if err := f.db.Order("id").Find(&budgets).Error; err != nil {
				t.Fatal(err)
			}
			var grantsBefore []model.FleetGrant
			if err := f.db.Order("id").Find(&grantsBefore).Error; err != nil {
				t.Fatal(err)
			}
			result, err := f.store.StopConfiguration(t.Context(), f.node, f.request, stopValidators())
			if err != nil || result.Revision != 2 || result.Replayed {
				t.Fatalf("stop publication=%+v %v", result, err)
			}
			stopOutboxCounts(t, f, 1)
			for _, target := range []stopSubject{f.native, f.stream} {
				var share model.FleetRateShare
				if err := f.db.First(&share, "id = ?", target.grant.ID).Error; err != nil || stopTestRates(t, share.Outstanding) != stopTestRates(t, target.share.Outstanding) || share.AppliedRevision != target.share.Revision {
					t.Fatalf("publication released unacknowledged rate authority: %+v %v", share, err)
				}
			}
			var stopped int64
			if err := f.db.Model(&model.FleetGrantStop{}).Count(&stopped).Error; err != nil || stopped != 0 {
				t.Fatalf("publication invented an applied stop: %d %v", stopped, err)
			}
			var candidates int64
			if err := f.db.Model(&model.FleetCreditCandidate{}).Where("grant_id = ?", f.delta.grant.ID).Count(&candidates).Error; err != nil || candidates != 1 {
				t.Fatalf("unrelated refill was stranded: %d %v", candidates, err)
			}
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					replay, err := f.store.StopConfiguration(t.Context(), f.node, f.request, StopConfigurationValidators{})
					if err != nil || !replay.Replayed || replay.Revision != result.Revision || !bytes.Equal(replay.PayloadSHA256, result.PayloadSHA256) {
						t.Errorf("stop retry changed publication: %+v %v", replay, err)
					}
				})
			}
			wg.Wait()
			if _, err := f.store.AcknowledgeAuthorityUpdates(t.Context(), f.node, f.batch); !errors.Is(err, ErrRevision) {
				t.Fatalf("old-base authority ACK changed new publication: %v", err)
			}
			if _, err := f.store.AcknowledgeCredit(t.Context(), f.node, f.creditBatch); !errors.Is(err, ErrRevision) {
				t.Fatalf("old-base credit ACK changed new publication: %v", err)
			}
			generation, err := f.store.BeginSession(t.Context(), f.node, journalHello(t, f.store, f.node))
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := f.store.Delivery(t.Context(), f.node, generation)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := configuration.Decode(f.base.Payload)
			after, err := configuration.Decode(deployment.Payload)
			if err != nil {
				t.Fatal(err)
			}
			foundDelta := false
			for i, runtime := range after.Runtimes {
				if !bytes.Equal(runtime.Configuration, before.Runtimes[i].Configuration) || !reflect.DeepEqual(runtime.Admissions, before.Runtimes[i].Admissions) || !reflect.DeepEqual(runtime.FileAssets, before.Runtimes[i].FileAssets) {
					t.Fatal("stop publication changed frozen networking, credentials, descriptors or assets")
				}
				for _, grant := range runtime.Grants {
					if grant.ID == f.delta.grant.ID {
						want := f.delta.authority()
						want.Policy.Revision = 2
						if grant != want {
							t.Fatalf("unrelated delta authority rolled back: got=%+v want=%+v", grant, want)
						}
						foundDelta = true
					} else if grant.Policy.Enabled || grant.Policy.Upload != (limits.Rate{}) || grant.Policy.Download != (limits.Rate{}) || grant.Policy.CreditCeiling != 100 {
						t.Fatalf("selected grant not stopped without changing credit: %+v", grant)
					}
				}
			}
			if !foundDelta {
				t.Fatal("initial authority delivered only by delta disappeared from the full bundle")
			}
			applied := &nodev1.ApplyResult{AuthorityGeneration: generation, Revision: deployment.Revision, PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_RECEIVED}
			if err := f.store.Observe(t.Context(), f.node, generation, applied); err != nil {
				t.Fatal(err)
			}
			stopOutboxCounts(t, f, 1)
			applied.Stage = nodev1.ApplyStage_APPLY_STAGE_APPLIED
			if err := f.store.Observe(t.Context(), f.node, generation, applied); err != nil {
				t.Fatal(err)
			}
			stopOutboxCounts(t, f, 0)
			if err := f.db.Model(&model.FleetGrantStop{}).Count(&stopped).Error; err != nil || stopped != 2 {
				t.Fatalf("applied stop evidence=%d %v", stopped, err)
			}
			var afterBudgets []model.FleetBudget
			if err := f.db.Order("id").Find(&afterBudgets).Error; err != nil || !reflect.DeepEqual(budgets, afterBudgets) {
				t.Fatalf("stop changed quota usage or reservations: %v", err)
			}
			var grantsAfter []model.FleetGrant
			if err := f.db.Order("id").Find(&grantsAfter).Error; err != nil || !reflect.DeepEqual(grantsBefore, grantsAfter) {
				t.Fatalf("stop changed issued or measured grant accounting: %v", err)
			}
			f.delta.grant = reserveStopCredit(t, f.store, f.delta, 25)
			if err := f.store.QueueCreditUpdate(t.Context(), f.delta.grant.ID, 2); err != nil {
				t.Fatalf("new baseline stranded subsequent refill: %v", err)
			}
			var next model.FleetCreditUpdate
			if err := f.db.First(&next, "grant_id = ?", f.delta.grant.ID).Error; err != nil || next.BaseConfigurationRevision != 2 || next.CreditCeiling != 200 || next.AcknowledgedRevision != 0 {
				t.Fatalf("new credit did not bind B+1: %+v %v", next, err)
			}
		})
	}
}

func TestStopConfigurationRefusesUnprovenUnrelatedAuthorityPostgres(t *testing.T) {
	for _, failure := range []string{"missing validators", "blocked", "authority denied", "credit denied"} {
		t.Run(failure, func(t *testing.T) {
			f := newStopFixture(t, false)
			validators := stopValidators()
			switch failure {
			case "missing validators":
				validators = StopConfigurationValidators{}
			case "blocked":
				if err := f.db.Model(&model.FleetAuthorityUpdate{}).Where("grant_id = ?", f.delta.grant.ID).Update("delivery_blocked", true).Error; err != nil {
					t.Fatal(err)
				}
			case "authority denied":
				validators.Authority = func(context.Context, *gorm.DB, []model.FleetAuthorityUpdate) (map[string]bool, error) {
					return nil, nil
				}
			case "credit denied":
				validators.Credit = func(context.Context, *gorm.DB, []model.FleetCreditUpdate) (map[string]bool, error) { return nil, nil }
			}
			if _, err := f.store.StopConfiguration(t.Context(), f.node, f.request, validators); !errors.Is(err, ErrStopComposition) {
				t.Fatalf("ambiguous unrelated authority accepted: %v", err)
			}
			var node model.FleetNode
			var share model.FleetRateShare
			if err := f.db.First(&node, "id = ?", f.node.NodeID).Error; err != nil || node.DesiredRevision != 1 {
				t.Fatalf("failed composition changed deployment: %+v %v", node, err)
			}
			if err := f.db.First(&share, "id = ?", f.native.grant.ID).Error; err != nil || share.Revision != 1 || stopTestRates(t, share.Desired) != stopTestRates(t, f.native.share.Desired) {
				t.Fatalf("failed composition changed selected authority: %+v %v", share, err)
			}
			var count int64
			if err := f.db.Model(&model.FleetOperation{}).Where("id = ?", f.request.OperationID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("failed composition retained a receipt: %d %v", count, err)
			}
		})
	}
}
