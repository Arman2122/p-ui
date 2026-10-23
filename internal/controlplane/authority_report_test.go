package controlplane

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestAuthorityReportIsIdempotentScopedAndNeverBillablePostgres(t *testing.T) {
	s, db, nodes, client, term, budgets := setupLedger(t)
	p := nodes[0]
	registration := bootstrapTestRuntime(t, s, p, "xray")
	grant := reserve(t, s, p, client, term, budgets, 1000)
	generation, err := s.BeginSession(t.Context(), p, journalHello(t, s, p))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(t.Context(), p.NodeID, []byte("test policy")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delivery(t.Context(), p, generation); err != nil {
		t.Fatal(err)
	}
	j := registration.Runtimes[0]
	report := &nodev1.AuthorityReport{NodeId: p.NodeID, InstallationId: p.InstallationID, AgentStateJournalId: registration.AgentStateJournalId, RuntimeId: j.RuntimeId, StateJournalId: j.StateJournalId, CreditJournalId: j.CreditJournalId, Sequence: 1, ObservedUnixMilli: time.Now().UnixMilli(), RuntimeReady: true, Grants: []*nodev1.GrantAuthority{{GrantId: grant.GrantID, ClientId: client, TermId: term, PolicyRevision: 1, CreditCeiling: 1000, ReservedHighWater: 1000, AdmissionHighWater: 1, AdmissionKnown: true, SpendableBytes: 999, SpendableKnown: true, AuthorizationActive: true}}}
	receipt, err := s.IngestAuthority(t.Context(), p, report)
	if err != nil || receipt.Sequence != 1 || receipt.StateJournalId != j.StateJournalId {
		t.Fatalf("report=%v %v", receipt, err)
	}
	if err := db.Model(&model.FleetRuntimeJournal{}).Where("node_id = ? AND runtime_id = ?", p.NodeID, j.RuntimeId).Update("received_unix_milli", 1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestAuthority(t.Context(), p, report); err != nil {
		t.Fatal(err)
	}
	var cursor model.FleetRuntimeJournal
	if err := db.First(&cursor, "node_id = ? AND runtime_id = ?", p.NodeID, j.RuntimeId).Error; err != nil {
		t.Fatal(err)
	}
	if cursor.ReceivedUnixMilli != 1 || cursor.Sequence != 1 || cursor.ObservedUnixMilli != report.ObservedUnixMilli {
		t.Fatalf("replay refreshed evidence: %+v", cursor)
	}
	for _, test := range []struct {
		name string
		edit func(*nodev1.AuthorityReport)
		want error
	}{
		{"different replay", func(r *nodev1.AuthorityReport) { r.RuntimeReady = false }, ErrConflict},
		{"sequence gap", func(r *nodev1.AuthorityReport) { r.Sequence = 3 }, ErrSequence},
		{"foreign term", func(r *nodev1.AuthorityReport) { r.Sequence = 2; r.Grants[0].TermId = uuid.NewString() }, ErrScope},
		{"reservation regression", func(r *nodev1.AuthorityReport) { r.Sequence = 2; r.Grants[0].ReservedHighWater = 999 }, ErrConflict},
		{"partial foreign batch", func(r *nodev1.AuthorityReport) {
			r.Sequence = 2
			r.Grants[0].AdmissionHighWater = 2
			r.Grants[0].SpendableBytes = 998
			foreign := proto.Clone(r.Grants[0]).(*nodev1.GrantAuthority)
			foreign.GrantId = uuid.NewString()
			r.Grants = append(r.Grants, foreign)
		}, ErrScope},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := proto.Clone(report).(*nodev1.AuthorityReport)
			test.edit(bad)
			if _, err := s.IngestAuthority(t.Context(), p, bad); !errors.Is(err, test.want) {
				t.Fatalf("report rejection=%v want %v", err, test.want)
			}
		})
	}
	var observation model.FleetGrantAuthority
	if err := db.First(&observation, "grant_id = ?", grant.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if observation.ReportSequence != 1 || observation.AdmissionHighWater != 1 || observation.SpendableBytes != 999 {
		t.Fatalf("rejected report partially committed: %+v", observation)
	}
	for _, id := range budgets {
		budget(t, db, id, 0, 1000)
	}
	for _, table := range []any{&model.FleetMeter{}, &model.FleetUsageCursor{}, &model.FleetTermUsage{}, &model.FleetTermUsageDay{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("authority polluted accounting %T: %d %v", table, count, err)
		}
	}
	heartbeat := proto.Clone(report).(*nodev1.AuthorityReport)
	heartbeat.Sequence = 2
	heartbeat.Grants = nil
	heartbeat.ObservedUnixMilli++
	if _, err := s.IngestAuthority(t.Context(), p, heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&observation, "grant_id = ?", grant.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if observation.ReportSequence != 1 {
		t.Fatal("runtime heartbeat rewrote grant identity")
	}
}

func TestJournalReplacementRequiresExplicitRecoveryPostgres(t *testing.T) {
	for _, kind := range []string{"agent lost", "worker journals lost", "master binding lost"} {
		t.Run(kind, func(t *testing.T) {
			s, db, nodes, client, term, budgets := setupLedger(t)
			p := nodes[0]
			registration := bootstrapTestRuntime(t, s, p, "xray")
			grant := reserve(t, s, p, client, term, budgets, 100)
			replacement := proto.Clone(registration).(*nodev1.JournalRegistration)
			switch kind {
			case "agent lost":
				replacement.AgentStateJournalId = uuid.NewString()
			case "worker journals lost":
				replacement.Runtimes[0].StateJournalId = uuid.NewString()
				replacement.Runtimes[0].CreditJournalId = uuid.NewString()
			case "master binding lost":
				if err := db.Delete(&model.FleetNodeJournal{}, "node_id = ?", p.NodeID).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RegisterJournals(t.Context(), p, replacement); !errors.Is(err, ErrRecovery) {
				t.Fatalf("replacement adopted: %v", err)
			}
			var hold model.FleetNodeJournal
			if err := db.First(&hold, "node_id = ?", p.NodeID).Error; err != nil {
				t.Fatal(err)
			}
			if !hold.RecoveryRequired || hold.Reason == "" {
				t.Fatalf("missing durable hold: %+v", hold)
			}
			if _, err := s.RegisterJournals(t.Context(), p, registration); !errors.Is(err, ErrRecovery) {
				t.Fatalf("old identity cleared hold: %v", err)
			}
			grant.OperationID = uuid.NewString()
			grant.Bytes = 1
			if _, err := s.Reserve(t.Context(), grant); !errors.Is(err, ErrRecovery) {
				t.Fatalf("held node received authority: %v", err)
			}
			if _, err := s.Delivery(t.Context(), p, 1); !errors.Is(err, ErrRecovery) {
				t.Fatalf("held node received deployment: %v", err)
			}
			for _, id := range budgets {
				budget(t, db, id, 0, 100)
			}
		})
	}
}

func TestFundingRequiresPriorRuntimeJournalBindingPostgres(t *testing.T) {
	s, _, nodes, client, term, budgets := setupLedger(t)
	p := nodes[0]
	request := ReserveRequest{OperationID: uuid.NewString(), GrantID: uuid.NewString(), NodeID: p.NodeID, InstallationID: p.InstallationID, RuntimeID: "xray", ClientID: client, TermID: term, BudgetIDs: budgets, Bytes: 100}
	if _, err := s.Reserve(t.Context(), request); !errors.Is(err, ErrScope) {
		t.Fatalf("unbound funding=%v", err)
	}
	bootstrapTestRuntime(t, s, p, "xray")
	if _, err := s.Reserve(t.Context(), request); err != nil {
		t.Fatalf("legitimate bootstrap funding=%v", err)
	}
}
