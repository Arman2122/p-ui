package governor

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestCreditTopupSurvivesSameBaseRestoreWithoutReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credit.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := grantForTest(1000)
	grant.Policy.Revision = 3
	grant.Policy.CreditRevision = 1
	if err := s.Apply(grant); err != nil {
		t.Fatal(err)
	}
	account, _ := s.Lookup(grant.ClientID)
	if err := account.Take(t.Context(), limits.Upload, 1000); err != nil {
		t.Fatal(err)
	}
	credit := authority.Credit{GrantID: grant.ID, ClientID: grant.ClientID, TermID: grant.Policy.Term, PolicyRevision: 3, CreditRevision: 2, CreditCeiling: 1500}
	if err := s.ApplyCredit(t.Context(), []authority.Credit{credit}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Reconcile([]Grant{grant}); err != nil {
		t.Fatalf("same base restore lost durable topup: %v", err)
	}
	account, _ = s.Lookup(grant.ClientID)
	if got := account.Snapshot(); got.Policy.CreditCeiling != 1500 || got.Policy.CreditRevision != 2 || got.PreviouslyReserved != 1000 || got.MeasuredUpload != 0 {
		t.Fatalf("bad restored authority: %+v", got)
	}
	if err := s.ApplyCredit(t.Context(), []authority.Credit{credit}); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 500); err != nil {
		t.Fatal(err)
	}
	if err := account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("topup replay reissued bytes: %v", err)
	}
	conflict := grant
	conflict.Policy.ExpiresUnixMilli = 1
	if err := s.Apply(conflict); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("same-base merge changed unrelated policy: %v", err)
	}
	if err := s.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	credit.CreditRevision++
	credit.CreditCeiling++
	if err := s.ApplyCredit(t.Context(), []authority.Credit{credit}); !errors.Is(err, ErrGrant) {
		t.Fatalf("suspended grant expanded: %v", err)
	}
}
