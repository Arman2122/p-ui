package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

// Systemd runs each daemon as a transient service unit under the system
// manager. The unit outlives the panel, so a panel restart or an ACME renewal
// no longer drops a tunnel, and the panel re-adopts it by name on the way back.
type Systemd struct {
	conn *sd.Conn
	// jobTimeout bounds how long a start or stop job may take to be reported.
	jobTimeout time.Duration
}

// NewSystemd connects to the system manager. A non-root panel needs the polkit
// rule the installer ships, or every call answers "access denied".
func NewSystemd(ctx context.Context) (*Systemd, error) {
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("supervisor: systemd: %w", err)
	}
	return &Systemd{conn: conn, jobTimeout: 30 * time.Second}, nil
}

func (s *Systemd) Name() string { return string(ModeSystemd) }

func (s *Systemd) Close() error {
	s.conn.Close()
	return nil
}

func unitFile(name string) string { return name + ".service" }

var specMarker = regexp.MustCompile(`\[spec:([0-9a-f]{16})\]`)

// describe carries the spec hash inside the unit's Description, which is the
// one free-form property a transient unit keeps for its whole life.
func describe(spec Spec) string {
	desc := spec.Description
	if desc == "" {
		desc = spec.Name
	}
	return fmt.Sprintf("%s [spec:%s]", desc, spec.Hash())
}

func hashFromDescription(desc string) string {
	if m := specMarker.FindStringSubmatch(desc); m != nil {
		return m[1]
	}
	return ""
}

// transientProperties is the Spec in systemd's words. Pure, so the translation
// is tested without a manager; the e2e test proves the manager accepts it.
func transientProperties(spec Spec) ([]sd.Property, error) {
	mask, err := capabilityMask(spec.Capabilities)
	if err != nil {
		return nil, err
	}
	slice := spec.Slice
	if slice == "" {
		slice = DefaultSlice
	}
	restart := spec.Restart
	if restart == "" {
		restart = RestartOnFailure
	}
	usec := func(d time.Duration) uint64 { return uint64(d / time.Microsecond) }
	props := []sd.Property{
		sd.PropDescription(describe(spec)),
		sd.PropType("simple"),
		sd.PropExecStart(spec.Exec, true),
		sd.PropSlice(slice),
		{Name: "Restart", Value: godbus.MakeVariant(string(restart))},
		{Name: "KillSignal", Value: godbus.MakeVariant(int32(spec.killSignal()))},
		{Name: "TimeoutStopUSec", Value: godbus.MakeVariant(usec(spec.effectiveStop()))},
		// A stopped unit is garbage-collected; a failed one is kept so Status can
		// read why, and the next Ensure resets it before starting again.
		{Name: "CollectMode", Value: godbus.MakeVariant("inactive")},
	}
	if spec.RestartAfter > 0 {
		props = append(props, sd.Property{Name: "RestartUSec", Value: godbus.MakeVariant(usec(spec.RestartAfter))})
	}
	if spec.StartLimitInterval > 0 {
		props = append(props, sd.Property{Name: "StartLimitIntervalUSec", Value: godbus.MakeVariant(usec(spec.StartLimitInterval))})
	}
	if spec.StartLimitBurst > 0 {
		props = append(props, sd.Property{Name: "StartLimitBurst", Value: godbus.MakeVariant(spec.StartLimitBurst)})
	}
	if len(spec.Env) > 0 {
		props = append(props, sd.Property{Name: "Environment", Value: godbus.MakeVariant(spec.Env)})
	}
	if spec.WorkingDir != "" {
		props = append(props, sd.Property{Name: "WorkingDirectory", Value: godbus.MakeVariant(spec.WorkingDir)})
	}
	if spec.User != "" {
		props = append(props, sd.Property{Name: "User", Value: godbus.MakeVariant(spec.User)})
	}
	if mask != 0 {
		props = append(props, sd.Property{Name: "AmbientCapabilities", Value: godbus.MakeVariant(mask)})
	}
	return props, nil
}

func (s *Systemd) Ensure(ctx context.Context, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	props, err := transientProperties(spec)
	if err != nil {
		return err
	}
	unit := unitFile(spec.Name)
	cur, err := s.Status(ctx, spec.Name)
	switch {
	case err == nil && cur.State == StateRunning && cur.SpecHash == spec.Hash():
		return nil
	case err == nil && cur.State == StateRunning:
		if err := s.Stop(ctx, spec.Name); err != nil {
			return err
		}
	case err == nil && cur.State == StateFailed:
		if err := s.conn.ResetFailedUnitContext(ctx, unit); err != nil {
			return fmt.Errorf("supervisor: reset %s: %w", unit, err)
		}
	case err != nil && !errors.Is(err, ErrNotRunning):
		return err
	}
	return s.await(ctx, unit, "start", func(ch chan<- string) (int, error) {
		return s.conn.StartTransientUnitContext(ctx, unit, "replace", props, ch)
	})
}

func (s *Systemd) Reload(ctx context.Context, name string) error {
	cur, err := s.Status(ctx, name)
	if err != nil {
		return err
	}
	if cur.State != StateRunning {
		return ErrNotRunning
	}
	return s.conn.KillUnitWithTarget(ctx, unitFile(name), sd.All, int32(syscall.SIGHUP))
}

func (s *Systemd) Stop(ctx context.Context, name string) error {
	unit := unitFile(name)
	err := s.await(ctx, unit, "stop", func(ch chan<- string) (int, error) {
		return s.conn.StopUnitContext(ctx, unit, "replace", ch)
	})
	if err != nil && strings.Contains(err.Error(), "NoSuchUnit") {
		return nil
	}
	return err
}

// await runs one manager job and waits for its result word: "done" is success,
// anything else ("failed", "timeout", "canceled", "dependency") is not.
func (s *Systemd) await(ctx context.Context, unit, verb string, run func(chan<- string) (int, error)) error {
	ch := make(chan string, 1)
	if _, err := run(ch); err != nil {
		return fmt.Errorf("supervisor: %s %s: %w", verb, unit, err)
	}
	timer := time.NewTimer(s.jobTimeout)
	defer timer.Stop()
	select {
	case result := <-ch:
		if result != "done" && (verb != "stop" || result != "canceled") {
			return fmt.Errorf("supervisor: %s %s: job %s", verb, unit, result)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("supervisor: %s %s: no answer from systemd after %s", verb, unit, s.jobTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Systemd) Status(ctx context.Context, name string) (Status, error) {
	unit := unitFile(name)
	units, err := s.conn.ListUnitsByNamesContext(ctx, []string{unit})
	if err != nil {
		return Status{}, fmt.Errorf("supervisor: list %s: %w", unit, err)
	}
	if len(units) == 0 || units[0].LoadState == "not-found" || units[0].ActiveState == "inactive" {
		return Status{State: StateStopped}, ErrNotRunning
	}
	props, err := s.conn.GetUnitPropertiesContext(ctx, unit)
	if err != nil {
		return Status{}, fmt.Errorf("supervisor: properties of %s: %w", unit, err)
	}
	svc, err := s.conn.GetUnitTypePropertiesContext(ctx, unit, "Service")
	if err != nil {
		return Status{}, fmt.Errorf("supervisor: service properties of %s: %w", unit, err)
	}
	st := Status{
		SpecHash: hashFromDescription(str(props["Description"])),
		PID:      int(num(svc["MainPID"])),
		Restarts: uint32(num(svc["NRestarts"])),
		Result:   str(svc["Result"]),
	}
	if since := num(props["ActiveEnterTimestamp"]); since > 0 {
		st.Since = time.UnixMicro(int64(since))
	}
	switch units[0].ActiveState {
	case "active", "activating", "reloading":
		st.State = StateRunning
	case "failed":
		st.State = StateFailed
	default:
		st.State = StateStopped
	}
	return st, nil
}

// Logs asks the journal, where a transient unit's output goes; short-iso stamps
// each line the way the exec backend's ring does. An unknown unit has no lines.
func (s *Systemd) Logs(ctx context.Context, name string, n int) ([]string, error) {
	if n <= 0 {
		n = ringLines
	}
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unitFile(name), "-n", strconv.Itoa(n), "-o", "short-iso", "--no-pager", "-q").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("supervisor: journalctl %s: %s", name, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("supervisor: journalctl %s: %w", name, err)
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// List names every unit under the panel's prefix, whatever state it is in, so
// a failed leftover from an inbound the panel deleted is swept too.
func (s *Systemd) List(ctx context.Context) ([]string, error) {
	units, err := s.conn.ListUnitsByPatternsContext(ctx, nil, []string{unitPrefix + "*.service"})
	if err != nil {
		return nil, fmt.Errorf("supervisor: list units: %w", err)
	}
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, strings.TrimSuffix(u.Name, ".service"))
	}
	return names, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case uint32:
		return uint64(n)
	case int64:
		return uint64(n)
	case int32:
		return uint64(n)
	}
	return 0
}
