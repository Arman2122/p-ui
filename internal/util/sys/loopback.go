package sys

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// FreeLoopbackPort asks the OS for a loopback port free on TCP and UDP alike:
// the Xray bridge allocated from it binds both, and a sidecar's API port one.
func FreeLoopbackPort() (int, error) {
	var lastErr error
	for range 8 {
		l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		port := l.Addr().(*net.TCPAddr).Port
		pc, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		_ = l.Close()
		if err != nil {
			lastErr = err
			continue
		}
		_ = pc.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no loopback port free on both transports: %w", lastErr)
}
