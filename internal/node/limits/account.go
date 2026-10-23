package limits

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var (
	ErrBlocked  = errors.New("client forwarding is blocked")
	ErrExpired  = errors.New("client authorization expired")
	ErrQuota    = errors.New("client credit exhausted")
	ErrRevision = errors.New("stale or conflicting policy revision")
	ErrTerm     = errors.New("policy belongs to another term")
	ErrSize     = errors.New("invalid admission size")
	ErrClock    = errors.New("authorization clock is unavailable")
)

type Mode uint8

const (
	Blocked Mode = iota
	Limited
	Unlimited
)

type Direction uint8

const (
	Upload Direction = iota
	Download
)

type Rate struct {
	Mode          Mode
	BitsPerSecond uint64
	BurstBytes    int
}

type Policy struct {
	Term              string
	Revision          uint64
	AuthorityRevision uint64
	Enabled           bool
	ExpiresUnixMilli  int64
	QuotaUnlimited    bool
	CreditCeiling     uint64
	CreditRevision    uint64
	Upload            Rate
	Download          Rate
}

type Snapshot struct {
	Policy             Policy
	AdmittedUpload     uint64
	AdmittedDownload   uint64
	PreviouslyReserved uint64
	MeasuredUpload     uint64
	MeasuredDownload   uint64
	Quarantined        bool
}

type Persistence interface {
	StorePolicy(Policy) error
	Reserve(Policy, uint64) error
}

type Clock interface{ Now() (time.Time, error) }

type Account struct {
	quarantined        bool
	mu                 sync.Mutex
	policy             Policy
	uploaded           uint64
	downloaded         uint64
	previouslyReserved uint64
	measured           [2]uint64
	persistence        Persistence
	buckets            [2]*rate.Limiter
	changed            chan struct{}
	clock              Clock
}

func (a *Account) SetClock(clock Clock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clock = clock
}

func (a *Account) Quarantine() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.quarantined {
		a.quarantined = true
		close(a.changed)
		a.changed = make(chan struct{})
	}
}

func New(policy Policy) (*Account, error) {
	if err := validate(policy); err != nil {
		return nil, err
	}
	a := &Account{policy: policy, changed: make(chan struct{})}
	a.buckets[Upload] = bucket(policy.Upload)
	a.buckets[Download] = bucket(policy.Download)
	return a, nil
}

func Restore(policy Policy, previouslyReserved uint64, persistence Persistence) (*Account, error) {
	if persistence == nil {
		return nil, errors.New("durable account requires persistence")
	}
	a, err := New(policy)
	if err != nil {
		return nil, err
	}
	a.previouslyReserved = previouslyReserved
	a.persistence = persistence
	for _, b := range a.buckets {
		if b != nil {
			b.AllowN(time.Now(), b.Burst())
		}
	}
	return a, nil
}

func validate(p Policy) error {
	if p.Term == "" || p.Revision == 0 || p.ExpiresUnixMilli < 0 {
		return fmt.Errorf("invalid policy identity or expiry")
	}
	for _, r := range []Rate{p.Upload, p.Download} {
		switch r.Mode {
		case Blocked, Unlimited:
			if r.BitsPerSecond != 0 || r.BurstBytes != 0 {
				return fmt.Errorf("non-limited rate carries limit values")
			}
		case Limited:
			if r.BitsPerSecond == 0 || r.BitsPerSecond > 1<<53 ||
				r.BurstBytes < 1 || r.BurstBytes > 1<<20 {
				return fmt.Errorf("invalid limited rate")
			}
		default:
			return fmt.Errorf("unknown rate mode %d", r.Mode)
		}
	}
	if p.QuotaUnlimited && p.CreditCeiling != 0 {
		return fmt.Errorf("unlimited quota carries a credit ceiling")
	}
	return nil
}

func bucket(r Rate) *rate.Limiter {
	if r.Mode != Limited {
		return nil
	}
	return rate.NewLimiter(rate.Limit(float64(r.BitsPerSecond)/8), r.BurstBytes)
}

func (a *Account) Apply(p Policy) error {
	if err := validate(p); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applyLocked(p)
}

func (a *Account) applyLocked(p Policy) error {
	if p.Term != a.policy.Term {
		return ErrTerm
	}
	if p.AuthorityRevision < a.policy.AuthorityRevision || p.CreditRevision < a.policy.CreditRevision || p.Revision < a.policy.Revision || p.Revision == a.policy.Revision && p != a.policy && !CreditAdvance(a.policy, p) && !AuthorityAdvance(a.policy, p) {
		return ErrRevision
	}
	if (p.CreditRevision != 0 || a.policy.CreditRevision != 0) && (p.CreditCeiling < a.policy.CreditCeiling || p.CreditRevision == a.policy.CreditRevision && p.CreditCeiling != a.policy.CreditCeiling) {
		return ErrRevision
	}
	if (p.AuthorityRevision != 0 || a.policy.AuthorityRevision != 0) && p.QuotaUnlimited != a.policy.QuotaUnlimited {
		return ErrRevision
	}
	if p == a.policy {
		return nil
	}
	if a.persistence != nil {
		if err := a.persistence.StorePolicy(p); err != nil {
			return err
		}
	}
	now := time.Now()
	for dir, next := range []Rate{p.Upload, p.Download} {
		old := []Rate{a.policy.Upload, a.policy.Download}[dir]
		if next == old {
			continue
		}
		if a.buckets[dir] != nil && next.Mode == Limited {
			a.buckets[dir].SetLimitAt(now, rate.Limit(float64(next.BitsPerSecond)/8))
			a.buckets[dir].SetBurstAt(now, next.BurstBytes)
		} else {
			a.buckets[dir] = bucket(next)
			if a.buckets[dir] != nil {
				// A policy transition must not mint a fresh burst.
				a.buckets[dir].AllowN(now, next.BurstBytes)
			}
		}
	}
	a.policy = p
	close(a.changed)
	a.changed = make(chan struct{})
	return nil
}

func (a *Account) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Snapshot{
		Policy: a.policy, AdmittedUpload: a.uploaded, AdmittedDownload: a.downloaded,
		PreviouslyReserved: a.previouslyReserved, MeasuredUpload: a.measured[Upload], MeasuredDownload: a.measured[Download],
		Quarantined: a.quarantined,
	}
}

func (a *Account) Record(dir Direction, n int) {
	if dir > Download || n <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	admitted := [2]uint64{a.uploaded, a.downloaded}[dir]
	a.measured[dir] += min(uint64(n), admitted-a.measured[dir])
}

func (a *Account) Check() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allowed(time.Now(), Upload, 1)
}

func (a *Account) allowed(now time.Time, dir Direction, n uint64) error {
	if a.clock != nil {
		var err error
		now, err = a.clock.Now()
		if err != nil {
			return errors.Join(ErrClock, err)
		}
	}
	if a.quarantined || !a.policy.Enabled || []Rate{a.policy.Upload, a.policy.Download}[dir].Mode == Blocked {
		return ErrBlocked
	}
	if a.policy.ExpiresUnixMilli != 0 && now.UnixMilli() >= a.policy.ExpiresUnixMilli {
		return ErrExpired
	}
	if a.uploaded > math.MaxUint64-a.previouslyReserved || a.downloaded > math.MaxUint64-a.previouslyReserved-a.uploaded || n > math.MaxUint64-a.previouslyReserved-a.uploaded-a.downloaded {
		return ErrQuota
	}
	total := a.previouslyReserved + a.uploaded + a.downloaded
	if !a.policy.QuotaUnlimited && (total > a.policy.CreditCeiling || n > a.policy.CreditCeiling-total) {
		return ErrQuota
	}
	return nil
}

// Take consumes forwarding authority, which is distinct from measured billable usage.
// Failed downstream writes do not refund already consumed spend authority.
func (a *Account) Take(ctx context.Context, dir Direction, n int) error {
	_, err := a.take(ctx, dir, n, false)
	return err
}

func (a *Account) TakeUpTo(ctx context.Context, dir Direction, n int) (int, error) {
	return a.take(ctx, dir, n, true)
}

func (a *Account) take(ctx context.Context, dir Direction, requested int, partial bool) (int, error) {
	if dir > Download || requested <= 0 {
		return 0, ErrSize
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		a.mu.Lock()
		now := time.Now()
		n := requested
		if partial {
			if err := a.allowed(now, dir, 1); err != nil {
				a.mu.Unlock()
				return 0, err
			}
			if !a.policy.QuotaUnlimited {
				n = int(min(uint64(n), a.policy.CreditCeiling-a.previouslyReserved-a.uploaded-a.downloaded))
			}
			if b := a.buckets[dir]; b != nil {
				n = min(n, b.Burst())
			}
		}
		if err := a.allowed(now, dir, uint64(n)); err != nil {
			a.mu.Unlock()
			return 0, err
		}
		limiter := a.buckets[dir]
		if limiter == nil || limiter.AllowN(now, n) {
			if a.persistence != nil {
				if err := a.persistence.Reserve(a.policy, a.previouslyReserved+a.uploaded+a.downloaded+uint64(n)); err != nil {
					a.mu.Unlock()
					return 0, err
				}
			}
			if dir == Upload {
				a.uploaded += uint64(n)
			} else {
				a.downloaded += uint64(n)
			}
			a.mu.Unlock()
			return n, nil
		}
		if n > limiter.Burst() {
			a.mu.Unlock()
			return 0, ErrSize
		}
		deficit := float64(n) - limiter.TokensAt(now)
		delay := max(time.Millisecond, time.Duration(deficit/float64(limiter.Limit())*float64(time.Second)))
		if expires := a.policy.ExpiresUnixMilli; expires != 0 {
			expiryNow := now
			if a.clock != nil {
				var err error
				expiryNow, err = a.clock.Now()
				if err != nil {
					a.mu.Unlock()
					return 0, errors.Join(ErrClock, err)
				}
			}
			delay = min(delay, time.UnixMilli(expires).Sub(expiryNow))
		}
		changed := a.changed
		a.mu.Unlock()
		timer := time.NewTimer(max(delay, time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
