package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Arman2122/p-ui/internal/util/proc"
)

// forceStopTimeout is the wait after SIGKILL, the same both cores used.
const forceStopTimeout = 2 * time.Second

// Exec runs daemons as children of the panel: what internal/xray and
// internal/mtproto did on their own, hoisted. A child dies with the panel, which
// is why this is the fallback and systemd the target.
type Exec struct {
	mu    sync.Mutex
	units map[string]*execUnit
}

type execUnit struct {
	spec Spec

	mu              sync.RWMutex
	cmd             *exec.Cmd
	done            chan struct{}
	exitErr         error
	started         time.Time
	intentionalStop atomic.Bool
}

// NewExec returns a backend with nothing running.
func NewExec() *Exec {
	return &Exec{units: map[string]*execUnit{}}
}

func (e *Exec) Name() string { return string(ModeExec) }

func (e *Exec) Ensure(ctx context.Context, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.User != "" {
		return fmt.Errorf("supervisor: exec backend cannot run %s as %q; it runs daemons as the panel's own user", spec.Name, spec.User)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur, ok := e.units[spec.Name]; ok {
		if cur.running() && cur.spec.Hash() == spec.Hash() {
			return nil
		}
		if cur.running() {
			if err := cur.stop(); err != nil {
				return err
			}
		}
		delete(e.units, spec.Name)
	}
	unit := &execUnit{spec: spec}
	if err := unit.start(); err != nil {
		return err
	}
	e.units[spec.Name] = unit
	return nil
}

func (e *Exec) Status(_ context.Context, name string) (Status, error) {
	e.mu.Lock()
	unit, ok := e.units[name]
	e.mu.Unlock()
	if !ok {
		return Status{State: StateStopped}, ErrNotRunning
	}
	return unit.status(), nil
}

func (e *Exec) Reload(_ context.Context, name string) error {
	e.mu.Lock()
	cur, ok := e.units[name]
	e.mu.Unlock()
	if !ok || !cur.running() {
		return ErrNotRunning
	}
	cur.mu.RLock()
	defer cur.mu.RUnlock()
	return cur.cmd.Process.Signal(syscall.SIGHUP)
}

func (e *Exec) Stop(_ context.Context, name string) error {
	e.mu.Lock()
	unit, ok := e.units[name]
	if ok {
		delete(e.units, name)
	}
	e.mu.Unlock()
	if !ok || !unit.running() {
		return nil
	}
	return unit.stop()
}

func (e *Exec) List(context.Context) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]string, 0, len(e.units))
	for name, unit := range e.units {
		if unit.running() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Close stops every child: the panel is going away and they would die with it.
func (e *Exec) Close() error {
	e.mu.Lock()
	units := e.units
	e.units = map[string]*execUnit{}
	e.mu.Unlock()
	var errs []error
	for _, unit := range units {
		if unit.running() {
			errs = append(errs, unit.stop())
		}
	}
	return errors.Join(errs...)
}

func (u *execUnit) start() error {
	cmd := exec.CommandContext(context.Background(), u.spec.Exec[0], u.spec.Exec[1:]...)
	if len(u.spec.Env) > 0 {
		cmd.Env = append(os.Environ(), u.spec.Env...)
	}
	cmd.Dir = u.spec.WorkingDir
	if u.spec.Output != nil {
		cmd.Stdout, cmd.Stderr = u.spec.Output, u.spec.Output
	}
	done := make(chan struct{})
	u.mu.Lock()
	u.cmd, u.done, u.exitErr, u.started = cmd, done, nil, time.Now()
	u.mu.Unlock()
	u.intentionalStop.Store(false)
	if err := cmd.Start(); err != nil {
		close(done)
		u.mu.Lock()
		u.cmd = nil
		u.mu.Unlock()
		return fmt.Errorf("supervisor: start %s: %w", u.spec.Name, err)
	}
	go u.wait(cmd, done)
	return nil
}

func (u *execUnit) wait(cmd *exec.Cmd, done chan struct{}) {
	defer close(done)
	err := cmd.Wait()
	if flusher, ok := u.spec.Output.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	if u.intentionalStop.Load() {
		return
	}
	if err == nil {
		err = errors.New("exited")
	}
	u.mu.Lock()
	u.exitErr = err
	u.mu.Unlock()
	if u.spec.OnExit != nil {
		u.spec.OnExit(err)
	}
}

func (u *execUnit) running() bool {
	u.mu.RLock()
	cmd, done := u.cmd, u.done
	u.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

func (u *execUnit) stop() error {
	u.intentionalStop.Store(true)
	u.mu.RLock()
	cmd, done := u.cmd, u.done
	u.mu.RUnlock()
	return proc.Stop(cmd, done, u.spec.effectiveStop(), forceStopTimeout, u.spec.Name)
}

func (u *execUnit) status() Status {
	u.mu.RLock()
	cmd, exitErr, started := u.cmd, u.exitErr, u.started
	u.mu.RUnlock()
	st := Status{SpecHash: u.spec.Hash(), Since: started}
	switch {
	case u.running():
		st.State = StateRunning
		st.PID = cmd.Process.Pid
	case exitErr != nil:
		st.State = StateFailed
		st.Result = exitErr.Error()
	default:
		st.State = StateStopped
		st.Result = "success"
	}
	return st
}
