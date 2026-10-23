package governor

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type authorityTestClock func() (time.Time, error)

func (f authorityTestClock) Now() (time.Time, error) { return f() }

func TestAuthorityPolicyAndRateMetadataCommitWithoutResettingCredit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credit.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	grant := grantForTest(1000)
	grant.Policy.AuthorityRevision, grant.Policy.CreditRevision, grant.RateRevision = 1, 1, 1
	update := authority.PolicyUpdate{GrantID: grant.ID, ClientID: grant.ClientID, Policy: grant.Policy}
	store.SetClock(authorityTestClock(func() (time.Time, error) {
		if n := store.db.Stats().OpenTxN; n != 0 {
			return time.Time{}, fmt.Errorf("authority validation holds %d reader transactions while locking account", n)
		}
		return time.Now(), nil
	}))
	if err := store.ApplyAuthority(t.Context(), []authority.PolicyUpdate{update}); err != nil {
		t.Fatal(err)
	}
	account, _ := store.Lookup(grant.ClientID)
	if err := account.Take(t.Context(), limits.Upload, 100); err != nil {
		t.Fatal(err)
	}
	account.Record(limits.Upload, 100)
	if err := store.ApplyCredit(t.Context(), []authority.Credit{{GrantID: grant.ID, ClientID: grant.ClientID, TermID: grant.Policy.Term, PolicyRevision: 1, CreditRevision: 2, CreditCeiling: 1500}}); err != nil {
		t.Fatal(err)
	}
	update.Policy.AuthorityRevision, update.Policy.Upload = 2, limits.Rate{Mode: limits.Limited, BitsPerSecond: 8000, BurstBytes: 100}
	for range 2 {
		if err := store.ApplyAuthority(t.Context(), []authority.PolicyUpdate{update}); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := store.Observe()
	if err != nil || len(observed) != 1 || observed[0].Grant.RateRevision != 2 || observed[0].Grant.Policy.AuthorityRevision != 2 || observed[0].Snapshot.Policy.CreditCeiling != 1500 || observed[0].Snapshot.MeasuredUpload != 100 {
		t.Fatalf("authority metadata or counters differ: %+v %v", observed, err)
	}
	journal := store.JournalID()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	account, _ = store.Lookup(grant.ClientID)
	if store.JournalID() != journal || account.Snapshot().Policy.AuthorityRevision != 2 || account.Snapshot().Policy.CreditRevision != 2 || account.Snapshot().PreviouslyReserved != 1000 || account.Snapshot().MeasuredUpload != 0 {
		t.Fatal("authority reopen reset reservations or fabricated measured usage")
	}
	if err := account.Take(t.Context(), limits.Download, 500); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("authority replay multiplied available credit: %v", err)
	}
}
