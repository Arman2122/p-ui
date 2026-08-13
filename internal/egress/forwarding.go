package egress

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if err := persistForwarding(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// persistForwarding writes the drop-in, and only when it would change: an
// unchanged file keeps its mtime so an operator can see when the panel last
// touched it.
func persistForwarding() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if current, err := os.ReadFile(ForwardingDropIn); err == nil && string(current) == forwardingDropInBody {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(ForwardingDropIn), 0o755); err != nil {
		return fmt.Errorf("egress: create %s: %w", filepath.Dir(ForwardingDropIn), err)
	}
	if err := os.WriteFile(ForwardingDropIn, []byte(forwardingDropInBody), 0o644); err != nil {
		return fmt.Errorf("egress: write %s: %w", ForwardingDropIn, err)
	}
	return nil
}
