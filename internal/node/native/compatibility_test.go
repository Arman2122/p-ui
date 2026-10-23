package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestNativeJournalRefusesIncompatibleFormatWithoutChangingState(t *testing.T) {
	for _, version := range []string{"unversioned", "format1", "format2", "future", "corrupt"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credit.db")
			journal, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			grant := testGrant(1000)
			if err := journal.Apply(grant); err != nil {
				t.Fatal(err)
			}
			grant.RateRevision, grant.Policy.AuthorityRevision, grant.Policy.CreditRevision = 1, 1, 1
			if _, err := journal.stageAuthority(t.Context(), []Assignment{{Grant: grant}}, time.Now()); err != nil {
				t.Fatal(err)
			}
			err = journal.db.Update(func(tx *bolt.Tx) error {
				meta := tx.Bucket([]byte("identity"))
				switch version {
				case "unversioned":
					return meta.Delete([]byte("state-format"))
				case "format1":
					return meta.Put([]byte("state-format"), binary.BigEndian.AppendUint64(nil, 1))
				case "format2":
					return meta.Put([]byte("state-format"), binary.BigEndian.AppendUint64(nil, 2))
				case "future":
					return meta.Put([]byte("state-format"), binary.BigEndian.AppendUint64(nil, 4))
				default:
					return meta.Put([]byte("state-format"), []byte{3})
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if reopened != nil {
				_ = reopened.Close()
			}
			if !errors.Is(err, state.ErrStateFormat) {
				t.Fatalf("incompatible native journal reopened: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refused native journal was changed: %v", err)
			}
		})
	}
}
