package controller

import (
	"os"
	"strings"
	"testing"
)

/*
A settings save must move the cron entries the settings decide.

Four jobs are scheduled once, inside startTask, from settings read at process
start, and nothing ever touched the cron again:

the CPU watchdog and the memory watchdog, each gated on whether any notifier
wants that alert, so switching one on in Settings scheduled nothing and produced
no alerts at all until a restart; the Telegram stats report, whose schedule comes
from tgRunTime, so changing the report time reported success, hot-reloaded the
bot and left the report firing on the old schedule; and the callback-hash
cleanup that exists only because the bot does.

The panel said yes and did nothing, in a place where the operator has no way to
tell. Both findings are the same mechanism. F341 / F397.

Driven at the source: the hook needs a live cron, a live settings service and a
running server, none of which decides whether the handler calls it.
*/
func TestASettingsSaveReschedulesTheJobsSettingsDecide(t *testing.T) {
	body := readControllerSource(t, "setting.go")

	if !strings.Contains(body, "var rescheduleSettingJobsFunc func()") {
		t.Fatal("the reschedule hook is gone; this guard is now vacuous")
	}

	start := strings.Index(body, "func (a *SettingController) updateSetting(")
	if start < 0 {
		t.Fatal("updateSetting was renamed; this guard is now vacuous")
	}
	end := strings.Index(body[start:], "\n}\n")
	handler := body[start : start+end]

	if !strings.Contains(handler, "rescheduleSettingJobsFunc()") {
		t.Error("saving settings does not move the cron entries those settings decide, so enabling an alert schedules no watchdog and a new report time never takes effect")
	}
}

// And the web layer really registers it, or the hook is nil and the handler's
// call is a no-op that reads like a fix.
func TestTheWebLayerRegistersTheRescheduleHook(t *testing.T) {
	raw, err := os.ReadFile("../web.go")
	if err != nil {
		t.Fatalf("read web.go: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, "controller.SetRescheduleSettingJobsFunc(s.rescheduleSettingJobs)") {
		t.Fatal("nothing registers the reschedule hook, so the handler's call does nothing")
	}
	if !strings.Contains(body, "func (s *Server) rescheduleSettingJobs()") {
		t.Fatal("rescheduleSettingJobs is gone; this guard is now vacuous")
	}

	fn := body[strings.Index(body, "func (s *Server) rescheduleSettingJobs()"):]
	fn = fn[:strings.Index(fn, "\n}\n")]
	// Removing first is what makes it idempotent: called on every save, it has
	// to converge rather than stack another copy of each job onto the cron.
	if !strings.Contains(fn, "s.cron.Remove(id)") {
		t.Error("the reschedule does not remove the previous entries, so every settings save adds another copy of each job")
	}
	for _, added := range []string{"NewCheckCpuJob()", "NewCheckMemJob()", "NewStatsNotifyJob()", "NewCheckHashStorageJob()"} {
		if !strings.Contains(fn, added) {
			t.Errorf("%s is not rescheduled, so its setting still needs a restart", added)
		}
	}
}
