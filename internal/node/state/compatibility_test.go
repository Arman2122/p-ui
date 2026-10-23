package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func TestStoreRefusesIncompatibleFormatWithoutChangingDurableState(t *testing.T) {
	for _, version := range []string{"unversioned", "format1", "format2", "future", "corrupt"} {
		t.Run(version, func(t *testing.T) {
			id := identity()
			path := filepath.Join(t.TempDir(), "state.db")
			store, err := Open(path, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Receive(deployment(id, 1, 1, "retained desired")); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendUsage([]*nodev1.UsageCheckpoint{checkpoint()}); err != nil {
				t.Fatal(err)
			}
			err = store.db.Update(func(tx *bolt.Tx) error {
				meta := tx.Bucket(metaBucket)
				switch version {
				case "unversioned":
					return meta.Delete([]byte("state-format"))
				case "format1":
					return putNumber(meta, "state-format", 1)
				case "format2":
					return putNumber(meta, "state-format", 2)
				case "future":
					return putNumber(meta, "state-format", 4)
				default:
					return meta.Put([]byte("state-format"), []byte{2})
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path, id)
			if reopened != nil {
				_ = reopened.Close()
			}
			if !errors.Is(err, ErrStateFormat) {
				t.Fatalf("incompatible state reopened: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected journal was modified: %v", err)
			}
		})
	}
}
