package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type hotProofEngine struct {
	inactiveEngine
	closed  int
	changed []admission.Subject
	apply   func(context.Context) error
}

func (e *hotProofEngine) Close() error { e.closed++; return nil }

func (e *hotProofEngine) Apply(ctx context.Context, _ []byte, _ func(string) (*limits.Account, bool)) error {
	return e.apply(ctx)
}

func (e *hotProofEngine) InvalidateAuthentication(_ context.Context, subjects []admission.Subject) error {
	e.changed = subjects
	return nil
}

func TestWorkerHotProofCommitFencesFailedApplyWithoutClosingUnchangedSubjects(t *testing.T) {
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credit, err := governor.Open(filepath.Join(t.TempDir(), "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer credit.Close()
	engine := new(hotProofEngine)
	w, err := New(t.Context(), "ssh", "ssh", engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	defer w.cancel()
	newDescriptor := func() admission.Descriptor {
		return admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: "proof-test"}
	}
	a, b := newDescriptor(), newDescriptor()
	deployment := func(revision uint64) *nodev1.Deployment {
		t.Helper()
		payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "ssh", Core: "ssh", Configuration: json.RawMessage(`{}`), Admissions: []admission.Descriptor{a, b}}}})
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		return &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: revision, Payload: payload, PayloadSha256: digest[:]}
	}
	var currentA, currentB *admission.BoundProof
	engine.apply = func(ctx context.Context) error {
		binding := admission.BindingFromContext(ctx)
		currentA, _ = binding.Capture(a.ClientID, a.InboundTag)
		currentB, _ = binding.Capture(b.ClientID, b.InboundTag)
		if currentA.Current() || currentB.Current() {
			t.Fatal("candidate became current before the engine and durable state applied")
		}
		return nil
	}
	if err := w.Apply(t.Context(), deployment(1)); err != nil {
		t.Fatal(err)
	}
	if !currentA.Current() || !currentB.Current() || engine.closed != 0 {
		t.Fatal("qualified engine was closed or committed proof was unavailable")
	}
	oldA, oldB := currentA, currentB
	a.CredentialGeneration++
	rejected := errors.New("controlled hot apply failure")
	engine.apply = func(ctx context.Context) error {
		binding := admission.BindingFromContext(ctx)
		currentA, _ = binding.Capture(a.ClientID, a.InboundTag)
		currentB, _ = binding.Capture(b.ClientID, b.InboundTag)
		if currentA.Current() || oldA.Current() || currentB != oldB || !oldB.Current() || engine.closed != 0 {
			t.Fatal("prepare retired an unchanged proof or enabled a changed one")
		}
		return rejected
	}
	next := deployment(2)
	if err := w.Apply(t.Context(), next); !errors.Is(err, rejected) {
		t.Fatalf("failed hot update=%v", err)
	}
	failed := currentA
	if failed.Current() || oldA.Current() || !oldB.Current() || len(engine.changed) != 1 || engine.changed[0].ClientID != a.ClientID {
		t.Fatal("failed update affected an unrelated client or activated its candidate")
	}
	engine.apply = func(ctx context.Context) error {
		currentA, _ = admission.BindingFromContext(ctx).Capture(a.ClientID, a.InboundTag)
		if currentA.Current() || !oldB.Current() {
			t.Fatal("retry broke the unchanged proof fence")
		}
		return nil
	}
	if err := w.Apply(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if !currentA.Current() || failed.Current() || oldA.Current() || !oldB.Current() || engine.closed != 0 {
		t.Fatal("retry revived a failed proof or restarted unrelated sessions")
	}
	cursors, err := store.Cursors()
	if err != nil || cursors.Applied != 2 {
		t.Fatalf("new proof lacks durable acknowledgment: %+v %v", cursors, err)
	}
}
