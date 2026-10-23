package governor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	boltErrors "go.etcd.io/bbolt/errors"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func grantForTest(credit uint64) Grant {
	return Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{
		Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: credit,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	}}
}

func TestRuntimeCreditSurvivesAbruptExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	grant := grantForTest(1000)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRuntimeCrashHelper$")
	command.Env = append(os.Environ(), "PENHOON_TEST_GOVERNOR="+path, "PENHOON_TEST_CLIENT="+grant.ClientID)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker crash setup: %v: %s", err, output)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, ok := store.Lookup(grant.ClientID)
	if !ok {
		t.Fatal("persisted client missing")
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("replayed uncertain credit: %v", err)
	}
	snapshot := account.Snapshot()
	if snapshot.PreviouslyReserved != 1000 || snapshot.AdmittedUpload != 0 || snapshot.AdmittedDownload != 0 || snapshot.MeasuredUpload != 0 || snapshot.MeasuredDownload != 0 {
		t.Fatalf("uncertain reservation turned into usage: %+v", snapshot)
	}
	grant.Policy.Revision++
	grant.Policy.CreditCeiling = 1500
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Upload, 500); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("top-up multiplied remaining credit: %v", err)
	}
}

func TestRuntimeCrashHelper(t *testing.T) {
	path := os.Getenv("PENHOON_TEST_GOVERNOR")
	if path == "" {
		return
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	account, ok := store.Lookup(os.Getenv("PENHOON_TEST_CLIENT"))
	if !ok {
		t.Fatal("client missing")
	}
	if err := account.Take(context.Background(), limits.Upload, 10); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestRuntimeCreditConcurrencyAndStorageFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	grant := grantForTest(20000)
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	account, _ := store.Lookup(grant.ClientID)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			err := account.Take(t.Context(), limits.Upload, 1000)
			if err == nil {
				admitted.Add(1000)
			} else if !errors.Is(err, limits.ErrQuota) {
				t.Errorf("admission=%v", err)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 20000 {
		t.Fatalf("admitted=%d", admitted.Load())
	}
	grant.Policy.Revision++
	grant.Policy.CreditCeiling = 40000
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, boltErrors.ErrDatabaseNotOpen) {
		t.Fatalf("credit without durable reservation=%v", err)
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedDownload != 0 || snapshot.AdmittedUpload != 20000 {
		t.Fatalf("failed reservation admitted bytes: %+v", snapshot)
	}
}

func TestRuntimeGrantIdentityAndPolicyTombstone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := grantForTest(1000)
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	old, _ := store.Lookup(grant.ClientID)
	duplicate := grant
	duplicate.ID = uuid.NewString()
	if err := store.Apply(duplicate); !errors.Is(err, ErrGrant) {
		t.Fatalf("parallel grant for same client=%v", err)
	}
	grant.Policy.Enabled = false
	grant.Policy.Revision++
	if err := store.Apply(grant); err != nil {
		t.Fatal(err)
	}
	if err := old.Take(t.Context(), limits.Upload, 1); !errors.Is(err, limits.ErrBlocked) {
		t.Fatalf("old session remained authorized=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok := store.Lookup(grant.ClientID); ok {
		t.Fatal("restart undid revocation")
	}
	grant.Policy.Enabled = true
	grant.Policy.Revision--
	if err := store.Apply(grant); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("stale policy undid revocation=%v", err)
	}
}
