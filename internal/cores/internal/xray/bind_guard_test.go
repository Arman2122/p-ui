package xray

import (
	"os"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

func TestGeneratedBindConflictPreservesRunningCore(t *testing.T) {
	for _, entry := range []string{"reconcile", "apply"} {
		t.Run(entry, func(t *testing.T) {
			requirePosixHost(t)
			rig := newRig(t)
			adapter := New(Deps{BaseConfig: rig.baseConfig})
			first, second := rig.instance(1), rig.instance(1)
			second.ID, second.Tag, second.Port = 10, "inbound-10", first.Port+1
			desired := []core.Instance{first, second}
			if err := adapter.Reconcile(t.Context(), desired); err != nil {
				t.Fatal(err)
			}
			rig.waitReady()
			process := adapter.mgr.Current()
			pid, config := process.PID(), process.GetConfig()
			written, err := os.ReadFile(engine.GetConfigPath())
			if err != nil {
				t.Fatal(err)
			}
			second.Port = first.Port
			if entry == "reconcile" {
				err = adapter.Reconcile(t.Context(), []core.Instance{first, second})
			} else {
				err = adapter.ApplyInstance(t.Context(), second)
			}
			if err == nil || !strings.Contains(err.Error(), "config refused") || !strings.Contains(err.Error(), first.Tag) || !strings.Contains(err.Error(), second.Tag) {
				t.Errorf("conflicting desired listener returned %v", err)
			}
			if adapter.mgr.Current() != process || !process.IsRunning() || process.PID() != pid || !process.GetConfig().Equals(config) {
				t.Fatal("conflicting desired listener changed the running process or its configuration")
			}
			after, err := os.ReadFile(engine.GetConfigPath())
			if err != nil || string(after) != string(written) {
				t.Fatalf("rejected config changed the on-disk configuration: %v", err)
			}
			health, err := adapter.Health(t.Context())
			if err != nil || health.State != core.HealthRunning || !strings.Contains(health.Detail, "config refused") {
				t.Fatalf("held configuration health = %+v, %v", health, err)
			}
			if err := adapter.Reconcile(t.Context(), desired); err != nil {
				t.Fatal(err)
			}
			health, err = adapter.Health(t.Context())
			if err != nil || strings.Contains(health.Detail, "config refused") || process.PID() != pid {
				t.Fatalf("reverted desired config did not clear refusal without restarting: %+v, %v", health, err)
			}
		})
	}
}
