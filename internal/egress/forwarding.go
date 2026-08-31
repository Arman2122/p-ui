package egress

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

/*
The panel owns host forwarding, but only once something on this host needs it.

ForwardingNotes used to say this was "reported and never owned". That was the
right call when an L3 inbound was exotic; it is the wrong one now, because a
fresh install from GitHub ships net.ipv4.ip_forward=0 and the first WireGuard
inbound anyone creates completes handshakes and routes nothing. Telling an
operator to run sysctl by hand is a step every single install has to take, and
one they discover only after their users report a dead tunnel.

Enabled and never disabled. Turning forwarding ON breaks nothing; turning it off
would break docker, any other VPN, and every container network on the box, none
of which this panel put there.
*/

// ForwardingDropIn is where the persisted knob lives. A drop-in rather than an
// edit to sysctl.conf, so uninstalling is a delete and never a careful unpick.
const ForwardingDropIn = "/etc/sysctl.d/99-p-ui-forwarding.conf"

const forwardingDropInBody = `# Written by Penhoon UI: an L3 inbound on this host forwards packets, and
# without these it completes handshakes and routes nothing. Removed by uninstall.
net.ipv4.ip_forward=1
net.ipv6.conf.all.forwarding=1
`

/*
EnsureForwarding turns both forwarding knobs on and makes them survive a reboot.

Runtime first so the inbound works now, then the drop-in so it still works after
a reboot -- the panel converges at boot too, but a client reconnecting in the
window before that would find a black hole, and P9-C exists because the
documented manual fix did not survive a reboot either.

Idempotent: a knob already on is not rewritten, and a drop-in whose content
already matches is left alone, so this is free on every pass after the first.
*/
func (m *Manager) EnsureForwarding(ctx context.Context) error {
	var failures []error
	// Before the knobs below, never after: accept_ra defaults to 1, which the
	// kernel reads as "accept unless forwarding is enabled", so turning
	// forwarding on is what costs an RA-configured host its own IPv6.
	if err := m.keepRouterAdvertisements(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, key := range []string{IPForwardKey, IPForward6Key} {
		value, err := m.plane.Sysctl(ctx, key)
		if err != nil {
			// An absent key is a kernel without that family, not a failure to fix.
			if errors.Is(err, ErrNoDevice) {
				continue
			}
			failures = append(failures, fmt.Errorf("read %s: %w", key, err))
			continue
		}
		if value == "1" {
			continue
		}
		if err := m.plane.SetSysctl(ctx, key, "1"); err != nil {
			failures = append(failures, fmt.Errorf("set %s: %w", key, err))
		}
	}
	if err := m.plane.PersistSysctl(ctx, ForwardingDropIn, forwardingDropInBody); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// AcceptRADropIn keeps Router Advertisements working on a host this panel turned
// into a router. Numbered BELOW the forwarding drop-in so sysctl applies it
// first at boot: the knob has to read 2 before forwarding reads 1.
const AcceptRADropIn = "/etc/sysctl.d/98-p-ui-accept-ra.conf"

func acceptRAKey(device string) string { return "net.ipv6.conf." + device + ".accept_ra" }

/*
keepRouterAdvertisements preserves RA on the interfaces that actually use it.

accept_ra=1 -- the default -- means "accept unless forwarding is enabled". This
panel enables forwarding, so on a host whose IPv6 comes from a Router
Advertisement (AWS, Vultr, Linode; not Hetzner) the panel silently costs the
host its own default route. Value 2 is the kernel's own answer: accept anyway.

Only a device the kernel is CURRENTLY configuring from an RA is touched, and
that is the security half rather than tidiness. accept_ra=2 on a client-facing
tunnel would let any VPN client advertise itself as this host's gateway -- so
the test is a route the kernel attributes to an RA, which no wg device has. A
prefix allowlist would have failed the other way, silently, the first time a new
core named a device.

Nothing is written when no device uses RA, so one momentary read of zero cannot
delete the line that keeps the next boot working.
*/
func (m *Manager) keepRouterAdvertisements(ctx context.Context) error {
	devices, err := m.plane.RouterAdvertisedDevices(ctx)
	if err != nil || len(devices) == 0 {
		return err
	}
	devices = slices.Clone(devices)
	slices.Sort(devices)

	var failures []error
	body := "# Written by Penhoon UI. accept_ra=1 means \"accept unless forwarding is\n" +
		"# enabled\", and this panel enables forwarding. Removed by uninstall.\n"
	for _, device := range devices {
		key := acceptRAKey(device)
		body += key + "=2\n"
		// A 0 is an operator saying no and is never overruled; a 2 is already right.
		if value, readErr := m.plane.Sysctl(ctx, key); readErr != nil || value != "1" {
			continue
		}
		if err := m.plane.SetSysctl(ctx, key, "2"); err != nil {
			failures = append(failures, fmt.Errorf("set %s: %w", key, err))
		}
	}
	if err := m.plane.PersistSysctl(ctx, AcceptRADropIn, body); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
