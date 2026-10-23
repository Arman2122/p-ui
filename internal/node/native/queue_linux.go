//go:build linux

package native

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
)

func (p *Linux) purgeQueues(lease Lease) error {
	for _, name := range []string{"phvup", downOut} {
		link, err := netlink.LinkByName(name)
		if err != nil {
			var missing netlink.LinkNotFoundError
			if errors.As(err, &missing) {
				continue
			}
			return err
		}
		if (name == "phvup" && (link.Type() != "ifb" || link.Attrs().Alias != p.owner)) || (name == downOut && !p.OwnsDownloadLink(link)) {
			return ErrOwnership
		}
		classes, err := netlink.ClassList(link, netlink.HANDLE_NONE)
		if err != nil {
			return err
		}
		parent := classRoot | uint32(lease.Class)
		found := false
		for _, class := range classes {
			if class.Attrs().Handle == parent {
				found = true
			}
		}
		if !found {
			continue
		}
		queues, err := netlink.QdiscList(link)
		if err != nil {
			return err
		}
		removed := false
		for _, queue := range queues {
			if queue.Attrs().Parent != parent {
				continue
			}
			if queue.Type() != "pfifo" {
				return ErrOwnership
			}
			if queue.Attrs().Handle != 0 {
				if err := netlink.QdiscDel(queue); err != nil {
					return fmt.Errorf("purge %s class %d: %w", name, lease.Class, err)
				}
				removed = true
			}
		}
		if !removed {
			queue := &netlink.GenericQdisc{QdiscAttrs: netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Parent: parent, Handle: uint32(lease.Class+1) << 16}, QdiscType: "pfifo"}
			if err := netlink.QdiscReplace(queue); err != nil {
				return fmt.Errorf("replace %s leaf %d: %w", name, lease.Class, err)
			}
		}
		classes, err = netlink.ClassList(link, netlink.HANDLE_NONE)
		if err != nil {
			return err
		}
		for _, class := range classes {
			if class.Attrs().Handle == parent {
				stats := class.Attrs().Statistics
				if stats == nil || stats.Queue == nil || stats.Queue.Qlen != 0 || stats.Queue.Backlog != 0 {
					return ErrBusy
				}
			}
		}
	}
	return nil
}
