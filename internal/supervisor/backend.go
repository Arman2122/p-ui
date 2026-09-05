package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/logger"
)

// State is what the daemon is doing now, in the same words for both backends.
type State string

const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
)

// Status is the answer to "how is this daemon". Restarts counts the restarts
// the backend made on its own, which is how a crash surfaces without a callback.
type Status struct {
	State    State
	PID      int
	Restarts uint32
	// Result is the last exit, in the backend's words ("success", "exit status 3").
	Result   string
	SpecHash string
	Since    time.Time
}

// ErrNotRunning is Status's answer for a name the backend has never started, or
// has stopped and forgotten.
var ErrNotRunning = errors.New("supervisor: not running")

// Backend runs daemons. Ensure is idempotent: a daemon already running the same
// Spec is left alone, one running a different Spec is replaced.
type Backend interface {
	Name() string
	Ensure(ctx context.Context, spec Spec) error
	Status(ctx context.Context, name string) (Status, error)
	Stop(ctx context.Context, name string) error
	// Reload signals a running daemon to re-read its config (SIGHUP) without a
	// restart; ErrNotRunning when there is nothing to signal.
	Reload(ctx context.Context, name string) error
	// List names every unit this backend runs under the panel's prefix, so a
	// reconcile can stop what the desired set no longer contains.
	List(ctx context.Context) ([]string, error)
	// Logs returns the daemon's last n lines, oldest first and time-stamped: the
	// exec backend keeps them itself across restarts, systemd asks the journal.
	Logs(ctx context.Context, name string, n int) ([]string, error)
	Close() error
}

// Mode is the PUI_SUPERVISOR knob: one for the whole panel, never per core.
type Mode string

const (
	ModeAuto    Mode = "auto"
	ModeSystemd Mode = "systemd"
	ModeExec    Mode = "exec"
)

// Select picks the backend the environment asks for. auto takes systemd when
// the panel runs under it and the manager answers on D-Bus, else exec.
func Select(ctx context.Context) (Backend, error) {
	mode, err := config.SupervisorMode()
	if err != nil {
		return nil, err
	}
	switch Mode(mode) {
	case ModeExec:
		return NewExec(), nil
	case ModeSystemd:
		return NewSystemd(ctx)
	case ModeAuto:
		if !systemdAvailable() {
			return NewExec(), nil
		}
		backend, err := NewSystemd(ctx)
		if err != nil {
			logger.Warning("supervisor: systemd is present but unreachable, daemons run as panel children:", err)
			return NewExec(), nil
		}
		return backend, nil
	}
	return nil, fmt.Errorf("supervisor: unknown PUI_SUPERVISOR mode %q", mode)
}

func systemdAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	info, err := os.Stat("/run/systemd/system")
	return err == nil && info.IsDir()
}
