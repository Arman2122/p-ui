package service

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
)

/*
A WireGuard subnet must not collide with something the host already answers for.

The panel treated `SELECT * FROM inbounds` as the authority on which subnets are
claimed. It is not: the resource being allocated is the host's ONE routing
table, and eth0, docker0 or a private-network NIC are in it without any row
saying so. Nothing on the WireGuard save path asked the host anything.

So a device prefix overlapping the LAN saved cleanly, and desiredRoutes then
installed a connected route for it -- capturing traffic meant for the LAN, for
docker, or for the panel's own management network. The same held for a
hand-entered client allowedIP.

Reading the host FAILS OPEN, deliberately: a netlink hiccup, or any non-Linux
box, must not start refusing every WireGuard save. That is byte-for-byte the
behaviour before this existed. F067.
*/

func stubHostAddrs(t *testing.T, addrs []egress.AddrSpec, err error) {
	t.Helper()
	prev := wireguardHostAddrs
	wireguardHostAddrs = func(context.Context) ([]egress.AddrSpec, error) { return addrs, err }
	t.Cleanup(func() { wireguardHostAddrs = prev })
}

func hostAddr(t *testing.T, prefix, device string) egress.AddrSpec {
	t.Helper()
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		t.Fatalf("parse %q: %v", prefix, err)
	}
	return egress.AddrSpec{Prefix: p, Device: device}
}

func TestAWireguardInboundCannotClaimASubnetTheHostHolds(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.20.0.5/24", "eth0")}, nil)

	_, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51981, "10.20.0.1/24"))
	if err == nil {
		t.Fatal("an inbound claimed a subnet eth0 already carries; the connected route it installs captures traffic meant for that network")
	}
	if !strings.Contains(err.Error(), "eth0") {
		t.Fatalf("the refusal does not name the device holding the address, so the operator cannot act on it: %v", err)
	}
}

func TestAWireguardInboundOnAFreeSubnetIsStillAccepted(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.20.0.5/24", "eth0")}, nil)

	if _, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51982, "10.77.0.1/24")); err != nil {
		t.Fatalf("a subnet nothing on the host holds was refused: %v", err)
	}
}

// The inbound's own device must not read as a squatter on itself. Without the
// self term in `mine`, re-saving a working inbound would refuse it.
func TestAWireguardInboundIsNotItsOwnSquatter(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, nil, nil)

	created, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51983, "10.31.0.1/24"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Now the host reports that inbound's OWN device carrying its own address.
	device, ok := egressIngressDevice(context.Background(), created)
	if !ok {
		t.Skip("this kind exposes no ingress device; the self-exclusion cannot be exercised")
	}
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.31.0.1/24", device)}, nil)

	if err := (&InboundService{}).checkWireguardHostAddressConflict(created); err != nil {
		t.Fatalf("an inbound was refused for carrying its own address on its own device: %v", err)
	}
}

// Fail open: the host being unreadable must not refuse the save.
func TestAnUnreadableHostDoesNotRefuseAWireguardSave(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, nil, context.DeadlineExceeded)

	if _, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51984, "10.44.0.1/24")); err != nil {
		t.Fatalf("a netlink failure refused a WireGuard save; on a non-Linux box that would refuse every one: %v", err)
	}
}

// And a client address is allocated AROUND the host, not refused.
func TestClientAddressesAvoidASubnetTheHostHolds(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, nil, nil)

	inboundSvc := &InboundService{}
	created, _, _, err := inboundSvc.AddInbound(wgkernelInbound(51985, "10.55.0.1/24"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The host turns out to hold the low half of that subnet.
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.55.0.2/31", "docker0")}, nil)

	pool, err := inboundSvc.wireguardPoolFor(created)
	if err != nil {
		t.Fatalf("wireguardPoolFor: %v", err)
	}
	got, err := allocateWireguardAddress(nil, pool.base, pool.blocked)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	blocked := netip.MustParsePrefix("10.55.0.2/31")
	if addr := wireguardHostAddr(got); blocked.Contains(addr) {
		t.Fatalf("a client was handed %s, inside a range docker0 already answers for", got)
	}
}

/*
A client must never be handed the device's own address.

pool.blocked carried other inbounds' device prefixes and pool.taken carried
other inbounds' client addresses. This inbound's OWN device address was in
neither, and the allocator's only protection was that it starts at .2.

So a device on 10.0.0.5/24 hands 10.0.0.5/32 to the fourth client allocated.
That client and the host device then answer to the same address: its return
traffic is delivered locally instead of into the tunnel, and nothing reports it.

Only the host address is taken, not the prefix -- the prefix IS the pool, and
blocking it would leave nothing to allocate from. F101.
*/
func TestAClientIsNotHandedTheDevicesOwnAddress(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, nil, nil)

	inboundSvc := &InboundService{}
	created, _, _, err := inboundSvc.AddInbound(wgkernelInbound(51991, "10.60.0.5/24"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	pool, err := inboundSvc.wireguardPoolFor(created)
	if err != nil {
		t.Fatalf("wireguardPoolFor: %v", err)
	}

	// Walk the first handful; .5 is the fourth address the allocator reaches.
	used := append([]string{}, pool.taken...)
	for range 6 {
		got, aErr := allocateWireguardAddress(used, pool.base, pool.blocked)
		if aErr != nil {
			t.Fatalf("allocate: %v", aErr)
		}
		if wireguardHostAddr(got) == netip.MustParseAddr("10.60.0.5") {
			t.Fatalf("a client was handed %s, the address the device itself answers on; its return traffic is delivered locally instead of into the tunnel", got)
		}
		used = append(used, got)
	}
	// The pool itself must still be usable.
	if len(used) != len(pool.taken)+6 {
		t.Fatalf("allocated %d addresses, want %d", len(used), len(pool.taken)+6)
	}
}
