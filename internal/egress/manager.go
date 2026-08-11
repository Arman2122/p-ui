package egress

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/Arman2122/p-ui/internal/logger"
)

// Manager owns every kernel object the egress model derives from an id, and holds
// no view of what it wrote: every entry point snapshots the host and diffs.
type Manager struct {
	mu      sync.Mutex
	plane   Plane
	drivers *Registry
}

func New(plane Plane, drivers *Registry) *Manager {
	if drivers == nil {
		drivers = NewRegistry()
	}
	return &Manager{plane: plane, drivers: drivers}
}

// Ensure converges one row and nothing else: its table, its front and every rule
// its attached inbounds need. A disabled row converges to no objects at all.
func (m *Manager) Ensure(ctx context.Context, e Egress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		return err
	}
	return m.converge(ctx, snap, e)
}

// Attach claims one more ingress device. It is synchronous by design: a tick that
// caught up later leaves a just-attached inbound on the server's own identity.
func (m *Manager) Attach(ctx context.Context, e Egress, iif string) error {
	if iif == "" {
		return fmt.Errorf("egress: attach to egress %d needs an ingress device", e.ID)
	}
	e.Ingress = append(slices.Clone(e.Ingress), iif)
	return m.Ensure(ctx, e)
}

// Detach drops one ingress device. Everything else about the egress stays up, so
// the next attach costs one rule and never touches the core's config.
func (m *Manager) Detach(ctx context.Context, e Egress, iif string) error {
	e.Ingress = slices.DeleteFunc(slices.Clone(e.Ingress), func(name string) bool { return name == iif })
	return m.Ensure(ctx, e)
}

/*
Selects reports whether the band routes iif into egress id in every family, and
an id of 0 asks the opposite: that nothing in the band selects iif at all.

It reads the host rather than the last apply's error, so a caller can tell "my
attachment landed" from "some other row on this host is unhappy" — the two a
whole-host Reconcile joins into one error.
*/
func (m *Manager) Selects(ctx context.Context, iif string, id int) (bool, error) {
	if iif == "" {
		return false, fmt.Errorf("egress: asking which egress serves no device at all")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	found := map[Family]int{}
	for _, rule := range snap.Rules {
		if rule.Iif != iif {
			continue
		}
		if id == 0 || rule.Priority != Prio(id) || rule.Table != Table(id) {
			return false, nil
		}
		found[rule.Family]++
	}
	if id == 0 {
		return true, nil
	}
	for _, family := range Families {
		if found[family] == 0 {
			return false, nil
		}
	}
	return true, nil
}

// Remove deletes every object this id owns, using the device Device(id) derives —
// a driver naming its own device must go through Ensure with Enable false.
func (m *Manager) Remove(ctx context.Context, id int) error {
	return m.Ensure(ctx, Egress{ID: id})
}

// Reconcile drives the whole host toward rows. Every id the band still holds is
// converged too, so a row deleted while the panel was down is cleaned up.
func (m *Manager) Reconcile(ctx context.Context, rows []Egress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		return err
	}

	byID := make(map[int]Egress, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for _, rule := range snap.Rules {
		if id, mine := prioEgressID(rule.Priority); mine {
			if _, known := byID[id]; !known {
				byID[id] = Egress{ID: id}
			}
		}
	}
	for _, route := range snap.Routes {
		if id, mine := tableEgressID(route.Table); mine {
			if _, known := byID[id]; !known {
				byID[id] = Egress{ID: id}
			}
		}
	}

	ids := slices.Sorted(maps.Keys(byID))
	failures := make([]error, 0, len(ids))
	for _, id := range ids {
		if err := m.converge(ctx, snap, byID[id]); err != nil {
			failures = append(failures, fmt.Errorf("egress %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

// want is every object one row asks for, split so the ordering rule can be enforced:
// the blackhole goes in before any rule points at it, and out after the last one.
type want struct {
	device     string
	blackholes []RouteSpec
	fronts     []RouteSpec
	rules      []RuleSpec
	sysctls    map[string]string
}

// plan derives the objects one row wants. An unresolvable row still gets its
// blackhole and its rules: containment is the safe failure, release is not.
func (m *Manager) plan(e Egress, links map[string]struct{}) (want, error) {
	if !e.Enable {
		return want{}, nil
	}
	var problem error
	var fill Fill
	driver, known := m.drivers.For(e.Type)
	if !known {
		problem = fmt.Errorf("%w: %q", ErrUnknownDriver, e.Type)
	} else if fill, problem = driver.Fill(e); problem != nil {
		fill = Fill{}
	}

	w := want{device: fill.Device}
	_, present := links[fill.Device]
	for _, family := range Families {
		w.blackholes = append(w.blackholes, RouteSpec{
			Family: family, Table: Table(e.ID), Type: RouteBlackhole,
			Dst: family.DefaultRoute(), Metric: BlackholeMetric,
		})
		if fill.Device != "" && present {
			w.fronts = append(w.fronts, RouteSpec{
				Family: family, Table: Table(e.ID), Type: RouteUnicast,
				Dst: family.DefaultRoute(), Device: fill.Device, Metric: FrontMetric,
			})
		}
	}
	for _, iif := range uniqueSorted(e.Ingress) {
		for _, family := range Families {
			w.rules = append(w.rules, RuleSpec{
				Family: family, Priority: Prio(e.ID), Iif: iif, Table: Table(e.ID),
			})
		}
	}
	if present {
		w.sysctls = fill.Sysctls
	}
	return w, problem
}

// have is the part of the snapshot one id owns. Ownership is structural, so a
// correct object made by an earlier process, or by hand, is adopted not rebuilt.
type have struct {
	blackholes []RouteSpec
	fronts     []RouteSpec
	rules      []RuleSpec
	foreign    []string
}

func ownedView(snap Snapshot, id int, device string) have {
	var h have
	for _, rule := range snap.Rules {
		if rule.Priority != Prio(id) {
			continue
		}
		if rule.Table != Table(id) {
			h.foreign = append(h.foreign, rule.String())
			continue
		}
		h.rules = append(h.rules, rule)
	}
	for _, route := range snap.Routes {
		if route.Table != Table(id) {
			continue
		}
		switch {
		case route.Type == RouteBlackhole && route.Dst == route.Family.DefaultRoute():
			h.blackholes = append(h.blackholes, route)
		case route.Type == RouteUnicast && route.Dst == route.Family.DefaultRoute() && ownsDevice(device, route.Device):
			h.fronts = append(h.fronts, route)
		default:
			h.foreign = append(h.foreign, route.String())
		}
	}
	return h
}

// ownsDevice accepts the row's own device and anything in the panel's peg<id>
// namespace, so a front pointing at the WRONG peg is replaced, not left leaking.
func ownsDevice(want, got string) bool {
	if got == "" {
		return false
	}
	if want != "" && got == want {
		return true
	}
	_, mine := ownedEgressID(got)
	return mine
}

func (m *Manager) converge(ctx context.Context, snap Snapshot, e Egress) error {
	if err := checkID(e.ID); err != nil {
		return err
	}
	links := make(map[string]struct{}, len(snap.Links))
	for _, name := range snap.Links {
		links[name] = struct{}{}
	}
	w, planErr := m.plan(e, links)
	h := ownedView(snap, e.ID, w.device)

	addRules, delRules := diff(w.rules, h.rules)
	addBlack, delBlack := diff(w.blackholes, h.blackholes)
	addFront, delFront := diff(w.fronts, h.fronts)

	failures := []error{planErr}
	// Removing the rule is what stops traffic, so a teardown starts here and a
	// build-up never reaches here with anything to do.
	live := map[Family]int{}
	for _, rule := range h.rules {
		live[rule.Family]++
	}
	for _, rule := range delRules {
		if err := m.plane.DelRule(ctx, rule); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("egress: remove %s: %w", rule, err))
			continue
		}
		live[rule.Family]--
	}

	// The blackhole is part of the table's identity: a rule pointing at a table
	// with no match falls through to main and out with the server's own address.
	contained := map[Family]bool{}
	for _, route := range h.blackholes {
		contained[route.Family] = true
	}
	for _, route := range addBlack {
		if err := m.plane.AddRoute(ctx, route); err != nil {
			failures = append(failures, fmt.Errorf("egress: contain %s: %w", route, err))
			continue
		}
		contained[route.Family] = true
	}
	for _, rule := range addRules {
		if !contained[rule.Family] {
			failures = append(failures, fmt.Errorf("egress: refusing %s: its table has no blackhole, so the lookup would fall through to main", rule))
			continue
		}
		if err := m.plane.AddRule(ctx, rule); err != nil {
			failures = append(failures, fmt.Errorf("egress: install %s: %w", rule, err))
			continue
		}
		live[rule.Family]++
	}

	// The front is deleted before its replacement is added: the kernel refuses a
	// second route with the same table, destination and metric.
	for _, route := range delFront {
		if err := m.plane.DelRoute(ctx, route); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("egress: withdraw %s: %w", route, err))
		}
	}
	for _, route := range addFront {
		if err := m.plane.AddRoute(ctx, route); err != nil {
			if retryable(err) {
				logger.Debugf("egress: %s cannot be installed yet, its table keeps only the blackhole: %v", route, err)
				continue
			}
			failures = append(failures, fmt.Errorf("egress: route %s: %w", route, err))
		}
	}

	for _, route := range delBlack {
		if live[route.Family] > 0 {
			failures = append(failures, fmt.Errorf("egress: keeping %s: a rule still points at its table", route))
			continue
		}
		if err := m.plane.DelRoute(ctx, route); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("egress: uncontain %s: %w", route, err))
		}
	}

	failures = append(failures, m.applySysctls(ctx, w.sysctls)...)
	// Foreign objects are named at debug because drift repair runs on a tick;
	// Preflight is where they refuse a boot or an attach, once and loudly.
	for _, object := range h.foreign {
		logger.Debugf("egress %d: %s is in the reserved band and is not this panel's, leaving it alone", e.ID, object)
	}
	return errors.Join(failures...)
}

func (m *Manager) applySysctls(ctx context.Context, sysctls map[string]string) []error {
	var failures []error
	for _, key := range slices.Sorted(maps.Keys(sysctls)) {
		value := sysctls[key]
		current, readErr := m.plane.Sysctl(ctx, key)
		if readErr == nil && current == value {
			continue
		}
		if readErr != nil && retryable(readErr) {
			logger.Debugf("egress: %s is not readable yet, its device is still absent: %v", key, readErr)
			continue
		}
		writeErr := m.plane.SetSysctl(ctx, key, value)
		switch {
		case writeErr == nil:
		case retryable(writeErr):
			logger.Debugf("egress: %s is not writable yet, its device is still absent: %v", key, writeErr)
		default:
			failures = append(failures, fmt.Errorf("egress: set %s=%s: %w", key, value, writeErr))
		}
	}
	return failures
}

// diff is the whole convergence engine. It counts rather than sets, so a duplicate
// the panel never issued is still deleted down to the one copy that is wanted.
func diff[T comparable](want, have []T) (add, del []T) {
	pool := make(map[T]int, len(have))
	for _, item := range have {
		pool[item]++
	}
	for _, item := range want {
		if pool[item] > 0 {
			pool[item]--
			continue
		}
		add = append(add, item)
	}
	for _, item := range have {
		if pool[item] > 0 {
			pool[item]--
			del = append(del, item)
		}
	}
	return add, del
}

func uniqueSorted(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item != "" {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// retryable is a front that cannot carry the route yet — absent between "the core
// is restarting" and "the core is up", or up with that family switched off on it.
func retryable(err error) bool {
	return errors.Is(err, ErrNoDevice) || errors.Is(err, ErrFamilyDisabled)
}

// settledDel absorbs the one answer that already means the wanted end state. An add
// answered EEXIST is NOT benign: something the diff could not claim holds that slot.
func settledDel(err error) bool { return errors.Is(err, ErrNotInstalled) }
