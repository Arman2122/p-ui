package governor

import (
	"path/filepath"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestAuthorityDistinguishesReservedAdmittedBurnedAndMeasured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credit.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := grantForTest(1000)
	if err := s.Apply(grant); err != nil {
		t.Fatal(err)
	}
	account, _ := s.Lookup(grant.ClientID)
	if err := account.Take(t.Context(), limits.Upload, 1); err != nil {
		t.Fatal(err)
	}
	read := func(store *Store) authority.Grant {
		t.Helper()
		page, err := store.Authority(t.Context(), "", 1)
		if err != nil || len(page.Grants) != 1 || page.Next != "" {
			t.Fatalf("authority page=%+v err=%v", page, err)
		}
		return page.Grants[0]
	}
	got := read(s)
	if got.ReservedHighWater != 1000 || got.BurnedHighWater != 0 || !got.AdmissionKnown || got.AdmissionHighWater != 1 || !got.SpendableKnown || got.SpendableBytes != 999 || !got.AuthorizationActive {
		t.Fatalf("reservation is not admission: %+v", got)
	}
	if snapshot := account.Snapshot(); snapshot.MeasuredUpload != 0 || snapshot.MeasuredDownload != 0 {
		t.Fatalf("authority became usage: %+v", snapshot)
	}
	journal := s.JournalID()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got = read(s)
	if s.JournalID() != journal || got.ReservedHighWater != 1000 || got.BurnedHighWater != 1000 || got.AdmissionKnown || got.AdmissionHighWater != 0 || !got.SpendableKnown || got.SpendableBytes != 0 || !got.AuthorizationActive {
		t.Fatalf("restart authority misrepresented: %+v", got)
	}
	if err := s.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	got = read(s)
	if got.AuthorizationActive || got.SpendableBytes != 0 {
		t.Fatalf("quarantined authority eligible for refill: %+v", got)
	}
}

func TestAuthorityPagesDormantGrantsWithoutAdmitting(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 3 {
		if err := s.Apply(grantForTest(100)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := s.Authority(t.Context(), cursor, 1)
		if err != nil || len(page.Grants) != 1 {
			t.Fatalf("page=%+v err=%v", page, err)
		}
		grant := page.Grants[0]
		if seen[grant.GrantID] || grant.ReservedHighWater != 0 || grant.SpendableBytes != 100 {
			t.Fatalf("dormant page=%+v", grant)
		}
		seen[grant.GrantID] = true
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 3 {
		t.Fatalf("paged %d grants", len(seen))
	}
}
