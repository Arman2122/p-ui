package limits

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreditTopupPreservesConsumptionAndRateDebt(t *testing.T) {
	p := policy()
	p.CreditRevision = 1
	p.CreditCeiling = 10
	p.Upload = Rate{Mode: Limited, BitsPerSecond: 8, BurstBytes: 10}
	a := mustAccount(t, p)
	if err := a.Take(t.Context(), Upload, 10); err != nil {
		t.Fatal(err)
	}
	a.Record(Upload, 10)
	if err := a.ApplyCredit(p.Term, p.Revision, 2, 20); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyCredit(p.Term, p.Revision, 2, 20); err != nil {
		t.Fatal(err)
	}
	snapshot := a.Snapshot()
	if snapshot.AdmittedUpload != 10 || snapshot.MeasuredUpload != 10 || snapshot.Policy.CreditRevision != 2 || snapshot.Policy.CreditCeiling != 20 || snapshot.Policy.Revision != p.Revision {
		t.Fatalf("topup reset accounting or policy: %+v", snapshot)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := a.Take(ctx, Upload, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("topup minted rate tokens: %v", err)
	}
	if err := a.Take(t.Context(), Download, 10); err != nil {
		t.Fatal(err)
	}
	if err := a.Take(t.Context(), Download, 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("replayed topup multiplied quota: %v", err)
	}
}

func TestCreditTopupCannotExpandInvalidAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Policy)
		want   error
	}{
		{"disabled", func(p *Policy) { p.Enabled = false }, ErrBlocked},
		{"expired", func(p *Policy) { p.ExpiresUnixMilli = 1 }, ErrExpired},
		{"both directions blocked", func(p *Policy) { p.Upload = Rate{}; p.Download = Rate{} }, ErrBlocked},
		{"unlimited", func(p *Policy) { p.QuotaUnlimited = true; p.CreditCeiling = 0 }, ErrRevision},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := policy()
			p.CreditRevision = 1
			test.mutate(&p)
			a := mustAccount(t, p)
			if err := a.ApplyCredit(p.Term, p.Revision, 2, 20000); !errors.Is(err, test.want) {
				t.Fatalf("invalid expansion=%v", err)
			}
			if a.Snapshot().Policy != p {
				t.Fatal("rejected credit changed policy")
			}
		})
	}
	for _, test := range []struct {
		name, term                string
		policy, revision, ceiling uint64
		want                      error
	}{
		{"foreign term", "foreign", 1, 2, 20000, ErrTerm},
		{"old base", "term-a", 2, 2, 20000, ErrRevision},
		{"conflicting replay", "term-a", 1, 1, 20000, ErrRevision},
		{"ceiling decrease", "term-a", 1, 2, 9999, ErrRevision},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := policy()
			p.CreditRevision = 1
			a := mustAccount(t, p)
			if err := a.ApplyCredit(test.term, test.policy, test.revision, test.ceiling); !errors.Is(err, test.want) {
				t.Fatalf("invalid credit identity=%v", err)
			}
		})
	}
}
