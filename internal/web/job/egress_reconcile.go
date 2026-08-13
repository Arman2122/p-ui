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
	egressService  service.EgressService
	routingService service.RoutingService
}

func NewEgressReconcileJob() *EgressReconcileJob { return &EgressReconcileJob{} }

// Run converges once. A host that cannot be policy-routed at all says so on
// every tick, so those answers are logged at debug and never as an alarm.
func (j *EgressReconcileJob) Run() {
	ctx := context.Background()
	// The knob a fresh install ships off. Here as well as on the mutation path
	// because a reboot, an image rebuild or an operator turning it off leaves an
	// L3 inbound handshaking and forwarding nothing until something notices.
	if err := j.routingService.EnsureHostForwarding(ctx); err != nil {
		logger.Debug("host forwarding could not be enabled on this pass:", err)
	}
	err := j.egressService.Reconcile(ctx)
	switch {
	case err == nil:
	case egressReconcileIsAHostFact(err):
		logger.Debug("egress reconcile is not available on this host:", err)
	default:
		logger.Warning("egress reconcile failed:", err)
	}
}

// egressReconcileIsAHostFact separates a host that cannot carry this feature —
// the same answer on every tick, forever — from drift the next pass may repair.
func egressReconcileIsAHostFact(err error) bool {
	return errors.Is(err, egress.ErrPlatformUnsupported) ||
		errors.Is(err, egress.ErrPermission) ||
		errors.Is(err, egress.ErrFamilyUnsupported)
}
