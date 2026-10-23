//go:build linux

package native

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestNativeBindingRecoveryIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_BINDING_RECOVERY_TEST") != "1" {
		t.Skip("requires private network/mount namespaces")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	dir := t.TempDir()
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	usage, err := state.Open(filepath.Join(dir, "usage.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	journal, err := Open(filepath.Join(dir, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = journal.Close() }()
	first, next := DefaultNetworkBinding(), NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.240.2"), GatewayIPv4: netip.MustParseAddr("172.31.240.3")}
	pending, err := journal.StageNetwork(first)
	if err != nil {
		t.Fatal(err)
	}
	plane, err := NewLinux(identity.InstallationID, journal.JournalID())
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.ConfigureNetwork(pending); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := journal.CommitNetwork(first); err != nil {
		t.Fatal(err)
	}
	if err := plane.ConfigureNetwork(NetworkState{Current: &first}); err != nil {
		t.Fatal(err)
	}
	controller, err := NewController(journal, usage, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	attrs := netlink.NewLinkAttrs()
	attrs.Name = "phvbinding"
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: attrs, PeerName: "bindingpeer"}); err != nil {
		t.Fatal(err)
	}
	link, err := netlink.LinkByName(attrs.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetAlias(link, plane.owner); err != nil {
		t.Fatal(err)
	}
	if err := plane.Guard(t.Context(), []string{attrs.Name}); err != nil {
		t.Fatal(err)
	}
	address, _ := netlink.ParseAddr("10.244.1.1/32")
	if err := netlink.AddrAdd(link, address); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	grant := testGrant(4 * MaxSliceBytes)
	grant.Policy.Download = limits.Rate{Mode: limits.Limited, BitsPerSecond: 8 << 20, BurstBytes: 65536}
	assignment := Assignment{Grant: grant, Selectors: []Selector{{Device: attrs.Name, Address: netip.MustParseAddr("10.244.1.2")}}}
	if err := controller.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.StageNetwork(next); !errors.Is(err, ErrBusy) {
		t.Fatalf("active slice permitted network change: %v", err)
	}
	if err := plane.ConfigureNetwork(NetworkState{Current: &first, Pending: &next}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("kernel accepted nonquiescent network transaction: %v", err)
	}
	if err := plane.ConfigureNetwork(NetworkState{Current: &first}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Quiesce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.StageNetwork(next); err != nil {
		t.Fatal(err)
	}
	out, err := plane.downloadLink(downOut)
	if err != nil {
		t.Fatal(err)
	}
	in, err := plane.downloadLink(downIn)
	if err != nil {
		t.Fatal(err)
	}
	wrongMAC, _ := net.ParseMAC("02:00:00:00:ff:ff")
	neighbor := &netlink.Neigh{LinkIndex: out.Attrs().Index, Family: netlink.FAMILY_V4, IP: net.IP(next.GatewayIPv4.AsSlice()), HardwareAddr: wrongMAC, State: netlink.NUD_PERMANENT}
	if err := netlink.NeighSet(neighbor); err != nil {
		t.Fatal(err)
	}
	if err := plane.ConfigureNetwork(NetworkState{Current: &first, Pending: &next}); err != nil {
		t.Fatal(err)
	}
	if err := plane.CheckNetwork(next); !errors.Is(err, ErrOwnership) {
		t.Fatalf("preflight accepted conflicting next-hop neighbor: %v", err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("adopted conflicting next-hop neighbor: %v", err)
	}
	neighbor.HardwareAddr = in.Attrs().HardwareAddr
	if err := netlink.NeighSet(neighbor); err != nil {
		t.Fatal(err)
	}
	address = &netlink.Addr{IPNet: &net.IPNet{IP: net.IP(next.PeerIPv4.AsSlice()), Mask: net.CIDRMask(32, 32)}, Scope: unix.RT_SCOPE_LINK, Flags: unix.IFA_F_NOPREFIXROUTE}
	if err := netlink.AddrAdd(in, address); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		journal, err = Open(filepath.Join(dir, "credit.db"))
		if err != nil {
			t.Fatal(err)
		}
		pending, err = journal.Network()
		if err != nil {
			t.Fatal(err)
		}
		plane, err = NewLinux(identity.InstallationID, journal.JournalID())
		if err != nil {
			t.Fatal(err)
		}
		if err := plane.ConfigureNetwork(pending); err != nil {
			t.Fatal(err)
		}
		if err := plane.Initialize(t.Context()); err != nil {
			t.Fatalf("partial binding recovery failed: %v", err)
		}
	}
	addresses, err := netlink.AddrList(in, netlink.FAMILY_V4)
	if err != nil || len(addresses) != 1 || addresses[0].IPNet.String() != "172.31.240.2/32" {
		t.Fatalf("partial recovery retained stale peer: %v %v", addresses, err)
	}
	_, destination, _ := net.ParseCIDR("10.244.1.2/32")
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN, Dst: destination}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST)
	if err != nil || len(routes) != 1 || !routes[0].Gw.Equal(net.IP(next.GatewayIPv4.AsSlice())) {
		t.Fatalf("partial recovery retained stale gateway: %v %v", routes, err)
	}
	if err := journal.CommitNetwork(next); err != nil {
		t.Fatal(err)
	}
	rows, err := journal.Records()
	if err != nil || len(rows) != 1 || rows[0].Reserved != MaxSliceBytes || rows[0].Lease != nil {
		t.Fatalf("binding recovery released or replayed quota: %+v %v", rows, err)
	}
}
