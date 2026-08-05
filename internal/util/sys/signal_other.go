//go:build !linux

package sys

import "syscall"

// Unreachable off Linux: nothing delivers this signal there, and the panel does
// not run there. It exists so `go test ./...` compiles on a dev machine.
var SIGUSR1 = syscall.Signal(0xa)
