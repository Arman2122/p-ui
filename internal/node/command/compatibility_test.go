package command

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

func TestManagedWorkerRefusesIncompatibleStateBeforeBackend(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed worker entrypoint requires Linux")
	}
	for _, format := range []uint32{1, 2, 3, 4} {
		if format == compatibility.StateFormat {
			continue
		}
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			directory := t.TempDir()
			configuration := credentials.Config{Identity: identity.Principal{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}}
			encoded, err := json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(directory, "config.json")
			if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			journalPath := filepath.Join(directory, "journal.db")
			store, err := state.Open(journalPath, state.Identity{NodeID: configuration.Identity.NodeID, InstallationID: configuration.Identity.InstallationID, ControlPlaneID: configuration.Identity.ControlPlaneID})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := bolt.Open(journalPath, 0o600, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket([]byte("meta")).Put([]byte("state-format"), binary.BigEndian.AppendUint64(nil, uint64(format)))
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			creditPath := filepath.Join(directory, "credit.db")
			credit := []byte("retained native credit journal; backend must not open it")
			if err := os.WriteFile(creditPath, credit, 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			factory := func(context.Context, string, string, *state.Store, credentials.Config) (worker.Backend, io.Closer, error) {
				called = true
				return nil, nil, errors.New("backend reached incompatible state")
			}
			err = ManagedWorker(t.Context(), []string{"--state-dir", directory, "--config", configPath, "--agent-uid", "1000"}, io.Discard, "native", time.Millisecond, factory)
			if called || !errors.Is(err, state.ErrStateFormat) {
				t.Fatalf("incompatible state reached backend: called=%v error=%v", called, err)
			}
			after, err := os.ReadFile(journalPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("startup refusal changed worker journal: %v", err)
			}
			after, err = os.ReadFile(creditPath)
			if err != nil || !bytes.Equal(credit, after) {
				t.Fatalf("startup refusal changed native credit: %v", err)
			}
		})
	}
}
