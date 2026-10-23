package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type inactiveEngine struct{}

func (inactiveEngine) Validate([]byte) error { return nil }
func (inactiveEngine) Apply(context.Context, []byte, func(string) (*limits.Account, bool)) error {
	return nil
}
func (inactiveEngine) Close() error { return nil }

func TestWorkerRefusesReplacementCreditJournal(t *testing.T) {
	dir := t.TempDir()
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(dir, "usage.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	path := filepath.Join(dir, "governor.db")
	credit, err := governor.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstID := credit.JournalID()
	w, err := New(t.Context(), "xray", "xray", inactiveEngine{}, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	w.cancel()
	if err := credit.Close(); err != nil {
		t.Fatal(err)
	}
	credit, err = governor.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err = New(t.Context(), "xray", "xray", inactiveEngine{}, store, credit)
	if err != nil || credit.JournalID() != firstID {
		t.Fatalf("ordinary restart rejected: %v", err)
	}
	w.cancel()
	if err := credit.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	credit, err = governor.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = credit.Close() }()
	if _, err := New(t.Context(), "xray", "xray", inactiveEngine{}, store, credit); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("accepted replacement credit journal: %v", err)
	}
}
