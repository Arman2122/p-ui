//go:build linux

package sys

import "syscall"

// SIGUSR1 asks the running panel to reload. Split by GOOS only so the tests and
// arch guards compile off Linux — the panel itself is Linux-only.
var SIGUSR1 = syscall.SIGUSR1
