//go:build linux

package coretest

import "syscall"

// markControl stamps a dialled socket with an fwmark, so a test can send from
// the same handle the panel's routing selects an egress by.
func markControl(mark uint32) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var err error
		if ctl := c.Control(func(fd uintptr) {
			err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark))
		}); ctl != nil {
			return ctl
		}
		return err
	}
}
