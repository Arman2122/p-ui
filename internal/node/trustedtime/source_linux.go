//go:build linux

package trustedtime

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func SystemSource() (Source, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	boot := strings.TrimSpace(string(data))
	if !validBoot(boot) {
		return nil, ErrUntrusted
	}
	return func() (Instant, error) {
		var stamp unix.Timespec
		if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &stamp); err != nil {
			return Instant{}, err
		}
		return Instant{Boot: boot, Nano: stamp.Nano()}, nil
	}, nil
}
