package native

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/authority"
)

func TestNetworkBindingRejectsUnsafeAddresses(t *testing.T) {
	for _, test := range []NetworkBinding{
		{},
		{PeerIPv4: netip.MustParseAddr("127.0.0.2"), GatewayIPv4: netip.MustParseAddr("10.0.0.3")},
		{PeerIPv4: netip.MustParseAddr("1.1.1.1"), GatewayIPv4: netip.MustParseAddr("10.0.0.3")},
		{PeerIPv4: netip.MustParseAddr("::ffff:10.0.0.2"), GatewayIPv4: netip.MustParseAddr("10.0.0.3")},
		{PeerIPv4: netip.MustParseAddr("169.254.0.2"), GatewayIPv4: netip.MustParseAddr("169.254.1.3")},
		{PeerIPv4: netip.MustParseAddr("10.0.0.2"), GatewayIPv4: netip.MustParseAddr("10.0.0.2")},
	} {
		if err := test.Validate(); !errors.Is(err, ErrAuthority) {
			t.Fatalf("unsafe binding %+v accepted: %v", test, err)
		}
	}
}

func TestNetworkBindingRequiresQuiescenceAndPersistsPendingRecovery(t *testing.T) {
	c, journal, _, _, assignment := selectorFixture(t)
	assignment.Selectors = []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.0.0.2")}}
	if err := c.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	first := DefaultNetworkBinding()
	if _, err := journal.StageNetwork(first); !errors.Is(err, ErrBusy) {
		t.Fatalf("active authority allowed network rebinding: %v", err)
	}
	if err := c.Quiesce(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := journal.Records()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.StageNetwork(first); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Reserve(assignment.Grant.ID, assignment.Selectors, MaxSliceBytes, time.Now()); !errors.Is(err, ErrBusy) {
		t.Fatalf("pending network allowed forwarding funding: %v", err)
	}
	page, err := c.Authority(t.Context(), "", 1)
	if err != nil || len(page.Grants) != 1 || page.Grants[0].SpendableKnown {
		t.Fatalf("pending network projected spendable authority: %+v %v", page, err)
	}
	if err := c.ApplyCredit(t.Context(), []authority.Credit{nativeCredit(assignment, 2, assignment.Grant.Policy.CreditCeiling+1000)}); !errors.Is(err, ErrBusy) {
		t.Fatalf("pending network accepted fresh credit: %v", err)
	}
	if err := journal.CommitNetwork(first); err != nil {
		t.Fatal(err)
	}
	next := NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.240.2"), GatewayIPv4: netip.MustParseAddr("172.31.240.3")}
	if _, err := journal.StageNetwork(next); err != nil {
		t.Fatal(err)
	}
	path := journal.db.Path()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state, err := reopened.Network()
	if err != nil || state.Current == nil || *state.Current != first || state.Pending == nil || *state.Pending != next {
		t.Fatalf("lost binding recovery transaction: %+v %v", state, err)
	}
	if _, err := reopened.StageNetwork(first); !errors.Is(err, ErrBusy) {
		t.Fatalf("recovery silently selected another target: %v", err)
	}
	if err := reopened.CommitNetwork(next); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Records()
	if err != nil || len(after) != 1 || after[0].Reserved != before[0].Reserved || after[0].Lease != nil {
		t.Fatalf("network transition released reserved credit: %+v %v", after, err)
	}
}
