package controller

import (
	"sync"
	"testing"
	"time"
)

/*
X-Forwarded-For is read from the right, because everything a client sends
arrives on the left.

The header is append-only: each hop adds the peer it saw, so the list reads
`<client>, <proxy1>, <proxy2>`. A request that arrives at the edge already
carrying `X-Forwarded-For: 1.2.3.4` is forwarded on as `1.2.3.4, <real client>`.
Taking the leftmost entry therefore took whatever the caller wrote — and the
login rate limiter keys on this value, so a fresh, unblocked key was one header
away. F205.
*/
func TestForwardedForIsReadFromTheRight(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		ok     bool
		why    string
	}{
		{
			name:   "a spoofed leading entry is ignored",
			header: "1.2.3.4, 203.0.113.7",
			want:   "203.0.113.7",
			ok:     true,
			why:    "the attacker controls the left; the edge proxy appended the truth on the right",
		},
		{
			name:   "a single untrusted entry is the client",
			header: "203.0.113.7",
			want:   "203.0.113.7",
			ok:     true,
			why:    "one hop, nothing to disambiguate",
		},
		{
			name:   "several spoofed entries change nothing",
			header: "10.9.9.9, 1.2.3.4, evil, 203.0.113.7",
			want:   "203.0.113.7",
			ok:     true,
			why:    "only the rightmost untrusted entry is evidence",
		},
		{
			name:   "an empty header names no client",
			header: "",
			ok:     false,
			why:    "the caller falls back to the direct peer",
		},
		{
			name:   "an unparsable header names no client",
			header: "not-an-ip, also-not",
			ok:     false,
			why:    "nothing in it is an address",
		},
		{
			name:   "whitespace around entries is tolerated",
			header: "  1.2.3.4 ,  203.0.113.7  ",
			want:   "203.0.113.7",
			ok:     true,
			why:    "proxies pad the list differently",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := clientIPFromForwardedFor(c.header)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (%s)", ok, c.ok, c.why)
			}
			if ok && got != c.want {
				t.Fatalf("clientIPFromForwardedFor(%q) = %q, want %q (%s)", c.header, got, c.want, c.why)
			}
		})
	}
}

/*
The cap holds when the attempts arrive together.

allow() used to check the budget, return, and let the caller count the failure
only after the password had been checked — with the lock released in between. So
N simultaneous requests all read the same under-cap state and all proceeded: a
5-attempt limit that permitted 5 per round with unbounded rounds in flight. F206.

This drives 50 concurrent admissions against a cap of 5 and asserts at most 5
were let through. It fails on the old shape by roughly an order of magnitude,
which is why the number is asserted rather than the behaviour described.
*/
func TestTheLoginCapHoldsAgainstASimultaneousBurst(t *testing.T) {
	const (
		maxAllowed = 5
		burst      = 50
	)
	limiter := newLoginLimiter(maxAllowed, time.Minute, time.Minute)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	start := make(chan struct{})
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together, so the race is the thing under test
			if _, ok := limiter.allow("203.0.113.7", "admin"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed > maxAllowed {
		t.Fatalf("%d of %d simultaneous attempts were admitted against a cap of %d: the budget is read and spent under separate locks", allowed, burst, maxAllowed)
	}
	if allowed == 0 {
		t.Fatal("no attempt was admitted at all; the limiter is refusing a fresh key")
	}
}

// A reservation that never reports an outcome must not hold budget forever: it
// ages out with everything else. Otherwise a handler that dies mid-login would
// permanently narrow that key's cap.
func TestAnAbandonedAttemptStopsCountingOnceItAgesOut(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(2, time.Minute, time.Minute)
	limiter.now = func() time.Time { return now }

	for range 2 {
		if _, ok := limiter.allow("203.0.113.7", "admin"); !ok {
			t.Fatal("a fresh key must admit up to the cap")
		}
	}
	if _, ok := limiter.allow("203.0.113.7", "admin"); ok {
		t.Fatal("the cap must hold while the reservations are live")
	}

	now = now.Add(2 * time.Minute) // past the window
	if _, ok := limiter.allow("203.0.113.7", "admin"); !ok {
		t.Fatal("abandoned reservations must age out, or a crashed handler narrows the cap forever")
	}
}

// And a resolved outcome releases its reservation rather than counting twice.
func TestAFailureConsumesItsOwnReservation(t *testing.T) {
	limiter := newLoginLimiter(5, time.Minute, time.Minute)

	for range 4 {
		if _, ok := limiter.allow("203.0.113.7", "admin"); !ok {
			t.Fatal("under the cap, an attempt must be admitted")
		}
		if _, blocked := limiter.registerFailure("203.0.113.7", "admin"); blocked {
			t.Fatal("four failures must not reach a cap of five")
		}
	}
	if _, ok := limiter.allow("203.0.113.7", "admin"); !ok {
		t.Fatal("the fifth attempt is still within the cap; reservations are being double-counted")
	}
}
