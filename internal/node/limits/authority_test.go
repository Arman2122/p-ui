package limits

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuthorityUpdatesPreserveCreditDebtAndPermanentStop(t *testing.T) {
	policy := Policy{Term: "term", Revision: 7, AuthorityRevision: 1, CreditRevision: 1, CreditCeiling: 1000, Enabled: true, Upload: Rate{Mode: Limited, BitsPerSecond: 8, BurstBytes: 10}, Download: Rate{Mode: Unlimited}}
	account := mustAccount(t, policy)
	if err := account.Take(t.Context(), Upload, 10); err != nil {
		t.Fatal(err)
	}
	account.Record(Upload, 10)
	if err := account.ApplyCredit("term", 7, 2, 2000); err != nil {
		t.Fatal(err)
	}
	policy.AuthorityRevision, policy.Upload.BurstBytes = 2, 20
	for range 2 {
		if err := account.ApplyAuthority(policy); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := account.Snapshot()
	if snapshot.Policy.AuthorityRevision != 2 || snapshot.Policy.CreditRevision != 2 || snapshot.Policy.CreditCeiling != 2000 || snapshot.AdmittedUpload != 10 || snapshot.MeasuredUpload != 10 {
		t.Fatalf("authority replay reset credit or usage: %+v", snapshot)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := account.Take(ctx, Upload, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("authority resize minted burst: %v", err)
	}
	conflict := policy
	conflict.Upload.BitsPerSecond = 16
	if err := account.ApplyAuthority(conflict); !errors.Is(err, ErrRevision) {
		t.Fatalf("same authority revision changed rates: %v", err)
	}
	idle := account.Snapshot().Policy
	idle.AuthorityRevision, idle.Upload, idle.Download = 3, Rate{}, Rate{}
	if err := account.ApplyAuthority(idle); err != nil {
		t.Fatal(err)
	}
	unblock := idle
	unblock.AuthorityRevision, unblock.Upload = 4, policy.Upload
	if err := account.ApplyAuthority(unblock); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	if err := account.Take(ctx2, Upload, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("idle resume minted burst: %v", err)
	}
	stop := unblock
	stop.AuthorityRevision, stop.Enabled, stop.Upload = 5, false, Rate{}
	if err := account.ApplyAuthority(stop); err != nil {
		t.Fatal(err)
	}
	unblock.AuthorityRevision = 6
	if err := account.ApplyAuthority(unblock); !errors.Is(err, ErrRevision) {
		t.Fatalf("same-base stopped authority revived: %v", err)
	}
}

func TestFullSnapshotCannotRegressAuthorityOrIssuedCredit(t *testing.T) {
	current := Policy{Term: "term", Revision: 7, AuthorityRevision: 5, CreditRevision: 3, CreditCeiling: 2000, Enabled: true, Upload: Rate{Mode: Unlimited}, Download: Rate{Mode: Unlimited}}
	for _, test := range []struct {
		name   string
		mutate func(*Policy)
	}{
		{"authority", func(p *Policy) { p.AuthorityRevision = 4 }},
		{"credit revision", func(p *Policy) { p.CreditRevision = 2 }},
		{"same credit revision ceiling", func(p *Policy) { p.CreditCeiling = 1000 }},
		{"new credit revision smaller ceiling", func(p *Policy) { p.CreditRevision = 4; p.CreditCeiling = 1000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := mustAccount(t, current)
			next := current
			next.Revision++
			test.mutate(&next)
			if err := account.Apply(next); !errors.Is(err, ErrRevision) {
				t.Fatalf("full snapshot regressed %s: %v", test.name, err)
			}
			if account.Snapshot().Policy != current {
				t.Fatal("rejected full snapshot changed policy")
			}
		})
	}
}

func TestManagedFullSnapshotCannotChangeQuotaMode(t *testing.T) {
	current := Policy{Term: "term", Revision: 1, AuthorityRevision: 1, CreditRevision: 1, Enabled: true, QuotaUnlimited: true, Upload: Rate{Mode: Unlimited}, Download: Rate{Mode: Unlimited}}
	account := mustAccount(t, current)
	next := current
	next.Revision, next.AuthorityRevision, next.CreditRevision, next.QuotaUnlimited, next.CreditCeiling = 2, 2, 2, false, 1000
	if err := account.Apply(next); !errors.Is(err, ErrRevision) {
		t.Fatalf("managed full snapshot changed quota mode: %v", err)
	}
	if account.Snapshot().Policy != current {
		t.Fatal("rejected quota transition changed managed policy")
	}
}
