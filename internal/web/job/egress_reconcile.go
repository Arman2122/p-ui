package job

import (
	"context"
	"errors"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
EgressReconcileJob repairs drift in the kernel state the egress rows describe.

It is not the primary path — every mutation converges synchronously — but one
object genuinely needs it: the front device belongs to the core, and a core
restart recreates the device WITHOUT restoring the `default dev <front>` route
that points at it. Until this job runs, that egress's table holds only its
blackhole, so its users are contained rather than leaking.
*/
type EgressReconcileJob struct {
	egressService service.EgressService
}

func NewEgressReconcileJob() *EgressReconcileJob { return &EgressReconcileJob{} }

// Run converges once. A host that cannot be policy-routed at all says so on
// every tick, so those two answers are logged at debug and never as an alarm.
func (j *EgressReconcileJob) Run() {
	err := j.egressService.Reconcile(context.Background())
	switch {
	case err == nil:
	case errors.Is(err, egress.ErrPlatformUnsupported), errors.Is(err, egress.ErrPermission):
		logger.Debug("egress reconcile is not available on this host:", err)
	default:
		logger.Warning("egress reconcile failed:", err)
	}
}
