/*
Package proc holds the parts of running a managed child process that every core
does identically, so core #4 inherits them instead of writing a third copy.

Deliberately small. The wrappers in internal/xray and internal/mtproto own what
is genuinely theirs — a temp config to remove, a crash report to write, an API
port to discover — and share only what was already the same in both, character
for character.
*/
package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

/*
Stop ends a child the way both cores already did: SIGTERM, wait out the graceful
window, then SIGKILL and wait again.

The ladder is not decoration. A daemon killed outright leaves its clients with a
half-open connection and, for a core that writes state on shutdown, a file it
never finished; a daemon only ever asked politely can hang forever and block the
reconcile behind it. Both timeouts are the caller's because what "graceful"
means belongs to the daemon, not to this helper.

A process that has already exited is success: the caller wanted it stopped and
it is stopped. Signal returning ErrProcessDone is the race where it died between
the liveness check and the signal, which is normal rather than exceptional.
*/
func Stop(cmd *exec.Cmd, done <-chan struct{}, graceful, force time.Duration, kill string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return WaitForExit(done, force, kill)
		}
		return err
	}
	if err := WaitForExit(done, graceful, kill); err == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return WaitForExit(done, force, kill)
}

/*
WaitForExit blocks until the child's done channel closes or the timeout expires.

A nil channel is a child that was never started, which is not a failure to wait
for. The name is in the timeout error because a caller sees it in a log line
next to whatever else timed out, and "timed out waiting for a process" names
nothing an operator can act on.
*/
func WaitForExit(done <-chan struct{}, timeout time.Duration, name string) error {
	if done == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s to stop after %s", name, timeout)
	}
}
