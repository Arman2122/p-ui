//go:build !linux

package outbound

import (
	"syscall"

	"github.com/Arman2122/p-ui/internal/egress"
)

// Refused rather than ignored: a dialer that quietly skips the mark still
// connects, and would report the developer workstation's own latency and exit
// IP as the uplink's.
func markedSocketControl(uint32) (func(network, address string, c syscall.RawConn) error, error) {
	return nil, egress.ErrPlatformUnsupported
}
