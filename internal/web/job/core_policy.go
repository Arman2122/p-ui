package job

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/xray"
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
	policyService service.PolicyService

	// bannedSeen carries each banned pair's last timestamp across passes: a frozen
	// lastSeen is a dead connection, and re-reporting one would ban forever.
	bannedSeen map[string]int64
}

func NewCorePolicyJob() *CorePolicyJob { return &CorePolicyJob{} }

func (j *CorePolicyJob) Run() {
	ctx := context.Background()
	j.convergeShaping(ctx)
	j.convergeRates(ctx)
	j.enforceIPLimits(ctx)
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

// enforceIPLimits observes, decides and reports. Observation is per-core
// fail-open inside the service, so one core being down no longer silences the rest.
func (j *CorePolicyJob) enforceIPLimits(ctx context.Context) {
	/*
		No early return on the toggle. PUI_ENABLE_FAIL2BAN gates ENFORCEMENT, which
		it does through fail2BanIsInstalled below — returning here instead froze
		observation as well, and with it the client IP log, /clientIps and both info
		modals, for as long as the toggle was off. The tier just below this one
		already keeps observing when it cannot enforce; this now agrees with it.
	*/
	scan := j.policyService.ObserveSessions(ctx)
	if len(scan.ByEmail) == 0 {
		return
	}
	verdicts, err := j.policyService.EvaluateIPLimits(scan)
	if err != nil {
		logger.Warning("ip limit evaluation failed:", err)
		return
	}
	// A cut is the core's own mechanism and needs no firewall; only the ban line
	// waits on fail2ban, so the two actuators are gated apart.
	j.reportOverLimit(ctx, verdicts, fail2BanCanEnforce())
}

/*
reportOverLimit carries out each verdict: it cuts the sessions a core can end,
and writes the fail2ban line for the addresses only the firewall can reach.

Both run after the evaluation's transaction has committed, so their filesystem and
network round trips never extend a write lock the node sync also takes.

The cut is level-triggered and idempotent — run every pass, it drops a straggler
still connected when a cap was lowered and then returns nothing once it is gone —
so it needs no fail2ban and no dedup. The ban line does: AdvancedSince runs for
EVERY verdict, so a client back under its cap is forgotten and a frozen pair is
not re-reported once fail2ban's own ban lapses. It runs with banning off too, so
flipping the toggle on does not then report every frozen pair at once.
*/
func (j *CorePolicyJob) reportOverLimit(ctx context.Context, verdicts []service.IPLimitVerdict, canBan bool) {
	if j.bannedSeen == nil {
		j.bannedSeen = make(map[string]int64)
	}
	for _, verdict := range verdicts {
		for _, cut := range verdict.Cuts {
			n, err := j.policyService.CutOverLimit(ctx, cut, verdict.Email)
			if err != nil {
				logger.Warningf("[LIMIT_IP] failed to cut %s from %s: %v", verdict.Email, cut.Source, err)
				continue
			}
			if n > 0 {
				logger.Infof("[LIMIT_IP] cut %d session(s) of %s from %s over its cap", n, verdict.Email, cut.Source)
			}
		}
	}

	type report struct {
		email  string
		banned []policy.Observation
	}
	var reports []report
	for _, verdict := range verdicts {
		actionable, next := policy.AdvancedSince(verdict.Email, verdict.Ban, j.bannedSeen)
		j.bannedSeen = next
		if len(actionable) > 0 {
			reports = append(reports, report{email: verdict.Email, banned: actionable})
		}
	}
	if len(reports) == 0 {
		return
	}

	if !canBan {
		// The line below is the jail's input, not a log message: a jail left
		// installed would ban on it even though this host is set not to ban.
		for _, entry := range reports {
			logger.Debugf("[LIMIT_IP] Client %s: %d address(es) the firewall would ban, held (banning is off)",
				entry.email, len(entry.banned))
		}
		return
	}

	logFile, err := os.OpenFile(xray.GetIPLimitLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Errorf("failed to open IP limit log file: %s", err)
		return
	}
	defer logFile.Close()
	banLogger := log.New(logFile, "", log.LstdFlags)

	for _, entry := range reports {
		for _, banned := range entry.banned {
			banLogger.Printf(IPLimitLogFormat, entry.email, banned.IP, banned.LastSeenUnixMilli/1000)
		}
		logger.Infof("[LIMIT_IP] Client %s: %d address(es) sent to the firewall", entry.email, len(entry.banned))
	}
}

/*
IPLimitLogFormat is deployed infrastructure, not a log message.

p-ui.sh's create_iplimit_jails writes a fail2ban filter whose failregex matches
this exact wording into filter.d/p-ui-ipl.conf on every installed box, so a
reformat silently disables every jail already in the field. Timestamp is unix
SECONDS, which is what the persisted blob and every node sync also carry.
*/
const IPLimitLogFormat = "[LIMIT_IP] Email = %s || Disconnecting OLD IP = %s || Timestamp = %d"

/*
fail2BanCanEnforce answers whether a reported address would actually be banned.

The probe used to be `fail2ban-client -h`, which the client's own argparse
answers with the daemon stopped and with p-ui's jail files deleted — something
the panel's own menu offers to do. So `enforce` went true on boxes where no
packet could be dropped, and the job logged bans that never happened.
*/
func fail2BanCanEnforce() bool {
	if !isFail2BanEnabled() {
		return false
	}
	return service.Fail2banJailIsRunning()
}

func isFail2BanEnabled() bool {
	value, ok := os.LookupEnv("PUI_ENABLE_FAIL2BAN")
	return !ok || value == "true"
}
