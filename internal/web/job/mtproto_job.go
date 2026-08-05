package job

import (
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
MtprotoJob keeps the mtg sidecars converged on the enabled mtproto inbounds and
restarts any that died.

Supervision only — the billing moved into the one traffic job that collects from
every core. The two did not generalise together: this core self-heals by
reconciling its whole desired set, which is cheap for a handful of sidecars,
while Xray self-heals through a 1s crash check because rebuilding its config is
the most expensive thing the panel does.
*/
type MtprotoJob struct {
	inboundService service.InboundService
}

// NewMtprotoJob creates a new mtproto reconcile job instance.
func NewMtprotoJob() *MtprotoJob {
	return new(MtprotoJob)
}

// Run converges the running mtg processes on the desired mtproto inbounds.
func (j *MtprotoJob) Run() {
	desired, err := j.inboundService.DesiredMtprotoInstances()
	if err != nil {
		logger.Warning("mtproto job: get desired instances failed:", err)
		return
	}
	if err := mtproto.GetManager().Reconcile(desired); err != nil {
		logger.Warning("mtproto job: reconcile failed:", err)
	}
}
