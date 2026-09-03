package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Drives the real system manager, so it runs only where an operator says so:
// PUI_SYSTEMD_E2E=1 on a Linux host with systemd, as a user allowed to manage
// p-ui-* units (root, or the panel's user with the installer's polkit rule).
func requireSystemd(t *testing.T) *Systemd {
	t.Helper()
	if os.Getenv("PUI_SYSTEMD_E2E") != "1" {
		t.Skip("set PUI_SYSTEMD_E2E=1 on a systemd host to drive the real manager")
	}
	b, err := NewSystemd(context.Background())
	if err != nil {
		t.Fatalf("NewSystemd: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

/*
The systemd backend is what lets a daemon outlive the panel. The unit must be
adopted by name when its spec matches, replaced when it does not, visible to
systemctl under the panel's slice, and gone after Stop. A failed unit is reset
and started again rather than reported as "already exists".
*/
func TestSystemdBackendLifecycle(t *testing.T) {
	b := requireSystemd(t)
	ctx := context.Background()
	spec := Spec{
		Name: UnitName("e2e", int(time.Now().Unix()%100000)), Description: "supervisor e2e",
		Exec: []string{"/bin/sleep", "300"}, Restart: RestartNo, StopTimeout: 3 * time.Second,
	}
	t.Cleanup(func() { _ = b.Stop(ctx, spec.Name) })

	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	first := waitState(t, b, spec.Name, StateRunning)
	if first.PID <= 0 || first.SpecHash != spec.Hash() {
		t.Fatalf("Status = %+v", first)
	}
	out, _ := exec.Command("systemctl", "show", "-p", "Slice", "--value", spec.Name+".service").Output()
	if strings.TrimSpace(string(out)) != DefaultSlice {
		t.Fatalf("unit is in slice %q, want %s", strings.TrimSpace(string(out)), DefaultSlice)
	}

	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again, _ := b.Status(ctx, spec.Name); again.PID != first.PID {
		t.Fatalf("a matching spec was not adopted: PID %d -> %d", first.PID, again.PID)
	}

	changed := spec
	changed.Exec = []string{"/bin/sleep", "301"}
	if err := b.Ensure(ctx, changed); err != nil {
		t.Fatalf("Ensure changed: %v", err)
	}
	after := waitState(t, b, spec.Name, StateRunning)
	if after.PID == first.PID || after.SpecHash != changed.Hash() {
		t.Fatalf("a changed spec did not replace the unit: PID %d -> %d, hash %s", first.PID, after.PID, after.SpecHash)
	}
	names, err := b.List(ctx)
	if err != nil || !contains(names, spec.Name) {
		t.Fatalf("List = %v, %v; want %s in it", names, err, spec.Name)
	}

	if err := b.Stop(ctx, spec.Name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := b.Status(ctx, spec.Name); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status after Stop = %v, want ErrNotRunning", err)
	}
}

func TestSystemdBackendRestartsAFailedUnit(t *testing.T) {
	b := requireSystemd(t)
	ctx := context.Background()
	spec := Spec{
		Name: UnitName("e2efail", int(time.Now().Unix()%100000)), Description: "supervisor e2e failure",
		Exec: []string{"/bin/sh", "-c", "exit 3"}, Restart: RestartNo,
	}
	t.Cleanup(func() { _ = b.Stop(ctx, spec.Name) })
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// A Restart=no unit that exits non-zero ends up failed; systemd may keep it
	// (so Status reads the failure) or collect it (so Status is ErrNotRunning).
	// Ensure recovers from either, which is the property that matters.
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := b.Status(ctx, spec.Name)
		if st.State != StateRunning || errors.Is(err, ErrNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the failing unit never left running: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	spec.Exec = []string{"/bin/sleep", "300"}
	if err := b.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure after failure: %v", err)
	}
	if st := waitState(t, b, spec.Name, StateRunning); st.PID <= 0 {
		t.Fatalf("recovered unit has no PID: %+v", st)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
