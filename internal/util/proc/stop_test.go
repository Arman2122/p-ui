package proc_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/util/proc"
)

func TestWaitForExitReturnsWhenTheChildIsGone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if err := proc.WaitForExit(done, time.Second, "test"); err != nil {
		t.Fatalf("WaitForExit on a closed channel = %v, want nil", err)
	}
}

// A child that was never started is not something to wait for, and blocking
// there would hang a reconcile behind a process that does not exist.
func TestWaitForExitTreatsNoChildAsStopped(t *testing.T) {
	if err := proc.WaitForExit(nil, time.Second, "test"); err != nil {
		t.Fatalf("WaitForExit(nil) = %v, want nil", err)
	}
}

// The name rides in the message because a caller reads it beside every other
// timeout in the log, and "a process" names nothing to act on.
func TestWaitForExitNamesWhatTimedOut(t *testing.T) {
	err := proc.WaitForExit(make(chan struct{}), 10*time.Millisecond, "mtg inbound 7")
	if err == nil {
		t.Fatal("waiting on a channel that never closes returned nil")
	}
	if !strings.Contains(err.Error(), "mtg inbound 7") {
		t.Fatalf("error %q does not name the child", err)
	}
}

// Stopping something that was never started is success: the caller wanted it
// stopped and it is stopped.
func TestStopIsANoopWithoutAProcess(t *testing.T) {
	if err := proc.Stop(nil, nil, time.Second, time.Second, "test"); err != nil {
		t.Fatalf("Stop(nil) = %v, want nil", err)
	}
	if err := proc.Stop(&exec.Cmd{}, nil, time.Second, time.Second, "test"); err != nil {
		t.Fatalf("Stop on an unstarted cmd = %v, want nil", err)
	}
}
