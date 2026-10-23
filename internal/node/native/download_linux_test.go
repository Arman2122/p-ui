//go:build linux

package native

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func configureTestNetwork(t *testing.T, plane *Linux) {
	t.Helper()
	if os.Getenv("PENHOON_NATIVE_BINDING_TEST") == "1" {
		binding := NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.240.2"), GatewayIPv4: netip.MustParseAddr("172.31.240.3")}
		if err := plane.ConfigureNetwork(NetworkState{Current: &binding}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeDownloadOwnershipIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_ROUTE_TEST") != "1" {
		t.Skip("requires private network/mount namespaces")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	plane, err := NewLinux(uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, network, _ := net.ParseCIDR("169.254.0.0/16")
	foreign := &netlink.Route{Dst: network, Type: unix.RTN_BLACKHOLE, Table: unix.RT_TABLE_MAIN}
	if err := netlink.RouteAdd(foreign); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("adopted covered peer address: %v", err)
	}
	if _, err := netlink.LinkByName(downOut); err == nil {
		t.Fatal("created network state before rejecting address collision")
	}
	if err := netlink.RouteDel(foreign); err != nil {
		t.Fatal(err)
	}
	_, network, _ = net.ParseCIDR("0.0.0.0/0")
	foreign = &netlink.Route{Dst: network, Type: unix.RTN_BLACKHOLE, Table: downTable, Protocol: unix.RTPROT_STATIC}
	if err := netlink.RouteAdd(foreign); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("adopted foreign route table: %v", err)
	}
	if err := netlink.RouteDel(foreign); err != nil {
		t.Fatal(err)
	}
	rule := netlink.NewRule()
	rule.Table = unix.RT_TABLE_MAIN
	rule.Priority = 100
	rule.Family = netlink.FAMILY_V4
	if err := netlink.RuleAdd(rule); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("ignored overriding policy rule: %v", err)
	}
	if err := netlink.RuleDel(rule); err != nil {
		t.Fatal(err)
	}
	beforeForward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		t.Fatal(err)
	}
	beforeFilter, err := os.ReadFile("/proc/sys/net/ipv4/conf/all/rp_filter")
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	afterForward, _ := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	afterFilter, _ := os.ReadFile("/proc/sys/net/ipv4/conf/all/rp_filter")
	if string(beforeForward) != string(afterForward) || string(beforeFilter) != string(afterFilter) {
		t.Fatal("native initialization changed global forwarding or RPF")
	}
	if err := plane.Initialize(t.Context()); err != nil {
		t.Fatalf("owned routing was not restartable: %v", err)
	}
	testDownloadRouteCleanup(t, plane)
	replacement, err := NewLinux(plane.owner[len("penhoon-node-v2:"):], uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("another journal adopted native routing: %v", err)
	}
	peer, err := netlink.LinkByName(downIn)
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetAlias(peer, "foreign-owner"); err != nil {
		t.Fatal(err)
	}
	if err := plane.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("overwrote foreign peer alias: %v", err)
	}
}

func testDownloadRouteCleanup(t *testing.T, plane *Linux) {
	t.Helper()
	attrs := netlink.NewLinkAttrs()
	attrs.Name = "phvreap"
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: attrs, PeerName: "reappeer"}); err != nil {
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
	address, _ := netlink.ParseAddr("10.239.8.1/32")
	if err := netlink.AddrAdd(link, address); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	destination := netip.MustParsePrefix("10.239.8.2/32")
	if err := plane.AddRoute(t.Context(), attrs.Name, destination); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := plane.DeleteRoute(t.Context(), attrs.Name, destination); err != nil {
			t.Fatal(err)
		}
	}
	data, err := plane.run(t.Context(), "list set inet "+table+" native_dest4\n")
	if err != nil || strings.Contains(string(data), destination.Addr().String()) {
		t.Fatalf("deleted route retained destination guard: %s, %v", data, err)
	}
	if err := plane.AddRoute(t.Context(), attrs.Name, destination); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkDel(link); err != nil {
		t.Fatal(err)
	}
	if err := plane.PruneRoutes(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, network, _ := net.ParseCIDR(destination.String())
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN, Dst: network}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST)
	if err != nil || len(routes) != 0 {
		t.Fatalf("dead tunnel retained queued route: %+v, %v", routes, err)
	}
	data, err = plane.run(t.Context(), "list set inet "+table+" native_dest4\n")
	if err != nil || strings.Contains(string(data), destination.Addr().String()) {
		t.Fatalf("dead tunnel retained destination guard: %s, %v", data, err)
	}
}
