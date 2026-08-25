package xray

import (
	"runtime"
	"testing"
)

/*
requirePosixHost skips a test that drives a real child process.

These tests use #!/bin/sh stand-in binaries, send SIGTERM and wait out a
graceful-exit window, assert file modes, and rename over an open file. None of
that has a Windows analogue, so on Windows they fail for reasons that say
nothing about the code under test.

They are skipped rather than left red because a fixed set of failures on every
local run teaches a maintainer to stop reading failures — and this repo has
already had one commit land with a panic on screen. CI and the Linux VM are
where these run for real. Ledger L12.
*/
func requirePosixHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX host: this test drives a real child process (ledger L12)")
	}
}
