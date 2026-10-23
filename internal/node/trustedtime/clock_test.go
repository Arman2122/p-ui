package trustedtime_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

type storage struct {
	data []byte
	fail bool
}

func (s *storage) ReadClock() ([]byte, error) { return bytes.Clone(s.data), nil }
func (s *storage) WriteClock(data []byte) error {
	if s.fail {
		return errors.New("disk unavailable")
	}
	s.data = bytes.Clone(data)
	return nil
}

func TestTimeResponsesCannotReplayRegressOrSurviveAnUnknownBoot(t *testing.T) {
	disk := new(storage)
	at := trustedtime.Instant{Boot: uuid.NewString(), Nano: int64(time.Second)}
	source := func() (trustedtime.Instant, error) { return at, nil }
	clock, err := trustedtime.New(disk, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.Now(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("unanchored clock: %v", err)
	}
	nonce, err := clock.Begin()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UnixMilli()
	at.Nano += int64(1500 * time.Microsecond)
	if err := clock.Accept(nonce, base); err != nil {
		t.Fatal(err)
	}
	want := base + 2
	if now, err := clock.Now(); err != nil || now.UnixMilli() != want {
		t.Fatalf("round-trip upper bound: %v, %v", now, err)
	}
	if err := clock.Accept(nonce, base+1000); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("replayed response: %v", err)
	}
	at.Nano += int64(3 * time.Second)
	clock, err = trustedtime.New(disk, source)
	if err != nil {
		t.Fatal(err)
	}
	want += 3000
	if now, err := clock.Now(); err != nil || now.UnixMilli() != want {
		t.Fatalf("same-boot restart: %v, %v", now, err)
	}
	nonce, _ = clock.Begin()
	if err := clock.Accept(nonce, base-10000); err != nil {
		t.Fatal(err)
	}
	if now, _ := clock.Now(); now.UnixMilli() != want {
		t.Fatalf("master rollback regressed clock: %v", now)
	}
	nonce, _ = clock.Begin()
	at.Nano += int64(trustedtime.MaxRoundTrip + time.Nanosecond)
	if err := clock.Accept(nonce, base); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("stale time response: %v", err)
	}
	at.Boot, at.Nano = uuid.NewString(), int64(time.Hour)
	clock, err = trustedtime.New(disk, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.Now(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("new boot reused old authority: %v", err)
	}
	nonce, _ = clock.Begin()
	disk.fail = true
	if err := clock.Accept(nonce, base+100000); err == nil || err.Error() != "disk unavailable" {
		t.Fatalf("failed durable time commit: %v", err)
	}
	if _, err := clock.Now(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("failed commit enabled authority: %v", err)
	}
	disk.fail = false
	nonce, _ = clock.Begin()
	if err := clock.Accept(nonce, base+100000); err != nil {
		t.Fatal(err)
	}
	if now, _ := clock.Now(); now.UnixMilli() != base+100000 {
		t.Fatalf("fresh master time: %v", now)
	}
}

func TestPersistedGovernorCannotReviveExpiredAuthorityAfterRestart(t *testing.T) {
	dir := t.TempDir()
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	journal, err := state.Open(filepath.Join(dir, "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = journal.Close() }()
	at := trustedtime.Instant{Boot: uuid.NewString(), Nano: int64(time.Second)}
	source := func() (trustedtime.Instant, error) { return at, nil }
	clock, err := trustedtime.New(journal, source)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := clock.Begin()
	base := time.Now().Add(time.Hour).UnixMilli()
	if err := clock.Accept(nonce, base); err != nil {
		t.Fatal(err)
	}
	creditPath := filepath.Join(dir, "credit.db")
	credit, err := governor.Open(creditPath)
	if err != nil {
		t.Fatal(err)
	}
	grant := governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, ExpiresUnixMilli: base + 1000, QuotaUnlimited: true, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
	credit.SetClock(clock)
	if err := credit.Apply(grant); err != nil {
		t.Fatal(err)
	}
	account, ok := credit.Lookup(grant.ClientID)
	if !ok {
		t.Fatal("missing account")
	}
	if err := account.Take(context.Background(), limits.Upload, 1); err != nil {
		t.Fatal(err)
	}
	if err := credit.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	at.Nano += int64(2 * time.Second)
	journal, err = state.Open(filepath.Join(dir, "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	clock, err = trustedtime.New(journal, source)
	if err != nil {
		t.Fatal(err)
	}
	credit, err = governor.Open(creditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer credit.Close()
	credit.SetClock(clock)
	account, ok = credit.Lookup(grant.ClientID)
	if !ok {
		t.Fatal("missing restored account")
	}
	if err := account.Take(context.Background(), limits.Upload, 1); !errors.Is(err, limits.ErrExpired) {
		t.Fatalf("expired authority revived: %v", err)
	}
	at.Boot, at.Nano = uuid.NewString(), 0
	if err := account.Take(context.Background(), limits.Upload, 1); !errors.Is(err, limits.ErrClock) {
		t.Fatalf("unknown boot admitted traffic: %v", err)
	}
}
