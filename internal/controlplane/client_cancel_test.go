package controlplane

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetCancellationPreservesStoppedAuthorityAndTermHistoryPostgres(t *testing.T) {
	s, db, nodes, _, _, _ := setupLedger(t)
	client := managedClient(t, s, db)
	active, err := s.Transition(t.Context(), activation(client))
	if err != nil {
		t.Fatal(err)
	}
	grant := reserve(t, s, nodes[0], client.ID, active.Term.ID, []string{active.Term.BudgetID}, 400)
	shareRequest := RateRequest{
		OperationID: uuid.NewString(), GrantID: grant.GrantID, BudgetID: client.ID,
		NodeID: grant.NodeID, InstallationID: grant.InstallationID, RuntimeID: grant.RuntimeID, Desired: limitedRates(100_000_000, 10000),
	}
	if _, err := s.RequestRateShare(t.Context(), shareRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(t.Context(), nodes[0], batch(nodes[0], grant, 1, 10, 10)); err != nil {
		t.Fatal(err)
	}
	request := TransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(active.Client.Revision),
		ExpectedCredentialGeneration: 1, ExpectedTermID: active.Term.ID, Kind: TrafficReset, SourcePolicyDigest: sourceDigest("first reset"),
		Spec: TermSpec{QuotaBytes: 500, Rates: limitedRates(100_000_000, 10000)},
	}
	pending, err := s.Transition(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	shareRequest.OperationID, shareRequest.Desired = uuid.NewString(), Rates{}
	share, err := s.RequestRateShare(t.Context(), shareRequest)
	if err != nil {
		t.Fatal(err)
	}
	applyBundle(t, s, nodes[0], stoppedBundle(grant, active.Term, share.Revision, false, Rates{}))
	complete := CompleteTransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, PendingTermID: pending.Term.ID,
		ExpectedRevision: uint64(pending.Client.Revision), ExpectedCredentialGeneration: 1, SourcePolicyDigest: sourceDigest("plan changed while draining"),
	}
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale plan snapshot completed=%v", err)
	}
	cancel := CancelTransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, PendingTermID: pending.Term.ID,
		ExpectedRevision: uint64(pending.Client.Revision), ExpectedCredentialGeneration: 1,
	}
	restored, err := s.CancelTransition(t.Context(), cancel)
	if err != nil || restored.PendingDrain || restored.Client.PendingTermID != nil || restored.Term.ID != active.Term.ID ||
		restored.Term.StartedUnixMilli != active.Term.StartedUnixMilli || restored.Term.ExpiresUnixMilli != active.Term.ExpiresUnixMilli {
		t.Fatalf("cancellation changed committed entitlement=%+v err=%v", restored, err)
	}
	if replay, err := s.CancelTransition(t.Context(), cancel); err != nil || !replay.Replayed || replay.Client.Revision != restored.Client.Revision {
		t.Fatalf("cancellation replay=%+v err=%v", replay, err)
	}
	budget(t, db, active.Term.BudgetID, 20, 380)
	budget(t, db, pending.Term.BudgetID, 0, 0)
	var stop model.FleetGrantStop
	if err := db.First(&stop, "grant_id = ?", grant.GrantID).Error; err != nil || stop.RateRevision != share.Revision {
		t.Fatalf("cancellation discarded stop proof=%+v err=%v", stop, err)
	}
	var retainedShare model.FleetRateShare
	if err := db.First(&retainedShare, "id = ?", share.ID).Error; err != nil {
		t.Fatal(err)
	}
	if rates, err := decodeRates(retainedShare.Outstanding); err != nil || rates != (Rates{}) {
		t.Fatalf("cancellation silently re-enabled forwarding=%+v err=%v", rates, err)
	}
	complete.SourcePolicyDigest = pending.Term.SourcePolicyDigest
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrRevision) {
		t.Fatalf("canceled successor activated=%v", err)
	}
	expired := time.Now().Add(-time.Minute).UnixMilli()
	if err := db.Model(&model.FleetTerm{}).Where("id = ?", active.Term.ID).Update("expires_unix_milli", expired).Error; err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.ExpectedRevision, request.SourcePolicyDigest = uuid.NewString(), uint64(restored.Client.Revision), sourceDigest("replacement reset")
	next, err := s.Transition(t.Context(), request)
	if err != nil || next.Term.Generation != 3 || next.Term.ExpiresUnixMilli != expired {
		t.Fatalf("replacement reused canceled generation or extended expiry=%+v err=%v", next, err)
	}
	cancel.OperationID, cancel.PendingTermID, cancel.ExpectedRevision = uuid.NewString(), next.Term.ID, uint64(next.Client.Revision)
	if canceled, err := s.CancelTransition(t.Context(), cancel); err != nil || canceled.Term.ExpiresUnixMilli != expired {
		t.Fatalf("expired predecessor was extended by cancellation=%+v err=%v", canceled, err)
	}
	grant.OperationID, grant.Bytes = uuid.NewString(), 1
	if _, err := s.Reserve(t.Context(), grant); !errors.Is(err, ErrScope) {
		t.Fatalf("cancellation revived expired quota authority=%v", err)
	}
	var abandoned model.FleetTerm
	if err := db.First(&abandoned, "id = ?", pending.Term.ID).Error; err != nil || abandoned.State != "closed" || abandoned.Generation != 2 {
		t.Fatalf("canceled epoch history changed=%+v err=%v", abandoned, err)
	}
}
