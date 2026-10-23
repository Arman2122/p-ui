package controlplane

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type authorityRecoveryFixture struct {
	store   *Store
	db      *gorm.DB
	nodes   []identity.Principal
	grants  []ReserveRequest
	budgets []string
	report  *nodev1.AuthorityReport
}

func newAuthorityRecoveryFixture(t *testing.T) authorityRecoveryFixture {
	t.Helper()
	s, db, nodes, client, term, budgets := setupLedger(t)
	f := authorityRecoveryFixture{store: s, db: db, nodes: nodes, budgets: budgets}
	if err := s.SetRateBudget(t.Context(), client, client, term, 1, limitedRates(10000, 1000)); err != nil {
		t.Fatal(err)
	}
	for index, node := range nodes {
		grant := reserve(t, s, node, client, term, budgets, 1000)
		f.grants = append(f.grants, grant)
		generation, err := s.BeginSession(t.Context(), node, journalHello(t, s, node))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Publish(t.Context(), node.NodeID, []byte("recovery-test-policy")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delivery(t.Context(), node, generation); err != nil {
			t.Fatal(err)
		}
		rates := Rates{}
		if index == 0 {
			rates = limitedRates(10000, 1000)
		}
		if _, err := s.RequestRateShare(t.Context(), RateRequest{OperationID: uuid.NewString(), GrantID: grant.GrantID, BudgetID: client, NodeID: node.NodeID, InstallationID: node.InstallationID, RuntimeID: "xray", Desired: rates}); err != nil {
			t.Fatal(err)
		}
	}
	registration := bootstrapTestRuntime(t, s, nodes[0], "xray")
	j := registration.Runtimes[0]
	f.report = &nodev1.AuthorityReport{
		NodeId: nodes[0].NodeID, InstallationId: nodes[0].InstallationID, AgentStateJournalId: registration.AgentStateJournalId, RuntimeId: "xray", StateJournalId: j.StateJournalId, CreditJournalId: j.CreditJournalId, Sequence: 1, ObservedUnixMilli: time.Now().UnixMilli(), RuntimeReady: true,
		Grants: []*nodev1.GrantAuthority{{GrantId: f.grants[0].GrantID, ClientId: client, TermId: term, PolicyRevision: 1, AuthorityRevision: 1, CreditCeiling: 1000, ReservedHighWater: 1000, AdmissionHighWater: 1, AdmissionKnown: true, SpendableBytes: 999, SpendableKnown: true, AuthorizationActive: true}},
	}
	if _, err := s.IngestAuthority(t.Context(), nodes[0], f.report); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f authorityRecoveryFixture) witness(credit, rate bool) *nodev1.AuthorityReport {
	report := proto.Clone(f.report).(*nodev1.AuthorityReport)
	report.Sequence = 2
	if credit {
		report.Grants[0].CreditCeiling, report.Grants[0].SpendableBytes = 1001, 1000
	}
	if rate {
		report.Grants[0].AuthorityRevision = 2
	}
	return report
}

func (f authorityRecoveryFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	var journal model.FleetRuntimeJournal
	var observed model.FleetGrantAuthority
	var share model.FleetRateShare
	if err := f.db.First(&journal, "node_id = ? AND runtime_id = ?", f.nodes[0].NodeID, "xray").Error; err != nil || journal.Sequence != 1 {
		t.Fatalf("rollback witness committed cursor=%+v %v", journal, err)
	}
	if err := f.db.First(&observed, "grant_id = ?", f.grants[0].GrantID).Error; err != nil || observed.ReportSequence != 1 || observed.CreditCeiling != 1000 || observed.AuthorityRevision != 1 || observed.AdmissionHighWater != 1 {
		t.Fatalf("rollback witness became accepted authority=%+v %v", observed, err)
	}
	if err := f.db.First(&share, "id = ?", f.grants[0].GrantID).Error; err != nil || share.Revision != 1 || share.AppliedRevision != 0 {
		t.Fatalf("rollback witness settled rate authority=%+v %v", share, err)
	}
	for _, id := range f.budgets {
		budget(t, f.db, id, 0, 2000)
	}
	for _, table := range []any{&model.FleetMeter{}, &model.FleetUsageCursor{}, &model.FleetTermUsage{}, &model.FleetTermUsageDay{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rollback witness changed accounting %T=%d %v", table, count, err)
		}
	}
}

func TestAuthorityRollbackWitnessPersistsGlobalHoldWithoutBillingPostgres(t *testing.T) {
	for _, test := range []struct {
		name         string
		credit, rate bool
		sequence     uint64
		reason       string
	}{
		{"credit", true, false, 2, "NODE_CREDIT_HIGH_WATER_AHEAD"},
		{"rate", false, true, 2, "NODE_RATE_HIGH_WATER_AHEAD"},
		{"both", true, true, 2, "NODE_CREDIT_HIGH_WATER_AHEAD"},
		{"credit-sequence-gap", true, false, 9, "NODE_CREDIT_HIGH_WATER_AHEAD"},
		{"rate-sequence-gap", false, true, 9, "NODE_RATE_HIGH_WATER_AHEAD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAuthorityRecoveryFixture(t)
			report := f.witness(test.credit, test.rate)
			report.Sequence = test.sequence
			if _, err := f.store.IngestAuthority(t.Context(), f.nodes[0], report); !errors.Is(err, ErrRecovery) {
				t.Fatalf("rollback witness=%v", err)
			}
			var fence model.FleetFence
			if err := f.db.First(&fence, "id = 1").Error; err != nil || !fence.RecoveryRequired || fence.Reason != test.reason || fence.WitnessNodeID == nil || *fence.WitnessNodeID != f.nodes[0].NodeID {
				t.Fatalf("rollback witness did not persist scoped global hold=%+v %v", fence, err)
			}
			f.assertUnchanged(t)
			reopened, err := NewStore(f.db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.IngestAuthority(t.Context(), f.nodes[0], report); !errors.Is(err, ErrRecovery) {
				t.Fatalf("retry cleared recovery hold=%v", err)
			}
			request := f.grants[1]
			request.OperationID, request.Bytes = uuid.NewString(), 1
			if _, err := reopened.Reserve(t.Context(), request); !errors.Is(err, ErrRecovery) {
				t.Fatalf("other node issued through rollback hold=%v", err)
			}
			if _, err := reopened.Publish(t.Context(), f.nodes[1].NodeID, []byte("new-policy")); !errors.Is(err, ErrRecovery) {
				t.Fatalf("other node published through rollback hold=%v", err)
			}
		})
	}
}

func TestInvalidAuthorityWitnessNeverCreatesGlobalHoldPostgres(t *testing.T) {
	for _, name := range []string{"foreign-term", "unknown-grant", "foreign-client", "unknown-share", "foreign-rate-owner", "regression", "mixed-foreign", "sequence-only", "journal-mismatch", "unverified-certificate"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityRecoveryFixture(t)
			report := f.witness(true, true)
			principal, want := f.nodes[0], ErrScope
			switch name {
			case "foreign-term":
				report.Grants[0].TermId = uuid.NewString()
			case "unknown-grant":
				report.Grants[0].GrantId = uuid.NewString()
			case "foreign-client":
				report.Grants[0].ClientId = uuid.NewString()
			case "unknown-share":
				if err := f.db.Delete(&model.FleetRateShare{}, "id = ?", f.grants[0].GrantID).Error; err != nil {
					t.Fatal(err)
				}
			case "foreign-rate-owner":
				if err := f.db.Model(&model.FleetRateBudget{}).Where("id = ?", f.grants[0].ClientID).Update("client_id", uuid.NewString()).Error; err != nil {
					t.Fatal(err)
				}
			case "regression":
				report.Grants[0].AdmissionHighWater, report.Grants[0].SpendableBytes, want = 0, 1001, ErrConflict
			case "mixed-foreign":
				foreign := proto.Clone(report.Grants[0]).(*nodev1.GrantAuthority)
				foreign.GrantId = uuid.NewString()
				report.Grants = append(report.Grants, foreign)
			case "sequence-only":
				report, want = f.witness(false, false), ErrSequence
				report.Sequence = 9
			case "journal-mismatch":
				report.StateJournalId, want = uuid.NewString(), ErrRecovery
			case "unverified-certificate":
				principal.Serial = "not-the-registered-certificate"
			}
			if _, err := f.store.IngestAuthority(t.Context(), principal, report); !errors.Is(err, want) {
				t.Fatalf("invalid witness=%v want=%v", err, want)
			}
			var fence model.FleetFence
			if err := f.db.First(&fence, "id = 1").Error; err != nil || fence.RecoveryRequired || fence.Reason != "" || fence.WitnessNodeID != nil {
				t.Fatalf("unproven witness froze fleet=%+v %v", fence, err)
			}
			var observed model.FleetGrantAuthority
			if err := f.db.First(&observed, "grant_id = ?", f.grants[0].GrantID).Error; err != nil || observed.ReportSequence != 1 || observed.CreditCeiling != 1000 {
				t.Fatalf("invalid witness changed observation=%+v %v", observed, err)
			}
		})
	}
}

func TestAuthorityRollbackRevalidatesAfterConcurrentIssuancePostgres(t *testing.T) {
	f := newAuthorityRecoveryFixture(t)
	var issued atomic.Bool
	var issueErr error
	callback := "test:rollback_witness_race"
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if tx.Statement.Table != "fleet_fences" || !ok || locking.Strength != "UPDATE" || !issued.CompareAndSwap(false, true) {
			return
		}
		request := f.grants[0]
		request.OperationID, request.Bytes = uuid.NewString(), 1
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, issueErr = f.store.Reserve(ctx, request)
	}); err != nil {
		t.Fatal(err)
	}
	_, reportErr := f.store.IngestAuthority(t.Context(), f.nodes[0], f.witness(true, false))
	_ = f.db.Callback().Query().Remove(callback)
	if !issued.Load() || issueErr != nil || !errors.Is(reportErr, ErrScope) {
		t.Fatalf("second-transaction revalidation=%v issued=%v issue=%v", reportErr, issued.Load(), issueErr)
	}
	var fence model.FleetFence
	if err := f.db.First(&fence, "id = 1").Error; err != nil || fence.RecoveryRequired {
		t.Fatalf("resolved witness created hold=%+v %v", fence, err)
	}
	for _, id := range f.budgets {
		budget(t, f.db, id, 0, 2001)
	}
}

func TestConcurrentAuthorityRollbackWitnessesDoNotUpgradeSharedLocksPostgres(t *testing.T) {
	f := newAuthorityRecoveryFixture(t)
	report := f.witness(true, false)
	registration := bootstrapTestRuntime(t, f.store, f.nodes[1], "xray")
	other := proto.Clone(report).(*nodev1.AuthorityReport)
	other.NodeId, other.InstallationId, other.AgentStateJournalId = f.nodes[1].NodeID, f.nodes[1].InstallationID, registration.AgentStateJournalId
	other.StateJournalId, other.CreditJournalId, other.Sequence = registration.Runtimes[0].StateJournalId, registration.Runtimes[0].CreditJournalId, 1
	other.Grants[0].GrantId = f.grants[1].GrantID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for index, item := range []*nodev1.AuthorityReport{report, other} {
		workers.Go(func() { _, err := f.store.IngestAuthority(ctx, f.nodes[index], item); results <- err })
	}
	workers.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, ErrRecovery) {
			t.Fatalf("concurrent rollback hold=%v", err)
		}
	}
	var fence model.FleetFence
	if err := f.db.First(&fence, "id = 1").Error; err != nil || !fence.RecoveryRequired || fence.WitnessNodeID == nil || fence.Reason != "NODE_CREDIT_HIGH_WATER_AHEAD" {
		t.Fatalf("concurrent hold lost witness=%+v %v", fence, err)
	}
}

func TestAuthorityRollbackPreservesExistingRecoveryReasonPostgres(t *testing.T) {
	f := newAuthorityRecoveryFixture(t)
	if err := f.store.HoldForRecovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IngestAuthority(t.Context(), f.nodes[0], f.witness(true, true)); !errors.Is(err, ErrRecovery) {
		t.Fatalf("existing hold=%v", err)
	}
	var fence model.FleetFence
	if err := f.db.First(&fence, "id = 1").Error; err != nil || !fence.RecoveryRequired || fence.Reason != "OPERATOR_RESTORE" || fence.WitnessNodeID != nil {
		t.Fatalf("rollback overwrote existing recovery reason=%+v %v", fence, err)
	}
}

func TestAuthorityRollbackRateWitnessSurvivesRevisionReusePostgres(t *testing.T) {
	for _, laterObservation := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-rate", true: "new-rate-and-observation"}[laterObservation], func(t *testing.T) {
			f := newAuthorityRecoveryFixture(t)
			var advanced atomic.Bool
			var advanceErr error
			callback := "test:rate_witness_reuse"
			if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
				if tx.Statement.Table != "fleet_fences" || !ok || locking.Strength != "UPDATE" || !advanced.CompareAndSwap(false, true) {
					return
				}
				grant := f.grants[0]
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				_, advanceErr = f.store.RequestRateShare(ctx, RateRequest{OperationID: uuid.NewString(), GrantID: grant.GrantID, BudgetID: grant.ClientID, NodeID: grant.NodeID, InstallationID: grant.InstallationID, RuntimeID: grant.RuntimeID})
				if advanceErr == nil && laterObservation {
					report := f.witness(false, true)
					report.Grants[0].AdmissionHighWater, report.Grants[0].SpendableBytes = 2, 998
					_, advanceErr = f.store.IngestAuthority(ctx, f.nodes[0], report)
				}
			}); err != nil {
				t.Fatal(err)
			}
			_, reportErr := f.store.IngestAuthority(t.Context(), f.nodes[0], f.witness(false, true))
			_ = f.db.Callback().Query().Remove(callback)
			if !advanced.Load() || advanceErr != nil || !errors.Is(reportErr, ErrRecovery) {
				t.Fatalf("validated rate witness erased by numeric catch-up: report=%v advanced=%v other=%v", reportErr, advanced.Load(), advanceErr)
			}
			var fence model.FleetFence
			if err := f.db.First(&fence, "id = 1").Error; err != nil || !fence.RecoveryRequired || fence.Reason != "NODE_RATE_HIGH_WATER_AHEAD" || fence.WitnessNodeID == nil || *fence.WitnessNodeID != f.nodes[0].NodeID {
				t.Fatalf("reused rate revision erased recovery witness=%+v %v", fence, err)
			}
			var observed model.FleetGrantAuthority
			expected := int64(1)
			if laterObservation {
				expected = 2
			}
			if err := f.db.First(&observed, "grant_id = ?", f.grants[0].GrantID).Error; err != nil || observed.ReportSequence != expected || observed.AdmissionHighWater != expected {
				t.Fatalf("witness overwrote later observation=%+v %v", observed, err)
			}
			for _, id := range f.budgets {
				budget(t, f.db, id, 0, 2000)
			}
		})
	}
}

func TestAuthorityRollbackRevalidatesOwnershipBeforeHoldPostgres(t *testing.T) {
	for _, kind := range []string{"rate-owner", "grant-term", "runtime-journal"} {
		t.Run(kind, func(t *testing.T) {
			f := newAuthorityRecoveryFixture(t)
			var changed atomic.Bool
			var changeErr error
			callback := "test:witness_scope_changed"
			if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
				if tx.Statement.Table != "fleet_fences" || !ok || locking.Strength != "UPDATE" || !changed.CompareAndSwap(false, true) {
					return
				}
				switch kind {
				case "rate-owner":
					changeErr = f.db.Model(&model.FleetRateBudget{}).Where("id = ?", f.grants[0].ClientID).Update("client_id", uuid.NewString()).Error
				case "grant-term":
					changeErr = f.db.Model(&model.FleetGrant{}).Where("id = ?", f.grants[0].GrantID).Update("term_id", uuid.NewString()).Error
				case "runtime-journal":
					changeErr = f.db.Model(&model.FleetRuntimeJournal{}).Where("node_id = ? AND runtime_id = ?", f.nodes[0].NodeID, "xray").Update("credit_journal_id", uuid.NewString()).Error
				}
			}); err != nil {
				t.Fatal(err)
			}
			_, reportErr := f.store.IngestAuthority(t.Context(), f.nodes[0], f.witness(true, true))
			_ = f.db.Callback().Query().Remove(callback)
			if !changed.Load() || changeErr != nil || !errors.Is(reportErr, ErrScope) {
				t.Fatalf("changed witness scope=%v changed=%v other=%v", reportErr, changed.Load(), changeErr)
			}
			var fence model.FleetFence
			if err := f.db.First(&fence, "id = 1").Error; err != nil || fence.RecoveryRequired {
				t.Fatalf("changed witness ownership froze fleet=%+v %v", fence, err)
			}
		})
	}
}
