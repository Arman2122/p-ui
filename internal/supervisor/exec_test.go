package supervisor

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func requirePosixHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX host: this test drives a real child process")
	}
}

func shSpec(name, script string) Spec {
	return Spec{Name: UnitName("test", 1), Description: name, Exec: []string{"/bin/sh", "-c", script}, StopTimeout: 2 * time.Second}
}

func waitState(t *testing.T, b Backend, name string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := b.Status(context.Background(), name)
		if st.State == want || time.Now().After(deadline) {
			if st.State != want {
				t.Fatalf("%s: state %s after 5s, want %s (result %q)", name, st.State, want, st.Result)
			}
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
}

/*
What the exec backend must do for the cores that stand on it: leave a daemon
running the same spec alone, replace one running a different spec, report a
crash through OnExit and Status, and stop on request. This is the behaviour
internal/mtproto and internal/xray each carried on their own.
*/
func TestExecBackendLifecycle(t *testing.T) {
	requirePosixHost(t)
	b := NewExec()
	t.Cleanup(func() { _ = b.Close() })
	ctx := context.Background()

	spec := shSpec("sleeper", "sleep 30")
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	first := waitState(t, b, spec.Name, StateRunning)
	if first.PID <= 0 {
		t.Fatal("a running daemon has no PID")
	}
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again, _ := b.Status(ctx, spec.Name); again.PID != first.PID {
		t.Fatalf("Ensure with the same spec restarted the daemon: PID %d -> %d", first.PID, again.PID)
	}

	changed := shSpec("sleeper", "sleep 31")
	if err := b.Ensure(ctx, changed); err != nil {
		t.Fatalf("Ensure changed: %v", err)
	}
	if after, _ := b.Status(ctx, spec.Name); after.PID == first.PID || after.SpecHash != changed.Hash() {
		t.Fatalf("Ensure with a changed spec kept the old daemon: PID %d -> %d, hash %s", first.PID, after.PID, after.SpecHash)
	}
	names, _ := b.List(ctx)
	if len(names) != 1 || names[0] != spec.Name {
		t.Fatalf("List = %v", names)
	}

	if err := b.Stop(ctx, spec.Name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := b.Status(ctx, spec.Name); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status after Stop = %v, want ErrNotRunning", err)
	}
}

func TestExecBackendReportsACrash(t *testing.T) {
	requirePosixHost(t)
	b := NewExec()
	t.Cleanup(func() { _ = b.Close() })
	var out bytes.Buffer
	var mu sync.Mutex
	var exitErr error
	spec := shSpec("crasher", "echo dying; exit 3")
	spec.Output = &out
	spec.OnExit = func(err error) {
		mu.Lock()
		exitErr = err
		mu.Unlock()
	}
	if err := b.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	st := waitState(t, b, spec.Name, StateFailed)
	if !strings.Contains(st.Result, "exit status 3") {
		t.Fatalf("Result = %q, want the exit status", st.Result)
	}
	mu.Lock()
	defer mu.Unlock()
	if exitErr == nil || !strings.Contains(exitErr.Error(), "exit status 3") {
		t.Fatalf("OnExit got %v, want the exit status", exitErr)
	}
	if !strings.Contains(out.String(), "dying") {
		t.Fatalf("the daemon's output did not reach Output: %q", out.String())
	}
}

func TestExecBackendRefusesAnotherUser(t *testing.T) {
	spec := shSpec("other", "sleep 1")
	spec.User = "p-ui"
	err := NewExec().Ensure(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "cannot run") {
		t.Fatalf("Ensure with User set = %v, want a refusal", err)
	}
}
