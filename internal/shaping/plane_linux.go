//go:build linux

package shaping

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// kernelPlane drives tc through netlink. Measured, one class change costs 15us
// here and 3363-6301us through a fork of the tc binary, flat to 5000 classes.
type kernelPlane struct{}

func hostPlane() Plane { return kernelPlane{} }

// Probe is the read half only: a qdisc dump is unprivileged, so it proves the
// netlink socket and nothing about writing. Preflight proves the rest.
func (kernelPlane) Probe(context.Context) error {
	if _, err := netlink.LinkList(); err != nil {
		return classify(err)
	}
	return nil
}

func (kernelPlane) Links(context.Context) ([]string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, classify(err)
	}
	out := make([]string, 0, len(links))
	for _, link := range links {
		out = append(out, link.Attrs().Name)
	}
	return out, nil
}

func (kernelPlane) Snapshot(_ context.Context, device string) (Snapshot, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return Snapshot{}, classify(err)
	}
	snap := Snapshot{Links: make([]string, 0, len(links))}
	byIndex := make(map[int]string, len(links))
	var target netlink.Link
	for _, link := range links {
		attrs := link.Attrs()
		snap.Links = append(snap.Links, attrs.Name)
		byIndex[attrs.Index] = attrs.Name
		if attrs.Name == device {
			target = link
		}
	}
	if target == nil {
		return snap, nil
	}
	snap.Exists = true

	qdiscs, err := netlink.QdiscList(target)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	for _, qdisc := range qdiscs {
		attrs := qdisc.Attrs()
		spec := QdiscSpec{Device: device, Type: qdisc.Type(), Handle: attrs.Handle, Parent: attrs.Parent}
		if htb, ok := qdisc.(*netlink.Htb); ok {
			spec.Default = uint16(htb.Defcls)
		}
		snap.Qdiscs = append(snap.Qdiscs, spec)
	}

	// A parent of zero dumps every class on the device. HANDLE_ROOT does NOT: the
	// kernel matches its MAJOR against a qdisc handle, so 0xffff finds the clsact
	// and this panel's classes under major 1 come back as an empty list.
	classes, err := netlink.ClassList(target, netlink.HANDLE_NONE)
	if err != nil {
		return Snapshot{}, classify(err)
	}
	for _, class := range classes {
		htb, ok := class.(*netlink.HtbClass)
		if !ok {
			continue
		}
		// A class directly under the root qdisc reads back with parent TC_H_ROOT and
		// not with the qdisc's own handle, which is what an add sends. Normalised
		// here, or every class differs from what was pushed and is re-added forever.
		parent := htb.Parent
		if parent == rootParent {
			parent = rootHandle
		}
		snap.Classes = append(snap.Classes, ClassSpec{
			Device: device, Handle: htb.Handle, Parent: parent,
			RateBytesPerSec: htb.Rate, CeilBytesPerSec: htb.Ceil,
		})
	}

	for _, parent := range [...]uint32{rootHandle, ingressBlock} {
		filters, err := netlink.FilterList(target, parent)
		if err != nil {
			return Snapshot{}, classify(err)
		}
		for _, filter := range filters {
			if spec, ok := toFilterSpec(device, parent, filter, byIndex); ok {
				snap.Filters = append(snap.Filters, spec)
			}
		}
	}
	return snap, nil
}

/*
toFilterSpec normalises one flower rule into the panel's own vocabulary.

The decode hands back a raw net.IP that may be four bytes for v4 and a raw mask,
so both are converted here: a v4-mapped address compares unequal to the same
address parsed from text, and the diff would then delete and re-add every filter
on every pass.
*/
func toFilterSpec(device string, parent uint32, filter netlink.Filter, byIndex map[int]string) (FilterSpec, bool) {
	flower, ok := filter.(*netlink.Flower)
	if !ok {
		// A non-flower filter is somebody else's, and the manager decides what that
		// means from the priority alone: an unmatched shape must not be adopted.
		attrs := filter.Attrs()
		return FilterSpec{Device: device, Parent: parent, Priority: attrs.Priority, Handle: attrs.Handle}, true
	}
	spec := FilterSpec{
		Device: device, Parent: parent,
		Priority: flower.Priority, Handle: flower.Handle, ClassID: flower.ClassId,
	}
	switch {
	case flower.DestIP != nil:
		spec.Match = MatchDst
		spec.Prefix, ok = toPrefix(flower.DestIP, flower.DestIPMask)
	case flower.SrcIP != nil:
		spec.Match = MatchSrc
		spec.Prefix, ok = toPrefix(flower.SrcIP, flower.SrcIPMask)
	default:
		return spec, true
	}
	if !ok {
		return spec, true
	}
	for _, action := range flower.Actions {
		if mirred, isMirred := action.(*netlink.MirredAction); isMirred {
			spec.Redirect = byIndex[mirred.Ifindex]
		}
	}
	return spec, true
}

func toPrefix(ip net.IP, mask net.IPMask) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	bits := addr.BitLen()
	if mask != nil {
		if ones, _ := mask.Size(); ones > 0 {
			bits = ones
		}
	}
	return netip.PrefixFrom(addr, bits), true
}

// linkIndex resolves the device on every call. A core restart gives its device a
// new ifindex, so one cached from an earlier pass names nothing or names a stranger.
func linkIndex(device string) (int, error) {
	link, err := netlink.LinkByName(device)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return 0, fmt.Errorf("%w: %s", ErrNoDevice, device)
		}
		return 0, classify(err)
	}
	return link.Attrs().Index, nil
}

func (kernelPlane) EnsureIFB(_ context.Context, name string) error {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = name
	if err := netlink.LinkAdd(&netlink.Ifb{LinkAttrs: attrs}); err != nil && !errors.Is(err, syscall.EEXIST) {
		return classify(err)
	}
	// A mirror device that is down silently drops every redirected packet, so
	// creation and bringing up are one operation and never two.
	link, err := netlink.LinkByName(name)
	if err != nil {
		return classify(err)
	}
	return classify(netlink.LinkSetUp(link))
}

func (kernelPlane) DeleteIFB(_ context.Context, name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return fmt.Errorf("%w: %s", ErrNotInstalled, name)
		}
		return classify(err)
	}
	return classify(netlink.LinkDel(link))
}

func toQdisc(spec QdiscSpec, index int) (netlink.Qdisc, error) {
	attrs := netlink.QdiscAttrs{LinkIndex: index, Handle: spec.Handle, Parent: spec.Parent}
	switch spec.Type {
	case QdiscHTB:
		htb := netlink.NewHtb(attrs)
		htb.Defcls = uint32(spec.Default)
		return htb, nil
	case QdiscFqCodel:
		return &netlink.FqCodel{QdiscAttrs: attrs}, nil
	case QdiscClsact:
		return &netlink.Clsact{QdiscAttrs: attrs}, nil
	}
	return nil, fmt.Errorf("shaping: %q is not a qdisc this panel installs", spec.Type)
}

func (kernelPlane) AddQdisc(_ context.Context, spec QdiscSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	qdisc, err := toQdisc(spec, index)
	if err != nil {
		return err
	}
	return classify(netlink.QdiscAdd(qdisc))
}

func (kernelPlane) DelQdisc(_ context.Context, spec QdiscSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	qdisc, err := toQdisc(spec, index)
	if err != nil {
		return err
	}
	return classify(netlink.QdiscDel(qdisc))
}

/*
toClass fills the burst budget the kernel would otherwise leave at zero.

netlink serialises Buffer verbatim, and NewHtbClass — the helper that computes it
— takes bits per second while everything here is already canonicalised to bytes.
A zero buffer gives HTB no burst allowance at all and collapses its accuracy, and
the quantum is set for the same reason: a 10 Gbit default class otherwise makes
the kernel log "quantum of class 1FFFF is big" on every start.
*/
func toClass(spec ClassSpec, index int) *netlink.HtbClass {
	const mtu = 1600
	buffer := netlink.Xmittime(spec.RateBytesPerSec, uint32(float64(spec.RateBytesPerSec)/netlink.Hz()+mtu))
	cbuffer := netlink.Xmittime(spec.CeilBytesPerSec, uint32(float64(spec.CeilBytesPerSec)/netlink.Hz()+mtu))
	return &netlink.HtbClass{
		ClassAttrs: netlink.ClassAttrs{LinkIndex: index, Handle: spec.Handle, Parent: spec.Parent},
		Rate:       spec.RateBytesPerSec,
		Ceil:       spec.CeilBytesPerSec,
		Buffer:     buffer,
		Cbuffer:    cbuffer,
		Quantum:    quantumFor(spec.RateBytesPerSec),
	}
}

func (kernelPlane) AddClass(_ context.Context, spec ClassSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	return classify(netlink.ClassAdd(toClass(spec, index)))
}

func (kernelPlane) ChangeClass(_ context.Context, spec ClassSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	return classify(netlink.ClassChange(toClass(spec, index)))
}

func (kernelPlane) DelClass(_ context.Context, spec ClassSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	return classify(netlink.ClassDel(toClass(spec, index)))
}

func toFilter(spec FilterSpec, index int) (*netlink.Flower, error) {
	ethType := uint16(unix.ETH_P_IP)
	if spec.Prefix.Addr().Is6() {
		ethType = unix.ETH_P_IPV6
	}
	flower := &netlink.Flower{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: index, Parent: spec.Parent, Handle: spec.Handle,
			Priority: spec.Priority, Protocol: ethType,
		},
		EthType: ethType,
		ClassId: spec.ClassID,
	}
	ip, mask := spec.Prefix.Addr().AsSlice(), net.CIDRMask(spec.Prefix.Bits(), spec.Prefix.Addr().BitLen())
	if spec.Match == MatchSrc {
		flower.SrcIP, flower.SrcIPMask = ip, mask
	} else {
		flower.DestIP, flower.DestIPMask = ip, mask
	}
	if spec.Redirect != "" {
		target, err := linkIndex(spec.Redirect)
		if err != nil {
			return nil, err
		}
		flower.Actions = []netlink.Action{netlink.NewMirredAction(target)}
	}
	return flower, nil
}

func (kernelPlane) AddFilter(_ context.Context, spec FilterSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	filter, err := toFilter(spec, index)
	if err != nil {
		return err
	}
	if err := netlink.FilterAdd(filter); err != nil {
		// Measured: EINVAL here is a priority another protocol already holds, which
		// is why v4 and v6 sit at their own priorities and never share one.
		if errors.Is(err, syscall.EINVAL) {
			return fmt.Errorf("%w: %s at prio %d: %w", ErrPriorityInUse, spec.Device, spec.Priority, err)
		}
		return classify(err)
	}
	return nil
}

func (kernelPlane) DelFilter(_ context.Context, spec FilterSpec) error {
	index, err := linkIndex(spec.Device)
	if err != nil {
		return err
	}
	filter, err := toFilter(spec, index)
	if err != nil {
		return err
	}
	return classify(netlink.FilterDel(filter))
}

// classify maps a syscall failure onto the sentinel the manager acts on. The
// errnos are the ones this mechanism was measured to answer with on 6.8.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EEXIST):
		return fmt.Errorf("%w: %w", ErrAlreadyInstalled, err)
	case errors.Is(err, syscall.EBUSY):
		return fmt.Errorf("%w: %w", ErrInUse, err)
	case errors.Is(err, syscall.ESRCH), errors.Is(err, syscall.ENOENT), errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %w", ErrNotInstalled, err)
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %w", ErrPermission, err)
	case errors.Is(err, syscall.ENODEV), errors.Is(err, syscall.ENXIO):
		return fmt.Errorf("%w: %w", ErrNoDevice, err)
	}
	return err
}
