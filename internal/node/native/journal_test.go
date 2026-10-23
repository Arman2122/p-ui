package native

import (
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func testGrant(credit uint64) governor.Grant {
	return governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{
		Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: credit,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	}}
}

func TestNativeReservationSurvivesLostKernel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.db")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g := testGrant(1000)
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	selectors := []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	lease, err := j.Reserve(g.ID, selectors, 700, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	if _, err := j.Reserve(g.ID, selectors, 700, time.Now()); !errors.Is(err, ErrBusy) {
		t.Fatalf("reissued unsettled slice: %v", err)
	}
	if err := j.Forget(g.ID, lease.ID); !errors.Is(err, ErrAuthority) {
		t.Fatalf("forgot unaccounted slice: %v", err)
	}
	if err := j.Finalize(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	if err := j.Forget(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	last, err := j.Reserve(g.ID, selectors, 700, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if last.Bytes != 300 || last.ID == lease.ID || last.Class != lease.Class {
		t.Fatalf("replayed credit or class: %+v", last)
	}
	if err := j.Finalize(g.ID, last.ID); err != nil {
		t.Fatal(err)
	}
	if err := j.Forget(g.ID, last.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Reserve(g.ID, selectors, 1, time.Now()); !errors.Is(err, limits.ErrQuota) {
		t.Fatalf("replayed exhausted credit: %v", err)
	}
	g.Policy.Revision++
	g.Policy.CreditCeiling = 1500
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	refill, err := j.Reserve(g.ID, selectors, 700, time.Now())
	if err != nil || refill.Bytes != 500 {
		t.Fatalf("refill = %+v, %v", refill, err)
	}
}

func TestNativeRevisionAndExpiry(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	g := testGrant(1000)
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	g.Policy.CreditCeiling++
	if err := j.Apply(g); !errors.Is(err, limits.ErrRevision) {
		t.Fatalf("conflicting replay = %v", err)
	}
	g.Policy.Revision++
	g.Policy.ExpiresUnixMilli = time.Now().Add(-time.Second).UnixMilli()
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	_, err = j.Reserve(g.ID, []Selector{{"phvtest", netip.MustParseAddr("10.0.0.2")}}, 1, time.Now())
	if !errors.Is(err, limits.ErrExpired) {
		t.Fatalf("expired authority = %v", err)
	}
}
