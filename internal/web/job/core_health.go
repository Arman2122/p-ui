package job

import (
	"context"
	"fmt"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/eventbus"
)

/*
Crash detection lives here, in the pass that also repairs: one place, every
core alike. A unit the backend reports failed, or one systemd restarted on its
own since the last pass (Restarts grew — exec never sets it, but exec never
restarts either, so failed is its whole signal), is a crash. It is published
once per outage rather than once per pass or per restart, and the all-clear
once the same daemon runs and answers again. The panel-converged core keeps its
own signal (xray.crash, a 1 s job) and is left out so one death is not two alerts.
*/
type instanceMark struct {
	restarts uint32
	crashed  bool
}

// observeHealth reads one core before the reconcile that would repair it, so a
// death under exec is seen as failed and not only as a fresh start.
func (j *CoreSuperviseJob) observeHealth(ctx context.Context, bound *core.Bound) {
	if bound.Health == nil || EventBus == nil {
		return
	}
	id := string(bound.Core.Describe().ID)
	ctx, cancel := context.WithTimeout(ctx, healthObserveBudget)
	defer cancel()
	h, err := bound.Health.Health(ctx)
	if err != nil {
		return
	}
	units := h.Instances
	if len(units) == 0 {
		units = []core.InstanceHealth{{State: h.State, Ready: h.Ready, Detail: h.Detail, Restarts: h.Restarts}}
	}
	if j.marks == nil {
		j.marks = map[string]instanceMark{}
	}
	seen := map[string]bool{}
	for _, u := range units {
		key := fmt.Sprintf("%s/%d", id, u.InstanceID)
		seen[key] = true
		prev, known := j.marks[key]
		mark := instanceMark{restarts: u.Restarts, crashed: prev.crashed}
		crashedNow := u.State == core.HealthFailed || (known && u.Restarts > prev.restarts)
		switch {
		case crashedNow && !prev.crashed:
			mark.crashed = true
			EventBus.Publish(eventbus.Event{Type: eventbus.EventCoreCrash, Source: unitSource(id, u), Timestamp: time.Now(), Data: &eventbus.CoreCrashData{CoreID: id, InstanceID: u.InstanceID, Err: crashDetail(u, prev)}})
		case prev.crashed && u.State == core.HealthRunning && u.Ready:
			mark.crashed = false
			EventBus.Publish(eventbus.Event{Type: eventbus.EventCoreUp, Source: unitSource(id, u), Timestamp: time.Now(), Data: &eventbus.CoreCrashData{CoreID: id, InstanceID: u.InstanceID}})
		}
		j.marks[key] = mark
	}
	for key := range j.marks {
		if len(key) > len(id) && key[:len(id)+1] == id+"/" && !seen[key] {
			delete(j.marks, key)
		}
	}
}

// healthObserveBudget bounds the probes of one core per pass.
const healthObserveBudget = 3 * time.Second

func unitSource(id string, u core.InstanceHealth) string {
	if u.InstanceID == 0 {
		return id
	}
	return fmt.Sprintf("%s inbound %d", id, u.InstanceID)
}

// crashDetail is what the daemon said, or how often the supervisor has had to
// bring it back when it said nothing.
func crashDetail(u core.InstanceHealth, prev instanceMark) string {
	if u.Detail != "" {
		return u.Detail
	}
	if u.Restarts > prev.restarts {
		return fmt.Sprintf("restarted by the supervisor (%d times so far)", u.Restarts)
	}
	return ""
}
