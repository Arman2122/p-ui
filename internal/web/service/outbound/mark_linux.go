//go:build linux

package outbound

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// markedSocketControl stamps every socket the dialer opens with the egress's
// fwmark, before connect, which is the only moment the routing decision reads it.
func markedSocketControl(mark uint32) (func(network, address string, c syscall.RawConn) error, error) {
	return func(_, _ string, c syscall.RawConn) error {
		var setErr error
		if err := c.Control(func(fd uintptr) {
			setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
		}); err != nil {
			return err
		}
		if setErr != nil {
			return fmt.Errorf("marking the probe socket needs CAP_NET_ADMIN: %w", setErr)
		}
		return nil
	}, nil
}
