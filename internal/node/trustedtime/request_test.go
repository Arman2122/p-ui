package trustedtime_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

func TestFreshRequestClockNeverReusesExpirationHighWater(t *testing.T) {
	disk := new(storage)
	at := trustedtime.Instant{Boot: uuid.NewString(), Nano: int64(time.Second)}
	source := func() (trustedtime.Instant, error) { return at, nil }
	clock, err := trustedtime.New(disk, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.RequestTime(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("unanchored demand time=%v", err)
	}
	nonce, _ := clock.Begin()
	base := int64(1_800_000_000_000)
	at.Nano += int64(4 * time.Second)
	if err := clock.Accept(nonce, base); err != nil {
		t.Fatal(err)
	}
	at.Nano += int64(1500 * time.Microsecond)
	if stamp, err := clock.RequestTime(); err != nil || stamp.UnixMilli() != base+1 {
		t.Fatalf("lower request bound=%v %v", stamp, err)
	}
	if stamp, err := clock.Now(); err != nil || stamp.UnixMilli() != base+4002 {
		t.Fatalf("upper expiry bound=%v %v", stamp, err)
	}
	nonce, _ = clock.Begin()
	if err := clock.Accept(nonce, base-60000); err != nil {
		t.Fatal(err)
	}
	if stamp, _ := clock.RequestTime(); stamp.UnixMilli() != base-60000 {
		t.Fatalf("historical upper bound inflated request deadline=%v", stamp)
	}
	clock, err = trustedtime.New(disk, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.RequestTime(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("restart replayed request clock=%v", err)
	}
	if stamp, err := clock.Now(); err != nil || stamp.UnixMilli() != base+4002 {
		t.Fatalf("restart lost expiry highwater=%v %v", stamp, err)
	}
	nonce, _ = clock.Begin()
	if err := clock.Accept(nonce, base+100); err != nil {
		t.Fatal(err)
	}
	at.Boot = uuid.NewString()
	if _, err := clock.RequestTime(); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("new boot reused request time=%v", err)
	}
}
