//go:build linux

package wireguard

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

// TestClassifyKeepsTheErrno pins both halves of a classified failure. The
// sentinel is what the panel branches on; the errno underneath is what an
// operator's bug report needs, and what tells EPERM apart from EACCES.
func TestClassifyKeepsTheErrno(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      error
		sentinel error
	}{
		{"a device the panel may not touch", syscall.EPERM, ErrPermission},
		{"a kernel with no wireguard link type", syscall.EOPNOTSUPP, ErrNoKernelSupport},
		{"a device that went away mid-write", syscall.ENODEV, ErrNoDevice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(fmt.Errorf("netlink: %w", tc.raw))
			if !errors.Is(got, tc.sentinel) {
				t.Fatalf("classify(%v) = %v, want it to wrap %v", tc.raw, got, tc.sentinel)
			}
			if !errors.Is(got, tc.raw) {
				t.Fatalf("classify(%v) = %v; the errno was formatted away, so nothing downstream can tell which failure this was", tc.raw, got)
			}
		})
	}
	if classify(nil) != nil {
		t.Fatal("classify(nil) invented an error")
	}
}
