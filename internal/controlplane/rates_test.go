package controlplane

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func limitedRates(bits uint64, burst int) Rates {
	return Rates{
		Upload:   limits.Rate{Mode: limits.Limited, BitsPerSecond: bits, BurstBytes: burst},
		Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: bits, BurstBytes: burst},
	}
}

func TestSharedRatesRequireConfirmedReductionPostgres(t *testing.T) {
	store, db, nodes, client, term, budgets := setupLedger(t)
	a := reserve(t, store, nodes[0], client, term, budgets, 1000)
	b := reserve(t, store, nodes[1], client, term, budgets, 1000, "singbox")
	rateBudget := uuid.NewString()
	if err := store.SetRateBudget(t.Context(), rateBudget, client, term, 1, limitedRates(100_000_000, 10000)); err != nil {
		t.Fatal(err)
	}
	first := RateRequest{
		OperationID: uuid.NewString(), GrantID: a.GrantID, BudgetID: rateBudget, NodeID: a.NodeID,
		InstallationID: a.InstallationID, RuntimeID: "xray", Desired: limitedRates(60_000_000, 6000),
	}
	shareA, err := store.RequestRateShare(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.RequestRateShare(t.Context(), first)
	if err != nil || replay != shareA {
		t.Fatalf("idempotent rate replay=%+v,%v", replay, err)
	}
	second := RateRequest{
		OperationID: uuid.NewString(), GrantID: b.GrantID, BudgetID: rateBudget, NodeID: b.NodeID,
		InstallationID: b.InstallationID, RuntimeID: "singbox", Desired: limitedRates(40_000_000, 4000),
	}
	if _, err := store.RequestRateShare(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	first.OperationID, first.Desired = uuid.NewString(), limitedRates(20_000_000, 2000)
	shareA, err = store.RequestRateShare(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	second.OperationID, second.Desired = uuid.NewString(), limitedRates(80_000_000, 8000)
	if _, err := store.RequestRateShare(t.Context(), second); !errors.Is(err, ErrBudget) {
		t.Fatalf("unconfirmed reduction reused: %v", err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[1], a.GrantID, uint64(shareA.Revision), first.Desired); !errors.Is(err, ErrScope) {
		t.Fatalf("foreign confirmation=%v", err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[0], a.GrantID, uint64(shareA.Revision-1), first.Desired); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale confirmation=%v", err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[0], a.GrantID, uint64(shareA.Revision), first.Desired); err != nil {
		t.Fatal(err)
	}
	shareB, err := store.RequestRateShare(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	var saved model.FleetRateShare
	if err := db.First(&saved, "id = ?", a.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	actualA, err := decodeRates(saved.Outstanding)
	if err != nil {
		t.Fatal(err)
	}
	actualB, err := decodeRates(shareB.Outstanding)
	if err != nil {
		t.Fatal(err)
	}
	if actualA.Upload.BitsPerSecond+actualB.Upload.BitsPerSecond != 100_000_000 || actualA.Upload.BurstBytes+actualB.Upload.BurstBytes != 10000 {
		t.Fatalf("combined envelope=%+v + %+v", actualA, actualB)
	}
	if err := store.SetRateBudget(t.Context(), rateBudget, client, term, 2, limitedRates(50_000_000, 5000)); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.RateBudgetPending(t.Context(), rateBudget); err != nil || !pending {
		t.Fatalf("tightening pending=%v,%v", pending, err)
	}
	first.OperationID, first.Desired = uuid.NewString(), Rates{}
	shareA, err = store.RequestRateShare(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[0], a.GrantID, uint64(shareA.Revision), first.Desired); err != nil {
		t.Fatal(err)
	}
	second.OperationID, second.Desired = uuid.NewString(), limitedRates(50_000_000, 5000)
	shareB, err = store.RequestRateShare(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[1], b.GrantID, uint64(shareB.Revision), second.Desired); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.RateBudgetPending(t.Context(), rateBudget); err != nil || pending {
		t.Fatalf("settled tightening pending=%v,%v", pending, err)
	}
}

func TestRateBudgetCannotMultiplyBurstOrClientScopePostgres(t *testing.T) {
	store, _, nodes, client, term, budgets := setupLedger(t)
	a := reserve(t, store, nodes[0], client, term, budgets, 1000)
	b := reserve(t, store, nodes[1], client, term, budgets, 1000)
	id := uuid.NewString()
	if err := store.SetRateBudget(t.Context(), id, client, term, 1, limitedRates(100_000_000, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRateBudget(t.Context(), uuid.NewString(), client, term, 1, limitedRates(100_000_000, 1000)); !errors.Is(err, ErrScope) {
		t.Fatalf("duplicate rate scope=%v", err)
	}
	_, err := store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: a.GrantID, BudgetID: id, NodeID: a.NodeID,
		InstallationID: a.InstallationID, RuntimeID: "xray", Desired: limitedRates(1_000_000, 1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: b.GrantID, BudgetID: id, NodeID: b.NodeID,
		InstallationID: b.InstallationID, RuntimeID: "xray", Desired: limitedRates(1_000_000, 1),
	})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("burst capacity multiplied despite available rate: %v", err)
	}
}

func TestRateAuthoritySurvivesTermRolloverPostgres(t *testing.T) {
	store, _, nodes, client, term, budgets := setupLedger(t)
	old := reserve(t, store, nodes[0], client, term, budgets, 1000)
	id := uuid.NewString()
	if err := store.SetRateBudget(t.Context(), id, client, term, 1, limitedRates(100_000_000, 10000)); err != nil {
		t.Fatal(err)
	}
	request := RateRequest{
		OperationID: uuid.NewString(), GrantID: old.GrantID, BudgetID: id,
		NodeID: old.NodeID, InstallationID: old.InstallationID, RuntimeID: old.RuntimeID, Desired: limitedRates(100_000_000, 10000),
	}
	if _, err := store.RequestRateShare(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	nextTerm := uuid.NewString()
	if err := store.SetRateBudget(t.Context(), id, client, nextTerm, 2, limitedRates(100_000_000, 10000)); err != nil {
		t.Fatalf("move stable rate pool to successor term: %v", err)
	}
	if err := store.SetRateBudget(t.Context(), uuid.NewString(), client, nextTerm, 1, limitedRates(100_000_000, 10000)); !errors.Is(err, ErrScope) {
		t.Fatalf("second client rate pool=%v", err)
	}
	if err := store.SetRateBudget(t.Context(), id, client, term, 2, limitedRates(100_000_000, 10000)); !errors.Is(err, ErrRevision) {
		t.Fatalf("same revision changed active term=%v", err)
	}
	next := reserve(t, store, nodes[1], client, nextTerm, budgets, 1000)
	nextRequest := RateRequest{
		OperationID: uuid.NewString(), GrantID: next.GrantID, BudgetID: id,
		NodeID: next.NodeID, InstallationID: next.InstallationID, RuntimeID: next.RuntimeID, Desired: limitedRates(60_000_000, 6000),
	}
	if _, err := store.RequestRateShare(t.Context(), nextRequest); !errors.Is(err, ErrBudget) {
		t.Fatalf("successor duplicated offline rate authority=%v", err)
	}
	request.OperationID, request.Desired = uuid.NewString(), limitedRates(40_000_000, 4000)
	reduced, err := store.RequestRateShare(t.Context(), request)
	if err != nil {
		t.Fatalf("retired term reduction=%v", err)
	}
	if _, err := store.RequestRateShare(t.Context(), nextRequest); !errors.Is(err, ErrBudget) {
		t.Fatalf("successor reused unconfirmed old-term reduction=%v", err)
	}
	if err := store.ConfirmRateShare(t.Context(), nodes[0], old.GrantID, uint64(reduced.Revision), request.Desired); err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.Desired = uuid.NewString(), limitedRates(41_000_000, 4100)
	if _, err := store.RequestRateShare(t.Context(), request); !errors.Is(err, ErrScope) {
		t.Fatalf("retired term regained rate authority=%v", err)
	}
	if _, err := store.RequestRateShare(t.Context(), nextRequest); err != nil {
		t.Fatalf("successor could not use confirmed released authority=%v", err)
	}
	if err := store.SetRateBudget(t.Context(), id, client, term, 1, limitedRates(100_000_000, 10000)); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale term regained active pool=%v", err)
	}
}
