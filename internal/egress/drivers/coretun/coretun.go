/*
Package coretun routes into a tun device that a core's own client daemon makes.

The third shape driver.go names, next to Xray's injected front and the uplink
this panel dials itself: an OpenVPN or OpenConnect client creates its device
when it connects and takes it away when it exits. The plane therefore only
names the device; while it is absent the table holds its blackhole, and the
route into it appears on the converge after the daemon comes up.
*/
package coretun

import (
	"fmt"

	"github.com/Arman2122/p-ui/internal/egress"
)

// Driver serves one core's device exits under the type the row is stored as.
type Driver struct {
	driverType string
	device     func(id int) (string, error)
}

// New returns a driver for driverType whose device names are answered by the
// core that creates them, never derived here a second time.
func New(driverType string, device func(id int) (string, error)) Driver {
	return Driver{driverType: driverType, device: device}
}

func (d Driver) Type() string { return d.driverType }

// DeviceName is the name the core gives this exit's tun, asked on teardown too.
func (d Driver) DeviceName(id int) (string, error) { return d.device(id) }

/*
Fill names the device and marks the egress: what reaches it is a socket this
host originated, which has no ingress device to select on.

IPv4 only. The daemon assigns the address at connect time, after this pure call,
so the plane cannot know which families the device holds; a v6 route into a tun
with no v6 address would borrow eth0's and leave with the host's own identity.
*/
func (d Driver) Fill(e egress.Egress) (egress.Fill, error) {
	device, err := d.DeviceName(e.ID)
	if err != nil {
		return egress.Fill{}, fmt.Errorf("%s: egress %d: %w", d.driverType, e.ID, err)
	}
	if device == "" {
		return egress.Fill{}, fmt.Errorf("%s: egress %d names no device", d.driverType, e.ID)
	}
	return egress.Fill{
		Device:   device,
		Marked:   true,
		Families: []egress.Family{egress.FamilyV4},
		// Replies arrive on the tun from an address the main table routes
		// elsewhere; 2 (loose) because the kernel applies max(all, <dev>).
		Sysctls: map[string]string{"net.ipv4.conf." + device + ".rp_filter": "2"},
	}, nil
}
