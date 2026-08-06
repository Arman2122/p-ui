package web

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// stoppableCore is a core registered under a kind the shutdown path has never
// heard of, so only a registry-driven shutdown can reach it.
type stoppableCore struct {
	kind    core.Kind
	stopped int
}

func (c *stoppableCore) Kinds() []core.Kind { return []core.Kind{c.kind} }

func (c *stoppableCore) Describe() core.Descriptor {
	return core.Descriptor{
		ID: c.kind,
		Caps: core.Capabilities{
			UserHotAdd: core.No(), PerUserStats: core.No(), QuotaPushdown: core.No(),
			OnlineUsers: core.No(), ShareLink: core.No(),
		},
	}
}

func (c *stoppableCore) Preflight(context.Context) error                  { return nil }
func (c *stoppableCore) Reconcile(context.Context, []core.Instance) error { return nil }

func (c *stoppableCore) StopAll(context.Context) error {
	c.stopped++
	return nil
}

// Shutdown used to name the one core it knew about, so every other core's
// processes outlived the panel that started them.
func TestStopCoresStopsEveryRegisteredCore(t *testing.T) {
	first := &stoppableCore{kind: "supervised-first"}
	second := &stoppableCore{kind: "supervised-second"}
	registry := core.NewRegistry()
	for _, registered := range []*stoppableCore{first, second} {
		if err := registry.Register(registered); err != nil {
			t.Fatalf("register %s: %v", registered.kind, err)
		}
	}

	(&Server{cores: registry}).stopCores()

	for _, registered := range []*stoppableCore{first, second} {
		if registered.stopped != 1 {
			t.Errorf("core %q was stopped %d times, want exactly 1", registered.kind, registered.stopped)
		}
	}
}
