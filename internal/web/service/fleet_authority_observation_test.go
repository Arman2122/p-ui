package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetObservedRateAuthorityCannotRegressOrSettleReservations(t *testing.T) {
	f := newAuthorityFixture(t)
	report := &nodev1.AuthorityReport{
		NodeId: f.request.NodeId, InstallationId: f.request.InstallationId, AgentStateJournalId: f.request.AgentStateJournalId,
		RuntimeId: f.request.RuntimeId, StateJournalId: f.request.StateJournalId, CreditJournalId: f.request.CreditJournalId, Sequence: 2, ObservedUnixMilli: time.Now().UnixMilli(), RuntimeReady: true,
		Grants: []*nodev1.GrantAuthority{{
			GrantId: f.credit.Grant.ID, ClientId: f.credit.Grant.ClientID, TermId: f.credit.Grant.TermID, PolicyRevision: 1, AuthorityRevision: 1,
			CreditCeiling: 300, AdmissionKnown: true, SpendableBytes: 300, SpendableKnown: true, AuthorizationActive: true,
		}},
	}
	if _, err := f.f.store.IngestAuthority(t.Context(), f.f.nodes[0], report); err != nil {
		t.Fatal(err)
	}
	var observed model.FleetGrantAuthority
	if err := f.f.db.First(&observed, "grant_id = ?", f.credit.Grant.ID).Error; err != nil || observed.AuthorityRevision != 1 {
		t.Fatalf("observed rate authority=%+v %v", observed, err)
	}
	var share model.FleetRateShare
	if err := f.f.db.First(&share, "id = ?", f.credit.Grant.ID).Error; err != nil || share.AppliedRevision != 0 {
		t.Fatalf("observation silently settled reserved rates=%+v %v", share, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*nodev1.AuthorityReport)
		want   error
	}{
		{"forgotten revision", func(r *nodev1.AuthorityReport) { r.Grants[0].AuthorityRevision = 0 }, controlplane.ErrConflict},
		{"unknown grant", func(r *nodev1.AuthorityReport) {
			r.Grants[0].GrantId = uuid.NewString()
			r.Grants[0].AuthorityRevision = 2
		}, controlplane.ErrScope},
		{"foreign client", func(r *nodev1.AuthorityReport) {
			r.Grants[0].ClientId = uuid.NewString()
			r.Grants[0].AuthorityRevision = 2
		}, controlplane.ErrScope},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := proto.Clone(report).(*nodev1.AuthorityReport)
			bad.Sequence = 3
			test.mutate(bad)
			if _, err := f.f.store.IngestAuthority(t.Context(), f.f.nodes[0], bad); !errors.Is(err, test.want) {
				t.Fatalf("authority report=%v want%v", err, test.want)
			}
			var runtime model.FleetRuntimeJournal
			if err := f.f.db.First(&runtime, "node_id = ? AND runtime_id = ?", f.request.NodeId, f.request.RuntimeId).Error; err != nil || runtime.Sequence != 2 {
				t.Fatalf("rejected report advanced cursor=%+v %v", runtime, err)
			}
		})
	}
}

func TestFleetObservedAheadRateRequiresPersistentRecovery(t *testing.T) {
	f := newAuthorityFixture(t)
	report := &nodev1.AuthorityReport{
		NodeId: f.request.NodeId, InstallationId: f.request.InstallationId, AgentStateJournalId: f.request.AgentStateJournalId,
		RuntimeId: f.request.RuntimeId, StateJournalId: f.request.StateJournalId, CreditJournalId: f.request.CreditJournalId, Sequence: 2, ObservedUnixMilli: time.Now().UnixMilli(), RuntimeReady: true,
		Grants: []*nodev1.GrantAuthority{{GrantId: f.credit.Grant.ID, ClientId: f.credit.Grant.ClientID, TermId: f.credit.Grant.TermID, PolicyRevision: 1, AuthorityRevision: 2, CreditCeiling: 300, AdmissionKnown: true, SpendableKnown: true, SpendableBytes: 300, AuthorizationActive: true}},
	}
	if _, err := f.f.store.IngestAuthority(t.Context(), f.f.nodes[0], report); !errors.Is(err, controlplane.ErrRecovery) {
		t.Fatalf("ahead fleet rate witness=%v", err)
	}
	var fence model.FleetFence
	if err := f.f.db.First(&fence, "id = 1").Error; err != nil || !fence.RecoveryRequired || fence.Reason != "NODE_RATE_HIGH_WATER_AHEAD" || fence.WitnessNodeID == nil || *fence.WitnessNodeID != f.f.nodes[0].NodeID {
		t.Fatalf("fleet rate witness did not persist recovery=%+v %v", fence, err)
	}
	var share model.FleetRateShare
	if err := f.f.db.First(&share, "id = ?", f.credit.Grant.ID).Error; err != nil || share.Revision != 1 || share.AppliedRevision != 0 {
		t.Fatalf("ahead witness settled fleet rate=%+v %v", share, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", f.active.Term.BudgetID).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 300 {
		t.Fatalf("ahead witness changed billing=%+v %v", budget, err)
	}
}
