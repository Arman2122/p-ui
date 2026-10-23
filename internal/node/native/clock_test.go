package native

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type fixedNativeClock struct {
	now time.Time
	err error
}

func (c *fixedNativeClock) Now() (time.Time, error) { return c.now, c.err }

type shiftedNativeClock struct{ offset time.Duration }

func (c shiftedNativeClock) Now() (time.Time, error) { return time.Now().Add(c.offset), nil }

func TestNativeLogicalClockPreventsWallRollbackRenewal(t *testing.T) {
	c, journal, _, _, assignment := selectorFixture(t)
	clock := &fixedNativeClock{now: time.Now().Add(time.Hour)}
	c.SetClock(clock)
	assignment.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	assignment.Grant.Policy.ExpiresUnixMilli = clock.now.Add(time.Minute).UnixMilli()
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	before, err := journal.Records()
	if err != nil || len(before) != 1 || before[0].Lease == nil || before[0].Lease.ExpiresUnixMilli != assignment.Grant.Policy.ExpiresUnixMilli {
		t.Fatalf("logical lease deadline: %+v %v", before, err)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if err := c.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	after, err := journal.Records()
	if err != nil || len(after) != 1 || after[0].Lease != nil || after[0].Reserved != before[0].Reserved {
		t.Fatalf("wall clock reopened expired authority: %+v %v", after, err)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || page.Grants[0].AuthorizationActive {
		t.Fatalf("expired logical authority: %+v %v", page, err)
	}
}

func TestNativeUntrustedClockWaitsWithoutFunding(t *testing.T) {
	c, journal, _, _, assignment := selectorFixture(t)
	clock := &fixedNativeClock{err: limits.ErrClock}
	c.SetClock(clock)
	if err := c.Collect(t.Context()); err != nil {
		t.Fatalf("empty runtime could not wait for master time: %v", err)
	}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, limits.ErrClock) {
		t.Fatalf("untrusted clock funded forwarding: %v", err)
	}
	records, err := journal.Records()
	if err != nil || len(records) != 0 {
		t.Fatalf("untrusted clock changed credit: %+v %v", records, err)
	}
	if _, err := c.Authority(t.Context(), "", 1); !errors.Is(err, limits.ErrClock) {
		t.Fatalf("untrusted clock projected authority: %v", err)
	}
	clock.now, clock.err = time.Now(), nil
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatalf("trusted time failed to unblock new authority: %v", err)
	}
}
