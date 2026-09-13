package job

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Arman2122/p-ui/internal/banset"
	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
CorePolicyJob applies the panel's product rules to whatever the cores report.

One registry-driven pass, so a core gets speed ladders and IP limits by
declaring what it can do rather than by anyone writing a job for it. It replaces
check_client_ip_job, which asked one core directly, skipped the whole run when
that core was down, and enforced by reaching past the runtime into its API.

Level-triggered throughout: nothing here remembers what it did last pass, so a
missed convergence, a core restart or an operator's own tc are all repaired on
the next tick with no special case.
*/
type CorePolicyJob struct {
	policyService  service.PolicyService
	settingService service.SettingService
	xrayService    service.XrayService
	inboundService service.InboundService
}

func NewCorePolicyJob() *CorePolicyJob { return &CorePolicyJob{} }

func (j *CorePolicyJob) Run() {
	ctx := context.Background()
	j.convergePlans(ctx, time.Now())
	if disabled := enforceProductQuotas(ctx); len(disabled) > 0 {
		cutDisabledClients(disabled, &j.settingService, &j.xrayService, &j.inboundService)
	}
	j.convergeShaping(ctx)
	j.convergeRates(ctx)
	j.enforceIPLimits(ctx)
}

func (j *CorePolicyJob) convergePlans(ctx context.Context, now time.Time) {
	result, err := j.policyService.ConvergePlanCycles(ctx, now)
	if err != nil {
		logger.Warning("plan cycle convergence failed:", err)
	}
	if result.NeedRestart {
		(&service.XrayService{}).SetToNeedRestart()
	}
	for _, notice := range result.Notices {
		audience := 0
		if notice.OwnerUserID != nil {
			audience = *notice.OwnerUserID
		}
		if _, err := (service.ActivityService{}).Record(ctx, model.ActivityEvent{
			AudienceUserID: audience, Kind: "client.plan_depleted", Level: "warning",
			TitleKey: "events.planDepleted.title", MessageKey: "events.planDepleted.body", CreatedAt: now.UnixMilli(),
		}, map[string]string{
			"email": notice.Email, "plan": notice.PlanName,
			"used": strconv.FormatInt(notice.UsedBytes, 10), "quota": strconv.FormatInt(notice.QuotaBytes, 10),
		}); err != nil {
			logger.Warning("persist plan depletion notice failed:", err)
		}
		if EventBus != nil {
			EventBus.Publish(eventbus.Event{Type: eventbus.EventClientPlanDepleted, Source: notice.Email, Timestamp: now, Data: notice})
		}
	}
}

// convergeShaping brings the kernel to the rate each ladder entitles a client to.
// Fail-open: a missed throttle costs bandwidth, a universal one is a support storm.
func (j *CorePolicyJob) convergeShaping(ctx context.Context) {
	err := j.policyService.ConvergeShaping(ctx)
	switch {
	case err == nil:
	case shapingIsAHostFact(err):
		logger.Debug("shaping is not available on this host:", err)
	default:
		logger.Warning("shaping convergence failed:", err)
	}
}

// convergeRates hands the cores that throttle in-process the rate each ladder
// entitles a client to. Fail-open for the reason convergeShaping is.
func (j *CorePolicyJob) convergeRates(ctx context.Context) {
	if err := j.policyService.ConvergeRates(ctx); err != nil {
		logger.Warning("rate convergence failed:", err)
	}
}

// shapingIsAHostFact separates a host that cannot carry the mechanism — the same
// answer on every tick, forever — from drift the next pass may repair.
func shapingIsAHostFact(err error) bool {
	return errors.Is(err, shaping.ErrPlatformUnsupported) ||
		errors.Is(err, shaping.ErrPermission) ||
		errors.Is(err, shaping.ErrModuleMissing) ||
		errors.Is(err, shaping.ErrNoDevice)
}

func bansAreAHostFact(err error) bool {
	return errors.Is(err, banset.ErrPlatformUnsupported) ||
		errors.Is(err, banset.ErrPermission) ||
		errors.Is(err, banset.ErrNoNft)
}

/*
enforceIPLimits observes, decides and acts. Observation is per-core fail-open
inside the service, so one core being down does not silence the rest, and the
toggle gates the ban alone: returning early on it froze observation too, and
with it the client IP log and both info modals (F305). The firewall is converged
even when nobody was seen, since a lapsed ban leaves on this pass either way.
*/
func (j *CorePolicyJob) enforceIPLimits(ctx context.Context) {
	now := time.Now()
	scan := j.policyService.ObserveSessions(ctx)
	var verdicts []service.IPLimitVerdict
	if len(scan.ByEmail) > 0 {
		var err error
		if verdicts, err = j.policyService.EvaluateIPLimits(scan); err != nil {
			logger.Warning("ip limit evaluation failed:", err)
			verdicts = nil
		}
	}
	j.reportOverLimit(ctx, verdicts, now)
}

/*
reportOverLimit carries out the verdicts: it cuts the sessions a core can end,
records a ban for the addresses only the firewall reaches, and converges the
firewall on every ban this host holds.

All three run after the evaluation's transaction has committed, so their network
and filesystem work never extends a write lock the node sync also takes. Each is
level-triggered and idempotent: a cut run every pass drops a straggler still
connected when a cap was lowered and then finds nothing; a ban row that exists
is the memory that the pair is banned, so it is not written again; and the
converge prunes what the kernel has already let lapse.
*/
func (j *CorePolicyJob) reportOverLimit(ctx context.Context, verdicts []service.IPLimitVerdict, now time.Time) {
	byEmail := make(map[string]service.IPLimitVerdict, len(verdicts))
	for _, verdict := range verdicts {
		byEmail[verdict.Email] = verdict
		for _, cut := range verdict.Cuts {
			n, err := j.policyService.CutOverLimit(ctx, cut, verdict.Email)
			if err != nil {
				logger.Warningf("[LIMIT_IP] failed to cut %s from %s: %v", verdict.Email, cut.Source, err)
				continue
			}
			if n > 0 {
				logger.Infof("[LIMIT_IP] cut %d session(s) of %s from %s over its cap", n, verdict.Email, cut.Source)
				j.publishDeviceLimit(verdict, cut.Source, "cut", "", 0, now)
			}
		}
	}

	if config.IPLimitBansEnabled() {
		created, err := j.policyService.RecordBans(ctx, verdicts, now)
		if err != nil {
			logger.Warning("[LIMIT_IP] recording bans failed:", err)
		}
		var nodeNames map[string]string
		for _, row := range created {
			where := ""
			if row.NodeGuid != "" {
				if nodeNames == nil {
					nodeNames, _ = j.policyService.NodeNames(ctx)
				}
				if where = nodeNames[row.NodeGuid]; where == "" {
					where = row.NodeGuid
				}
			}
			action := "ban"
			if row.Reason == service.BanReasonSharedSkipped {
				action = "reported"
			}
			j.publishDeviceLimit(byEmail[row.ClientEmail], row.Source, action, where, row.Until, now)
		}
	} else if held := countBans(verdicts); held > 0 {
		logger.Debugf("[LIMIT_IP] %d address(es) the firewall would drop, held (banning is off)", held)
	}

	err := j.policyService.ConvergeBans(ctx, now)
	switch {
	case err == nil:
	case bansAreAHostFact(err):
		logger.Debug("firewall bans are not available on this host:", err)
	default:
		logger.Warning("ban convergence failed:", err)
	}
}

// publishDeviceLimit is one event per act, not per pass: a cut that ended
// something, or a ban row that was created, which the row's existence makes once.
func (j *CorePolicyJob) publishDeviceLimit(verdict service.IPLimitVerdict, source, action, where string, until int64, now time.Time) {
	if EventBus == nil {
		return
	}
	EventBus.Publish(eventbus.Event{
		Type:      eventbus.EventClientDeviceLimit,
		Source:    verdict.Email,
		Timestamp: now,
		Data: &eventbus.ClientDeviceLimitData{
			Email: verdict.Email, IP: source, Limit: verdict.Limit,
			Devices: len(verdict.Keep) + len(verdict.Over),
			Action:  action, Where: where, Until: until,
		},
	})
}

func countBans(verdicts []service.IPLimitVerdict) int {
	n := 0
	for _, verdict := range verdicts {
		if len(verdict.BanPorts) > 0 {
			n += len(verdict.Ban)
		}
	}
	return n
}
