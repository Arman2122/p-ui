package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type authorityFixture struct {
	f            fleetClientFixture
	materialized FleetMaterialization
	active       controlplane.TransitionResult
	credit       FleetCredit
	request      *nodev1.AuthorityDeliveryRequest
}

func newAuthorityFixture(t *testing.T) authorityFixture {
	t.Helper()
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	materialized.Placement = f.approve(t, materialized)
	active := f.activate(t, materialized.Placement)
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyProbe(t, f, f.nodes[0], generation, fleetAdmissionBundle(materialized))
	credit, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: materialized.Placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: uint64(materialized.Placement.Revision), ExpectedCredentialGeneration: uint64(materialized.Client.CredentialGeneration), Bytes: 300,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 100_000_000, BurstBytes: 1 << 20}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 100_000_000, BurstBytes: 1 << 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f, 1, time.Now())
	scope, err := controlplane.PublishedAuthorityScopeTx(f.db, f.nodes[0].NodeID, revision, "xray", materialized.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scope.BaseGrant != nil {
		t.Fatal("auth-only graph unexpectedly contains a grant")
	}
	journal := fleetCreditRequest(t, f)
	request := &nodev1.AuthorityDeliveryRequest{
		NodeId: journal.NodeId, InstallationId: journal.InstallationId, AgentStateJournalId: journal.AgentStateJournalId,
		RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId, BaseConfigurationRevision: revision, BasePayloadSha256: scope.PayloadSHA256,
	}
	return authorityFixture{f: f, materialized: materialized, active: active, credit: credit, request: request}
}

func (f authorityFixture) queue(t *testing.T, enabled bool) *nodev1.AuthorityUpdateBatch {
	t.Helper()
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), f.credit.Grant.ID, f.request.BaseConfigurationRevision, enabled); err != nil {
		t.Fatal(err)
	}
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || batch == nil || len(batch.Updates) != 1 {
		t.Fatalf("authority delivery=%+v %v", batch, err)
	}
	return batch
}

func TestFleetInitialAuthorityOutboxDoesNotRepublishConfiguration(t *testing.T) {
	f := newAuthorityFixture(t)
	wrongBase := proto.Clone(f.request).(*nodev1.AuthorityDeliveryRequest)
	wrongBase.BasePayloadSha256[0] ^= 1
	if _, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], wrongBase, ValidateFleetAuthorityDeliveryTx); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("foreign projected base=%v", err)
	}
	batch := f.queue(t, true)
	entry := batch.Updates[0]
	if entry.GrantId != f.credit.Grant.ID || entry.ClientId != f.materialized.Client.ID || entry.TermId != f.active.Term.ID || entry.CreditCeiling != 300 || entry.AuthorityRevision != 1 ||
		entry.PlacementId != f.materialized.Placement.ID || !entry.Enabled || entry.Upload.BitsPerSecond != 100_000_000 || entry.Download.BurstBytes != 1<<20 {
		t.Fatalf("initial authority=%+v", entry)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", f.request.NodeId).Error; err != nil {
		t.Fatal(err)
	}
	if node.DesiredRevision != 1 || node.AppliedRevision != 1 {
		t.Fatalf("grant republished fullgraph=%+v", node)
	}
	if _, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, nil); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("missing source validator=%v", err)
	}
	foreign := proto.Clone(f.request).(*nodev1.AuthorityDeliveryRequest)
	foreign.StateJournalId = uuid.NewString()
	if _, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], foreign, ValidateFleetAuthorityDeliveryTx); err == nil || err.Error() != "credit runtime journal changed" {
		t.Fatalf("foreign worker journal result=%v", err)
	}
	for range 2 {
		ack, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch)
		digest, digestErr := authority.AuthorityDigest(batch)
		if err != nil || digestErr != nil || !bytes.Equal(ack.GetPayloadSha256(), digest) {
			t.Fatalf("authority acknowledgement=%v %v", ack, err)
		}
	}
	idle, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(idle.Updates) != 0 || idle.BaseConfigurationRevision != 1 || !bytes.Equal(idle.BasePayloadSha256, f.request.BasePayloadSha256) {
		t.Fatalf("settled authority=%+v %v", idle, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", f.active.Term.BudgetID).Error; err != nil {
		t.Fatal(err)
	}
	if budget.UsedBytes != 0 || budget.ReservedBytes != 300 {
		t.Fatalf("delivery charged or duplicated quota=%+v", budget)
	}
}

func TestFleetAuthorityNewBaseRequiresNewReceipt(t *testing.T) {
	f := newAuthorityFixture(t)
	first := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], first); err != nil {
		t.Fatal(err)
	}
	bundle := fleetAdmissionBundle(f.materialized)
	bundle.Runtimes[0].Grants = fleetCreditBundle(t, f.f.db, f.active.Term, f.credit).Runtimes[0].Grants
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyProbe(t, f.f, f.f.nodes[0], generation, bundle)
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, f.request.NodeId, revision, "xray", f.materialized.Client.ID)
	if err != nil || scope.BaseGrant == nil {
		t.Fatalf("new grant baseline=%+v %v", scope, err)
	}
	f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = revision, scope.PayloadSHA256
	fleetAuthorityReport(t, f.f, 2, time.Now())
	second := f.queue(t, true)
	if second.Updates[0].AuthorityRevision != first.Updates[0].AuthorityRevision {
		t.Fatal("same rate was unnecessarily revised")
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], first); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("old-base acknowledgement accepted=%v", err)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], second); err != nil {
		t.Fatal(err)
	}
}

func TestFleetAuthorityStaleAcknowledgmentCannotReleaseNewerHold(t *testing.T) {
	f := newAuthorityFixture(t)
	initial := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], initial); err != nil {
		t.Fatal(err)
	}
	rateRequest := controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID, NodeID: f.request.NodeId, InstallationID: f.request.InstallationId, RuntimeID: "xray",
		Desired: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 50_000_000, BurstBytes: 1 << 19}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 50_000_000, BurstBytes: 1 << 19}},
	}
	if _, err := f.f.store.RequestRateShare(t.Context(), rateRequest); err != nil {
		t.Fatal(err)
	}
	decrease := f.queue(t, true)
	rateRequest.OperationID = uuid.NewString()
	rateRequest.Desired.Upload.BitsPerSecond, rateRequest.Desired.Download.BitsPerSecond = 75_000_000, 75_000_000
	if _, err := f.f.store.RequestRateShare(t.Context(), rateRequest); err != nil {
		t.Fatal(err)
	}
	current := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], decrease); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("stale rate acknowledgement=%v", err)
	}
	var share model.FleetRateShare
	if err := f.f.db.First(&share, "id = ?", f.credit.Grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	var held controlplane.Rates
	if json.Unmarshal([]byte(share.Outstanding), &held) != nil || held.Upload.BitsPerSecond != 100_000_000 || share.AppliedRevision != 1 {
		t.Fatalf("stale ACK released held rate=%+v", share)
	}
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], current); err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&share, "id = ?", f.credit.Grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(share.Outstanding), &held) != nil || held.Upload.BitsPerSecond != 75_000_000 || share.AppliedRevision != 3 {
		t.Fatalf("current ACK did not settle authority=%+v", share)
	}
}

func TestFleetAuthoritySourceFencePreservesReservedQuotaAndAllowsStop(t *testing.T) {
	f := newAuthorityFixture(t)
	_ = f.queue(t, true)
	if err := f.f.db.Model(&model.ClientRecord{}).Where("id = ?", f.f.client.Id).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	idle, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || len(idle.Updates) != 0 {
		t.Fatalf("disabled source still received authority=%+v %v", idle, err)
	}
	var queued model.FleetAuthorityUpdate
	if err := f.f.db.First(&queued, "grant_id = ?", f.credit.Grant.ID).Error; err != nil || !queued.DeliveryBlocked {
		t.Fatalf("unauthorized prefix not suppressed=%+v %v", queued, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", f.active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 300 || budget.UsedBytes != 0 {
		t.Fatalf("source edit discarded reserved authority=%+v %v", budget, err)
	}
	request := controlplane.RateRequest{OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID, NodeID: f.request.NodeId, InstallationID: f.request.InstallationId, RuntimeID: "xray"}
	if _, err := f.f.store.RequestRateShare(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	stop := f.queue(t, false)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], stop); err != nil {
		t.Fatal(err)
	}
	var proof model.FleetGrantStop
	if err := f.f.db.First(&proof, "grant_id = ?", f.credit.Grant.ID).Error; err != nil || proof.RateRevision != 2 || proof.DeploymentRevision != 1 {
		t.Fatalf("applied stop proof=%+v %v", proof, err)
	}
	request.OperationID = uuid.NewString()
	if _, err := f.f.store.RequestRateShare(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), f.credit.Grant.ID, 1, true); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("disabled same-base grant revived=%v", err)
	}
}

func TestFleetAuthorityQueueRollsBackWithItsAllocation(t *testing.T) {
	f := newAuthorityFixture(t)
	rollback := errors.New("allocator publication aborted")
	if err := f.f.db.Transaction(func(tx *gorm.DB) error {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		if err := store.QueueAuthorityUpdate(t.Context(), f.credit.Grant.ID, 1, true); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int64
	if err := f.f.db.Model(&model.FleetAuthorityUpdate{}).Where("grant_id = ?", f.credit.Grant.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("aborted authority intent survived=%d %v", count, err)
	}
}

func TestFleetIncrementalAuthorityRefillsThroughCreditChannel(t *testing.T) {
	f := newAuthorityFixture(t)
	initial := f.queue(t, true)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], initial); err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, 2, time.Now(), &nodev1.GrantAuthority{
		GrantId: f.credit.Grant.ID, ClientId: f.credit.Grant.ClientID, TermId: f.credit.Grant.TermID,
		PolicyRevision: 1, CreditCeiling: 300, ReservedHighWater: 280, AdmissionHighWater: 280, AdmissionKnown: true, SpendableBytes: 20, SpendableKnown: true, AuthorizationActive: true,
	})
	refilled, err := RefillCreditTx(t.Context(), f.f.db, FleetRefillRequest{
		PlacementID: f.materialized.Placement.ID, GrantID: f.credit.Grant.ID, TermID: f.active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, AuthoritySequence: 2, LowWaterBytes: 40, TargetBytes: 200,
	})
	if err != nil || refilled.Grant.IssuedBytes != 480 || refilled.Rates.Revision != 1 {
		t.Fatalf("incrementally installed grant refill=%+v %v", refilled, err)
	}
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), f.credit.Grant.ID, 1, true); !errors.Is(err, controlplane.ErrCreditDeliveryRequired) {
		t.Fatalf("quota-only change silently accepted by authority channel=%v", err)
	}
	credit, err := f.f.store.FetchCredit(t.Context(), f.f.nodes[0], fleetCreditRequest(t, f.f), ValidateFleetCreditDeliveryTx)
	if err != nil || len(credit.Updates) != 1 || credit.Updates[0].CreditCeiling != 480 {
		t.Fatalf("credit channel omitted reserved refill=%+v %v", credit, err)
	}
}

func TestFleetAuthorityTermReplacementCannotPoisonOneClientBatch(t *testing.T) {
	f := newAuthorityFixture(t)
	_ = f.queue(t, true)
	if _, err := f.f.store.RequestRateShare(t.Context(), controlplane.RateRequest{
		OperationID: uuid.NewString(), GrantID: f.credit.Grant.ID, BudgetID: f.materialized.Client.ID,
		NodeID: f.request.NodeId, InstallationID: f.request.InstallationId, RuntimeID: "xray",
	}); err != nil {
		t.Fatal(err)
	}
	stop := f.queue(t, false)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], stop); err != nil {
		t.Fatal(err)
	}
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID,
		Kind: controlplane.TrafficReset, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1,
	})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("reset transition=%+v %v", pending, err)
	}
	active, err := (FleetClientService{}).FinishTransition(t.Context(), &f.f.admin, FleetFinishRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID,
		PendingTermID: pending.Term.ID, ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1,
	})
	if err != nil || active.Term.ID == f.active.Term.ID || active.Term.ExpiresUnixMilli != f.active.Term.ExpiresUnixMilli {
		t.Fatalf("successor term=%+v %v", active, err)
	}
	next, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil || next.Grant.ID == f.credit.Grant.ID {
		t.Fatalf("successor authority=%+v %v", next, err)
	}
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), next.Grant.ID, 1, true); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("same-base grant replacement entered delivery prefix=%v", err)
	}
	bundle := fleetAdmissionBundle(f.materialized)
	bundle.Runtimes[0].Grants = fleetCreditBundle(t, f.f.db, active.Term, next).Runtimes[0].Grants
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyProbe(t, f.f, f.f.nodes[0], generation, bundle)
	if err := f.f.store.QueueAuthorityUpdate(t.Context(), f.credit.Grant.ID, revision, false); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("old grant queued against successor base=%v", err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, f.request.NodeId, revision, "xray", f.materialized.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.credit, f.active = next, active
	f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = revision, scope.PayloadSHA256
	fleetAuthorityReport(t, f.f, 2, time.Now())
	batch := f.queue(t, true)
	if len(batch.Updates) != 1 || batch.Updates[0].GrantId != next.Grant.ID || batch.Updates[0].TermId != active.Term.ID {
		t.Fatalf("successor prefix poisoned=%+v", batch)
	}
}
