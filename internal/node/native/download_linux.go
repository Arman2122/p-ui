//go:build linux

package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

const (
	downOut               = "phvdownout"
	downIn                = "phvdownin"
	downTable             = 32000
	downPriority          = 30000
	downProtocol          = 247
	inetForwarding        = 1
	inetAcceptRedirects   = 4
	inetSendRedirects     = 6
	inetReversePathFilter = 8
	inetAcceptLocal       = 23
)

func (p *Linux) CheckNetwork(target NetworkBinding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := target.Validate(); err != nil {
		return err
	}
	if err := p.checkDownloadAddress(target); err != nil {
		return err
	}
	out, outErr := p.downloadLink(downOut)
	in, inErr := p.downloadLink(downIn)
	anchored := outErr == nil && inErr == nil
	if !anchored {
		var missingOut, missingIn netlink.LinkNotFoundError
		if !errors.As(outErr, &missingOut) || !errors.As(inErr, &missingIn) {
			return ErrOwnership
		}
	}
	if err := p.checkDownloadSpace(anchored); err != nil {
		return err
	}
	if anchored {
		_, err := p.inspectDownloadBinding(out, in)
		return err
	}
	return nil
}

func (p *Linux) knownPeer(address netip.Addr) bool {
	return address == p.network.PeerIPv4 || p.previousNetwork != nil && address == p.previousNetwork.PeerIPv4
}

func (p *Linux) knownGateway(address netip.Addr) bool {
	return address == p.network.GatewayIPv4 || p.previousNetwork != nil && address == p.previousNetwork.GatewayIPv4
}

func (p *Linux) checkDownloadAddress(target NetworkBinding) error {
	wanted := []netip.Addr{target.PeerIPv4, target.GatewayIPv4}
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	owned := make(map[int]string)
	for _, link := range links {
		if p.OwnsDownloadLink(link) {
			owned[link.Attrs().Index] = link.Attrs().Name
		}
		addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			ip, ok := netip.AddrFromSlice(address.IP)
			if !ok {
				return ErrOwnership
			}
			ip = ip.Unmap()
			if name := owned[link.Attrs().Index]; name != "" {
				ones, bits := address.Mask.Size()
				if name != downIn || !p.knownPeer(ip) || ones != 32 || bits != 32 || address.Scope != unix.RT_SCOPE_LINK {
					return fmt.Errorf("%w: unexpected native download address", ErrOwnership)
				}
				continue
			}
			for _, target := range wanted {
				if ip == target {
					return fmt.Errorf("%w: native download address is already assigned", ErrOwnership)
				}
			}
		}
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.Dst == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil || prefix.Bits() == 0 {
			continue
		}
		if owned[route.LinkIndex] == downIn && prefix.IsSingleIP() && p.knownPeer(prefix.Addr()) && route.Protocol == unix.RTPROT_KERNEL {
			continue
		}
		for _, target := range wanted {
			if prefix.Contains(target) {
				return fmt.Errorf("%w: native download address overlaps host routing", ErrOwnership)
			}
		}
	}
	return nil
}

func (p *Linux) downloadAlias(name string) string { return p.tableOwner + ";role:" + name }

func (p *Linux) downloadLink(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	if link.Type() != "veth" || link.Attrs().Alias != p.downloadAlias(name) {
		return nil, fmt.Errorf("%w: download device %s type=%s alias=%q", ErrOwnership, name, link.Type(), link.Attrs().Alias)
	}
	return link, nil
}

func (p *Linux) OwnsDownloadLink(link netlink.Link) bool {
	name := link.Attrs().Name
	return (name == downOut || name == downIn) && link.Type() == "veth" && link.Attrs().Alias == p.downloadAlias(name)
}

func ValidateIPv6Forwarding(addresses []netip.Addr) error {
	for _, address := range addresses {
		if address.Is6() {
			data, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/forwarding")
			if err != nil || strings.TrimSpace(string(data)) != "1" {
				return fmt.Errorf("%w: IPv6 forwarding must be enabled by the host administrator", ErrAuthority)
			}
			break
		}
	}
	return nil
}

func setDownloadIPv4(link netlink.Link) error {
	request := nl.NewNetlinkRequest(unix.RTM_SETLINK, unix.NLM_F_ACK)
	message := nl.NewIfInfomsg(unix.AF_UNSPEC)
	message.Index = int32(link.Attrs().Index)
	request.AddData(message)
	spec := nl.NewRtAttr(unix.IFLA_AF_SPEC, nil)
	family := spec.AddRtAttr(unix.AF_INET, nil)
	config := family.AddRtAttr(unix.IFLA_INET_CONF, nil)
	for key, value := range map[int]uint32{inetForwarding: 1, inetAcceptRedirects: 0, inetSendRedirects: 0, inetReversePathFilter: 2, inetAcceptLocal: 1} {
		config.AddRtAttr(key, nl.Uint32Attr(value))
	}
	request.AddData(spec)
	_, err := request.Execute(unix.NETLINK_ROUTE, 0)
	return err
}

func (p *Linux) checkDownloadSpace(anchored bool) error {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		if err := checkDownloadRuleActions(family); err != nil {
			return err
		}
		rules, err := netlink.RuleList(family)
		if err != nil {
			return err
		}
		ownedCount := 0
		for _, rule := range rules {
			if rule.Priority == downPriority || rule.Table == downTable {
				ownedCount++
				if !anchored || ownedCount > 1 || !ownedDownloadRule(rule) {
					return fmt.Errorf("%w: native routing rule namespace is occupied", ErrOwnership)
				}
			} else if rule.Priority > 0 && rule.Priority < downPriority && (rule.IifName == "" || rule.IifName == downIn) {
				return fmt.Errorf("%w: a higher-priority rule can bypass native download routing", ErrOwnership)
			}
		}
		routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: downTable}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return err
		}
		for _, route := range routes {
			if !anchored || route.Protocol != downProtocol {
				return fmt.Errorf("%w: native routing table is occupied", ErrOwnership)
			}
			if route.Type == unix.RTN_UNREACHABLE && (route.Dst == nil || route.Dst.String() == "0.0.0.0/0" || route.Dst.String() == "::/0") {
				continue
			}
			link, err := netlink.LinkByIndex(route.LinkIndex)
			if err != nil {
				return err
			}
			if _, err := p.ownedDevice(link.Attrs().Name); err != nil {
				return err
			}
			if route.Dst == nil || route.Gw != nil || route.Type != unix.RTN_UNICAST {
				return ErrOwnership
			}
			prefix, err := netip.ParsePrefix(route.Dst.String())
			if err != nil || !prefix.IsSingleIP() {
				return ErrOwnership
			}
		}
	}
	return nil
}

func ownedDownloadRule(rule netlink.Rule) bool {
	return rule.Priority == downPriority && rule.Table == downTable && rule.IifName == downIn && rule.Protocol == downProtocol && rule.Mark == 0 && rule.Mask == nil && rule.Src == nil && rule.Dst == nil && rule.OifName == "" && !rule.Invert && rule.Tos == 0 && rule.TunID == 0 && rule.Goto == -1 && rule.Flow == -1 && rule.SuppressIfgroup == -1 && rule.SuppressPrefixlen == -1 && rule.Dport == nil && rule.Sport == nil && rule.IPProto == 0 && rule.UIDRange == nil
}

// netlink.RuleList omits the kernel action type, so validate that field from the dump.
func checkDownloadRuleActions(family int) error {
	request := nl.NewNetlinkRequest(unix.RTM_GETRULE, unix.NLM_F_DUMP|unix.NLM_F_REQUEST)
	request.AddData(nl.NewIfInfomsg(family))
	messages, err := request.Execute(unix.NETLINK_ROUTE, unix.RTM_NEWRULE)
	if err != nil {
		return err
	}
	for _, data := range messages {
		if len(data) < unix.SizeofRtMsg {
			return ErrOwnership
		}
		message := nl.DeserializeRtMsg(data)
		attrs, err := nl.ParseRouteAttr(data[message.Len():])
		if err != nil {
			return err
		}
		reserved := int(message.Table) == downTable
		for _, attr := range attrs {
			if attr.Attr.Type == nl.FRA_PRIORITY || attr.Attr.Type == nl.FRA_TABLE {
				if len(attr.Value) != 4 {
					return ErrOwnership
				}
				value := nl.NativeEndian().Uint32(attr.Value)
				reserved = reserved || attr.Attr.Type == nl.FRA_PRIORITY && value == downPriority || attr.Attr.Type == nl.FRA_TABLE && value == downTable
			}
		}
		if reserved && message.Type != unix.RTN_UNICAST {
			return ErrOwnership
		}
	}
	return nil
}

func (p *Linux) initializeDownload(ctx context.Context, anchored bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	out, outErr := p.downloadLink(downOut)
	in, inErr := p.downloadLink(downIn)
	if p.durableNetwork && outErr == nil && inErr == nil {
		anchored = true
	}
	if err := p.checkDownloadSpace(anchored); err != nil {
		return err
	}
	if err := p.checkDownloadAddress(p.network); err != nil {
		return err
	}
	if outErr != nil || inErr != nil {
		var missingOut, missingIn netlink.LinkNotFoundError
		if !errors.As(outErr, &missingOut) || !errors.As(inErr, &missingIn) {
			return ErrOwnership
		}
		attrs := netlink.NewLinkAttrs()
		attrs.Name = downOut
		attrs.Alias = p.downloadAlias(downOut)
		attrs.MTU = 65535
		if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: attrs, PeerName: downIn}); err != nil {
			return fmt.Errorf("create native download pair: %w", err)
		}
		created, err := netlink.LinkByName(downOut)
		if err != nil {
			return err
		}
		if err := netlink.LinkSetAlias(created, p.downloadAlias(downOut)); err != nil {
			return err
		}
		in, inErr = netlink.LinkByName(downIn)
		if inErr != nil {
			return inErr
		}
		if err := netlink.LinkSetAlias(in, p.downloadAlias(downIn)); err != nil {
			return err
		}
		out, outErr = p.downloadLink(downOut)
		if outErr != nil {
			return outErr
		}
		in, inErr = p.downloadLink(downIn)
		if inErr != nil {
			return inErr
		}
	}
	peer, err := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *out.Attrs()})
	if err != nil {
		return err
	}
	if peer != in.Attrs().Index {
		return fmt.Errorf("%w: download peer index %d want %d", ErrOwnership, peer, in.Attrs().Index)
	}
	if err := setDownloadIPv4(in); err != nil {
		return fmt.Errorf("configure native download IPv4 peer: %w", err)
	}
	if err := p.reconcileDownloadBinding(out, in); err != nil {
		return err
	}
	for _, link := range []netlink.Link{out, in} {
		if err := netlink.LinkSetIP6AddrGenMode(link, 1); err != nil && !errors.Is(err, unix.EAFNOSUPPORT) {
			return fmt.Errorf("disable native download address generation: %w", err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return err
		}
	}
	if _, err := p.sharedHTB(out); err != nil {
		return err
	}
	attrs := netlink.QdiscAttrs{LinkIndex: out.Attrs().Index, Handle: 0xffff0000, Parent: 0xfffffff1}
	if err := netlink.QdiscAdd(&netlink.Clsact{QdiscAttrs: attrs}); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	priority := uint32(0)
	reset := netlink.NewSkbEditAction()
	reset.Priority = &priority
	filter := &netlink.MatchAll{FilterAttrs: netlink.FilterAttrs{LinkIndex: out.Attrs().Index, Parent: egressParent, Priority: 1, Protocol: unix.ETH_P_ALL, Handle: 1}, Actions: []netlink.Action{reset}}
	filters, err := netlink.FilterList(out, egressParent)
	if err != nil {
		return err
	}
	for _, current := range filters {
		match, ok := current.(*netlink.MatchAll)
		if !ok || match.Handle != 1 || match.Priority != 1 || match.Protocol != unix.ETH_P_ALL || len(match.Actions) != 1 {
			return ErrOwnership
		}
		action, ok := match.Actions[0].(*netlink.SkbEditAction)
		if !ok || action.Priority == nil || *action.Priority != 0 || action.Mark != nil || action.Mask != nil || action.PType != nil || action.QueueMapping != nil || action.Action != netlink.TC_ACT_PIPE {
			return ErrOwnership
		}
	}
	if len(filters) == 0 {
		if err := netlink.FilterAdd(filter); err != nil {
			return fmt.Errorf("prepare native download priority reset: %w", err)
		}
	}
	if len(filters) > 1 {
		return ErrOwnership
	}
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := netlink.RuleList(family)
		if err != nil {
			return err
		}
		found := false
		for _, rule := range rules {
			if rule.Priority == downPriority {
				found = true
			}
		}
		if !found {
			rule := netlink.NewRule()
			rule.Family = family
			rule.Priority = downPriority
			rule.Table = downTable
			rule.Protocol = downProtocol
			rule.IifName = downIn
			if err := netlink.RuleAdd(rule); err != nil {
				return fmt.Errorf("install native download rule: %w", err)
			}
		}
		prefix := "0.0.0.0/0"
		if family == netlink.FAMILY_V6 {
			prefix = "::/0"
		}
		_, defaultRoute, _ := net.ParseCIDR(prefix)
		if err := netlink.RouteReplace(&netlink.Route{Family: family, Table: downTable, Dst: defaultRoute, Type: unix.RTN_UNREACHABLE, Protocol: downProtocol, Priority: 32767}); err != nil {
			return fmt.Errorf("install native download unreachable family %d: %w", family, err)
		}
		gateway := net.IP(p.network.GatewayIPv4.AsSlice())
		if family == netlink.FAMILY_V6 {
			gateway = net.ParseIP("fe80::2")
		}
		if err := netlink.NeighSet(&netlink.Neigh{LinkIndex: out.Attrs().Index, Family: family, IP: gateway, HardwareAddr: in.Attrs().HardwareAddr, State: netlink.NUD_PERMANENT}); err != nil {
			return fmt.Errorf("install native download neighbor IPv%d: %w", family, err)
		}
	}
	return nil
}

func (p *Linux) AddRoute(ctx context.Context, device string, destination netip.Prefix) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addDownloadRoute(ctx, device, destination)
}

func (p *Linux) addDownloadRoute(ctx context.Context, device string, destination netip.Prefix) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !destination.IsSingleIP() {
		return ErrAuthority
	}
	if err := ValidateIPv6Forwarding([]netip.Addr{destination.Addr()}); err != nil {
		return err
	}
	link, err := p.ownedDevice(device)
	if err != nil {
		return err
	}
	out, err := p.downloadLink(downOut)
	if err != nil {
		return err
	}
	family := netlink.FAMILY_V4
	gateway := net.IP(p.network.GatewayIPv4.AsSlice())
	version := 4
	if destination.Addr().Is6() {
		family = netlink.FAMILY_V6
		gateway = net.ParseIP("fe80::2")
		version = 6
	}
	_, network, err := net.ParseCIDR(destination.String())
	if err != nil {
		return err
	}
	addresses, err := netlink.AddrList(link, family)
	if err != nil {
		return err
	}
	var source net.IP
	for _, address := range addresses {
		if ip, ok := netip.AddrFromSlice(address.IP); ok && ip.Unmap().IsGlobalUnicast() {
			source = address.IP
			break
		}
	}
	if source == nil {
		return fmt.Errorf("%w: native tunnel has no source address", ErrAuthority)
	}
	routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: unix.RT_TABLE_MAIN, Dst: network}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.LinkIndex == out.Attrs().Index && route.Protocol == downProtocol && route.Gw.Equal(gateway) {
			continue
		}
		if route.LinkIndex != link.Attrs().Index || route.Gw != nil {
			return fmt.Errorf("%w: native client route conflicts with another owner", ErrOwnership)
		}
		if err := netlink.RouteDel(&route); err != nil {
			return err
		}
	}
	actual := &netlink.Route{Family: family, Table: downTable, Protocol: downProtocol, LinkIndex: link.Attrs().Index, Dst: network, Scope: netlink.SCOPE_LINK, Type: unix.RTN_UNICAST}
	if err := netlink.RouteReplace(actual); err != nil {
		return fmt.Errorf("install actual native route %s: %w", destination, err)
	}
	main := &netlink.Route{Family: family, Table: unix.RT_TABLE_MAIN, Protocol: downProtocol, LinkIndex: out.Attrs().Index, Dst: network, Gw: gateway, Src: source, Flags: unix.RTNH_F_ONLINK, MTU: link.Attrs().MTU, Priority: downTable}
	if err := netlink.RouteReplace(main); err != nil {
		return fmt.Errorf("install queued native route %s: %w", destination, err)
	}
	_, err = p.run(ctx, fmt.Sprintf("add element inet %s native_dest%d { %s }\n", table, version, destination.Addr()))
	return err
}

func (p *Linux) Routes(device string) ([]netip.Prefix, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	link, err := p.ownedDevice(device)
	if err != nil {
		return nil, err
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: downTable, LinkIndex: link.Attrs().Index}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_OIF)
	if err != nil {
		return nil, err
	}
	var result []netip.Prefix
	for _, route := range routes {
		if route.Protocol != downProtocol || route.Dst == nil {
			return nil, ErrOwnership
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil {
			return nil, err
		}
		result = append(result, prefix)
	}
	return result, nil
}

func (p *Linux) DeleteRoute(ctx context.Context, device string, destination netip.Prefix) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	link, err := p.ownedDevice(device)
	if err != nil {
		return err
	}
	_, network, err := net.ParseCIDR(destination.String())
	if err != nil {
		return err
	}
	family := netlink.FAMILY_V4
	if destination.Addr().Is6() {
		family = netlink.FAMILY_V6
	}
	actual, err := netlink.RouteListFiltered(family, &netlink.Route{Table: downTable, Dst: network}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST)
	if err != nil {
		return err
	}
	for _, route := range actual {
		if route.Protocol != downProtocol {
			return ErrOwnership
		}
		if route.LinkIndex != link.Attrs().Index {
			return nil
		}
		if err := netlink.RouteDel(&route); err != nil {
			return err
		}
	}
	out, err := p.downloadLink(downOut)
	if err != nil {
		return err
	}
	routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: unix.RT_TABLE_MAIN, Dst: network}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.LinkIndex != out.Attrs().Index || route.Protocol != downProtocol {
			return ErrOwnership
		}
		if err := netlink.RouteDel(&route); err != nil {
			return err
		}
	}
	return p.removeDownloadDestination(ctx, destination.Addr())
}

func (p *Linux) removeDownloadDestination(ctx context.Context, address netip.Addr) error {
	version := 4
	if address.Is6() {
		version = 6
	}
	_, err := p.run(ctx, fmt.Sprintf("add element inet %s native_dest%d { %s }\ndelete element inet %s native_dest%d { %s }\n", table, version, address, table, version, address))
	return err
}

func (p *Linux) PruneRoutes(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	out, err := p.downloadLink(downOut)
	if err != nil {
		return err
	}
	actual, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: downTable}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	present := make(map[string]bool)
	for _, route := range actual {
		if route.Dst != nil && route.Type == unix.RTN_UNICAST && route.Protocol == downProtocol {
			present[route.Dst.String()] = true
		}
	}
	queued, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: unix.RT_TABLE_MAIN, LinkIndex: out.Attrs().Index}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_OIF)
	if err != nil {
		return err
	}
	for _, route := range queued {
		if route.Protocol != downProtocol || route.Dst == nil {
			return ErrOwnership
		}
		if present[route.Dst.String()] {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil || !prefix.IsSingleIP() {
			return ErrOwnership
		}
		if err := netlink.RouteDel(&route); err != nil {
			return err
		}
		if err := p.removeDownloadDestination(ctx, prefix.Addr()); err != nil {
			return err
		}
	}
	for _, version := range []int{4, 6} {
		data, err := p.run(ctx, fmt.Sprintf("list set inet %s native_dest%d\n", table, version))
		if err != nil {
			return err
		}
		var inventory struct {
			Nftables []struct{ Set *struct{ Elem []netip.Addr } }
		}
		if err := json.Unmarshal(data, &inventory); err != nil {
			return err
		}
		for _, entry := range inventory.Nftables {
			if entry.Set == nil {
				continue
			}
			for _, address := range entry.Set.Elem {
				if !present[netip.PrefixFrom(address, address.BitLen()).String()] {
					if err := p.removeDownloadDestination(ctx, address); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
