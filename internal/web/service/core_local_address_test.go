package service

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// localSessions is one client seen by one core at the given tunnel addresses.
func localSessions(email string, addrs ...string) []core.Session {
	local := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		local = append(local, netip.MustParseAddr(a))
	}
	return []core.Session{{
		Email:             email,
		Source:            netip.MustParseAddr("203.0.113.9"),
		Local:             local,
		LastSeenUnixMilli: time.Now().UnixMilli(),
	}}
}

/*
TestTheTunnelAddressIsReportedPerCore.

The whole point of the field: two clients reach each other over the address they
hold on the SAME core, so merging the cores' answers into one list would offer an
address that is only routable from somewhere the reader is not.
*/
func TestTheTunnelAddressIsReportedPerCore(t *testing.T) {
	tunnel := &declaredCore{id: "tunnel", kind: "wgkernel"}
	tunnel.sessions = localSessions("dual@x", "10.77.0.2")
	other := &declaredCore{id: "second", kind: "ocserv"}
	other.sessions = localSessions("dual@x", "192.168.9.4")

	got := clientLocalAddresses(context.Background(),
		registryOf(t, sessionCore{tunnel}, sessionCore{other}), "dual@x")

	want := []ClientLocalAddress{
		{Core: "second", Address: "192.168.9.4"},
		{Core: "tunnel", Address: "10.77.0.2"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %+v, want %+v sorted by core", got, want)
	}
}

// TestAnL7CoreReportsNoTunnelAddress: reporting none is the honest answer, not a
// gap. Its users share the host's stack and hold no address of their own.
func TestAnL7CoreReportsNoTunnelAddress(t *testing.T) {
	proxy := &declaredCore{id: "proxy", kind: "vless"}
	proxy.sessions = localSessions("web@x")

	got := clientLocalAddresses(context.Background(), registryOf(t, sessionCore{proxy}), "web@x")
	if len(got) != 0 {
		t.Fatalf("addresses = %+v, want none: an L7 core's users hold no in-tunnel address", got)
	}
}

/*
TestOneUnreachableCoreDoesNotBlankTheOthers.

Fail-open PER core, like ObserveSessions. Answering nothing at all because one
daemon is down reads as "this client has no address anywhere", which is a
different and wrong statement.
*/
func TestOneUnreachableCoreDoesNotBlankTheOthers(t *testing.T) {
	down := &declaredCore{id: "down", kind: "mtproto"}
	down.failWith = errors.New("dial: connection refused")
	up := &declaredCore{id: "tunnel", kind: "wgkernel"}
	up.sessions = localSessions("dual@x", "10.77.0.2")

	got := clientLocalAddresses(context.Background(),
		registryOf(t, sessionCore{down}, sessionCore{up}), "dual@x")

	want := []ClientLocalAddress{{Core: "tunnel", Address: "10.77.0.2"}}
	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %+v, want %+v from the core that could answer", got, want)
	}
}

// TestOnlyTheAskedClientIsReported: a session list carries every client on the
// core, and answering with another one hands out somebody else's address.
func TestOnlyTheAskedClientIsReported(t *testing.T) {
	tunnel := &declaredCore{id: "tunnel", kind: "wgkernel"}
	tunnel.sessions = append(localSessions("alice@x", "10.77.0.2"),
		localSessions("bob@x", "10.77.0.3")...)

	got := clientLocalAddresses(context.Background(), registryOf(t, sessionCore{tunnel}), "alice@x")
	want := []ClientLocalAddress{{Core: "tunnel", Address: "10.77.0.2"}}
	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %+v, want only alice's %+v", got, want)
	}
}

// TestARepeatedAddressIsReportedOnce: a core may report one session per live
// connection, all on the one address the client holds.
func TestARepeatedAddressIsReportedOnce(t *testing.T) {
	tunnel := &declaredCore{id: "tunnel", kind: "wgkernel"}
	tunnel.sessions = append(localSessions("alice@x", "10.77.0.2"),
		localSessions("alice@x", "10.77.0.2")...)

	got := clientLocalAddresses(context.Background(), registryOf(t, sessionCore{tunnel}), "alice@x")
	if len(got) != 1 {
		t.Fatalf("addresses = %+v, want the one address reported once", got)
	}
}
