//go:build linux

package ocserv

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// The unit test's fake says what the fix does; this says the exec backend and a
// real daemon agree: killed behind the panel's back, the daemon is back after
// one reconcile as a new process, answering occtl on its socket.
func TestARealDeadDaemonIsStartedAgain(t *testing.T) {
	daemon, ctl, _ := e2eGate(t)
	c := newE2ECore(t, daemon, ctl)
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	ctx := context.Background()
	inst := core.Instance{ID: 6, Kind: Kind, Tag: "ocs-6", Listen: "127.0.0.1", Port: freeTCPPort(t), Enable: true, Settings: mintedSettings(t, c, "10.216.0.0/24")}
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	unit := supervisor.UnitName(string(ID), 6)
	before, err := c.backend.Status(ctx, unit)
	if err != nil || before.State != supervisor.StateRunning || before.PID == 0 {
		t.Fatalf("status = %+v, %v; want a running daemon with a pid", before, err)
	}
	c.mu.Lock()
	sock := c.instances[6].paths.ctl
	c.mu.Unlock()
	answers := func() bool {
		return exec.CommandContext(ctx, ctl, "-s", sock, "show", "status").Run() == nil
	}
	waitFor(t, 10*time.Second, answers, func() string { return "the daemon never answered occtl" })

	if err := syscall.Kill(before.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		st, err := c.backend.Status(ctx, unit)
		return err == nil && st.State == supervisor.StateFailed
	}, func() string { return "the backend never saw the daemon die" })

	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile after the crash: %v", err)
	}
	after, err := c.backend.Status(ctx, unit)
	if err != nil || after.State != supervisor.StateRunning || after.PID == before.PID {
		t.Fatalf("status after reconcile = %+v, %v; want a new running daemon (was pid %d)", after, err, before.PID)
	}
	waitFor(t, 10*time.Second, answers, func() string { return "the restarted daemon never answered occtl" })
}
