package job

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"

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

	// bannedSeen carries each banned pair's last timestamp across passes. A core
	// refreshes lastSeen only on new activity, so a frozen value is a dead
	// connection it has not reaped, and re-reporting it would ban forever.
	bannedSeen map[string]int64
}

func NewCorePolicyJob() *CorePolicyJob { return &CorePolicyJob{} }

func (j *CorePolicyJob) Run() {
	ctx := context.Background()
	j.convergeShaping(ctx)
	j.enforceIPLimits(ctx)
}

// convergeShaping brings the kernel to the rate every client's ladder currently
// entitles them to. Fail-open: a throttle that did not land costs bandwidth,
// while a throttle applied to everyone is a support storm.
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
	if !isFail2BanEnabled() {
		return
	}
	scan := j.policyService.ObserveSessions(ctx)
	if len(scan.ByEmail) == 0 {
		return
	}
	// Without fail2ban nothing can act on the log line, so the pass collects the
	// addresses for the panel to display and enforces nothing.
	enforce := service.AnyClientHasAnIPLimit() && fail2BanIsInstalled()

	verdicts, err := j.policyService.EvaluateIPLimits(scan, enforce)
	if err != nil {
		logger.Warning("ip limit evaluation failed:", err)
		return
	}
	j.reportOverLimit(ctx, verdicts)
}

/*
reportOverLimit writes the fail2ban line and cuts the sessions that can be cut.

Both appliers run after the evaluation's transaction has committed, so their
filesystem and network round trips never extend a write lock the node sync also
takes.
*/
func (j *CorePolicyJob) reportOverLimit(ctx context.Context, verdicts []service.IPLimitVerdict) {
	if j.bannedSeen == nil {
		j.bannedSeen = make(map[string]int64)
	}
	type report struct {
		verdict service.IPLimitVerdict
		banned  []policy.Observation
	}
	var reports []report
	for _, verdict := range verdicts {
		actionable, next := policy.AdvancedSince(verdict.Email, verdict.Ban, j.bannedSeen)
		j.bannedSeen = next
		if len(actionable) > 0 {
			reports = append(reports, report{verdict: verdict, banned: actionable})
		}
	}
	if len(reports) == 0 {
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
			banLogger.Printf(IPLimitLogFormat, entry.verdict.Email, banned.IP, banned.LastSeenUnixMilli/1000)
		}
		logger.Infof("[LIMIT_IP] Client %s: reported %d address(es) over its cap", entry.verdict.Email, len(entry.banned))
		if !entry.verdict.Bounce {
			continue
		}
		if err := j.policyService.BounceClient(ctx, entry.verdict.Inbound, entry.verdict.Email); err != nil {
			logger.Warningf("[LIMIT_IP] Failed to cut %s off: %v", entry.verdict.Email, err)
		}
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

func fail2BanIsInstalled() bool {
	if !isFail2BanEnabled() {
		return false
	}
	return exec.CommandContext(context.Background(), "fail2ban-client", "-h").Run() == nil
}

func isFail2BanEnabled() bool {
	value, ok := os.LookupEnv("PUI_ENABLE_FAIL2BAN")
	return !ok || value == "true"
}
