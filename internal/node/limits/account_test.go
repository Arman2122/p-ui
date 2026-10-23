package limits

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func policy() Policy {
	return Policy{
		Term: "term-a", Revision: 1, Enabled: true, CreditCeiling: 10000,
		Upload: Rate{Mode: Unlimited}, Download: Rate{Mode: Unlimited},
	}
}

func mustAccount(t *testing.T, p Policy) *Account {
	t.Helper()
	a, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSharedCreditAcrossConcurrentDirections(t *testing.T) {
	a := mustAccount(t, policy())
	var wg sync.WaitGroup
	var admitted atomic.Int64
	var unexpected atomic.Int64
	for i := range 40 {
		wg.Go(func() {
			for range 100 {
				err := a.Take(context.Background(), Direction(i%2), 10)
				if err == nil {
					admitted.Add(10)
				} else if !errors.Is(err, ErrQuota) {
					unexpected.Add(1)
				}
			}
		})
	}
	wg.Wait()
	s := a.Snapshot()
	if admitted.Load() != 10000 || s.AdmittedUpload+s.AdmittedDownload != 10000 || unexpected.Load() != 0 {
		t.Fatalf("admitted=%d snapshot=%+v unexpected=%d", admitted.Load(), s, unexpected.Load())
	}
}

func TestRevisionReplayDoesNotRefillQuotaOrBurst(t *testing.T) {
	p := policy()
	p.Upload = Rate{Mode: Limited, BitsPerSecond: 8, BurstBytes: 10}
	a := mustAccount(t, p)
	if err := a.Take(context.Background(), Upload, 10); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.Take(ctx, Upload, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replay minted a burst: %v", err)
	}
	p.Revision++
	p.CreditCeiling = 20
	if err := a.Apply(p); err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().AdmittedUpload; got != 10 {
		t.Fatalf("uploaded=%d, want 10", got)
	}
}

func TestBlockInterruptsWaitingAndExistingTraffic(t *testing.T) {
	p := policy()
	p.Upload = Rate{Mode: Limited, BitsPerSecond: 8, BurstBytes: 10}
	a := mustAccount(t, p)
	if err := a.Take(context.Background(), Upload, 10); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Take(ctx, Upload, 10) }()
	p.Revision++
	p.Upload = Rate{Mode: Blocked}
	if err := a.Apply(p); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrBlocked) {
		t.Fatalf("waiting take=%v", err)
	}
	if err := a.Take(ctx, Upload, 1); !errors.Is(err, ErrBlocked) {
		t.Fatalf("next take=%v", err)
	}
}

func TestTermAndPolicyConflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Policy)
		want   error
	}{
		{"same-revision-different-credit", func(p *Policy) { p.CreditCeiling++ }, ErrRevision},
		{"old-revision", func(p *Policy) { p.Revision = 1 }, ErrRevision},
		{"different-term", func(p *Policy) { p.Term = "term-b"; p.Revision++ }, ErrTerm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy()
			p.Revision = 2
			a := mustAccount(t, p)
			tc.mutate(&p)
			if err := a.Apply(p); !errors.Is(err, tc.want) {
				t.Fatalf("apply=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestExpiredAndUnallocatedPoliciesRefuseTraffic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Policy)
		want   error
	}{
		{"expired", func(p *Policy) { p.ExpiresUnixMilli = time.Now().Add(-time.Second).UnixMilli() }, ErrExpired},
		{"zero-credit", func(p *Policy) { p.CreditCeiling = 0 }, ErrQuota},
		{"zero-rate-is-blocked", func(p *Policy) { p.Upload = Rate{} }, ErrBlocked},
		{"disabled", func(p *Policy) { p.Enabled = false }, ErrBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy()
			tc.mutate(&p)
			a := mustAccount(t, p)
			if err := a.Take(context.Background(), Upload, 1); !errors.Is(err, tc.want) {
				t.Fatalf("take=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestRateIsSharedAcrossConnections(t *testing.T) {
	p := policy()
	p.Upload = Rate{Mode: Limited, BitsPerSecond: 8000, BurstBytes: 100}
	a := mustAccount(t, p)
	start := time.Now()
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() { errs <- a.Take(ctx, Upload, 100) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 850*time.Millisecond {
		t.Fatalf("1000 bytes with 100-byte burst at 1000 B/s took %s", elapsed)
	}
	if got := a.Snapshot().AdmittedUpload; got != 1000 {
		t.Fatalf("uploaded=%d", got)
	}
}

func TestPolicyChangeDoesNotRechargeBurst(t *testing.T) {
	p := policy()
	p.Upload = Rate{Mode: Limited, BitsPerSecond: 8, BurstBytes: 10}
	a := mustAccount(t, p)
	if err := a.Take(context.Background(), Upload, 10); err != nil {
		t.Fatal(err)
	}
	p.Revision++
	p.Upload.BurstBytes = 20
	if err := a.Apply(p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.Take(ctx, Upload, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resize minted burst: %v", err)
	}
}

func TestCreditReductionDoesNotEraseConsumption(t *testing.T) {
	p := policy()
	a := mustAccount(t, p)
	if err := a.Take(context.Background(), Download, 100); err != nil {
		t.Fatal(err)
	}
	p.Revision++
	p.CreditCeiling = 50
	if err := a.Apply(p); err != nil {
		t.Fatal(err)
	}
	if err := a.Take(context.Background(), Upload, 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("take after reduction=%v", err)
	}
	if got := a.Snapshot().AdmittedDownload; got != 100 {
		t.Fatalf("downloaded=%d", got)
	}
}
