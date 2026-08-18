//go:build linux

package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// kernelPlane drives the host through netlink. Every operation is one syscall
// round trip, so the manager keeps no cached view of the result.
//
// Parameterised by family because a second module -- AmneziaWG -- creates a
// different link type and answers on a different generic netlink family, while
// addresses, routes and link state are identical for both. Duplicating those
// would mean two copies of the code that decides whether a device is ours.
type kernelPlane struct {
	linkType string
	devices  DeviceDriver
}

func hostPlane() Plane { return kernelPlane{linkType: wireguardLinkType, devices: wgctrlDriver{}} }

/*
DeviceDriver is the half of a Plane that speaks a module's own netlink family:
reading a device and configuring one. Everything else a Plane does is family
agnostic.
*/
type DeviceDriver interface {
	// Probe reports whether this module is usable on this host at all.
	Probe() error
	Device(name string) (*wgtypes.Device, error)
	ConfigureDevice(name string, cfg wgtypes.Config) error
}

// NewKernelPlane returns a Plane for a module that creates linkType and answers
// through devices. Exported so a core outside this package can reuse the
// address, route and link handling without reimplementing it.
func NewKernelPlane(linkType string, devices DeviceDriver) Plane {
	return kernelPlane{linkType: linkType, devices: devices}
}

// wgctrlDriver is kernel WireGuard's own driver.
type wgctrlDriver struct{}

func (wgctrlDriver) Probe() error {
	return probeKernel(func() error {
		client, err := wgctrl.New()
		if err != nil {
			return classify(err)
		}
		return classify(client.Close())
	}, func() bool {
		_, err := os.Stat(kernelModulePath)
		return err == nil
	})
}

func (wgctrlDriver) Device(name string) (*wgtypes.Device, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Device(name)
}

func (wgctrlDriver) ConfigureDevice(name string, cfg wgtypes.Config) error {
	client, err := wgctrl.New()
	if err != nil {
		return err
	}
	defer client.Close()
	return client.ConfigureDevice(name, cfg)
}

func (p kernelPlane) Probe(context.Context) error { return p.devices.Probe() }

func (p kernelPlane) Snapshot(_ context.Context, name string) (Snapshot, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return Snapshot{}, nil
		}
		return Snapshot{}, classify(err)
	}
	if link.Type() != p.linkType {
		return Snapshot{}, fmt.Errorf("%w: %s is a %q link", ErrNotWireguardLink, name, link.Type())
	}
	attrs := link.Attrs()
	snap := Snapshot{
		Exists: true,
		Link:   LinkState{Index: attrs.Index, MTU: attrs.MTU, Up: attrs.Flags&net.FlagUp != 0},
	}

	device, err := p.devices.Device(name)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	snap.Device = *device

	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	for _, a := range addrs {
		if prefix, ok := toPrefix(a.IPNet); ok {
			snap.Addrs = append(snap.Addrs, prefix)
		}
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	for _, r := range routes {
		if prefix, ok := toPrefix(r.Dst); ok {
			snap.Routes = append(snap.Routes, prefix)
		}
	}
	return snap, nil
}

const wireguardLinkType = "wireguard"

func (p kernelPlane) Links(context.Context) ([]string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, classify(err)
	}
	out := make([]string, 0, len(links))
	for _, l := range links {
		if l.Type() == p.linkType {
			out = append(out, l.Attrs().Name)
		}
	}
	return out, nil
}

func (p kernelPlane) EnsureLink(_ context.Context, spec LinkSpec) (LinkState, error) {
	link, err := netlink.LinkByName(spec.Name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if !errors.As(err, &missing) {
			return LinkState{}, classify(err)
		}
		attrs := netlink.NewLinkAttrs()
		attrs.Name = spec.Name
		// An MTU of zero is left out so the kernel default applies rather than a
		// zero-byte interface being requested.
		if spec.MTU > 0 {
			attrs.MTU = spec.MTU
		}
		if addErr := netlink.LinkAdd(p.newLink(attrs)); addErr != nil {
			return LinkState{}, classify(addErr)
		}
		link, err = netlink.LinkByName(spec.Name)
		if err != nil {
			return LinkState{}, classify(err)
		}
		if upErr := netlink.LinkSetUp(link); upErr != nil {
			return LinkState{}, classify(upErr)
		}
		attrsNow := link.Attrs()
		return LinkState{Index: attrsNow.Index, MTU: attrsNow.MTU, Up: true, Created: true}, nil
	}
	if link.Type() != p.linkType {
		return LinkState{}, fmt.Errorf("%w: %s is a %q link", ErrNotWireguardLink, spec.Name, link.Type())
	}
	attrs := link.Attrs()
	if spec.MTU > 0 && attrs.MTU != spec.MTU {
		if mtuErr := netlink.LinkSetMTU(link, spec.MTU); mtuErr != nil {
			return LinkState{}, classify(mtuErr)
		}
		attrs.MTU = spec.MTU
	}
	up := attrs.Flags&net.FlagUp != 0
	if !up {
		if upErr := netlink.LinkSetUp(link); upErr != nil {
			return LinkState{}, classify(upErr)
		}
		up = true
	}
	return LinkState{Index: attrs.Index, MTU: attrs.MTU, Up: up}, nil
}

func (p kernelPlane) DeleteLink(_ context.Context, name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return nil
		}
		return classify(err)
	}
	return classify(netlink.LinkDel(link))
}

func (p kernelPlane) Configure(_ context.Context, name string, cfg wgtypes.Config) error {
	return classify(p.devices.ConfigureDevice(name, cfg))
}

func (p kernelPlane) AddAddr(_ context.Context, name string, addr netip.Prefix) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return classify(err)
	}
	ipnet := toIPNet(addr)
	return classify(netlink.AddrAdd(link, &netlink.Addr{IPNet: &ipnet}))
}

func (p kernelPlane) DelAddr(_ context.Context, name string, addr netip.Prefix) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return classify(err)
	}
	ipnet := toIPNet(addr)
	return classify(netlink.AddrDel(link, &netlink.Addr{IPNet: &ipnet}))
}

func (p kernelPlane) AddRoute(_ context.Context, name string, dst netip.Prefix) error {
	route, err := peerRoute(name, dst)
	if err != nil {
		return err
	}
	return classify(netlink.RouteAdd(route))
}

func (p kernelPlane) DelRoute(_ context.Context, name string, dst netip.Prefix) error {
	route, err := peerRoute(name, dst)
	if err != nil {
		return err
	}
	return classify(netlink.RouteDel(route))
}

// peerRoute is the on-link route wg-quick installs for an AllowedIPs prefix the
// device's own addresses do not already cover.
func peerRoute(name string, dst netip.Prefix) (*netlink.Route, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, classify(err)
	}
	ipnet := toIPNet(dst)
	return &netlink.Route{LinkIndex: link.Attrs().Index, Dst: &ipnet, Scope: netlink.SCOPE_LINK}, nil
}

// classify maps a syscall failure onto the sentinel an operator can act on, so a
// raw errno never reaches the panel's error banner.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w: %w", ErrPermission, err)
	case errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.EAFNOSUPPORT):
		return fmt.Errorf("%w: %w", ErrNoKernelSupport, err)
	case errors.Is(err, syscall.ENODEV), errors.Is(err, syscall.ENXIO), errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %w", ErrNoDevice, err)
	}
	return err
}

// newLink builds the link this plane's family creates. WireGuard has a typed
// netlink struct; anything else is a generic link named by its kind.
func (p kernelPlane) newLink(attrs netlink.LinkAttrs) netlink.Link {
	if p.linkType == wireguardLinkType {
		return &netlink.Wireguard{LinkAttrs: attrs}
	}
	return &netlink.GenericLink{LinkAttrs: attrs, LinkType: p.linkType}
}
