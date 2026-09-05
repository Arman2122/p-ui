//go:build linux

package openvpn

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// The unit test's fake says what the fix does; this says the exec backend and a
// real daemon agree: killed behind the panel's back, the daemon is back after
// one reconcile as a new process, greeting on its management socket.
func TestARealDeadDaemonIsStartedAgain(t *testing.T) {
	bin := e2eGate(t)
	c := newE2ECore(t, bin)
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	ctx := context.Background()
	inst := core.Instance{ID: 6, Kind: Kind, Tag: "ovpn-6", Port: freeUDPPort(t), Enable: true, Settings: mintedSettings(t, c, "10.215.0.0/24", ProtoUDP)}
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	unit := supervisor.UnitName(string(ID), 6)
	before, err := c.backend.Status(ctx, unit)
	if err != nil || before.State != supervisor.StateRunning || before.PID == 0 {
		t.Fatalf("status = %+v, %v; want a running daemon with a pid", before, err)
	}
	c.mu.Lock()
	sock := c.instances[6].paths.mgmt
	c.mu.Unlock()
	greets := func() bool {
		conn, _, err := management(ctx, sock)
		if err != nil || conn == nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	waitFor(t, 10*time.Second, greets, func() string { return "the daemon never opened its management socket" })

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
	waitFor(t, 10*time.Second, greets, func() string { return "the restarted daemon never opened its management socket" })
}
