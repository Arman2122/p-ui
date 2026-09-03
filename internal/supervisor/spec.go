/*
Package supervisor runs the daemons the cores need, behind one interface with
two backends: exec, a child of the panel process, and systemd, a transient unit
that outlives the panel. The Spec speaks systemd's own vocabulary from the start
so that switching backends never touches a core adapter.
*/
package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// RestartPolicy is systemd's Restart= vocabulary; the exec backend applies it
// through the reconcile loop rather than a timer of its own.
type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// Spec describes one daemon. Everything that changes the running process is
// part of Hash, so Ensure can tell "already running as asked" from "restart".
type Spec struct {
	// Name is the unit name without ".service"; see UnitName.
	Name        string
	Description string
	Exec        []string
	Env         []string
	WorkingDir  string
	// User is who the daemon runs as; empty keeps the panel's own user. Only the
	// systemd backend can switch users, so the exec backend refuses a non-empty
	// value rather than silently running the daemon as the panel.
	User string
	// Capabilities are granted ambiently, so a non-root daemon can still bind a
	// privileged port or open netlink. Names as in capabilities(7).
	Capabilities []string

	Restart            RestartPolicy
	RestartAfter       time.Duration
	StartLimitInterval time.Duration
	StartLimitBurst    uint32
	StopTimeout        time.Duration
	KillSignal         syscall.Signal

	// Slice groups the units under one cgroup; empty means DefaultSlice.
	Slice string

	// Output receives stdout and stderr on the exec backend. The systemd
	// backend logs to the journal and ignores it.
	Output io.Writer
	// OnExit is called by the exec backend when the daemon exits on its own.
	// The systemd backend has no callback: its restarts show up in Status.
	OnExit func(err error)
}

const (
	// DefaultSlice is the cgroup every core daemon lands in under systemd.
	DefaultSlice = "p-ui-cores.slice"
	unitPrefix   = "p-ui-"
)

var unitNameRe = regexp.MustCompile(`^[A-Za-z0-9:_.\\-]+$`)

// UnitName is the one place a daemon's unit name is spelled out, so the
// orphan sweep and the polkit rule agree on the prefix.
func UnitName(coreID string, instance int) string {
	return fmt.Sprintf("%s%s-%d", unitPrefix, coreID, instance)
}

// IsOwnedUnit reports whether a unit name is one this panel would have created.
func IsOwnedUnit(name string) bool {
	return strings.HasPrefix(name, unitPrefix)
}

// Validate refuses what neither backend could run.
func (s Spec) Validate() error {
	switch {
	case s.Name == "":
		return errors.New("supervisor: spec has no name")
	case !unitNameRe.MatchString(s.Name):
		return fmt.Errorf("supervisor: %q is not a valid unit name", s.Name)
	case !IsOwnedUnit(s.Name):
		return fmt.Errorf("supervisor: %q does not start with %s; use UnitName", s.Name, unitPrefix)
	case len(s.Exec) == 0 || s.Exec[0] == "":
		return fmt.Errorf("supervisor: %s has no command", s.Name)
	}
	if _, err := capabilityMask(s.Capabilities); err != nil {
		return err
	}
	switch s.Restart {
	case "", RestartNo, RestartOnFailure, RestartAlways:
	default:
		return fmt.Errorf("supervisor: %s: unknown restart policy %q", s.Name, s.Restart)
	}
	return nil
}

// Hash covers every field that changes the running process. Output and OnExit
// are the panel's side of the process, not the process, so they are left out.
func (s Spec) Hash() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	write(s.Name)
	write(s.Exec...)
	write("env")
	write(s.Env...)
	write(s.WorkingDir, s.User)
	write("caps")
	write(s.Capabilities...)
	write(string(s.Restart), s.RestartAfter.String(), s.StartLimitInterval.String(),
		fmt.Sprint(s.StartLimitBurst), s.StopTimeout.String(), fmt.Sprint(int(s.KillSignal)), s.Slice)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// capabilityBits is the subset of capabilities(7) a core daemon can ask for.
// A name outside it is a typo, refused at Validate rather than ignored by systemd.
var capabilityBits = map[string]uint{
	"CAP_CHOWN":            0,
	"CAP_DAC_OVERRIDE":     1,
	"CAP_KILL":             5,
	"CAP_NET_BIND_SERVICE": 10,
	"CAP_NET_ADMIN":        12,
	"CAP_NET_RAW":          13,
	"CAP_SYS_MODULE":       16,
	"CAP_SYS_ADMIN":        21,
}

func capabilityMask(names []string) (uint64, error) {
	var mask uint64
	for _, name := range names {
		bit, ok := capabilityBits[strings.ToUpper(name)]
		if !ok {
			return 0, fmt.Errorf("supervisor: unknown capability %q", name)
		}
		mask |= 1 << bit
	}
	return mask, nil
}

func (s Spec) effectiveStop() time.Duration {
	if s.StopTimeout > 0 {
		return s.StopTimeout
	}
	return 5 * time.Second
}

func (s Spec) killSignal() syscall.Signal {
	if s.KillSignal != 0 {
		return s.KillSignal
	}
	return syscall.SIGTERM
}
