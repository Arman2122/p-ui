//go:build linux

package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// kernelPlane drives the host through netlink. Every operation is one syscall
// round trip, so the manager keeps no cached view of the result.
type kernelPlane struct{}

func hostPlane() Plane { return kernelPlane{} }

func netlinkFamily(f Family) int {
	if f == FamilyV6 {
		return netlink.FAMILY_V6
	}
	return netlink.FAMILY_V4
}

// Probe is the read half only: RuleList is unprivileged, so it proves the
// netlink socket and nothing about writing. probeHost proves the rest.
func (kernelPlane) Probe(context.Context) error {
	if _, err := netlink.RuleList(netlink.FAMILY_V4); err != nil {
		return classify(err)
	}
	return nil
}

func (kernelPlane) Snapshot(context.Context) (Snapshot, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return Snapshot{}, classify(err)
	}
	snap := Snapshot{Links: make([]string, 0, len(links))}
	byIndex := make(map[int]string, len(links))
	for _, l := range links {
		attrs := l.Attrs()
		snap.Links = append(snap.Links, attrs.Name)
		byIndex[attrs.Index] = attrs.Name
	}

	addrs, err := netlink.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	for _, a := range addrs {
		if prefix, ok := toPrefix(a.IPNet); ok {
			snap.Addrs = append(snap.Addrs, AddrSpec{Prefix: prefix, Device: byIndex[a.LinkIndex]})
		}
	}

	for _, family := range Families {
		rules, err := netlink.RuleList(netlinkFamily(family))
		if err != nil {
			return Snapshot{}, classify(err)
		}
		for _, r := range rules {
			if _, mine := prioEgressID(r.Priority); !mine {
				continue
			}
			snap.Rules = append(snap.Rules, RuleSpec{Family: family, Priority: r.Priority, Iif: r.IifName, Table: r.Table})
		}

		// RT_TABLE_UNSPEC with the table filter set is what makes the dump cover
		// every table: without the filter mask the library drops all but main.
		routes, err := netlink.RouteListFiltered(netlinkFamily(family),
			&netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return Snapshot{}, classify(err)
		}
		for _, r := range routes {
			if _, mine := tableEgressID(r.Table); !mine {
				continue
			}
			snap.Routes = append(snap.Routes, toRouteSpec(family, r, byIndex))
		}
	}
	return snap, nil
}

// toRouteSpec normalises one kernel route. A blackhole's link index is not its
// device — v6 reports lo — so only a unicast route is credited with one.
func toRouteSpec(family Family, r netlink.Route, byIndex map[int]string) RouteSpec {
	spec := RouteSpec{Family: family, Table: r.Table, Dst: family.DefaultRoute(), Metric: r.Priority}
	if prefix, ok := toPrefix(r.Dst); ok {
		spec.Dst = prefix
	}
	switch r.Type {
	case unix.RTN_UNICAST:
		spec.Type = RouteUnicast
		spec.Device = byIndex[r.LinkIndex]
	case unix.RTN_BLACKHOLE:
		spec.Type = RouteBlackhole
	default:
		spec.Type = RouteOther
		spec.Device = byIndex[r.LinkIndex]
	}
	return spec
}

func toPrefix(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := n.Mask.Size()
	return netip.PrefixFrom(addr.Unmap(), ones), true
}

func toIPNet(p netip.Prefix) *net.IPNet {
	return &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}
}

func toNetlinkRule(spec RuleSpec) *netlink.Rule {
	rule := netlink.NewRule()
	rule.Family = netlinkFamily(spec.Family)
	rule.Priority = spec.Priority
	rule.IifName = spec.Iif
	rule.Table = spec.Table
	return rule
}

func (kernelPlane) AddRule(_ context.Context, spec RuleSpec) error {
	return classify(netlink.RuleAdd(toNetlinkRule(spec)))
}

func (kernelPlane) DelRule(_ context.Context, spec RuleSpec) error {
	return classify(netlink.RuleDel(toNetlinkRule(spec)))
}

// toNetlinkRoute builds the route the kernel matches on. Measured: iproute2 gives a
// v4 device route SCOPE_LINK, and a SCOPE_UNIVERSE delete then answers ESRCH.
func toNetlinkRoute(spec RouteSpec, index int) *netlink.Route {
	dst := spec.Dst
	if !dst.IsValid() {
		dst = spec.Family.DefaultRoute()
	}
	route := &netlink.Route{Table: spec.Table, Dst: toIPNet(dst), Priority: spec.Metric}
	if spec.Type == RouteBlackhole {
		route.Type = unix.RTN_BLACKHOLE
		return route
	}
	route.LinkIndex = index
	if spec.Family == FamilyV4 {
		route.Scope = netlink.SCOPE_LINK
	}
	return route
}

func (kernelPlane) AddRoute(_ context.Context, spec RouteSpec) error {
	index, err := routeLinkIndex(spec)
	if err != nil {
		return err
	}
	if err := netlink.RouteAdd(toNetlinkRoute(spec, index)); err != nil {
		return classifyRouteAdd(spec, err)
	}
	return nil
}

/*
classifyRouteAdd separates a family switched off on the front device from a host
that will not let the panel write routes at all.

Measured on 6.8.0-111: a v6 unicast route through a device with disable_ipv6=1 is
refused with EACCES, which classify would otherwise read as a missing
CAP_NET_ADMIN and report to an operator already running as root. The v6 rule and
the v6 blackhole install regardless, so the flow stays contained.
*/
func classifyRouteAdd(spec RouteSpec, err error) error {
	if spec.Family == FamilyV6 && spec.Type == RouteUnicast && ipv6DisabledOn(spec.Device) {
		return fmt.Errorf("%w: %s has net.ipv6.conf.%s.disable_ipv6=1", ErrFamilyDisabled, spec.Device, spec.Device)
	}
	return classify(err)
}

// ipv6DisabledOn reads the per-device switch the kernel consults before it
// answers EACCES. An unreadable knob is not a claim in either direction.
func ipv6DisabledOn(device string) bool {
	if device == "" {
		return false
	}
	raw, err := os.ReadFile(sysctlPath("net.ipv6.conf." + device + ".disable_ipv6"))
	return err == nil && strings.TrimSpace(string(raw)) == "1"
}

func (kernelPlane) DelRoute(_ context.Context, spec RouteSpec) error {
	index, err := routeLinkIndex(spec)
	if err != nil {
		return err
	}
	return classify(netlink.RouteDel(toNetlinkRoute(spec, index)))
}

// routeLinkIndex resolves the device on every call. An Xray restart gives the
// front a new ifindex, so an index cached from an earlier pass names nothing.
func routeLinkIndex(spec RouteSpec) (int, error) {
	if spec.Type == RouteBlackhole || spec.Device == "" {
		return 0, nil
	}
	link, err := netlink.LinkByName(spec.Device)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return 0, fmt.Errorf("%w: %s", ErrNoDevice, spec.Device)
		}
		return 0, classify(err)
	}
	return link.Attrs().Index, nil
}

func sysctlPath(key string) string {
	return filepath.Join("/proc/sys", filepath.Join(strings.Split(key, ".")...))
}

func (kernelPlane) Sysctl(_ context.Context, key string) (string, error) {
	raw, err := os.ReadFile(sysctlPath(key))
	if err != nil {
		return "", classifySysctl(err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (kernelPlane) SetSysctl(_ context.Context, key, value string) error {
	return classifySysctl(os.WriteFile(sysctlPath(key), []byte(value+"\n"), 0o644))
}

// classifySysctl reads an absent key as an absent device: the per-device knobs
// under /proc/sys/net/ipv4/conf only exist for as long as the device does.
func classifySysctl(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrNoDevice, err)
	}
	return classify(err)
}

// classify maps a syscall failure onto the sentinel the manager acts on. The errnos
// are measured: EEXIST on a duplicate, ENOENT on a rule, ESRCH on a route.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EEXIST):
		return fmt.Errorf("%w: %w", ErrAlreadyInstalled, err)
	case errors.Is(err, syscall.ESRCH), errors.Is(err, syscall.ENOENT), errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %w", ErrNotInstalled, err)
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %w", ErrPermission, err)
	case errors.Is(err, syscall.ENODEV), errors.Is(err, syscall.ENXIO):
		return fmt.Errorf("%w: %w", ErrNoDevice, err)
	// A kernel booted with ipv6.disable=1 registers no fib rules for the family,
	// so every object in it answers this and none of them will ever install.
	case errors.Is(err, syscall.EAFNOSUPPORT), errors.Is(err, syscall.EOPNOTSUPP):
		return fmt.Errorf("%w: %w", ErrFamilyUnsupported, err)
	}
	return err
}
