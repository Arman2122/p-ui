package wireguard

import (
	"context"
	"errors"

	"github.com/Arman2122/p-ui/internal/logger"
)

// Preflight reports whether kernel WireGuard can run on this host. A failure
// disables this core alone; Xray and mtg are behind their own.
func (m *Manager) Preflight(ctx context.Context) error {
	return m.plane.Probe(ctx)
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
