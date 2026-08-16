package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// The refusals an operator has to be able to tell apart, and a test has to be
// able to match without comparing a sentence.
var (
	ErrCoreUnknown       = errors.New("core: no core with that id is registered")
	ErrCoreNotSupervised = errors.New("core: this core runs no daemon this panel supervises")
)

/*
RestartCore stops a core's daemons and converges them back on desired state.

Asked of the registry rather than named: /restartXrayService and its siblings are
an entire process-management API family with one core's name in the URL, so an
mtg sidecar could not be restarted from the panel at all — an operator's only
recourse was ssh and a kill.

Restart is StopAll followed by Reconcile because those are the two capabilities a
supervised core already has to implement. There is nothing protocol-specific
left: a core that runs one process per host and a core that runs one per inbound
are both served, and so is the next one.
*/
func (s *ServerService) RestartCore(ctx context.Context, coreID string) error {
	manager := runtime.GetManager()
	if manager == nil {
		return ErrCoreUnknown
	}
	bound, found := coreByID(manager.Cores(), coreID)
	if !found {
		return fmt.Errorf("%w: %q", ErrCoreUnknown, coreID)
	}

	// The panel converges this one itself, through the config it generates, so
	// reconciling an instance set would drop everything injected after it.
	if coreID == string(cores.PanelConvergedCore) {
		return s.RestartXrayService()
	}
	if bound.Supervise == nil {
		return fmt.Errorf("%w: %q", ErrCoreNotSupervised, coreID)
	}

	if err := bound.Supervise.StopAll(ctx); err != nil {
		// Reported, not returned: a daemon that would not stop cleanly is still
		// a daemon the reconcile below is about to replace.
		logger.Warning("core restart:", coreID, "did not stop cleanly:", err)
	}
	desired, err := (&InboundService{}).DesiredInstances(bound.Core.Kinds())
	if err != nil {
		return err
	}
	return bound.Supervise.Reconcile(ctx, desired)
}

// coreByID finds a registered core by the id its descriptor declares.
func coreByID(reg *core.Registry, id string) (*core.Bound, bool) {
	if reg == nil {
		return nil, false
	}
	for _, bound := range reg.Cores() {
		if string(bound.Core.Describe().ID) == id {
			return bound, true
		}
	}
	return nil, false
}
