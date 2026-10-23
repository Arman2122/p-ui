package controlplane

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestAppliedStopProofCannotResurrectAcrossReenablePostgres(t *testing.T) {
	f := newStopFixture(t, true)
	generation, stopped := deliverStop(t, f)
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, stopped)); err != nil {
		t.Fatal(err)
	}
	assertStopProofCount(t, f, 2)
	bundle, err := configuration.Decode(stopped.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for i := range bundle.Runtimes {
		for j := range bundle.Runtimes[i].Grants {
			bundle.Runtimes[i].Grants[j].Policy.Enabled = true
		}
	}
	if revision, err := f.store.PublishConfiguration(t.Context(), f.node, bundle); err != nil || revision != 3 {
		t.Fatalf("reenable publication=%d %v", revision, err)
	}
	assertStopProofCount(t, f, 0)
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, stopped)); err != nil {
		t.Fatalf("retained disabled-base ACK failed: %v", err)
	}
	assertStopProofCount(t, f, 0)
	pending, err := f.store.Transition(t.Context(), TransitionRequest{
		OperationID: uuid.NewString(), ClientID: f.native.client.ID, ExpectedRevision: uint64(f.native.client.Revision), ExpectedCredentialGeneration: 1,
		ExpectedTermID: f.native.term.ID, SourcePolicyDigest: f.native.placement.SourcePolicyDigest, Kind: TrafficReset,
		Spec: TermSpec{QuotaBytes: 1000, Rates: limitedRates(100000000, 10000)},
	})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("replacement term intent=%+v %v", pending, err)
	}
	finish := CompleteTransitionRequest{
		OperationID: uuid.NewString(), ClientID: f.native.client.ID, PendingTermID: pending.Term.ID,
		ExpectedRevision: uint64(pending.Client.Revision), ExpectedCredentialGeneration: 1, SourcePolicyDigest: f.native.placement.SourcePolicyDigest,
	}
	if _, err := f.store.CompleteTransition(t.Context(), finish); !errors.Is(err, ErrDrain) {
		t.Fatalf("pending reenable consumed stale disabled proof: %v", err)
	}
	enabled, err := f.store.Delivery(t.Context(), f.node, generation)
	if err != nil {
		t.Fatal(err)
	}
	// An enabled applied receipt must also clear an already-stored obsolete proof.
	if err := f.db.Create(&model.FleetGrantStop{GrantID: f.native.grant.ID, DeploymentRevision: 2, RateRevision: 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, enabled)); err != nil {
		t.Fatal(err)
	}
	assertStopProofCount(t, f, 0)
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, stopped)); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unexpected old-base replay failure: %v", err)
	}
	assertStopProofCount(t, f, 0)
	if _, err := f.store.CompleteTransition(t.Context(), finish); !errors.Is(err, ErrDrain) {
		t.Fatalf("reenabled base was mistaken for an applied stop: %v", err)
	}
	request := StopConfigurationRequest{OperationID: uuid.NewString(), ExpectedBaseRevision: enabled.Revision, ExpectedBaseSHA256: enabled.PayloadSha256, GrantIDs: []string{f.native.grant.ID}}
	if _, err := f.store.StopConfiguration(t.Context(), f.node, request, stopValidators()); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.Delivery(t.Context(), f.node, generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, current)); err != nil {
		t.Fatal(err)
	}
	if completed, err := f.store.CompleteTransition(t.Context(), finish); err != nil || completed.PendingDrain || completed.Term.ID != pending.Term.ID {
		t.Fatalf("current applied stop did not finish the drain: %+v %v", completed, err)
	}
}

func assertStopProofCount(t *testing.T, f stopFixture, want int64) {
	t.Helper()
	var count int64
	if err := f.db.Model(&model.FleetGrantStop{}).Where("grant_id IN ?", []string{f.native.grant.ID, f.stream.grant.ID}).Count(&count).Error; err != nil || count != want {
		t.Fatalf("stale stop proof count=%d want=%d: %v", count, want, err)
	}
}
