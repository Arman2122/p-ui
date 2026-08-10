package wireguard

import (
	"errors"
	"slices"
	"testing"
)

// TestProbeLoadsTheModuleBeforeLookingForIt pins the order the two halves are
// asked in. Reversed, a host whose wireguard.ko has simply not autoloaded yet is
// reported as having no kernel support, the picker greys the core out, and
// nothing the operator can do from the UI ever clears it.
func TestProbeLoadsTheModuleBeforeLookingForIt(t *testing.T) {
	var order []string
	loaded := false
	err := probeKernel(
		func() error {
			order = append(order, "open")
			// what the generic-netlink family lookup does to the host
			loaded = true
			return nil
		},
		func() bool {
			order = append(order, "module")
			return loaded
		},
	)
	if err != nil {
		t.Fatalf("Probe = %v on a host that supports WireGuard once the module is autoloaded", err)
	}
	if !slices.Equal(order, []string{"open", "module"}) {
		t.Fatalf("probe order = %v, want [open module]", order)
	}
}

// A host with no kernel WireGuard at all must still fail: wgctrl falls back to a
// userspace client and returns no error, so the module check is the whole answer.
func TestProbeFailsWithoutTheModule(t *testing.T) {
	err := probeKernel(func() error { return nil }, func() bool { return false })
	if !errors.Is(err, ErrNoKernelSupport) {
		t.Fatalf("Probe = %v, want ErrNoKernelSupport; wgctrl.New succeeds on an OpenVZ or LXC host with no kernel WireGuard", err)
	}
}
