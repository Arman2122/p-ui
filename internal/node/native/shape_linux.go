//go:build linux

package native

import (
	"errors"
	"fmt"
	"math"
	"net"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

const (
	classRoot     uint32 = 0x10000
	ingressParent uint32 = 0xfffffff2
	egressParent  uint32 = 0xfffffff3
)

func (p *Linux) sharedIFB(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if !errors.As(err, &missing) {
			return nil, err
		}
		attrs := netlink.NewLinkAttrs()
		attrs.Name, attrs.Alias, attrs.MTU = name, p.owner, 65536
		if err := netlink.LinkAdd(&netlink.Ifb{LinkAttrs: attrs}); err != nil {
			return nil, err
		}
		link, err = netlink.LinkByName(name)
		if err != nil {
			return nil, err
		}
		if err := netlink.LinkSetAlias(link, p.owner); err != nil {
			return nil, err
		}
		link.Attrs().Alias = p.owner
	}
	if link.Type() != "ifb" || link.Attrs().Alias != p.owner {
		return nil, fmt.Errorf("%w: %s type=%s alias=%q", ErrOwnership, name, link.Type(), link.Attrs().Alias)
	}
	return p.sharedHTB(link)
}

func (p *Linux) sharedHTB(link netlink.Link) (netlink.Link, error) {
	name := link.Attrs().Name
	qdiscs, err := netlink.QdiscList(link)
	if err != nil {
		return nil, err
	}
	found := false
	for _, qdisc := range qdiscs {
		if qdisc.Attrs().Handle == 0 || qdisc.Attrs().Parent != netlink.HANDLE_ROOT {
			continue
		}
		if qdisc.Type() != "htb" || qdisc.Attrs().Handle != classRoot {
			return nil, fmt.Errorf("%w: %s queue=%s handle=%x", ErrOwnership, name, qdisc.Type(), qdisc.Attrs().Handle)
		}
		found = true
	}
	if !found {
		qdisc := netlink.NewHtb(netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Handle: classRoot, Parent: netlink.HANDLE_ROOT})
		if err := netlink.QdiscAdd(qdisc); err != nil {
			return nil, err
		}
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, err
	}
	return link, nil
}

func (p *Linux) shape(lease Lease) error {
	return p.shapeWithClass(lease, true)
}

func (p *Linux) shapeWithClass(lease Lease, changeClass bool) error {
	return p.shapeClass(lease, changeClass, false)
}

func (p *Linux) shapeClass(lease Lease, changeClass, requireClass bool) error {
	for direction, rate := range []limits.Rate{lease.Grant.Policy.Upload, lease.Grant.Policy.Download} {
		name := "phvup"
		if direction == 1 {
			name = downOut
		}
		var ifb netlink.Link
		var err error
		if rate.Mode == limits.Limited {
			if direction == 0 {
				ifb, err = p.sharedIFB(name)
			} else {
				ifb, err = p.downloadLink(name)
				if err == nil {
					ifb, err = p.sharedHTB(ifb)
				}
			}
			if err != nil {
				return fmt.Errorf("prepare %s: %w", name, err)
			}
			bytesPerSecond := rate.BitsPerSecond / 8
			microseconds := 1_000_000 * float64(rate.BurstBytes) / float64(bytesPerSecond)
			if microseconds*netlink.TickInUsec() > math.MaxUint32 {
				return ErrAuthority
			}
			buffer := netlink.Xmittime(bytesPerSecond, uint32(rate.BurstBytes))
			class := &netlink.HtbClass{
				ClassAttrs: netlink.ClassAttrs{LinkIndex: ifb.Attrs().Index, Handle: classRoot | uint32(lease.Class), Parent: classRoot},
				Rate:       bytesPerSecond, Ceil: bytesPerSecond, Buffer: buffer, Cbuffer: buffer, Quantum: 1514,
			}
			classes, err := netlink.ClassList(ifb, netlink.HANDLE_NONE)
			if err != nil {
				return fmt.Errorf("list %s classes: %w", name, err)
			}
			exists := false
			for _, current := range classes {
				if current.Attrs().Handle == class.Handle {
					exists = true
				}
			}
			if requireClass && !exists {
				return ErrMissing
			}
			if !changeClass {
				if !exists {
					return ErrMissing
				}
			} else if exists {
				err = netlink.ClassChange(class)
			} else {
				err = netlink.ClassAdd(class)
			}
			if err != nil {
				return fmt.Errorf("set %s class %d: %w", name, lease.Class, err)
			}
		}
		for i, selector := range lease.Selectors {
			if direction == 1 {
				link, err := p.downloadLink(downOut)
				if err != nil {
					return err
				}
				protocol, priority := uint16(unix.ETH_P_IP), uint16(10)
				if selector.Address.Is6() {
					protocol, priority = unix.ETH_P_IPV6, 11
				}
				filter := &netlink.Flower{FilterAttrs: netlink.FilterAttrs{LinkIndex: link.Attrs().Index, Parent: classRoot, Priority: priority, Protocol: protocol, Handle: uint32(lease.Class)<<16 | uint32(i+1)}, ClassId: classRoot | uint32(lease.Class), EthType: protocol, DestIP: net.IP(selector.Address.AsSlice()), DestIPMask: net.CIDRMask(selector.Address.BitLen(), selector.Address.BitLen())}
				if rate.Mode == limits.Limited {
					if err := netlink.FilterReplace(filter); err != nil {
						return err
					}
				} else if err := netlink.FilterDel(filter); err != nil && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ESRCH) {
					return err
				}
				continue
			}
			link, err := p.ownedDevice(selector.Device)
			if err != nil {
				return err
			}
			attrs := netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Handle: 0xffff0000, Parent: 0xfffffff1}
			if err := netlink.QdiscAdd(&netlink.Clsact{QdiscAttrs: attrs}); err != nil && !errors.Is(err, syscall.EEXIST) {
				return fmt.Errorf("attach %s clsact: %w", selector.Device, err)
			}
			parent, protocol, priority := ingressParent, uint16(unix.ETH_P_IP), uint16(10)
			if direction == 1 {
				parent = egressParent
			}
			if selector.Address.Is6() {
				protocol, priority = unix.ETH_P_IPV6, 11
			}
			filter := &netlink.Flower{FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index, Parent: parent, Priority: priority,
				Protocol: protocol, Handle: uint32(lease.Class)<<16 | uint32(i+1),
			}, EthType: protocol}
			address := net.IP(selector.Address.AsSlice())
			mask := net.CIDRMask(selector.Address.BitLen(), selector.Address.BitLen())
			if direction == 0 {
				filter.SrcIP, filter.SrcIPMask = address, mask
			} else {
				filter.DestIP, filter.DestIPMask = address, mask
			}
			if rate.Mode == limits.Limited {
				classID := classRoot | uint32(lease.Class)
				action := netlink.NewSkbEditAction()
				action.Priority = &classID
				filter.Actions = []netlink.Action{action, netlink.NewMirredAction(ifb.Attrs().Index)}
				if err := netlink.FilterReplace(filter); err != nil {
					return fmt.Errorf("classify %s direction %d: %w", selector.Device, direction, err)
				}
			} else if err := netlink.FilterDel(filter); err != nil && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ESRCH) {
				return err
			}
		}
	}
	return nil
}

func (p *Linux) unshape(lease Lease) error {
	seen := make(map[string]bool)
	for _, selector := range lease.Selectors {
		if seen[selector.Device] {
			continue
		}
		seen[selector.Device] = true
		link, err := p.ownedDevice(selector.Device)
		if err != nil {
			var missing netlink.LinkNotFoundError
			if errors.As(err, &missing) {
				continue
			}
			return err
		}
		for _, parent := range []uint32{ingressParent, egressParent} {
			filters, err := netlink.FilterList(link, parent)
			if err != nil {
				return err
			}
			for _, filter := range filters {
				if filter.Attrs().Handle>>16 != uint32(lease.Class) {
					continue
				}
				if _, ok := filter.(*netlink.Flower); !ok {
					return ErrOwnership
				}
				if err := netlink.FilterDel(filter); err != nil && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ESRCH) {
					return err
				}
			}
		}
	}
	link, err := p.downloadLink(downOut)
	if err != nil {
		return err
	}
	filters, err := netlink.FilterList(link, classRoot)
	if err != nil {
		return err
	}
	for _, filter := range filters {
		if filter.Attrs().Handle>>16 == uint32(lease.Class) {
			if _, ok := filter.(*netlink.Flower); !ok {
				return ErrOwnership
			}
			if err := netlink.FilterDel(filter); err != nil && !errors.Is(err, syscall.ENOENT) {
				return err
			}
		}
	}
	return nil
}
