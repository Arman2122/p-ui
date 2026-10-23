//go:build linux

package nativeruntime

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/native"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

type guardedPlane struct {
	engine.Plane
	gate       *native.Linux
	owner      string
	management credentials.Config
	kind       string
	prefix     string
}

type wireguardDriver struct{}

func (wireguardDriver) Probe() error {
	c, err := wgctrl.New()
	if err != nil {
		return err
	}
	return c.Close()
}

func (wireguardDriver) Device(name string) (*wgtypes.Device, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Device(name)
}

func (wireguardDriver) ConfigureDevice(name string, config wgtypes.Config) error {
	c, err := wgctrl.New()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.ConfigureDevice(name, config)
}

func (p *guardedPlane) owned(name string) (netlink.Link, error) {
	if !strings.HasPrefix(name, p.prefix) {
		return nil, native.ErrOwnership
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	if link.Type() != p.kind || link.Attrs().Alias != p.owner {
		return nil, native.ErrOwnership
	}
	return link, nil
}

func (p *guardedPlane) Snapshot(ctx context.Context, name string) (engine.Snapshot, error) {
	if _, err := p.owned(name); err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return engine.Snapshot{}, nil
		}
		return engine.Snapshot{}, err
	}
	snapshot, err := p.Plane.Snapshot(ctx, name)
	if err != nil {
		return snapshot, err
	}
	snapshot.Routes, err = p.gate.Routes(name)
	return snapshot, err
}

func (p *guardedPlane) Links(context.Context) ([]string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, link := range links {
		if strings.HasPrefix(link.Attrs().Name, p.prefix) && link.Type() == p.kind && link.Attrs().Alias == p.owner {
			names = append(names, link.Attrs().Name)
		}
	}
	return names, nil
}

func (p *guardedPlane) EnsureLink(ctx context.Context, spec engine.LinkSpec) (engine.LinkState, error) {
	_, err := p.owned(spec.Name)
	created := false
	if err != nil {
		var missing netlink.LinkNotFoundError
		if !errors.As(err, &missing) {
			return engine.LinkState{}, err
		}
		attrs := netlink.NewLinkAttrs()
		attrs.Name = spec.Name
		attrs.MTU = spec.MTU
		if err := netlink.LinkAdd(&netlink.GenericLink{LinkAttrs: attrs, LinkType: p.kind}); err != nil {
			return engine.LinkState{}, err
		}
		link, err := netlink.LinkByName(spec.Name)
		if err != nil {
			return engine.LinkState{}, err
		}
		if err := netlink.LinkSetAlias(link, p.owner); err != nil {
			return engine.LinkState{}, err
		}
		created = true
	}
	if err := p.gate.Guard(ctx, []string{spec.Name}); err != nil {
		return engine.LinkState{}, err
	}
	state, err := p.Plane.EnsureLink(ctx, spec)
	state.Created = state.Created || created
	return state, err
}

func (p *guardedPlane) DeleteLink(ctx context.Context, name string) error {
	if _, err := p.owned(name); err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return nil
		}
		return err
	}
	routes, err := p.gate.Routes(name)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := p.gate.DeleteRoute(ctx, name, route); err != nil {
			return err
		}
	}
	return p.Plane.DeleteLink(ctx, name)
}

func (p *guardedPlane) Configure(ctx context.Context, name string, config wgtypes.Config) error {
	if _, err := p.owned(name); err != nil {
		return err
	}
	return p.Plane.Configure(ctx, name, config)
}

func (p *guardedPlane) AddAddr(ctx context.Context, name string, ip netip.Prefix) error {
	if _, err := p.owned(name); err != nil {
		return err
	}
	return p.Plane.AddAddr(ctx, name, ip)
}

func (p *guardedPlane) DelAddr(ctx context.Context, name string, ip netip.Prefix) error {
	if _, err := p.owned(name); err != nil {
		return err
	}
	return p.Plane.DelAddr(ctx, name, ip)
}

func (p *guardedPlane) AddRoute(ctx context.Context, name string, ip netip.Prefix) error {
	if _, err := p.owned(name); err != nil {
		return err
	}
	return p.gate.AddRoute(ctx, name, ip)
}

func (p *guardedPlane) DelRoute(ctx context.Context, name string, ip netip.Prefix) error {
	if _, err := p.owned(name); err != nil {
		return err
	}
	return p.gate.DeleteRoute(ctx, name, ip)
}

func (p *guardedPlane) preflight(instances []engine.Instance) error {
	var wanted []netip.Addr
	for _, instance := range instances {
		if _, err := p.owned(p.prefix + strconv.Itoa(instance.ID)); err != nil {
			var missing netlink.LinkNotFoundError
			if !errors.As(err, &missing) {
				return err
			}
		}
		for _, address := range instance.Address {
			prefix, _ := netip.ParsePrefix(address)
			wanted = append(wanted, prefix.Addr())
		}
		for _, peer := range instance.Peers {
			for _, address := range peer.AllowedIPs {
				prefix, _ := netip.ParsePrefix(address)
				wanted = append(wanted, prefix.Addr())
			}
		}
	}
	return p.preflightAddresses(wanted)
}

func (p *guardedPlane) preflightAddresses(wanted []netip.Addr) error {
	if err := native.ValidateIPv6Forwarding(wanted); err != nil {
		return err
	}
	protected, err := p.managementAddresses()
	if err != nil {
		return err
	}
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	owned := make(map[int]bool)
	for _, link := range links {
		name := link.Attrs().Name
		if p.gate.OwnsDownloadLink(link) || (link.Type() == "wireguard" && strings.HasPrefix(name, "phvw") || link.Type() == "amneziawg" && strings.HasPrefix(name, "phva") || link.Type() == "tuntap" && (strings.HasPrefix(name, "phvo") || strings.HasPrefix(name, "phvc"))) && link.Attrs().Alias == p.owner {
			owned[link.Attrs().Index] = true
		}
	}
	for _, ip := range wanted {
		if protected[ip] {
			return native.ErrOwnership
		}
	}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if owned[route.LinkIndex] || route.Dst == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil || prefix.Bits() == 0 {
			continue
		}
		for _, ip := range wanted {
			if prefix.Contains(ip) {
				return native.ErrOwnership
			}
		}
	}
	for _, link := range links {
		if owned[link.Attrs().Index] {
			continue
		}
		addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			ip, ok := netip.AddrFromSlice(address.IP)
			if !ok {
				continue
			}
			for _, target := range wanted {
				if target == ip.Unmap() {
					return native.ErrOwnership
				}
			}
		}
	}
	return nil
}

func (p *guardedPlane) managementAddresses() (map[netip.Addr]bool, error) {
	control, _, err := net.SplitHostPort(p.management.ControlAddress)
	if err != nil || control == "" {
		return nil, native.ErrOwnership
	}
	bootstrap, err := url.Parse(p.management.EnrollmentURL)
	if err != nil || bootstrap.Scheme != "https" || bootstrap.Hostname() == "" {
		return nil, native.ErrOwnership
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	protected := make(map[netip.Addr]bool)
	for _, host := range []string{control, bootstrap.Hostname()} {
		if ip, err := netip.ParseAddr(host); err == nil {
			protected[ip.Unmap()] = true
			continue
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, native.ErrOwnership
		}
		for _, ip := range addresses {
			protected[ip.Unmap()] = true
		}
	}
	return protected, nil
}
