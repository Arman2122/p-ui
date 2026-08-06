package job

import (
	"context"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
CoreSuperviseJob converges every registered core on the inbounds the panel wants
it running, and so restarts whatever died.

Registry-driven like the traffic job: a core is supervised because it is
registered, not because someone wrote a job for it. Supervision stays a separate
job from billing — they answer different questions at different rates — but
neither of them grows with the number of cores.
*/
type CoreSuperviseJob struct {
	inboundService service.InboundService

	// panelConverged is the core the panel reconciles through its own config
	// build; cores.PanelConvergedCore says why an instance set cannot describe it.
	panelConverged core.Kind
}

// NewCoreSuperviseJob is handed the panel-converged core rather than reading it,
// so that exception stays visible at the wiring site instead of hiding in here.
func NewCoreSuperviseJob(panelConverged core.Kind) *CoreSuperviseJob {
	return &CoreSuperviseJob{panelConverged: panelConverged}
}

// Run reconciles each registered core. One core failing is logged and skipped,
// never fatal: a daemon that will not converge must not stop the others being.
func (j *CoreSuperviseJob) Run() {
	if Cores == nil {
		return
	}
	ctx := context.Background()
	activeTags := make([]string, 0)
	desiredKnown := true
	for _, bound := range Cores.Cores() {
		id := bound.Core.Describe().ID
		if bound.Supervise == nil || id == j.panelConverged {
			continue
		}
		desired, err := j.inboundService.DesiredInstances(bound.Core.Kinds())
		if err != nil {
			logger.Warning("core", id, "desired state read failed:", err)
			desiredKnown = false
			continue
		}
		if err := bound.Supervise.Reconcile(ctx, desired); err != nil {
			logger.Warning("core", id, "reconcile failed:", err)
		}
		for _, inst := range desired {
			if len(inst.Users) > 0 {
				activeTags = append(activeTags, inst.Tag)
			}
		}
	}
	// The per-inbound online view gates on tags that moved bytes, which an idle
	// sidecar never does — a supervised inbound is active by being served at all.
	if desiredKnown {
		j.inboundService.RefreshLocalOnlineClients(nil, activeTags)
	}
}
