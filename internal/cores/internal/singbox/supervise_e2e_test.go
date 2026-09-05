//go:build linux

package singbox

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

/*
A daemon that died stayed dead: applyLocked returned on the flag it set at
start, and the exec backend restarts nothing on its own. Killed here the way a
crash looks to the panel — SIGKILL, no notice — the daemon must be back after
one reconcile, as a new process, answering on its stats port (which Reconcile
waits for before it returns).
*/
func TestARealDeadDaemonIsStartedAgain(t *testing.T) {
	bin := e2eBinary(t)
	certFile, keyFile := selfSigned(t, t.TempDir())
	c := New(Deps{Binary: bin, StateDir: t.TempDir(), ApplyDelay: time.Hour})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	ctx := context.Background()
	inst := core.Instance{ID: 4, Kind: KindTUIC, Tag: "tuic-4", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: settingsJSON(t, certFile, keyFile), Users: []core.User{e2eUser(1)}}
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	unit := supervisor.UnitName(string(ID), 4)
	before, err := c.backend.Status(ctx, unit)
	if err != nil || before.State != supervisor.StateRunning || before.PID == 0 {
		t.Fatalf("status = %+v, %v; want a running daemon with a pid", before, err)
	}

	if err := syscall.Kill(before.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.backend.Status(ctx, unit)
		if err == nil && st.State == supervisor.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the backend never saw the daemon die: %+v, %v", st, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile after the crash: %v", err)
	}
	after, err := c.backend.Status(ctx, unit)
	if err != nil || after.State != supervisor.StateRunning || after.PID == before.PID {
		t.Fatalf("status after reconcile = %+v, %v; want a new running daemon (was pid %d)", after, err, before.PID)
	}
	c.mu.Lock()
	starts := c.starts
	c.mu.Unlock()
	if starts != 2 {
		t.Fatalf("starts = %d; want 2, one per life of the daemon", starts)
	}
}
