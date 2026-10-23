//go:build linux

package trustedtime_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

func TestConcurrentTimeRefreshPreservesAdmissionClock(t *testing.T) {
	source, err := trustedtime.SystemSource()
	if err != nil {
		t.Fatal(err)
	}
	clock, err := trustedtime.New(new(storage), source)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := clock.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := clock.Accept(nonce, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var failed atomic.Bool
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			var last time.Time
			for range 1000 {
				now, err := clock.Now()
				if err != nil || now.Before(last) {
					failed.Store(true)
					return
				}
				last = now
			}
		})
	}
	for range 100 {
		nonce, err := clock.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := clock.Accept(nonce, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	group.Wait()
	if failed.Load() {
		t.Fatal("time refresh interrupted or regressed admission clock")
	}
}

func BenchmarkTrustedTimeAdmission(b *testing.B) {
	source, err := trustedtime.SystemSource()
	if err != nil {
		b.Fatal(err)
	}
	clock, err := trustedtime.New(new(storage), source)
	if err != nil {
		b.Fatal(err)
	}
	nonce, err := clock.Begin()
	if err != nil {
		b.Fatal(err)
	}
	if err := clock.Accept(nonce, time.Now().UnixMilli()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := clock.Now(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
