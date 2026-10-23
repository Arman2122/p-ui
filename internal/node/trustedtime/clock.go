package trustedtime

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var ErrUntrusted = errors.New("authenticated time is required for this boot")

const MaxRoundTrip = 5 * time.Second

type Storage interface {
	ReadClock() ([]byte, error)
	WriteClock([]byte) error
}

type Instant struct {
	Boot string
	Nano int64
}

type Source func() (Instant, error)

type anchor struct {
	Version   uint32
	Boot      string
	Nano      int64
	UnixMilli int64
}

type Clock struct {
	mu      sync.Mutex
	storage Storage
	source  Source
	saved   atomic.Pointer[anchor]
	request atomic.Pointer[anchor]
	nonce   string
	started Instant
}

func New(storage Storage, source Source) (*Clock, error) {
	if storage == nil || source == nil {
		return nil, ErrUntrusted
	}
	c := &Clock{storage: storage, source: source}
	data, err := storage.ReadClock()
	if err != nil {
		return nil, err
	}
	if len(data) != 0 {
		var saved anchor
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 || !validBoot(saved.Boot) || saved.Nano < 0 || saved.UnixMilli <= 0 {
			return nil, ErrUntrusted
		}
		c.saved.Store(&saved)
	}
	return c, nil
}

func validBoot(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func now(at Instant, saved *anchor) (int64, error) {
	if saved == nil || at.Boot != saved.Boot || at.Nano < saved.Nano || saved.UnixMilli <= 0 {
		return 0, ErrUntrusted
	}
	elapsed := (at.Nano - saved.Nano) / int64(time.Millisecond)
	if (at.Nano-saved.Nano)%int64(time.Millisecond) != 0 {
		elapsed++
	}
	if saved.UnixMilli > math.MaxInt64-elapsed {
		return 0, ErrUntrusted
	}
	return saved.UnixMilli + elapsed, nil
}

func (c *Clock) Now() (time.Time, error) {
	saved := c.saved.Load()
	at, err := c.source()
	if err != nil {
		return time.Time{}, err
	}
	now, err := now(at, saved)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(now), nil
}

// Fresh-request deadlines use a lower time bound, unlike expiration's conservative upper bound.
// It is deliberately memory-only: a restarted worker must synchronize before requesting activation.
func (c *Clock) RequestTime() (time.Time, error) {
	saved := c.request.Load()
	at, err := c.source()
	if err != nil || saved == nil || at.Boot != saved.Boot || at.Nano < saved.Nano {
		return time.Time{}, ErrUntrusted
	}
	elapsed := (at.Nano - saved.Nano) / int64(time.Millisecond)
	if saved.UnixMilli > math.MaxInt64-elapsed {
		return time.Time{}, ErrUntrusted
	}
	return time.UnixMilli(saved.UnixMilli + elapsed), nil
}

func (c *Clock) Begin() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, err := c.source()
	if err != nil {
		return "", err
	}
	if !validBoot(at.Boot) || at.Nano < 0 {
		return "", ErrUntrusted
	}
	c.nonce, c.started = uuid.NewString(), at
	return c.nonce, nil
}

// Accept is called only with a response received through the authenticated master connection.
// Charging the entire round trip to elapsed time makes transport delay shorten authority.
func (c *Clock) Accept(nonce string, serverUnixMilli int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if nonce == "" || nonce != c.nonce {
		return ErrUntrusted
	}
	c.nonce = ""
	at, err := c.source()
	if err != nil {
		return err
	}
	if at.Boot != c.started.Boot || at.Nano < c.started.Nano || at.Nano-c.started.Nano > int64(MaxRoundTrip) || serverUnixMilli <= 0 {
		return ErrUntrusted
	}
	roundTrip := (at.Nano - c.started.Nano + int64(time.Millisecond) - 1) / int64(time.Millisecond)
	if serverUnixMilli > math.MaxInt64-roundTrip {
		return ErrUntrusted
	}
	stamp := serverUnixMilli + roundTrip
	if saved := c.saved.Load(); saved != nil {
		stamp = max(stamp, saved.UnixMilli)
	}
	if previous, err := now(at, c.saved.Load()); err == nil {
		stamp = max(stamp, previous)
	}
	saved := anchor{Version: 1, Boot: at.Boot, Nano: at.Nano, UnixMilli: stamp}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := c.storage.WriteClock(data); err != nil {
		return err
	}
	c.saved.Store(&saved)
	c.request.Store(&anchor{Version: 1, Boot: at.Boot, Nano: at.Nano, UnixMilli: serverUnixMilli})
	return nil
}
