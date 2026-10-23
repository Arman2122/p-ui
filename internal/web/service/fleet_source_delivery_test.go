package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func rematerializeFleetSource(t *testing.T, f fleetClientFixture, previous FleetMaterialization, field string) FleetMaterialization {
	t.Helper()
	value := "changed-definition"
	if field == "uuid" {
		value = uuid.NewString()
	}
	if err := f.db.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update(field, value).Error; err != nil {
		t.Fatal(err)
	}
	var inbounds []int
	for _, binding := range previous.Inbounds {
		if binding.PanelInbound == nil {
			t.Fatal("fixture placement lost normalized inbound")
		}
		inbounds = append(inbounds, *binding.PanelInbound)
	}
	current, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{
		PanelClientID: f.client.Id, NodeID: previous.Placement.NodeID, InstallationID: previous.Placement.InstallationID,
		RuntimeID: previous.Placement.RuntimeID, CoreID: previous.Placement.CoreID, InboundIDs: inbounds,
	})
	if err != nil {
		t.Fatal(err)
	}
	current.Placement = f.approve(t, current)
	if current.Placement.ID != previous.Placement.ID || current.Placement.Revision <= previous.Placement.Revision {
		t.Fatalf("source did not retain placement and advance revision=%+v", current.Placement)
	}
	return current
}

func applyFleetSourcePublication(t *testing.T, f fleetClientFixture) uint64 {
	t.Helper()
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.Delivery(t.Context(), f.nodes[0], generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), f.nodes[0], generation, &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: delivery.Revision,
		PayloadSha256: delivery.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(delivery.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
	}); err != nil {
		t.Fatal(err)
	}
	return delivery.Revision
}

func reportFleetSourceNativeCredit(t *testing.T, f fleetClientFixture, grant model.FleetGrant, base, sequence uint64) {
	t.Helper()
	var runtime model.FleetRuntimeJournal
	if err := f.db.First(&runtime, "node_id = ? AND runtime_id = ?", grant.NodeID, grant.RuntimeID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IngestAuthority(t.Context(), f.nodes[0], &nodev1.AuthorityReport{
		NodeId: grant.NodeID, InstallationId: grant.InstallationID, AgentStateJournalId: f.hello(t, f.nodes[0]).AgentStateJournalId,
		RuntimeId: grant.RuntimeID, StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID, Sequence: sequence, ObservedUnixMilli: time.Now().UnixMilli(), RuntimeReady: true,
		Grants: []*nodev1.GrantAuthority{{
			GrantId: grant.ID, ClientId: grant.ClientID, TermId: grant.TermID, PolicyRevision: base, AuthorityRevision: 1,
			CreditCeiling: uint64(grant.IssuedBytes), ReservedHighWater: uint64(grant.IssuedBytes - 20), AdmissionHighWater: uint64(grant.IssuedBytes - 20), AdmissionKnown: true,
			SpendableBytes: 20, SpendableKnown: true, AuthorizationActive: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetSourceNativeCreditRetainsProvenanceAcrossStopAndRejectsChangedCredentialsPostgres(t *testing.T) {
	f, network, preview := fleetNativePublicationFixture(t, model.OCServ)
	rates := controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2_000_000, BurstBytes: 65536}}
	if _, err := network.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID},
		[]FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: rates}}); err != nil {
		t.Fatal(err)
	}
	applyFleetSourcePublication(t, f)
	var native FleetMaterialization
	if err := f.db.First(&native.Placement, "id = ?", preview.NativeBindings[0].PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&native.Client, "id = ?", native.Placement.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("placement_id = ?", native.Placement.ID).Find(&native.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	stream, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{
		PanelClientID: f.client.Id, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, RuntimeID: "xray", CoreID: "xray", InboundIDs: []int{f.inbounds[1].Id},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream.Placement = f.approve(t, stream)
	nextPreview, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{
		NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID,
		ExpectedRevision: preview.NodeRevision.Revision, ExpectedDefaultsID: preview.Defaults.ID,
	})
	if err != nil || !nextPreview.Ready {
		t.Fatalf("mixed native/stream publication preview=%+v err=%v", nextPreview, err)
	}
	if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: nextPreview.ID}); err != nil {
		t.Fatal(err)
	}
	base := applyFleetSourcePublication(t, f)
	var nativeGrant model.FleetGrant
	if err := f.db.First(&nativeGrant, "id = ?", *native.Placement.CurrentGrantID).Error; err != nil {
		t.Fatal(err)
	}
	streamCredit, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: stream.Placement.ID, TermID: nativeGrant.TermID,
		ExpectedPlacementRevision: uint64(stream.Placement.Revision), ExpectedCredentialGeneration: uint64(stream.Client.CredentialGeneration), Bytes: 100, Rates: rates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.QueueAuthorityUpdate(t.Context(), streamCredit.Grant.ID, base, true); err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f, 1, time.Now())
	scope, err := controlplane.PublishedAuthorityScopeTx(f.db, f.nodes[0].NodeID, base, "xray", stream.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal := fleetCreditRequest(t, f)
	batch, err := f.store.FetchAuthorityUpdates(t.Context(), f.nodes[0], &nodev1.AuthorityDeliveryRequest{
		NodeId: journal.NodeId, InstallationId: journal.InstallationId,
		AgentStateJournalId: journal.AgentStateJournalId, RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId,
		BaseConfigurationRevision: base, BasePayloadSha256: scope.PayloadSHA256,
	}, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(batch.Updates) != 1 {
		t.Fatalf("stream authority before stop=%+v err=%v", batch, err)
	}
	if _, err := f.store.AcknowledgeAuthorityUpdates(t.Context(), f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	reportFleetSourceNativeCredit(t, f, nativeGrant, base, 1)
	refill := FleetRefillRequest{
		PlacementID: native.Placement.ID, GrantID: nativeGrant.ID, TermID: nativeGrant.TermID,
		ExpectedPlacementRevision: uint64(native.Placement.Revision), ExpectedCredentialGeneration: uint64(native.Client.CredentialGeneration), AuthoritySequence: 1, LowWaterBytes: 40, TargetBytes: 100,
	}
	credit, err := RefillCreditTx(t.Context(), f.db, refill)
	if err != nil || credit.Grant.IssuedBytes != 180 {
		t.Fatalf("published native source did not refill=%+v err=%v", credit, err)
	}
	var deployment model.FleetDeployment
	if err := f.db.First(&deployment, "node_id = ? AND revision = ?", f.nodes[0].NodeID, base).Error; err != nil {
		t.Fatal(err)
	}
	stopped, err := f.store.StopConfiguration(t.Context(), f.nodes[0], controlplane.StopConfigurationRequest{
		OperationID: uuid.NewString(), ExpectedBaseRevision: base, ExpectedBaseSHA256: deployment.Digest, GrantIDs: []string{streamCredit.Grant.ID},
	}, controlplane.StopConfigurationValidators{Authority: ValidateFleetAuthorityDeliveryTx, Credit: ValidateFleetCreditDeliveryTx})
	if err != nil || stopped.Revision != base+1 {
		t.Fatalf("real validators rejected unchanged native pending credit=%+v err=%v", stopped, err)
	}
	if applied := applyFleetSourcePublication(t, f); applied != stopped.Revision {
		t.Fatalf("stop publication applied=%d want=%d", applied, stopped.Revision)
	}
	reportFleetSourceNativeCredit(t, f, credit.Grant, stopped.Revision, 2)
	refill.AuthoritySequence = 2
	credit, err = RefillCreditTx(t.Context(), f.db, refill)
	if err != nil || credit.Grant.IssuedBytes != 260 {
		t.Fatalf("frozen stop lost native source provenance=%+v err=%v", credit, err)
	}
	var nativeJournal model.FleetRuntimeJournal
	if err := f.db.First(&nativeJournal, "node_id = ? AND runtime_id = ?", f.nodes[0].NodeID, "native").Error; err != nil {
		t.Fatal(err)
	}
	request := &nodev1.CreditDeliveryRequest{
		NodeId: f.nodes[0].NodeID, InstallationId: f.nodes[0].InstallationID, AgentStateJournalId: f.hello(t, f.nodes[0]).AgentStateJournalId,
		RuntimeId: "native", StateJournalId: nativeJournal.StateJournalID, CreditJournalId: nativeJournal.CreditJournalID,
	}
	current, err := f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || len(current.Updates) != 1 || current.BaseConfigurationRevision != stopped.Revision {
		t.Fatalf("current stopped-base native credit=%+v err=%v", current, err)
	}
	rollback := errors.New("restore unique native provenance")
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		var publication model.FleetNetworkPublication
		var compilation model.FleetNetworkCompilation
		if err := tx.First(&publication, "node_id = ? AND deployment_revision = ?", f.nodes[0].NodeID, stopped.Revision).Error; err != nil {
			return err
		}
		if err := tx.First(&compilation, "id = ?", publication.CompilationID).Error; err != nil {
			return err
		}
		compilation.ID, publication.OperationID = uuid.NewString(), uuid.NewString()
		publication.CompilationID = compilation.ID
		if err := tx.Create(&compilation).Error; err != nil {
			return err
		}
		if err := tx.Create(&publication).Error; err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		ambiguous, err := store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
		if err != nil || len(ambiguous.Updates) != 0 {
			t.Fatalf("ambiguous native provenance authorized quota=%+v err=%v", ambiguous, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	rematerializeFleetSource(t, f, native, "uuid")
	stale, err := f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || len(stale.Updates) != 0 {
		t.Fatalf("native old credential graph received fresh quota=%+v err=%v", stale, err)
	}
}

func TestFleetSourceRematerializationBlocksOldAuthorityButAllowsStopPostgres(t *testing.T) {
	for _, field := range []string{"uuid", "security"} {
		t.Run(field, func(t *testing.T) {
			f := newAuthorityFixture(t)
			initial := f.queue(t, true)
			current := rematerializeFleetSource(t, f.f, f.materialized, field)
			if current.Placement.CurrentGrantID == nil || *current.Placement.CurrentGrantID != f.credit.Grant.ID {
				t.Fatal("source edit replaced existing grant")
			}
			batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
			if err != nil || len(batch.Updates) != 0 {
				t.Fatalf("old descriptor delivered after source reapproval=%+v err=%v", batch, err)
			}
			if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
				OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID,
				NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID, RuntimeID: "xray",
			}); err != nil {
				t.Fatal(err)
			}
			stop := f.queue(t, false)
			if len(stop.Updates) != 1 || stop.Updates[0].Enabled || stop.Updates[0].CredentialGeneration != initial.Updates[0].CredentialGeneration || stop.Updates[0].PlacementRevision != initial.Updates[0].PlacementRevision {
				t.Fatalf("source edit blocked frozen disabled delivery=%+v", stop)
			}
		})
	}
}

func TestFleetSourceRematerializationBlocksOldCreditAndRefillPostgres(t *testing.T) {
	for _, field := range []string{"uuid", "security"} {
		t.Run(field, func(t *testing.T) {
			f := newAuthorityFixture(t)
			if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
				t.Fatal(err)
			}
			credit, err := allocateFleetCreditTx(t.Context(), f.f.db, FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: f.active.Term.ID,
				ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100,
			}, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.f.store.QueueCreditUpdate(t.Context(), credit.Grant.ID, 1); err != nil {
				t.Fatal(err)
			}
			before, err := f.f.store.FetchCredit(t.Context(), f.f.nodes[0], fleetCreditRequest(t, f.f), ValidateFleetCreditDeliveryTx)
			if err != nil || len(before.Updates) != 1 {
				t.Fatalf("current auth-only scope credit=%+v err=%v", before, err)
			}
			current := rematerializeFleetSource(t, f.f, f.materialized, field)
			stale, err := f.f.store.FetchCredit(t.Context(), f.f.nodes[0], fleetCreditRequest(t, f.f), ValidateFleetCreditDeliveryTx)
			if err != nil || len(stale.Updates) != 0 {
				t.Fatalf("old descriptor received quota after reapproval=%+v err=%v", stale, err)
			}
			fleetAuthorityReport(t, f.f, 2, time.Now(), &nodev1.GrantAuthority{
				GrantId: credit.Grant.ID, ClientId: credit.Grant.ClientID, TermId: credit.Grant.TermID, PolicyRevision: 1,
				CreditCeiling: uint64(credit.Grant.IssuedBytes), ReservedHighWater: 380, AdmissionHighWater: 380, AdmissionKnown: true,
				SpendableKnown: true, SpendableBytes: 20, AuthorizationActive: true,
			})
			if _, err := RefillCreditTx(t.Context(), f.f.db, FleetRefillRequest{
				PlacementID: current.Placement.ID, GrantID: credit.Grant.ID, TermID: credit.Grant.TermID, ExpectedPlacementRevision: uint64(current.Placement.Revision),
				ExpectedCredentialGeneration: uint64(current.Client.CredentialGeneration), AuthoritySequence: 2, LowWaterBytes: 40, TargetBytes: 200,
			}); !errors.Is(err, ErrFleetRefill) {
				t.Fatalf("old runtime credentials funded a fresh refill=%v", err)
			}
			var saved model.FleetGrant
			if err := f.f.db.First(&saved, "id = ?", credit.Grant.ID).Error; err != nil || saved.IssuedBytes != credit.Grant.IssuedBytes || saved.Revision != credit.Grant.Revision {
				t.Fatalf("source refusal released or multiplied quota=%+v err=%v", saved, err)
			}
		})
	}
}

func TestFleetSourcePendingStopBlocksReapprovedAllocationAndRefillPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, f.materialized.Client.ID, enabled)); err != nil {
			t.Fatal(err)
		}
	}
	current := f.f.materialize(t, 0)
	current.Placement = f.f.approve(t, current)
	if _, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: current.Placement.ID, TermID: f.active.Term.ID,
		ExpectedPlacementRevision: uint64(current.Placement.Revision), ExpectedCredentialGeneration: uint64(current.Client.CredentialGeneration), Bytes: 100,
	}); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("source reapproval bypassed allocation stop barrier=%v", err)
	}
	if _, err := RefillCreditTx(t.Context(), f.f.db, FleetRefillRequest{
		PlacementID: current.Placement.ID, GrantID: f.credit.Grant.ID, TermID: f.active.Term.ID,
		ExpectedPlacementRevision: uint64(current.Placement.Revision), ExpectedCredentialGeneration: uint64(current.Client.CredentialGeneration), AuthoritySequence: 1, LowWaterBytes: 40, TargetBytes: 200,
	}); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("source reapproval bypassed refill stop barrier=%v", err)
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision {
		t.Fatalf("pending source stop changed quota=%+v err=%v", grant, err)
	}
}

func TestFleetSourceStopCompositionRejectsUnrelatedStalePendingAuthorityPostgres(t *testing.T) {
	for _, kind := range []string{"authority", "credit"} {
		t.Run(kind, func(t *testing.T) {
			f := newAuthorityFixture(t)
			other := f.f
			other.client = model.ClientRecord{Email: "unrelated-source", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: f.f.client.ExpiryTime, Reset: 1}
			if err := f.f.db.Create(&other.client).Error; err != nil {
				t.Fatal(err)
			}
			for _, inbound := range f.f.inbounds {
				if err := f.f.db.Create(&model.ClientInbound{ClientId: other.client.Id, InboundId: inbound.Id}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := f.f.db.Create(&core.ClientTraffic{Email: other.client.Email, InboundId: f.f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: other.client.ExpiryTime}).Error; err != nil {
				t.Fatal(err)
			}
			materialized := other.materialize(t, 0)
			materialized.Placement = other.approve(t, materialized)
			active := other.activate(t, materialized.Placement)
			bundle := fleetCreditBundle(t, f.f.db, f.active.Term, f.credit)
			bundle.Runtimes[0].Admissions = append(bundle.Runtimes[0].Admissions, fleetAdmissionBundle(materialized).Runtimes[0].Admissions...)
			generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
			if err != nil {
				t.Fatal(err)
			}
			base := fleetApplyProbe(t, f.f, f.f.nodes[0], generation, bundle)
			credit, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: materialized.Placement.ID, TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1,
				Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.f.store.QueueAuthorityUpdate(t.Context(), credit.Grant.ID, base, true); err != nil {
				t.Fatal(err)
			}
			fleetAuthorityReport(t, f.f, 2, time.Now())
			if kind == "credit" {
				scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, f.f.nodes[0].NodeID, base, "xray", materialized.Client.ID)
				if err != nil {
					t.Fatal(err)
				}
				f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = base, scope.PayloadSHA256
				batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
				if err != nil || len(batch.Updates) != 1 {
					t.Fatalf("unrelated current authority=%+v err=%v", batch, err)
				}
				if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
					t.Fatal(err)
				}
				credit, err = allocateFleetCreditTx(t.Context(), f.f.db, FleetCreditRequest{
					OperationID: uuid.NewString(), PlacementID: materialized.Placement.ID, TermID: active.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 80,
				}, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.f.store.QueueCreditUpdate(t.Context(), credit.Grant.ID, base); err != nil {
					t.Fatal(err)
				}
			}
			rematerializeFleetSource(t, other, materialized, "uuid")
			var deployment model.FleetDeployment
			var before model.FleetRateShare
			if err := f.f.db.First(&deployment, "node_id = ? AND revision = ?", f.f.nodes[0].NodeID, base).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.f.db.First(&before, "id = ?", f.credit.Grant.ID).Error; err != nil {
				t.Fatal(err)
			}
			_, err = f.f.store.StopConfiguration(t.Context(), f.f.nodes[0], controlplane.StopConfigurationRequest{
				OperationID: uuid.NewString(), ExpectedBaseRevision: base, ExpectedBaseSHA256: deployment.Digest, GrantIDs: []string{f.credit.Grant.ID},
			}, controlplane.StopConfigurationValidators{Authority: ValidateFleetAuthorityDeliveryTx, Credit: ValidateFleetCreditDeliveryTx})
			if !errors.Is(err, controlplane.ErrStopComposition) {
				t.Fatalf("stop publication authorized stale unrelated %s=%v", kind, err)
			}
			var after model.FleetRateShare
			var node model.FleetNode
			if err := f.f.db.First(&after, "id = ?", f.credit.Grant.ID).Error; err != nil || after.Revision != before.Revision {
				t.Fatalf("failed composition changed target rate=%+v err=%v", after, err)
			}
			var oldRates, newRates controlplane.Rates
			if json.Unmarshal([]byte(before.Outstanding), &oldRates) != nil || json.Unmarshal([]byte(after.Outstanding), &newRates) != nil || oldRates != newRates {
				t.Fatalf("failed composition released target authority=%+v want=%+v", newRates, oldRates)
			}
			if err := f.f.db.First(&node, "id = ?", f.f.nodes[0].NodeID).Error; err != nil || node.DesiredRevision != int64(base) || node.AppliedRevision != int64(base) {
				t.Fatalf("failed composition published a deployment=%+v err=%v", node, err)
			}
		})
	}
}
