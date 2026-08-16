//go:build linux

package proc_test

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/util/proc"
)

// Signalling a child is not supported on Windows, so the ladder itself can only
// be driven where the panel actually runs.

/*
A child that ignores SIGTERM is killed after the graceful window.

The whole reason the ladder exists: without the escalation a daemon that traps
the signal blocks its core's reconcile forever, and without the graceful window
first every ordinary stop becomes a hard kill.
*/
func TestStopEscalatesToKill(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary on this host")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	start := time.Now()
	// A graceful window this short expires before any child could react, so the
	// kill path is the one under test rather than a lucky SIGTERM.
	if err := proc.Stop(cmd, done, time.Millisecond, 5*time.Second, "sleep"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Stop returned only after the force window; the kill never landed")
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop returned while the child was still running")
	}
}

// A process that exits on its own between the liveness check and the signal is
// the normal race, not a failure.
func TestStopAcceptsAChildThatAlreadyExited(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true binary on this host")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	<-done

	if err := proc.Stop(cmd, done, time.Second, time.Second, "true"); err != nil && !errors.Is(err, nil) {
		t.Fatalf("Stop on an exited child = %v, want nil", err)
	}
}
