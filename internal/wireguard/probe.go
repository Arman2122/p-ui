package wireguard

import (
	"context"
	"errors"
	"fmt"

	"github.com/Arman2122/p-ui/internal/logger"
)

// kernelModulePath is the only evidence that separates a kernel device from the
// userspace fallback wgctrl silently offers when the module is absent.
const kernelModulePath = "/sys/module/wireguard"

// Preflight reports whether kernel WireGuard can run on this host. A failure
// disables this core alone; Xray and mtg are behind their own.
func (m *Manager) Preflight(ctx context.Context) error {
	return m.plane.Probe(ctx)
}

/*
probeKernel is the order the two halves of the answer must be asked in.

openControl comes first because the generic-netlink family lookup inside it makes
the kernel autoload wireguard.ko, so the module check after it is authoritative
rather than a report of whether anything happened to have loaded it yet. Asked
the other way round, a host that supports WireGuard perfectly answers "no kernel
support" until something else creates a device — and the panel never will,
because this answer is what greys the option out of the picker.

The module check assumes CONFIG_WIREGUARD=m, which holds on every supported
platform; a kernel with it built in exposes no /sys/module entry to find.
*/
func probeKernel(openControl func() error, moduleLoaded func() bool) error {
	if err := openControl(); err != nil {
		return err
	}
	if !moduleLoaded() {
		return fmt.Errorf("%w: %s is absent", ErrNoKernelSupport, kernelModulePath)
	}
	return nil
}

// note logs a host that cannot run kernel WireGuard exactly once and passes the
// error on. Without the once, the 10s reconcile writes the same line forever.
func (m *Manager) note(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPlatformUnsupported) || errors.Is(err, ErrNoKernelSupport) || errors.Is(err, ErrPermission) {
		m.warnOnce.Do(func() {
			logger.Warningf("wireguard: kernel WireGuard is unusable here, every wgkernel inbound stays down: %v", err)
		})
	}
	return err
}
