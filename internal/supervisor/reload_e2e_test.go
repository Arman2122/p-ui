//go:build linux

package supervisor

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A daemon that notes every SIGHUP it gets; the panel reloads a sing-box this way.
func hupNotingSpec(t *testing.T, name string) (Spec, string) {
	t.Helper()
	mark := filepath.Join(t.TempDir(), "hup")
	script := "trap 'echo reloaded >> " + mark + "' HUP; while :; do sleep 0.1; done"
	return Spec{
		Name: name, Description: "supervisor reload e2e",
		Exec: []string{"/bin/sh", "-c", script}, Restart: RestartNo, StopTimeout: 3 * time.Second,
	}, mark
}

func waitMark(t *testing.T, mark string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(mark); err == nil && strings.Contains(string(body), "reloaded") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the daemon never saw the SIGHUP")
}

func TestExecBackendReloadSignalsWithoutRestarting(t *testing.T) {
	// The panel handles SIGHUP itself (certificate reload), so its children start
	// with the default disposition; a test binary may have inherited "ignored".
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	t.Cleanup(func() { signal.Stop(hup) })
	b := NewExec()
	ctx := context.Background()
	spec, mark := hupNotingSpec(t, UnitName("e2e-reload-exec", 1))
	t.Cleanup(func() { _ = b.Stop(ctx, spec.Name) })
	if err := b.Reload(ctx, spec.Name); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Reload before Ensure = %v, want ErrNotRunning", err)
	}
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	before, _ := b.Status(ctx, spec.Name)
	if err := b.Reload(ctx, spec.Name); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	waitMark(t, mark)
	if after, _ := b.Status(ctx, spec.Name); after.PID != before.PID || after.State != StateRunning {
		t.Fatalf("a reload replaced the daemon: %+v -> %+v", before, after)
	}
}

func TestSystemdBackendReloadSignalsWithoutRestarting(t *testing.T) {
	b := requireSystemd(t)
	ctx := context.Background()
	spec, mark := hupNotingSpec(t, UnitName("e2e-reload", int(time.Now().Unix()%100000)))
	t.Cleanup(func() { _ = b.Stop(ctx, spec.Name) })
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	before := waitState(t, b, spec.Name, StateRunning)
	if err := b.Reload(ctx, spec.Name); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	waitMark(t, mark)
	if after, _ := b.Status(ctx, spec.Name); after.PID != before.PID || after.State != StateRunning {
		t.Fatalf("a reload replaced the unit: %+v -> %+v", before, after)
	}
}
