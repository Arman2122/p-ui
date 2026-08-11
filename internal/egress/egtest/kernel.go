// Package egtest is an in-memory stand-in for the host routing stack, honest about
// what the kernel answers: every errno here was measured on 6.8 through netlink.
package egtest

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/Arman2122/p-ui/internal/egress"
)

var _ egress.Plane = (*Kernel)(nil)

// Kernel is the fake stack. The op log is there so a test can assert ORDER — that
// the blackhole went in before the rule pointing at its table, and out after it.
type Kernel struct {
	mu      sync.Mutex
	rules   []egress.RuleSpec
	routes  []egress.RouteSpec
	links   []string
	addrs   []egress.AddrSpec
	sysctls map[string]string

	ops []string
	// snapshots counts the passes that read the host, so a test can wait for one
	// to have happened rather than guess at the scheduler.
	snapshots int

	// ProbeErr is what Probe answers, so a refused host can be driven.
	ProbeErr error
	// SnapshotErr fails the one read every entry point starts with.
	SnapshotErr error
	// Fail maps an op string, exactly as Ops records it, onto the error that op
	// answers with — which is how a partial apply is driven mid-pass.
	Fail map[string]error
}

func New() *Kernel {
	return &Kernel{sysctls: map[string]string{}}
}

func (k *Kernel) Probe(context.Context) error { return k.ProbeErr }

func (k *Kernel) Snapshot(context.Context) (egress.Snapshot, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.snapshots++
	if k.SnapshotErr != nil {
		return egress.Snapshot{}, k.SnapshotErr
	}
	return egress.Snapshot{
		Rules:  slices.Clone(k.rules),
		Routes: slices.Clone(k.routes),
		Links:  slices.Clone(k.links),
		Addrs:  slices.Clone(k.addrs),
	}, nil
}

// op records the attempt and answers the injected failure, if any. It records
// before failing on purpose: a test asserting order must see what was tried.
func (k *Kernel) op(kind string, subject fmt.Stringer) error {
	entry := kind + " " + subject.String()
	k.ops = append(k.ops, entry)
	return k.Fail[entry]
}

func (k *Kernel) AddRule(_ context.Context, spec egress.RuleSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("rule+", spec); err != nil {
		return err
	}
	// Measured: the kernel refuses an exact duplicate with EEXIST, through both this
	// library and `ip rule add`. Two rules at one priority need different devices.
	if slices.Contains(k.rules, spec) {
		return fmt.Errorf("%w: %s", egress.ErrAlreadyInstalled, spec)
	}
	k.rules = append(k.rules, spec)
	return nil
}

func (k *Kernel) DelRule(_ context.Context, spec egress.RuleSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("rule-", spec); err != nil {
		return err
	}
	at := slices.Index(k.rules, spec)
	if at < 0 {
		return fmt.Errorf("%w: %s", egress.ErrNotInstalled, spec)
	}
	k.rules = slices.Delete(k.rules, at, at+1)
	return nil
}

// sameRoute is the tuple the kernel refuses a duplicate on: one table may hold
// two default routes at different metrics, and does — the front and its blackhole.
func sameRoute(a, b egress.RouteSpec) bool {
	return a.Family == b.Family && a.Table == b.Table && a.Dst == b.Dst && a.Metric == b.Metric
}

func (k *Kernel) AddRoute(_ context.Context, spec egress.RouteSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("route+", spec); err != nil {
		return err
	}
	if spec.Type == egress.RouteUnicast && !slices.Contains(k.links, spec.Device) {
		return fmt.Errorf("%w: %s", egress.ErrNoDevice, spec.Device)
	}
	if slices.ContainsFunc(k.routes, func(r egress.RouteSpec) bool { return sameRoute(r, spec) }) {
		return fmt.Errorf("%w: %s", egress.ErrAlreadyInstalled, spec)
	}
	k.routes = append(k.routes, spec)
	return nil
}

func (k *Kernel) DelRoute(_ context.Context, spec egress.RouteSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("route-", spec); err != nil {
		return err
	}
	at := slices.IndexFunc(k.routes, func(r egress.RouteSpec) bool { return r == spec })
	if at < 0 {
		return fmt.Errorf("%w: %s", egress.ErrNotInstalled, spec)
	}
	k.routes = slices.Delete(k.routes, at, at+1)
	return nil
}

func (k *Kernel) Sysctl(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.sysctls[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", egress.ErrNoDevice, key)
	}
	return value, nil
}

func (k *Kernel) SetSysctl(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ops = append(k.ops, "sysctl "+key+"="+value)
	if err := k.Fail["sysctl "+key+"="+value]; err != nil {
		return err
	}
	// A per-device knob has no file until its device exists, so a write to one is
	// the same absent-device answer a read gives.
	if _, ok := k.sysctls[key]; !ok {
		return fmt.Errorf("%w: %s", egress.ErrNoDevice, key)
	}
	k.sysctls[key] = value
	return nil
}

// AddLink brings a device up. Any sysctl a caller declared for it appears at the
// kernel's own default of 2, which is what a fresh device inherits.
func (k *Kernel) AddLink(name string, sysctls ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !slices.Contains(k.links, name) {
		k.links = append(k.links, name)
	}
	for _, key := range sysctls {
		if _, ok := k.sysctls[key]; !ok {
			k.sysctls[key] = "2"
		}
	}
}

// DelLink models the device going away under the panel. Measured: the kernel purges
// every unicast route through it and leaves the rules detached but installed.
func (k *Kernel) DelLink(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.links = slices.DeleteFunc(k.links, func(l string) bool { return l == name })
	k.routes = slices.DeleteFunc(k.routes, func(r egress.RouteSpec) bool {
		return r.Type == egress.RouteUnicast && r.Device == name
	})
	k.addrs = slices.DeleteFunc(k.addrs, func(a egress.AddrSpec) bool { return a.Device == name })
	for key := range k.sysctls {
		if strings.Contains(key, "."+name+".") {
			delete(k.sysctls, key)
		}
	}
}

// SetSysctlValue plants a host fact preflight has to read, such as a strict
// conf.all.rp_filter, without going through the write path.
func (k *Kernel) SetSysctlValue(key, value string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sysctls[key] = value
}

// AddAddr plants an address on a device, for the gateway-overlap check. The real
// kernel refuses a duplicate, so a second plant of the same pair is a no-op.
func (k *Kernel) AddAddr(device string, prefix netip.Prefix) {
	k.mu.Lock()
	defer k.mu.Unlock()
	spec := egress.AddrSpec{Prefix: prefix, Device: device}
	if !slices.Contains(k.addrs, spec) {
		k.addrs = append(k.addrs, spec)
	}
}

// SeedRule and SeedRoute plant state the panel did not write: what an earlier
// process left behind, or what belongs to somebody else entirely.
func (k *Kernel) SeedRule(specs ...egress.RuleSpec) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rules = append(k.rules, specs...)
}

func (k *Kernel) SeedRoute(specs ...egress.RouteSpec) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.routes = append(k.routes, specs...)
}

// Rules and Routes are what the stand-in holds now, sorted so a test compares
// against a fixed list rather than against insertion order.
func (k *Kernel) Rules() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return sortedStrings(k.rules)
}

func (k *Kernel) Routes() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return sortedStrings(k.routes)
}

func sortedStrings[T fmt.Stringer](in []T) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, item.String())
	}
	slices.Sort(out)
	return out
}

// Ops is every write attempted, in order. Order is the point: a table must never
// hold a rule before it holds the blackhole that keeps its misses off main.
func (k *Kernel) Ops() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.ops)
}

// Writes is how many writes were attempted, so a second converging pass can be
// proven to have issued none at all.
func (k *Kernel) Writes() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.ops)
}

// Snapshots is how many passes have read the host. A converged pass writes
// nothing, so this is the only way to observe that one happened at all.
func (k *Kernel) Snapshots() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.snapshots
}

// ResetOps clears the log between the passes of one test.
func (k *Kernel) ResetOps() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ops = nil
}
