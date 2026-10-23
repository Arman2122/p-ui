package service

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func fleetCreditBundle(t *testing.T, db *gorm.DB, term model.FleetTerm, credit FleetCredit) configuration.Bundle {
	t.Helper()
	if credit.Grant.PlacementID == nil {
		t.Fatal("credit fixture requires a placement")
	}
	var placement model.FleetPlacement
	if err := db.First(&placement, "id = ?", *credit.Grant.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	var bindings []model.FleetPlacementInbound
	if err := db.Where("placement_id = ?", placement.ID).Order("instance_id").Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	var admissions []nodeadmission.Descriptor
	for _, binding := range bindings {
		admissions = append(admissions, nodeadmission.Descriptor{
			ClientID: placement.ClientID, PlacementID: placement.ID, PlacementRevision: uint64(placement.Revision),
			CredentialGeneration: uint64(placement.CredentialGeneration), InboundInstanceID: binding.InstanceID, InboundTag: "node-" + binding.InstanceID,
		})
	}
	var rates controlplane.Rates
	_ = json.Unmarshal([]byte(credit.Rates.Desired), &rates)
	return configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{
		ID: credit.Grant.RuntimeID, Core: "xray", Configuration: json.RawMessage(`{}`), Admissions: admissions,
		Grants: []governor.Grant{{ID: credit.Grant.ID, ClientID: credit.Grant.ClientID, RateRevision: uint64(credit.Rates.Revision), Policy: limits.Policy{
			Term: term.ID, Revision: 1, Enabled: true, CreditCeiling: uint64(credit.Grant.IssuedBytes), ExpiresUnixMilli: term.ExpiresUnixMilli,
			Upload: rates.Upload, Download: rates.Download,
		}}},
	}}}
}

func fleetApplyCredit(t *testing.T, f fleetClientFixture, term model.FleetTerm, credit FleetCredit) uint64 {
	t.Helper()
	var node identity.Principal
	for _, candidate := range f.nodes {
		if candidate.NodeID == credit.Grant.NodeID {
			node = candidate
		}
	}
	if node.NodeID == "" {
		t.Fatal("credit node is absent from fixture")
	}
	generation, err := f.store.BeginSession(t.Context(), node, f.hello(t, node))
	if err != nil {
		t.Fatalf("begin registered session: %v", err)
	}
	if _, err := f.store.PublishConfiguration(t.Context(), node, fleetCreditBundle(t, f.db, term, credit)); err != nil {
		t.Fatalf("publish credit configuration: %v", err)
	}
	deployment, err := f.store.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatalf("deliver credit configuration: %v", err)
	}
	if err := f.store.Observe(t.Context(), node, generation, &nodev1.ApplyResult{AuthorityGeneration: generation, Revision: deployment.Revision, PayloadSha256: deployment.PayloadSha256, AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload), Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED}); err != nil {
		t.Fatalf("apply credit configuration: %v", err)
	}
	return deployment.Revision
}

func fleetAuthorityReport(t *testing.T, f fleetClientFixture, sequence uint64, observed time.Time, grants ...*nodev1.GrantAuthority) {
	fleetAuthorityReportAtNode(t, f, 0, sequence, observed, grants...)
}

func fleetAuthorityReportAtNode(t *testing.T, f fleetClientFixture, index int, sequence uint64, observed time.Time, grants ...*nodev1.GrantAuthority) {
	t.Helper()
	node := f.nodes[index]
	var agent model.FleetNodeJournal
	var runtime model.FleetRuntimeJournal
	if err := f.db.First(&agent, "node_id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&runtime, "node_id = ? AND runtime_id = ?", node.NodeID, "xray").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IngestAuthority(t.Context(), node, &nodev1.AuthorityReport{
		NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: *agent.StateJournalID,
		RuntimeId: runtime.RuntimeID, StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID,
		Sequence: sequence, ObservedUnixMilli: observed.UnixMilli(), RuntimeReady: true, Grants: grants,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetRefillRequiresFreshAuthorityAndPublishesAtomically(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	initial, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyCredit(t, f, active.Term, initial)
	request := FleetRefillRequest{
		PlacementID: placement.ID, TermID: active.Term.ID, GrantID: initial.Grant.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, AuthoritySequence: 1, LowWaterBytes: 75, TargetBytes: 300,
	}
	if _, err := RefillCreditTx(t.Context(), f.db, request); !errors.Is(err, ErrFleetRefill) {
		t.Fatalf("unobserved grant refill=%v", err)
	}
	observed := &nodev1.GrantAuthority{
		GrantId: initial.Grant.ID, ClientId: initial.Grant.ClientID, TermId: initial.Grant.TermID,
		PolicyRevision: revision, CreditCeiling: 300, ReservedHighWater: 250, AdmissionHighWater: 250, AdmissionKnown: true,
		SpendableBytes: 50, SpendableKnown: true, AuthorizationActive: true,
	}
	fleetAuthorityReport(t, f, 1, time.Now().Add(-time.Minute), observed)
	if _, err := RefillCreditTx(t.Context(), f.db, request); !errors.Is(err, ErrFleetRefill) {
		t.Fatalf("recent receipt of stale spool authorized refill=%v", err)
	}
	fleetAuthorityReport(t, f, 2, time.Now())
	rollback := errors.New("configuration compilation rejected")
	for _, tc := range []struct {
		name  string
		model any
		where string
		id    string
		field string
		value any
		want  error
	}{
		{"worker-not-ready", &model.FleetRuntimeJournal{}, "node_id = ?", f.nodes[0].NodeID, "ready", false, ErrFleetRefill},
		{"stale-heartbeat", &model.FleetRuntimeJournal{}, "node_id = ?", f.nodes[0].NodeID, "received_unix_milli", time.Now().Add(-time.Minute).UnixMilli(), ErrFleetRefill},
		{"future-worker-clock", &model.FleetRuntimeJournal{}, "node_id = ?", f.nodes[0].NodeID, "observed_unix_milli", time.Now().Add(time.Minute).UnixMilli(), ErrFleetRefill},
		{"unknown-spendable", &model.FleetGrantAuthority{}, "grant_id = ?", initial.Grant.ID, "spendable_known", false, ErrFleetRefill},
		{"old-ceiling", &model.FleetGrantAuthority{}, "grant_id = ?", initial.Grant.ID, "credit_ceiling", int64(299), ErrFleetRefill},
		{"deployment-in-flight", &model.FleetNode{}, "id = ?", f.nodes[0].NodeID, "desired_revision", int64(revision + 1), ErrFleetRefill},
		{"unsettled-rate", &model.FleetRateShare{}, "id = ?", initial.Grant.ID, "applied_revision", int64(0), ErrFleetRefill},
		{"journal-recovery-hold", &model.FleetNodeJournal{}, "node_id = ?", f.nodes[0].NodeID, "recovery_required", true, controlplane.ErrRecovery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(tc.model).Where(tc.where, tc.id).Update(tc.field, tc.value).Error; err != nil {
					return err
				}
				if _, err := RefillCreditTx(t.Context(), tx, request); !errors.Is(err, tc.want) {
					t.Errorf("unsafe refill=%v want=%v", err, tc.want)
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		credit, err := RefillCreditTx(t.Context(), tx, request)
		if err != nil {
			return err
		}
		if credit.Grant.IssuedBytes != 550 {
			t.Fatalf("bounded refill=%+v", credit)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var grant model.FleetGrant
	if err := f.db.First(&grant, "id = ?", initial.Grant.ID).Error; err != nil || grant.IssuedBytes != 300 {
		t.Fatalf("failed compiler stranded new authority=%+v err=%v", grant, err)
	}
	var wg sync.WaitGroup
	var results [4]FleetCredit
	for i := range results {
		wg.Go(func() {
			err := f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				results[i], err = RefillCreditTx(t.Context(), tx, request)
				if err != nil {
					return err
				}
				store, err := controlplane.NewStore(tx)
				if err != nil {
					return err
				}
				_, err = store.PublishConfiguration(t.Context(), f.nodes[0], fleetCreditBundle(t, tx, active.Term, results[i]))
				return err
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	first := 0
	for _, result := range results {
		if !result.Replayed {
			first++
		}
		if result.Grant.IssuedBytes != 550 || result.Grant.ID != initial.Grant.ID {
			t.Fatalf("concurrent refill multiplied authority=%+v", result)
		}
	}
	if first != 1 {
		t.Fatalf("non-replayed refills=%d", first)
	}
	var budget model.FleetBudget
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 550 {
		t.Fatalf("authority telemetry became usage or duplicate quota=%+v err=%v", budget, err)
	}
	conflict := request
	conflict.TargetBytes++
	if _, err := RefillCreditTx(t.Context(), f.db, conflict); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("same observation authorized conflicting refill=%v", err)
	}
	revision = fleetApplyCredit(t, f, active.Term, results[0])
	observed.PolicyRevision, observed.CreditCeiling, observed.ReservedHighWater, observed.AdmissionHighWater, observed.SpendableBytes = revision, 550, 550, 550, 0
	fleetAuthorityReport(t, f, 3, time.Now(), observed)
	request.AuthoritySequence, request.TargetBytes = 3, 1000
	final, err := RefillCreditTx(t.Context(), f.db, request)
	if err != nil || final.Grant.IssuedBytes != 1000 {
		t.Fatalf("near-exhaustion refill did not clamp free quota=%+v err=%v", final, err)
	}
}
