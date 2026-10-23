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

func fleetCreditRequest(t *testing.T, f fleetClientFixture) *nodev1.CreditDeliveryRequest {
	t.Helper()
	node := f.nodes[0]
	var runtime model.FleetRuntimeJournal
	if err := f.db.First(&runtime, "node_id = ? AND runtime_id = ?", node.NodeID, "xray").Error; err != nil {
		t.Fatal(err)
	}
	return &nodev1.CreditDeliveryRequest{
		NodeId: node.NodeID, InstallationId: node.InstallationID, AgentStateJournalId: f.hello(t, node).AgentStateJournalId,
		RuntimeId: "xray", StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID,
	}
}

func TestFleetCreditOutboxPreservesRateAndFencesRuntimeGraph(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	initial, err := AllocateCreditTx(t.Context(), f.db, FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 200,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := fleetApplyCredit(t, f, active.Term, initial)
	refill := func(sequence uint64, previous FleetCredit) FleetCredit {
		t.Helper()
		ceiling := uint64(previous.Grant.IssuedBytes)
		fleetAuthorityReport(t, f, sequence, time.Now(), &nodev1.GrantAuthority{
			GrantId: initial.Grant.ID, ClientId: initial.Grant.ClientID, TermId: initial.Grant.TermID,
			PolicyRevision: revision, CreditCeiling: ceiling, ReservedHighWater: ceiling - 20, AdmissionHighWater: ceiling - 20,
			AdmissionKnown: true, SpendableKnown: true, SpendableBytes: 20, AuthorizationActive: true,
		})
		result, err := RefillCreditTx(t.Context(), f.db, FleetRefillRequest{
			PlacementID: placement.ID, TermID: active.Term.ID, GrantID: initial.Grant.ID,
			ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, AuthoritySequence: sequence, LowWaterBytes: 40, TargetBytes: 200,
		})
		if err != nil {
			t.Fatal(err)
		}
		var beforeRates, afterRates controlplane.Rates
		if err := json.Unmarshal([]byte(initial.Rates.Desired), &beforeRates); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result.Rates.Desired), &afterRates); err != nil {
			t.Fatal(err)
		}
		if result.Grant.IssuedBytes != previous.Grant.IssuedBytes+180 || result.Rates.Revision != initial.Rates.Revision || beforeRates != afterRates {
			t.Fatalf("credit-only refill changed rate/config authority=%+v err=%v", result, err)
		}
		return result
	}
	credit := refill(1, initial)
	request := fleetCreditRequest(t, f)
	if _, err := f.store.FetchCredit(t.Context(), f.nodes[0], request); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("missing source authorizer delivered pending authority=%v", err)
	}
	rollback := errors.New("restore source after delivery refusal test")
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetRuntimeJournal{}).Where("node_id = ?", f.nodes[0].NodeID).Update("ready", false).Error; err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		idle, err := store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
		if err != nil || authority.ValidateCreditBatch(idle, true) != nil || len(idle.Updates) != 0 {
			t.Errorf("unready worker received repeated credit delivery=%+v err=%v", idle, err)
		}
		var queued int64
		if err := tx.Model(&model.FleetCreditUpdate{}).Where("grant_id = ?", credit.Grant.ID).Count(&queued).Error; err != nil || queued != 1 {
			t.Errorf("temporary worker failure erased pending authority=%d err=%v", queued, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"uuid", uuid.NewString()}, {"enable", false}, {"total_gb", int64(500)}, {"expiry_time", int64(1)},
	} {
		t.Run("changed-source-"+tc.field, func(t *testing.T) {
			if err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update(tc.field, tc.value).Error; err != nil {
					return err
				}
				store, err := controlplane.NewStore(tx)
				if err != nil {
					return err
				}
				blocked, err := store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
				if err != nil || authority.ValidateCreditBatch(blocked, true) != nil || len(blocked.Updates) != 0 {
					t.Errorf("changed source received queued authority=%+v err=%v", blocked, err)
				}
				var queued int64
				if err := tx.Model(&model.FleetCreditUpdate{}).Where("grant_id = ?", credit.Grant.ID).Count(&queued).Error; err != nil || queued != 0 {
					t.Errorf("invalid first-page row would starve later clients=%d err=%v", queued, err)
				}
				var budget model.FleetBudget
				if err := tx.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 380 || budget.UsedBytes != 0 {
					t.Errorf("delivery refusal reclaimed authority=%+v err=%v", budget, err)
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
	batch, err := f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || authority.ValidateCreditBatch(batch, false) != nil || len(batch.Updates) != 1 || batch.BaseConfigurationRevision != revision || batch.Updates[0].CreditRevision != uint64(credit.Grant.Revision) || batch.Updates[0].CreditCeiling != 380 {
		t.Fatalf("bound credit delivery=%+v err=%v", batch, err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FleetFence{}).Where("id = 1").Updates(map[string]any{"recovery_required": true, "reason": "OPERATOR_RESTORE"}).Error; err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		if _, err := store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx); !errors.Is(err, controlplane.ErrRecovery) {
			t.Errorf("global recovery allowed credit fetch=%v", err)
		}
		if _, err := store.AcknowledgeCredit(t.Context(), f.nodes[0], batch); !errors.Is(err, controlplane.ErrRecovery) {
			t.Errorf("global recovery allowed credit acknowledgement=%v", err)
		}
		var journal model.FleetNodeJournal
		if err := tx.First(&journal, "node_id = ?", f.nodes[0].NodeID).Error; err != nil || journal.RecoveryRequired || journal.Reason != "" {
			t.Errorf("global restore fabricated node journal failure=%+v err=%v", journal, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := f.store.FetchCredit(t.Context(), f.nodes[1], request, ValidateFleetCreditDeliveryTx); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("foreign node received credit=%v", err)
	}
	bad := proto.Clone(batch).(*nodev1.CreditUpdateBatch)
	bad.Updates[0].CreditCeiling++
	if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], bad); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("changed ceiling receipt=%v", err)
	}
	digest, err := authority.CreditDigest(batch)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		receipt, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], batch)
		if err != nil || !bytes.Equal(receipt.PayloadSha256, digest) {
			t.Fatalf("lost credit receipt replay=%+v err=%v", receipt, err)
		}
	}
	idle, err := f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || authority.ValidateCreditBatch(idle, true) != nil || len(idle.Updates) != 0 {
		t.Fatalf("acknowledged credit re-delivered=%+v err=%v", idle, err)
	}
	credit = refill(2, credit)
	if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], batch); err != nil {
		t.Fatalf("last confirmed receipt lost after newer queue=%v", err)
	}
	batch, err = f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].CreditCeiling != 560 {
		t.Fatalf("old receipt acknowledged newer credit=%+v err=%v", batch, err)
	}
	credit = refill(3, credit)
	if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], batch); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("superseded unacknowledged batch must refetch=%v", err)
	}
	batch, err = f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].CreditCeiling != 740 {
		t.Fatalf("latest queued ceiling=%+v err=%v", batch, err)
	}
	wrongRevision := fleetCreditBundle(t, f.db, active.Term, credit)
	wrongRevision.Runtimes[0].Grants[0].Policy.CreditRevision = 1
	if _, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], wrongRevision); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("stale credit revision entered full graph=%v", err)
	}
	next, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], fleetCreditBundle(t, f.db, active.Term, credit))
	if err != nil || next <= revision {
		t.Fatalf("new full configuration=%d err=%v", next, err)
	}
	idle, err = f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || authority.ValidateCreditBatch(idle, true) != nil || len(idle.Updates) != 0 {
		t.Fatalf("pending full graph leaked old credit batch=%+v err=%v", idle, err)
	}
	if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], batch); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("old-base acknowledgement crossed full graph=%v", err)
	}
	if got := fleetApplyCredit(t, f, active.Term, credit); got != next {
		t.Fatalf("canonical credit revision broke full-graph idempotency=%d want=%d", got, next)
	}
	idle, err = f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx)
	if err != nil || len(idle.Updates) != 0 {
		t.Fatalf("new base re-delivered superseded credits=%+v err=%v", idle, err)
	}
	var budget model.FleetBudget
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 740 {
		t.Fatalf("credit acknowledgements returned or billed quota=%+v err=%v", budget, err)
	}
	request.StateJournalId = uuid.NewString()
	if _, err := f.store.FetchCredit(t.Context(), f.nodes[0], request, ValidateFleetCreditDeliveryTx); !errors.Is(err, controlplane.ErrRecovery) {
		t.Fatalf("replacement worker journal received authority=%v", err)
	}
	var journal model.FleetNodeJournal
	if err := f.db.First(&journal, "node_id = ?", f.nodes[0].NodeID).Error; err != nil || !journal.RecoveryRequired {
		t.Fatalf("worker replacement recovery hold did not survive refusal=%+v err=%v", journal, err)
	}
}
