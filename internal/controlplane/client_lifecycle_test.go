package controlplane

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func managedClient(t *testing.T, s *Store, db *gorm.DB) model.FleetClient {
	t.Helper()
	panel := model.ClientRecord{Email: uuid.NewString(), UUID: uuid.NewString(), Enable: true}
	if err := db.Create(&panel).Error; err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(panel.UUID))
	client, err := s.BindClient(t.Context(), panel.Id, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func activation(client model.FleetClient) TransitionRequest {
	return TransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(client.Revision),
		ExpectedCredentialGeneration: uint64(client.CredentialGeneration), Kind: Activation, SourcePolicyDigest: sourceDigest("activation snapshot"),
		Spec: TermSpec{QuotaBytes: 1000, DurationMillis: 3_600_000, Rates: limitedRates(100_000_000, 10000)},
	}
}

func sourceDigest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func TestFleetClientIdentityAndActivationFencingPostgres(t *testing.T) {
	s, db, _, _, _, _ := setupLedger(t)
	client := managedClient(t, s, db)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			replayed, err := s.BindClient(t.Context(), *client.PanelClientID, client.CredentialDigest)
			if err != nil || replayed.ID != client.ID {
				t.Errorf("concurrent binding=%+v err=%v", replayed, err)
			}
		})
	}
	wg.Wait()
	stale := activation(client)
	newDigest := sha256.Sum256([]byte("rotated credential"))
	if _, err := s.BindClient(t.Context(), *client.PanelClientID, newDigest[:]); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding silently rotated credentials=%v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("id = ?", *client.PanelClientID).Updates(map[string]any{"email": uuid.NewString(), "uuid": uuid.NewString()}).Error; err != nil {
		t.Fatal(err)
	}
	rotated, err := s.ReconcileCredentials(t.Context(), client.ID, 1, newDigest[:])
	if err != nil || rotated.ID != client.ID || rotated.CredentialGeneration != 2 || rotated.Revision != 2 {
		t.Fatalf("credential rotation=%+v err=%v", rotated, err)
	}
	if replay, err := s.ReconcileCredentials(t.Context(), client.ID, 1, newDigest[:]); err != nil || replay.CredentialGeneration != 2 {
		t.Fatalf("lost rotation response=%+v err=%v", replay, err)
	}
	if _, err := s.ReconcileCredentials(t.Context(), client.ID, 1, client.CredentialDigest); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale credential restoration=%v", err)
	}
	if _, err := s.Transition(t.Context(), stale); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale first-use demand activated account=%v", err)
	}
	stale.ExpectedRevision = uint64(rotated.Revision)
	if _, err := s.Transition(t.Context(), stale); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale credential generation activated account=%v", err)
	}
	stale.ExpectedRevision, stale.ExpectedCredentialGeneration = 1, uint64(rotated.CredentialGeneration)
	if _, err := s.Transition(t.Context(), stale); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale master revision activated account=%v", err)
	}
	request := activation(rotated)
	request.Spec.OpeningUsedBytes = 100
	request.Spec.OpeningUploaded, request.Spec.OpeningDownloaded = 40, 60
	var results [8]TransitionResult
	var failures [8]error
	for i := range results {
		wg.Go(func() { results[i], failures[i] = s.Transition(t.Context(), request) })
	}
	wg.Wait()
	for i, result := range results {
		if failures[i] != nil || result.Term.ID != results[0].Term.ID || result.PendingDrain || result.Term.StartedUnixMilli == 0 ||
			result.Term.ExpiresUnixMilli-result.Term.StartedUnixMilli != request.Spec.DurationMillis {
			t.Fatalf("concurrent first-use[%d]=%+v err=%v", i, result, failures[i])
		}
	}
	budget(t, db, results[0].Term.BudgetID, 100, 0)
	if replay, err := s.Transition(t.Context(), request); err != nil || !replay.Replayed {
		t.Fatalf("activation replay would repeat panel side effects=%+v err=%v", replay, err)
	}
	request.Spec.QuotaBytes++
	if _, err := s.Transition(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting activation replay=%v", err)
	}
	request.OperationID = uuid.NewString()
	if _, err := s.Transition(t.Context(), request); !errors.Is(err, ErrRevision) {
		t.Fatalf("second first-use demand minted another term=%v", err)
	}
	var count int64
	if err := db.Model(&model.FleetTerm{}).Where("client_id = ?", client.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("activation terms=%d err=%v", count, err)
	}
}

func stoppedBundle(grant ReserveRequest, term model.FleetTerm, rateRevision int64, enabled bool, rates Rates) configuration.Bundle {
	return configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{
		ID: grant.RuntimeID, Core: "xray", Configuration: json.RawMessage(`{}`),
		Grants: []governor.Grant{{ID: grant.GrantID, ClientID: grant.ClientID, RateRevision: uint64(rateRevision), Policy: limits.Policy{
			Term: grant.TermID, Revision: 1, Enabled: enabled, CreditCeiling: uint64(grant.Bytes), ExpiresUnixMilli: term.ExpiresUnixMilli,
			Upload: rates.Upload, Download: rates.Download,
		}}},
	}}}
}

func applyBundle(t *testing.T, s *Store, node identity.Principal, bundle configuration.Bundle) {
	t.Helper()
	generation, err := s.BeginSession(t.Context(), node, journalHello(t, s, node))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishConfiguration(t.Context(), node, bundle); err != nil {
		t.Fatal(err)
	}
	deployment, err := s.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(t.Context(), node, generation, &nodev1.ApplyResult{
		Revision: deployment.Revision, AuthorityGeneration: generation,
		PayloadSha256: deployment.PayloadSha256, Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED,
		AssetManifestSha256: configuration.PayloadAssetManifest(deployment.Payload),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetResetRequiresExplicitAppliedStopForEveryGrantPostgres(t *testing.T) {
	s, db, nodes, _, _, scopes := setupLedger(t)
	client := managedClient(t, s, db)
	active, err := s.Transition(t.Context(), activation(client))
	if err != nil {
		t.Fatal(err)
	}
	budgets := append([]string{active.Term.BudgetID}, scopes...)
	if _, err := s.Reserve(t.Context(), ReserveRequest{
		OperationID: uuid.NewString(), GrantID: uuid.NewString(), NodeID: nodes[0].NodeID,
		InstallationID: nodes[0].InstallationID, RuntimeID: "xray", ClientID: client.ID, TermID: active.Term.ID, BudgetIDs: scopes, Bytes: 1001,
	}); !errors.Is(err, ErrScope) {
		t.Fatalf("managed grant omitted entitlement budget=%v", err)
	}
	a := reserve(t, s, nodes[0], client.ID, active.Term.ID, budgets, 400)
	b := reserve(t, s, nodes[1], client.ID, active.Term.ID, budgets, 400)
	orphan := reserve(t, s, nodes[0], client.ID, active.Term.ID, budgets, 100, "xray-extra")
	requests := []RateRequest{
		{OperationID: uuid.NewString(), GrantID: a.GrantID, BudgetID: client.ID, NodeID: a.NodeID, InstallationID: a.InstallationID, RuntimeID: a.RuntimeID, Desired: limitedRates(60_000_000, 6000)},
		{OperationID: uuid.NewString(), GrantID: b.GrantID, BudgetID: client.ID, NodeID: b.NodeID, InstallationID: b.InstallationID, RuntimeID: b.RuntimeID, Desired: limitedRates(40_000_000, 4000)},
	}
	for _, request := range requests {
		if _, err := s.RequestRateShare(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Ingest(t.Context(), nodes[0], batch(nodes[0], a, 1, 50, 50)); err != nil {
		t.Fatal(err)
	}
	reset := TransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(active.Client.Revision),
		ExpectedCredentialGeneration: 1, ExpectedTermID: active.Term.ID, Kind: TrafficReset, SourcePolicyDigest: sourceDigest("reset snapshot"), Spec: TermSpec{QuotaBytes: 2000, Rates: limitedRates(100_000_000, 10000)},
	}
	pending, err := s.Transition(t.Context(), reset)
	if err != nil || !pending.PendingDrain || pending.Term.ExpiresUnixMilli != active.Term.ExpiresUnixMilli || pending.Term.StartedUnixMilli != 0 {
		t.Fatalf("reset intent=%+v err=%v", pending, err)
	}
	complete := CompleteTransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, PendingTermID: pending.Term.ID,
		ExpectedRevision: uint64(pending.Client.Revision), ExpectedCredentialGeneration: 1, SourcePolicyDigest: pending.Term.SourcePolicyDigest,
	}
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrDrain) {
		t.Fatalf("live predecessor completed=%v", err)
	}
	topUp := a
	topUp.OperationID, topUp.Bytes = uuid.NewString(), 1
	if _, err := s.Reserve(t.Context(), topUp); !errors.Is(err, ErrScope) {
		t.Fatalf("draining term received more quota=%v", err)
	}
	nextGrant := topUp
	nextGrant.GrantID, nextGrant.TermID, nextGrant.BudgetIDs = uuid.NewString(), pending.Term.ID, []string{pending.Term.BudgetID}
	if _, err := s.Reserve(t.Context(), nextGrant); !errors.Is(err, ErrScope) {
		t.Fatalf("pending successor received authority=%v", err)
	}
	enabled := stoppedBundle(a, active.Term, 1, true, requests[0].Desired)
	if _, err := s.PublishConfiguration(t.Context(), nodes[0], enabled); !errors.Is(err, ErrScope) {
		t.Fatalf("draining term re-enabled=%v", err)
	}
	raw, _ := json.Marshal(enabled)
	if _, err := s.Publish(t.Context(), nodes[0].NodeID, raw); !errors.Is(err, ErrScope) {
		t.Fatalf("raw publication bypassed term fence=%v", err)
	}
	shares := make([]model.FleetRateShare, 2)
	for i := range requests {
		requests[i].OperationID, requests[i].Desired = uuid.NewString(), Rates{}
		shares[i], err = s.RequestRateShare(t.Context(), requests[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ConfirmRateShare(t.Context(), nodes[i], requests[i].GrantID, uint64(shares[i].Revision), Rates{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrDrain) {
		t.Fatalf("rate-only confirmation substituted for disabled config=%v", err)
	}
	applyBundle(t, s, nodes[0], stoppedBundle(a, active.Term, shares[0].Revision, false, Rates{}))
	applyBundle(t, s, nodes[1], configuration.Bundle{Version: 1})
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrDrain) {
		t.Fatalf("omitted grant substituted for explicit stop=%v", err)
	}
	applyBundle(t, s, nodes[1], stoppedBundle(b, active.Term, shares[1].Revision, false, Rates{}))
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrDrain) {
		t.Fatalf("grant without a rate row escaped drain=%v", err)
	}
	orphanShare, err := s.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: orphan.GrantID,
		BudgetID: client.ID, NodeID: orphan.NodeID, InstallationID: orphan.InstallationID, RuntimeID: orphan.RuntimeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	applyBundle(t, s, nodes[0], stoppedBundle(orphan, active.Term, orphanShare.Revision, false, Rates{}))
	completed, err := s.CompleteTransition(t.Context(), complete)
	if err != nil || completed.PendingDrain || completed.Term.ExpiresUnixMilli != active.Term.ExpiresUnixMilli {
		t.Fatalf("confirmed reset=%+v err=%v", completed, err)
	}
	if replay, err := s.CompleteTransition(t.Context(), complete); err != nil || !replay.Replayed || replay.Term.ID != completed.Term.ID || replay.Term.StartedUnixMilli != completed.Term.StartedUnixMilli {
		t.Fatalf("reset replay=%+v err=%v", replay, err)
	}
	budget(t, db, active.Term.BudgetID, 100, 800)
	budget(t, db, completed.Term.BudgetID, 0, 0)
	for _, scope := range scopes {
		budget(t, db, scope, 100, 800)
	}
	if _, err := s.Ingest(t.Context(), nodes[0], batch(nodes[0], a, 2, 60, 60)); err != nil {
		t.Fatal(err)
	}
	budget(t, db, active.Term.BudgetID, 120, 780)
	budget(t, db, completed.Term.BudgetID, 0, 0)
	var previous, current model.FleetTermUsage
	if err := db.First(&previous, "term_id = ?", active.Term.ID).Error; err != nil || previous.Uploaded != 60 || previous.Downloaded != 60 {
		t.Fatalf("late report missing from predecessor projection=%+v err=%v", previous, err)
	}
	if err := db.First(&current, "term_id = ?", completed.Term.ID).Error; err != nil || current.Uploaded != 0 || current.Downloaded != 0 {
		t.Fatalf("late report changed new-term projection=%+v err=%v", current, err)
	}
}

func TestFleetRenewalAndOuterTransactionPostgres(t *testing.T) {
	s, db, _, _, _, _ := setupLedger(t)
	client := managedClient(t, s, db)
	request := activation(client)
	rollback := errors.New("master business update rejected")
	if err := db.Transaction(func(tx *gorm.DB) error {
		transactionStore, err := NewStore(tx)
		if err != nil {
			return err
		}
		if _, err := transactionStore.Transition(t.Context(), request); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	active, err := s.Transition(t.Context(), request)
	if err != nil {
		t.Fatalf("rolled-back activation leaked state=%v", err)
	}
	renew := TransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: uint64(active.Client.Revision),
		ExpectedCredentialGeneration: 1, ExpectedTermID: active.Term.ID, Kind: Renewal, SourcePolicyDigest: sourceDigest("renewal snapshot"), Spec: TermSpec{QuotaBytes: 1000, DurationMillis: 60000, Rates: limitedRates(100_000_000, 10000)},
	}
	if _, err := s.Transition(t.Context(), renew); !errors.Is(err, ErrRevision) {
		t.Fatalf("renewed unexpired term=%v", err)
	}
	if err := db.Model(&model.FleetTerm{}).Where("id = ?", active.Term.ID).Update("expires_unix_milli", time.Now().Add(-7*24*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	pending, err := s.Transition(t.Context(), renew)
	if err != nil || pending.Term.StartedUnixMilli != 0 || pending.Term.ExpiresUnixMilli != 0 {
		t.Fatalf("pending renewal starts before drain=%+v err=%v", pending, err)
	}
	complete := CompleteTransitionRequest{
		OperationID: uuid.NewString(), ClientID: client.ID,
		PendingTermID: pending.Term.ID, ExpectedRevision: uint64(pending.Client.Revision), ExpectedCredentialGeneration: 1, SourcePolicyDigest: pending.Term.SourcePolicyDigest,
	}
	digest := sha256.Sum256([]byte("credentials changed while draining"))
	rotated, err := s.ReconcileCredentials(t.Context(), client.ID, 1, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteTransition(t.Context(), complete); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale completion survived credential change=%v", err)
	}
	complete.ExpectedRevision, complete.ExpectedCredentialGeneration = uint64(rotated.Revision), uint64(rotated.CredentialGeneration)
	before := time.Now().UnixMilli()
	completed, err := s.CompleteTransition(t.Context(), complete)
	if err != nil || completed.Term.StartedUnixMilli < before || completed.Term.ExpiresUnixMilli-completed.Term.StartedUnixMilli != 60000 || completed.Term.Generation != 2 || completed.Term.CredentialGeneration != 2 {
		t.Fatalf("renewal accumulated old periods or started early=%+v err=%v", completed, err)
	}
}
