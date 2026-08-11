//go:build linux

package egress

import (
	"errors"
	"syscall"
	"testing"
)

/*
Every errno the band can produce has to reach a sentinel.

An unmapped one is not merely untidy: the manager cannot tell it from drift, so
EgressReconcileJob alarms with a raw errno and no remedy on every 10s tick, and
every attach reverts with ErrEgressNotRouted naming nothing an operator can act on.
*/
func TestClassifyMapsEveryErrnoTheBandProduces(t *testing.T) {
	cases := []struct {
		errno    syscall.Errno
		sentinel error
	}{
		{syscall.EEXIST, ErrAlreadyInstalled},
		{syscall.ESRCH, ErrNotInstalled},
		{syscall.ENOENT, ErrNotInstalled},
		{syscall.EPERM, ErrPermission},
		{syscall.ENODEV, ErrNoDevice},
		{syscall.ENXIO, ErrNoDevice},
		// A kernel booted with ipv6.disable=1 registers no fib rules for the family,
		// so every v6 object answers this and no retry will ever install one.
		{syscall.EAFNOSUPPORT, ErrFamilyUnsupported},
		{syscall.EOPNOTSUPP, ErrFamilyUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.errno.Error(), func(t *testing.T) {
			if got := classify(tc.errno); !errors.Is(got, tc.sentinel) {
				t.Fatalf("classify(%v) = %v, want %v", tc.errno, got, tc.sentinel)
			}
		})
	}
}
