package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type authorityFixture struct {
	directory, agent, runtime string
	id                        state.Identity
	store                     *state.Store
	credit                    *governor.Store
	worker                    *Worker
	engine                    *creditEngine
	deployment                *nodev1.Deployment
	batch                     *nodev1.AuthorityUpdateBatch
}

func newAuthorityFixture(t *testing.T, names ...string) *authorityFixture {
	t.Helper()
	f := &authorityFixture{directory: t.TempDir(), agent: uuid.NewString(), runtime: "xray", id: state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}}
	if len(names) != 0 {
		f.runtime = names[0]
	}
	f.open(t)
	t.Cleanup(func() { f.worker.cancel(); _ = f.credit.Close(); _ = f.store.Close() })
	descriptor := admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 7, PlacementID: uuid.NewString(), PlacementRevision: 11, InboundInstanceID: uuid.NewString(), InboundTag: "published-inbound"}
	runtime := configuration.Runtime{ID: f.runtime, Core: f.runtime, Configuration: json.RawMessage(`{}`), Admissions: []admission.Descriptor{descriptor}}
	payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{runtime}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	f.deployment = &nodev1.Deployment{NodeId: f.id.NodeID, ControlPlaneId: f.id.ControlPlaneID, AuthorityGeneration: 3, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := f.worker.apply(t.Context(), f.deployment, f.agent); err != nil {
		t.Fatal(err)
	}
	scopes, err := configuration.DescriptorScopes(runtime)
	if err != nil {
		t.Fatal(err)
	}
	f.batch = &nodev1.AuthorityUpdateBatch{NodeId: f.id.NodeID, InstallationId: f.id.InstallationID, AgentStateJournalId: f.agent, RuntimeId: f.runtime, StateJournalId: f.store.JournalID(), CreditJournalId: f.credit.JournalID(), BaseConfigurationRevision: 1, BasePayloadSha256: digest[:], Updates: []*nodev1.AuthorityUpdate{{GrantId: uuid.NewString(), ClientId: descriptor.ClientID, TermId: uuid.NewString(), AuthorityRevision: 1, CreditRevision: 1, CreditCeiling: 1000, Enabled: true, Upload: &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_UNLIMITED}, Download: &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_UNLIMITED}, CredentialGeneration: descriptor.CredentialGeneration, PlacementId: descriptor.PlacementID, PlacementRevision: descriptor.PlacementRevision, DescriptorSha256: scopes[descriptor.ClientID].SHA256}}}
	return f
}

func (f *authorityFixture) open(t *testing.T) {
	t.Helper()
	var err error
	f.store, err = state.Open(filepath.Join(f.directory, "state.db"), f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.credit, err = governor.Open(filepath.Join(f.directory, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.engine = new(creditEngine)
	f.worker, err = New(t.Context(), f.runtime, f.runtime, f.engine, f.store, f.credit)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *authorityFixture) reopen(t *testing.T) {
	t.Helper()
	f.worker.cancel()
	if err := f.worker.backend.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.credit.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.open(t)
	if err := f.worker.Apply(t.Context(), f.deployment); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityReceiverFencesScopeAndRestoresAuthOnlyBase(t *testing.T) {
	f := newAuthorityFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*nodev1.AuthorityUpdateBatch)
		want   error
	}{
		{"agent", func(b *nodev1.AuthorityUpdateBatch) { b.AgentStateJournalId = uuid.NewString() }, state.ErrIdentity},
		{"worker", func(b *nodev1.AuthorityUpdateBatch) { b.StateJournalId = uuid.NewString() }, state.ErrIdentity},
		{"credit", func(b *nodev1.AuthorityUpdateBatch) { b.CreditJournalId = uuid.NewString() }, state.ErrIdentity},
		{"base digest", func(b *nodev1.AuthorityUpdateBatch) { b.BasePayloadSha256 = bytes.Repeat([]byte{1}, 32) }, state.ErrRevision},
		{"credential", func(b *nodev1.AuthorityUpdateBatch) { b.Updates[0].CredentialGeneration++ }, state.ErrIdentity},
		{"descriptor set", func(b *nodev1.AuthorityUpdateBatch) { b.Updates[0].DescriptorSha256 = bytes.Repeat([]byte{1}, 32) }, state.ErrIdentity},
		{"mixed batch", func(b *nodev1.AuthorityUpdateBatch) {
			other := proto.Clone(b.Updates[0]).(*nodev1.AuthorityUpdate)
			other.GrantId = uuid.NewString()
			other.ClientId = uuid.NewString()
			b.Updates = append(b.Updates, other)
		}, state.ErrIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := proto.Clone(f.batch).(*nodev1.AuthorityUpdateBatch)
			test.mutate(bad)
			if err := f.worker.ApplyAuthority(t.Context(), bad); !errors.Is(err, test.want) {
				t.Fatalf("unsafe authority scope accepted: %v", err)
			}
		})
	}
	if pending, err := f.store.AuthorityIntents(1, f.deployment.PayloadSha256); err != nil || len(pending) != 0 {
		t.Fatalf("rejected batch persisted partial intent: %d %v", len(pending), err)
	}
	for range 2 {
		if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
			t.Fatal(err)
		}
	}
	update := f.batch.Updates[0]
	account, ok := f.credit.Lookup(update.ClientId)
	if !ok || f.engine.applications != 1 {
		t.Fatal("initial authority restarted core or did not create account")
	}
	if err := account.Take(t.Context(), limits.Upload, 100); err != nil {
		t.Fatal(err)
	}
	account.Record(limits.Upload, 100)
	if err := f.worker.ApplyCredit(t.Context(), &nodev1.CreditUpdateBatch{NodeId: f.id.NodeID, InstallationId: f.id.InstallationID, AgentStateJournalId: f.agent, RuntimeId: f.runtime, StateJournalId: f.store.JournalID(), CreditJournalId: f.credit.JournalID(), BaseConfigurationRevision: 1, Updates: []*nodev1.CreditUpdate{{GrantId: update.GrantId, ClientId: update.ClientId, TermId: update.TermId, CreditRevision: 2, CreditCeiling: 1500}}}); err != nil {
		t.Fatal(err)
	}
	update.AuthorityRevision = 2
	update.Upload = &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_LIMITED, BitsPerSecond: 8000, BurstBytes: 100}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	if f.engine.applications != 1 || account.Snapshot().Policy.CreditCeiling != 1500 {
		t.Fatal("rate update restarted core or lost newer credit")
	}
	f.reopen(t)
	account, ok = f.credit.Lookup(update.ClientId)
	if !ok || account.Snapshot().Quarantined || account.Snapshot().Policy.AuthorityRevision != 2 || account.Snapshot().Policy.CreditRevision != 2 || account.Snapshot().PreviouslyReserved != 1000 {
		t.Fatal("auth-only restore lost durable authority or credit")
	}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 500); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("authority restore reused reserved credit: %v", err)
	}
	stored, err := f.store.Desired()
	if err != nil || !proto.Equal(stored, f.deployment) {
		t.Fatal("authority intent rewrote base payload/hash")
	}
	next := proto.Clone(f.deployment).(*nodev1.Deployment)
	next.Revision = 2
	if err := f.store.Receive(next); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); !errors.Is(err, state.ErrRevision) {
		t.Fatalf("pending full configuration expanded authority: %v", err)
	}
	if err := f.worker.apply(t.Context(), next, f.agent); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.credit.Lookup(update.ClientId); ok {
		t.Fatal("changed base revived prior authority intent")
	}
	if pending, err := f.store.AuthorityIntents(1, f.deployment.PayloadSha256); err != nil || len(pending) != 0 {
		t.Fatal("retired base retained authority intent")
	}
}

type failAuthorityBackend struct{ *streamBackend }

func (*failAuthorityBackend) ApplyAuthority(context.Context, []authority.PolicyUpdate) error {
	return io.ErrUnexpectedEOF
}

func TestAuthorityIntentCommitsBeforeGovernorAndReplaysAfterRestart(t *testing.T) {
	f := newAuthorityFixture(t)
	f.worker.backend = &failAuthorityBackend{streamBackend: f.worker.backend.(*streamBackend)}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("failed governor application was acknowledged: %v", err)
	}
	if _, ok := f.credit.Lookup(f.batch.Updates[0].ClientId); ok {
		t.Fatal("failure fixture applied authority")
	}
	if pending, err := f.store.AuthorityIntents(1, f.deployment.PayloadSha256); err != nil || len(pending) != 1 {
		t.Fatalf("pending authority intent was not durable: %d %v", len(pending), err)
	}
	f.reopen(t)
	account, ok := f.credit.Lookup(f.batch.Updates[0].ClientId)
	if !ok || account.Snapshot().Policy.AuthorityRevision != 1 {
		t.Fatal("pending authority intent was not restored")
	}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	if f.engine.applications != 1 {
		t.Fatal("authority replay rebuilt the core")
	}
}

func TestNativeBackendCannotAcceptIncrementalAuthority(t *testing.T) {
	f := newAuthorityFixture(t, "native")
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); !errors.Is(err, authority.ErrReport) {
		t.Fatalf("unsupported backend accepted authority: %v", err)
	}
}

func TestChangedBaseCannotRestorePriorAuthOnlyGrant(t *testing.T) {
	f := newAuthorityFixture(t)
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	next := proto.Clone(f.deployment).(*nodev1.Deployment)
	next.Revision++
	if err := f.worker.apply(t.Context(), next, f.agent); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.credit.Lookup(f.batch.Updates[0].ClientId); ok {
		t.Fatal("changed base revived prior auth-only grant")
	}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); !errors.Is(err, state.ErrRevision) {
		t.Fatalf("old base accepted new authority: %v", err)
	}
}

func TestRateOnlyAuthorityChangeProducesDurableObservation(t *testing.T) {
	f := newAuthorityFixture(t)
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	first, err := f.worker.captureAuthority(t.Context())
	if err != nil || first == nil || len(first.Grants) != 1 || first.Grants[0].AuthorityRevision != 1 {
		t.Fatalf("initial authority observation: %+v %v", first, err)
	}
	if err := f.store.AcknowledgeAuthority(&nodev1.AuthorityReceipt{RuntimeId: f.runtime, StateJournalId: f.store.JournalID(), CreditJournalId: f.credit.JournalID(), Sequence: first.Sequence}); err != nil {
		t.Fatal(err)
	}
	f.batch.Updates[0].AuthorityRevision = 2
	f.batch.Updates[0].Upload = &nodev1.AuthorityRate{Mode: nodev1.AuthorityRateMode_AUTHORITY_RATE_LIMITED, BitsPerSecond: 8000, BurstBytes: 100}
	if err := f.worker.ApplyAuthority(t.Context(), f.batch); err != nil {
		t.Fatal(err)
	}
	second, err := f.worker.captureAuthority(t.Context())
	if err != nil || second == nil || len(second.Grants) != 1 || second.Grants[0].AuthorityRevision != 2 || second.Grants[0].PolicyRevision != first.Grants[0].PolicyRevision || second.Grants[0].CreditCeiling != first.Grants[0].CreditCeiling {
		t.Fatalf("rate-only authority observation was lost: %+v %v", second, err)
	}
	if second.Grants[0].ReservedHighWater != 0 || second.Grants[0].AdmissionHighWater != 0 {
		t.Fatal("rate revision became traffic or reservation")
	}
	f.reopen(t)
	pending, err := f.store.PendingAuthority()
	if err != nil || pending == nil || pending.Grants[0].AuthorityRevision != 2 {
		t.Fatalf("rate revision report was not durable: %+v %v", pending, err)
	}
}
