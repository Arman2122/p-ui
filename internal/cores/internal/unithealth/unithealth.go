// Package unithealth reads one supervised unit into the contract's health
// shape, so the daemon cores describe a dead unit in the same words.
package unithealth

import (
	"context"
	"errors"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// Of maps the backend's status of unit name; a death carries the unit's last
// line, since Status.Result names only the exit. Ready is the caller's to set.
func Of(ctx context.Context, b supervisor.Backend, name string) (core.InstanceHealth, error) {
	st, err := b.Status(ctx, name)
	switch {
	case errors.Is(err, supervisor.ErrNotRunning):
		return core.InstanceHealth{State: core.HealthStopped, Detail: "no unit"}, nil
	case err != nil:
		return core.InstanceHealth{}, err
	}
	h := core.InstanceHealth{PID: st.PID, Restarts: st.Restarts}
	if !st.Since.IsZero() {
		h.SinceUnixMilli = st.Since.UnixMilli()
	}
	switch st.State {
	case supervisor.StateRunning:
		h.State = core.HealthRunning
	case supervisor.StateFailed:
		h.State, h.Detail = core.HealthFailed, lastWords(ctx, b, name, st.Result)
	default:
		h.State, h.Detail = core.HealthStopped, st.Result
	}
	return h, nil
}

// lastWords joins the exit with the unit's final line, which is the reason
// more often than the exit code is.
func lastWords(ctx context.Context, b supervisor.Backend, name, result string) string {
	lines, err := b.Logs(ctx, name, 1)
	if err != nil || len(lines) == 0 {
		return result
	}
	return result + ": " + lines[0]
}
