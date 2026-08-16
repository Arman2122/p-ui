package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
Restarting a core is asked of the registry, not of a name in a URL.

The panel's process-management API was an Xray-only family — /stopXrayService,
/restartXrayService — so an mtg sidecar could not be restarted from the panel at
all. Restart is StopAll then Reconcile because those are the two capabilities a
supervised core already implements; nothing here knows a protocol.
*/
func TestRestartCoreStopsThenReconciles(t *testing.T) {
	initTestDB(t)
	probe := &restartProbeCore{}
	withWiredCore(t, probe)

	if err := (&ServerService{}).RestartCore(context.Background(), "restart-probe"); err != nil {
		t.Fatalf("RestartCore: %v", err)
	}
	if !probe.stopped {
		t.Error("the core was reconciled without being stopped first")
	}
	if !probe.reconciled {
		t.Error("the core was stopped and never brought back")
	}
	if probe.order != "stop,reconcile" {
		t.Errorf("order = %q; converging before stopping leaves the old daemons running", probe.order)
	}
}

// A daemon that will not stop cleanly is still one the reconcile replaces, so
// the stop failure is reported and the restart continues.
func TestRestartCoreSurvivesADirtyStop(t *testing.T) {
	initTestDB(t)
	probe := &restartProbeCore{stopErr: errors.New("no such process")}
	withWiredCore(t, probe)

	if err := (&ServerService{}).RestartCore(context.Background(), "restart-probe"); err != nil {
		t.Fatalf("RestartCore = %v; a dirty stop must not abort the restart", err)
	}
	if !probe.reconciled {
		t.Error("a failed stop stopped the restart; the core is now down with nothing bringing it back")
	}
}

func TestRestartCoreRefusesWhatItCannotRestart(t *testing.T) {
	initTestDB(t)
	withWiredCore(t, &restartProbeCore{})

	if err := (&ServerService{}).RestartCore(context.Background(), "nope"); !errors.Is(err, ErrCoreUnknown) {
		t.Fatalf("unknown core = %v, want %v", err, ErrCoreUnknown)
	}
}

// withWiredCore points the runtime manager at a registry holding only probe, and
// restores whatever was wired before.
func withWiredCore(t *testing.T, probe core.Core) {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(probe); err != nil {
		t.Fatalf("register the probe core: %v", err)
	}
	previous := runtime.GetManager()
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))
	t.Cleanup(func() { runtime.SetManager(previous) })
}

// restartProbeCore serves a kind no real core claims and records the order it
// was driven in.
type restartProbeCore struct {
	stopped    bool
	reconciled bool
	order      string
	stopErr    error
}

func (p *restartProbeCore) Describe() core.Descriptor {
	return core.Descriptor{
		ID: "restart-probe", TitleKey: "cores.restartprobe.title",
		Caps: core.Capabilities{
			UserHotAdd: core.No(), PerUserStats: core.No(), QuotaPushdown: core.No(),
			OnlineUsers: core.No(), ShareLink: core.No(),
		},
	}
}

func (p *restartProbeCore) Kinds() []core.Kind { return []core.Kind{"restart-probe"} }

func (p *restartProbeCore) Preflight(context.Context) error { return nil }

func (p *restartProbeCore) StopAll(context.Context) error {
	p.stopped = true
	p.order += "stop"
	return p.stopErr
}

func (p *restartProbeCore) Reconcile(context.Context, []core.Instance) error {
	p.reconciled = true
	if p.order != "" {
		p.order += ","
	}
	p.order += "reconcile"
	return nil
}
