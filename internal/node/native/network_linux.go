//go:build linux

package native

import (
	"bytes"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type downloadBindingInventory struct {
	neighbors []netlink.Neigh
	routes    []netlink.Route
	addresses []netlink.Addr
}

func (p *Linux) inspectDownloadBinding(out, in netlink.Link) (downloadBindingInventory, error) {
	var inventory downloadBindingInventory
	neighbors, err := netlink.NeighList(out.Attrs().Index, netlink.FAMILY_V4)
	if err != nil {
		return inventory, err
	}
	for _, neighbor := range neighbors {
		address, ok := netip.AddrFromSlice(neighbor.IP)
		if !ok || !p.knownGateway(address.Unmap()) || neighbor.State != netlink.NUD_PERMANENT || !bytes.Equal(neighbor.HardwareAddr, in.Attrs().HardwareAddr) {
			return inventory, ErrOwnership
		}
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_MAIN, LinkIndex: out.Attrs().Index}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_OIF)
	if err != nil {
		return inventory, err
	}
	for _, route := range routes {
		gateway, ok := netip.AddrFromSlice(route.Gw)
		if !ok || !p.knownGateway(gateway.Unmap()) || route.Protocol != downProtocol || route.Dst == nil {
			return inventory, ErrOwnership
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil || !prefix.IsSingleIP() {
			return inventory, ErrOwnership
		}
	}
	addresses, err := netlink.AddrList(in, netlink.FAMILY_V4)
	if err != nil {
		return inventory, err
	}
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok || !p.knownPeer(ip.Unmap()) {
			return inventory, ErrOwnership
		}
	}
	return downloadBindingInventory{neighbors: neighbors, routes: routes, addresses: addresses}, nil
}

func (p *Linux) reconcileDownloadBinding(out, in netlink.Link) error {
	inventory, err := p.inspectDownloadBinding(out, in)
	if err != nil {
		return err
	}
	for _, address := range inventory.addresses {
		ip, _ := netip.AddrFromSlice(address.IP)
		if ip.Unmap() != p.network.PeerIPv4 {
			if err := netlink.AddrDel(in, &address); err != nil {
				return err
			}
		}
	}
	address := &netlink.Addr{IPNet: &net.IPNet{IP: net.IP(p.network.PeerIPv4.AsSlice()), Mask: net.CIDRMask(32, 32)}, Scope: unix.RT_SCOPE_LINK, Flags: unix.IFA_F_NOPREFIXROUTE}
	if err := netlink.AddrReplace(in, address); err != nil {
		return err
	}
	for _, neighbor := range inventory.neighbors {
		if !neighbor.IP.Equal(net.IP(p.network.GatewayIPv4.AsSlice())) {
			if err := netlink.NeighDel(&neighbor); err != nil {
				return err
			}
		}
	}
	for _, route := range inventory.routes {
		if route.Gw.Equal(net.IP(p.network.GatewayIPv4.AsSlice())) {
			continue
		}
		route.Gw = net.IP(p.network.GatewayIPv4.AsSlice())
		if err := netlink.RouteReplace(&route); err != nil {
			return err
		}
	}
	return nil
}
