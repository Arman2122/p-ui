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
	// The trap comes first and the ready mark after it, so a signal sent once
	// the mark exists cannot land on a shell with the default disposition.
	script := "trap 'echo reloaded >> " + mark + "' HUP; echo ready > " + mark + ".ready; while :; do sleep 0.1; done"
	return Spec{
		Name: name, Description: "supervisor reload e2e",
		Exec: []string{"/bin/sh", "-c", script}, Restart: RestartNo, StopTimeout: 3 * time.Second,
	}, mark
}

func waitFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(path); err == nil && strings.Contains(string(body), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never contained %q", path, want)
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
	waitFile(t, mark+".ready", "ready")
	before, _ := b.Status(ctx, spec.Name)
	if err := b.Reload(ctx, spec.Name); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	waitFile(t, mark, "reloaded")
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
	waitFile(t, mark+".ready", "ready")
	if err := b.Reload(ctx, spec.Name); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	waitFile(t, mark, "reloaded")
	if after, _ := b.Status(ctx, spec.Name); after.PID != before.PID || after.State != StateRunning {
		t.Fatalf("a reload replaced the unit: %+v -> %+v", before, after)
	}
}
