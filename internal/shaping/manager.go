package shaping

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/Arman2122/p-ui/internal/logger"
)

// Key is one subject's kernel identity. Exactly one of Prefix and Mark is set;
// the mark path has no mechanism in v1 and is refused rather than ignored.
type Key struct {
	Prefix netip.Prefix
	Mark   uint32
}

// Limits are bits per second from the CLIENT's point of view. Zero means
// unlimited in that direction, so the zero value installs nothing at all.
type Limits struct {
	UpBps, DownBps int64
}

// Subject is one shaped user on one device. ID is the caller's correlation
// handle: it is carried to the readback and the logs, and the diff never uses it.
type Subject struct {
	ID     string
	Keys   []Key
	Limits Limits
}

// DeviceWant is one device's whole shaped population. No Subjects means converge
// to no owned objects on that device, which is how a stranded tree is removed.
type DeviceWant struct {
	Device   string
	Subjects []Subject
}

// Manager owns every kernel object this mechanism installs and holds no view of
// what it wrote: every pass snapshots each device and diffs against the kernel.
type Manager struct {
	mu    sync.Mutex
	plane Plane
	ns    *Namespaces
}

// NewManager takes the namespaces it may install on explicitly: a manager that
// decided its own would be a second opinion on which devices the panel owns.
func NewManager(p Plane, ns *Namespaces) *Manager {
	if ns == nil {
		ns = DefaultNamespaces()
	}
	return &Manager{plane: p, ns: ns}
}

// shaped is one subject reduced to what a single tree needs: the selectors, and
// the one direction's rate already canonicalised into the kernel's own unit.
type shaped struct {
	id       string
	prefixes []netip.Prefix
	rate     uint64
}

// key is the diff's identity for a subject: its selectors, never its ID and never
// a class id. Two passes that want the same addresses want the same class.
func (s shaped) key() string {
	parts := make([]string, 0, len(s.prefixes))
	for _, prefix := range s.prefixes {
		parts = append(parts, prefix.String())
	}
	return strings.Join(parts, ",")
}

// devicePlan is one DeviceWant after validation: the two trees it asks for, and
// the mirror device the upload tree needs.
type devicePlan struct {
	device   string
	mirror   string
	download []shaped
	upload   []shaped
}

/*
Converge drives the whole host toward want and issues the minimum op set.

It is a whole-host reconcile, not a per-device update: every mirror device this
panel owns whose id is not wanted here is deleted, so a caller must pass every
device it wants shaped in one call. A device whose snapshot fails is skipped
entirely and nothing about it is changed — converging from a partial view is the
exact failure the no-fingerprint-cache rule exists to prevent.
*/
func (m *Manager) Converge(ctx context.Context, want []DeviceWant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var failures []error
	plans := make([]devicePlan, 0, len(want))
	mirrors := map[string]string{}
	for _, w := range want {
		plan, err := m.planDevice(w)
		if err != nil {
			failures = append(failures, err)
		}
		if plan.device == "" {
			continue
		}
		if owner, clash := mirrors[plan.mirror]; clash {
			failures = append(failures, fmt.Errorf(
				"shaping: %s and %s both derive mirror %s, so one would shape the other's upload",
				owner, plan.device, plan.mirror))
			continue
		}
		mirrors[plan.mirror] = plan.device
		plans = append(plans, plan)
	}

	wantedMirrors := map[string]bool{}
	for _, plan := range plans {
		failures = append(failures, m.convergeDevice(ctx, plan, wantedMirrors)...)
	}
	failures = append(failures, m.collectMirrors(ctx, wantedMirrors))
	return errors.Join(failures...)
}

/*
planDevice validates one want and splits it into the two trees.

Every refusal here is fail-open by construction: the subject is dropped, so it
runs unshaped, and the error names it. Shaping one user as another is the only
failure a customer cannot detect for themselves, so a selector claimed twice
drops BOTH subjects rather than picking one.
*/
func (m *Manager) planDevice(w DeviceWant) (devicePlan, error) {
	if !m.ns.Owns(w.Device) {
		return devicePlan{}, fmt.Errorf("%w: %q", ErrNotOwned, w.Device)
	}
	id, mine := m.ns.DeviceID(w.Device)
	if !mine {
		return devicePlan{}, fmt.Errorf("%w: %q carries no inbound id", ErrNotOwned, w.Device)
	}
	plan := devicePlan{device: w.Device, mirror: IFBDevice(id)}

	var failures []error
	owner := map[netip.Prefix]string{}
	duplicated := map[netip.Prefix]bool{}
	keys := make(map[string][]netip.Prefix, len(w.Subjects))
	for _, subject := range w.Subjects {
		prefixes := make([]netip.Prefix, 0, len(subject.Keys))
		for _, key := range subject.Keys {
			if key.Mark != 0 && !key.Prefix.IsValid() {
				failures = append(failures, fmt.Errorf(
					"shaping: subject %q on %s selects on mark %d, which no mechanism in this version installs",
					subject.ID, w.Device, key.Mark))
				continue
			}
			if !key.Prefix.IsValid() || key.Prefix.Bits() != key.Prefix.Addr().BitLen() {
				failures = append(failures, fmt.Errorf(
					"shaping: subject %q on %s selects on %s, which is not a host prefix and would shape its whole subnet as one user",
					subject.ID, w.Device, key.Prefix))
				continue
			}
			prefix := key.Prefix.Masked()
			if first, seen := owner[prefix]; seen && first != subject.ID {
				duplicated[prefix] = true
				failures = append(failures, fmt.Errorf(
					"%w: %s is claimed by %q and %q, so neither is shaped", ErrDuplicateKey, prefix, first, subject.ID))
				continue
			}
			owner[prefix] = subject.ID
			prefixes = append(prefixes, prefix)
		}
		keys[subject.ID] = prefixes
	}

	for _, subject := range w.Subjects {
		prefixes := slices.DeleteFunc(keys[subject.ID], func(p netip.Prefix) bool { return duplicated[p] })
		slices.SortFunc(prefixes, comparePrefix)
		prefixes = slices.Compact(prefixes)
		if len(prefixes) == 0 {
			continue
		}
		if rate := KernelBytesPerSec(subject.Limits.DownBps); rate > 0 {
			plan.download = append(plan.download, shaped{id: subject.ID, prefixes: prefixes, rate: rate})
		}
		if rate := KernelBytesPerSec(subject.Limits.UpBps); rate > 0 {
			plan.upload = append(plan.upload, shaped{id: subject.ID, prefixes: prefixes, rate: rate})
		}
	}
	sortSubjects(plan.download)
	sortSubjects(plan.upload)
	return plan, errors.Join(failures...)
}

// convergeDevice builds the mirror first and tears it down last, because a mirred
// filter naming an absent device is refused and one naming a live device pins it.
func (m *Manager) convergeDevice(ctx context.Context, plan devicePlan, wantedMirrors map[string]bool) []error {
	var failures []error
	if len(plan.upload) > 0 {
		wantedMirrors[plan.mirror] = true
		mirror, err := m.plane.Snapshot(ctx, plan.mirror)
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("shaping: read %s: %w", plan.mirror, err))
		case mirror.Exists:
			failures = append(failures, m.convergeTree(ctx, plan.mirror, mirror, MatchSrc, plan.upload)...)
		default:
			// Created only when it is absent, so a converged pass issues no write at
			// all — an unconditional Ensure would churn the op log forever.
			if err := m.plane.EnsureIFB(ctx, plan.mirror); err != nil {
				failures = append(failures, fmt.Errorf("shaping: create mirror %s: %w", plan.mirror, err))
				break
			}
			mirror.Exists = true
			failures = append(failures, m.convergeTree(ctx, plan.mirror, mirror, MatchSrc, plan.upload)...)
		}
	}
	// One read serves both trees on this device: the egress tree and the ingress
	// hook hang off different parents, so neither pass can see the other's writes.
	snap, err := m.plane.Snapshot(ctx, plan.device)
	if err != nil {
		return append(failures, fmt.Errorf("shaping: read %s: %w", plan.device, err))
	}
	failures = append(failures, m.convergeTree(ctx, plan.device, snap, MatchDst, plan.download)...)
	failures = append(failures, m.convergeIngress(ctx, plan, snap)...)
	return failures
}

/*
convergeTree brings one device's HTB tree to exactly the subjects asked for.

Deletes run filter -> leaf qdisc -> class and adds run the exact reverse, because
the kernel enforces both directions: a class a filter still points at answers
EBUSY, and a filter naming an absent class is refused outright.
*/
func (m *Manager) convergeTree(ctx context.Context, device string, snap Snapshot, match MatchField, subjects []shaped) []error {
	if !snap.Exists {
		// The core owns the device and it is absent between a restart and the next
		// reconcile. Its objects died with it, so there is nothing stale to repair.
		logger.Debugf("shaping: %s is not on this host yet, its tree converges on the next pass", device)
		return nil
	}

	view, err := ownedView(snap, device, match)
	if err != nil {
		return []error{err}
	}
	if len(subjects) == 0 {
		return m.teardown(ctx, device, view)
	}

	var failures []error
	if !view.rooted {
		if err := m.plane.AddQdisc(ctx, rootQdisc(device)); err != nil {
			return append(failures, fmt.Errorf("shaping: install the root qdisc on %s: %w", device, err))
		}
	}
	failures = append(failures, m.ensureClass(ctx, view, defaultMinor, KernelBytesPerSec(UnlimitedBps))...)

	assigned := assignMinors(subjects, view)
	wantFilters := make([]FilterSpec, 0, len(subjects)*2)
	for _, subject := range subjects {
		for _, prefix := range subject.prefixes {
			wantFilters = append(wantFilters, FilterSpec{
				Device: device, Parent: rootHandle, Priority: ourPriority(prefix),
				Match: match, Prefix: prefix, ClassID: classHandle(assigned[subject.key()]),
			})
		}
	}

	// Indexed once per tree rather than rescanned per entry: the steady-state pass
	// writes nothing, and a linear scan on both sides made discovering that O(N^2).
	wanted := index(wantFilters)
	installed := index(view.filters)

	// Every filter that is not exactly what is wanted goes first: it is what stops
	// classification, and it is what unpins a class the GC is about to delete.
	for _, have := range view.filters {
		if wanted[have.identity()] {
			continue
		}
		if err := m.plane.DelFilter(ctx, have); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove %s: %w", have, err))
		}
	}
	live := map[uint16]bool{defaultMinor: true}
	for _, minor := range assigned {
		live[minor] = true
	}
	for _, minor := range slices.Sorted(maps.Keys(view.classes)) {
		if live[minor] {
			continue
		}
		failures = append(failures, m.dropClass(ctx, view, minor)...)
	}

	for _, subject := range subjects {
		failures = append(failures, m.ensureClass(ctx, view, assigned[subject.key()], subject.rate)...)
	}
	for _, want := range wantFilters {
		if installed[want.identity()] {
			continue
		}
		if err := m.plane.AddFilter(ctx, want); err != nil {
			failures = append(failures, fmt.Errorf("shaping: install %s: %w", want, err))
		}
	}
	return failures
}

// index keys filters on the identity the diff compares, so each side is walked
// once. Handle is excluded because the kernel assigns it and a readback carries it.
func index(filters []FilterSpec) map[FilterSpec]bool {
	out := make(map[FilterSpec]bool, len(filters))
	for _, filter := range filters {
		out[filter.identity()] = true
	}
	return out
}

// ensureClass adds a class or changes its rate, and never deletes and re-adds:
// the change is the 15us live-edit path and it preserves the class's counters.
func (m *Manager) ensureClass(ctx context.Context, view devices, minor uint16, rate uint64) []error {
	want := ClassSpec{
		Device: view.device, Handle: classHandle(minor), Parent: rootHandle,
		RateBytesPerSec: rate, CeilBytesPerSec: rate,
	}
	have, present := view.classes[minor]
	switch {
	case present && have == want:
	case present:
		if err := m.plane.ChangeClass(ctx, want); err != nil {
			return []error{fmt.Errorf("shaping: retune %s: %w", want, err)}
		}
	default:
		if err := m.plane.AddClass(ctx, want); err != nil {
			return []error{fmt.Errorf("shaping: install %s: %w", want, err)}
		}
	}
	view.classes[minor] = want

	// Per-flow queueing under EVERY class, default included, so one greedy download
	// cannot starve the same client's interactive traffic. NOT fq_codel — see
	// TestTheLeafIsNotCodel for the measurement that ruled it out.
	if _, leafed := view.leaves[want.Handle]; leafed {
		return nil
	}
	leaf := QdiscSpec{Device: view.device, Type: QdiscSfq, Parent: want.Handle}
	if err := m.plane.AddQdisc(ctx, leaf); err != nil {
		return []error{fmt.Errorf("shaping: install %s: %w", leaf, err)}
	}
	view.leaves[want.Handle] = leaf
	return nil
}

// dropClass removes one shaped user, in the order the kernel enforces.
func (m *Manager) dropClass(ctx context.Context, view devices, minor uint16) []error {
	var failures []error
	handle := classHandle(minor)
	if _, ok := view.leaves[handle]; ok {
		// Deleted by parent rather than by the handle the readback carries: the
		// parent is a leaf's stable identity and the handle is the kernel's to pick.
		leaf := QdiscSpec{Device: view.device, Type: QdiscSfq, Parent: handle}
		if err := m.plane.DelQdisc(ctx, leaf); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove %s: %w", leaf, err))
		}
		delete(view.leaves, handle)
	}
	class := view.classes[minor]
	if err := m.plane.DelClass(ctx, class); err != nil && !settledDel(err) {
		failures = append(failures, fmt.Errorf("shaping: remove %s: %w", class, err))
	}
	delete(view.classes, minor)
	return failures
}

// teardown removes this panel's whole tree from a device nothing wants shaped.
// The root qdisc goes last so no class is ever orphaned mid-pass.
func (m *Manager) teardown(ctx context.Context, device string, view devices) []error {
	if !view.rooted {
		return nil
	}
	var failures []error
	for _, have := range view.filters {
		if err := m.plane.DelFilter(ctx, have); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove %s: %w", have, err))
		}
	}
	for _, minor := range slices.Sorted(maps.Keys(view.classes)) {
		failures = append(failures, m.dropClass(ctx, view, minor)...)
	}
	root := rootQdisc(device)
	if err := m.plane.DelQdisc(ctx, root); err != nil && !settledDel(err) {
		failures = append(failures, fmt.Errorf("shaping: remove %s: %w", root, err))
	}
	return failures
}

/*
convergeIngress hangs the upload mirror off the device's ingress hook.

Upload cannot be shaped where it arrives — an ingress qdisc can only police, and
policing was measured to deliver 41.9 of a configured 100 Mbit — so every shaped
prefix is redirected to the mirror device, whose egress carries a real HTB tree.
*/
func (m *Manager) convergeIngress(ctx context.Context, plan devicePlan, snap Snapshot) []error {
	if !snap.Exists {
		return nil
	}
	want := make([]FilterSpec, 0, len(plan.upload)*2)
	for _, subject := range plan.upload {
		for _, prefix := range subject.prefixes {
			want = append(want, FilterSpec{
				Device: plan.device, Parent: ingressBlock, Priority: ourPriority(prefix),
				Match: MatchSrc, Prefix: prefix, Redirect: plan.mirror,
			})
		}
	}

	var have []FilterSpec
	// Counted, not merely noticed: this is what decides whether the shared hook
	// may be removed. A filter AHEAD of ours is a different thing and aborts.
	behind := 0
	for _, filter := range snap.Filters {
		switch {
		case filter.Parent == egressBlock:
			behind++
		case filter.Parent != ingressBlock:
		case filter.Prefix.IsValid() && filter.Priority == ourPriority(filter.Prefix) && filter.Match == MatchSrc:
			have = append(have, filter)
		case filter.Priority <= filterPrioV6:
			return []error{fmt.Errorf("%w: %s is ahead of this panel's own ingress filters on %s and would classify their packets first",
				ErrForeignObject, filter, plan.device)}
		default:
			behind++
		}
	}
	hooked := slices.ContainsFunc(snap.Qdiscs, func(q QdiscSpec) bool { return q.Parent == clsactParent })

	var failures []error
	if len(want) > 0 && !hooked {
		if err := m.plane.AddQdisc(ctx, clsactQdisc(plan.device)); err != nil {
			return append(failures, fmt.Errorf("shaping: install the ingress hook on %s: %w", plan.device, err))
		}
		hooked = true
	}
	wanted := index(want)
	installed := index(have)
	for _, filter := range have {
		if wanted[filter.identity()] {
			continue
		}
		if err := m.plane.DelFilter(ctx, filter); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove %s: %w", filter, err))
		}
	}
	for _, filter := range want {
		if installed[filter.identity()] {
			continue
		}
		if err := m.plane.AddFilter(ctx, filter); err != nil {
			failures = append(failures, fmt.Errorf("shaping: install %s: %w", filter, err))
		}
	}
	// The hook is shared with the egress block: an operator's own filter on EITHER
	// side is reason enough to leave the qdisc, because deleting it takes both.
	if len(want) == 0 && hooked && behind == 0 {
		if err := m.plane.DelQdisc(ctx, clsactQdisc(plan.device)); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove the ingress hook on %s: %w", plan.device, err))
		}
	}
	return failures
}

/*
collectMirrors deletes every mirror device whose id no want derives.

It enumerates links rather than reading a snapshot because the mirror OUTLIVES
the device it mirrors: measured, deleting pwg takes its clsact qdisc and its
mirred filters with it while the ifb device survives, so without this pass a
panel leaks one interface per inbound per lifetime.
*/
func (m *Manager) collectMirrors(ctx context.Context, wanted map[string]bool) error {
	links, err := m.plane.Links(ctx)
	if err != nil {
		return fmt.Errorf("shaping: enumerate devices: %w", err)
	}
	var failures []error
	for _, name := range links {
		id, mine := ownedIFBID(name)
		if !mine || wanted[name] {
			continue
		}
		pinned, err := m.stillRedirectedInto(ctx, id, name, links)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if pinned != "" {
			logger.Debugf("shaping: %s still redirects into %s, so the mirror outlives this pass", pinned, name)
			continue
		}
		if err := m.plane.DeleteIFB(ctx, name); err != nil && !settledDel(err) {
			failures = append(failures, fmt.Errorf("shaping: remove stranded mirror %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

/*
stillRedirectedInto names a device whose mirred filters would be left pointing at
a mirror this pass is about to delete.

The kernel answers TC_ACT_SHOT to a redirect at a departed device, so collecting
a mirror out from under a live filter does not leave that client unshaped — it
disconnects them outright, which inverts the rule that shaping fails open. A
device merely missing from this pass's want is not evidence that nothing feeds
its mirror: only the parent's own tree is.
*/
func (m *Manager) stillRedirectedInto(ctx context.Context, id int, mirror string, links []string) (string, error) {
	for _, prefix := range m.ns.Shapeable() {
		parent := parentOf(prefix, id)
		if !slices.Contains(links, parent) {
			continue
		}
		snap, err := m.plane.Snapshot(ctx, parent)
		if err != nil {
			return "", fmt.Errorf("shaping: read %s before collecting %s: %w", parent, mirror, err)
		}
		if slices.ContainsFunc(snap.Filters, func(f FilterSpec) bool { return f.Redirect == mirror }) {
			return parent, nil
		}
	}
	return "", nil
}

/*
Enforced reports what the KERNEL currently holds for each wanted subject, keyed by
Subject.ID and read back rather than remembered.

It takes the want rather than a device name because the kernel stores no
correlation handle: the ID travels in with the selectors that recover it, and
every number in the answer comes out of the snapshot.
*/
func (m *Manager) Enforced(ctx context.Context, want DeviceWant) (map[string]Limits, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan, planErr := m.planDevice(want)
	if plan.device == "" {
		return nil, planErr
	}
	out := map[string]Limits{}
	device, err := m.plane.Snapshot(ctx, plan.device)
	if err != nil {
		return nil, fmt.Errorf("shaping: read %s: %w", plan.device, err)
	}
	mirror, err := m.plane.Snapshot(ctx, plan.mirror)
	if err != nil {
		return nil, fmt.Errorf("shaping: read %s: %w", plan.mirror, err)
	}
	down := readRates(device, MatchDst)
	up := readRates(mirror, MatchSrc)
	// An upload budget bills only what the device actually feeds the mirror. An
	// intact mirror tree nothing redirects into enforces exactly nothing, and
	// reporting its rate is the confusion this readback exists to prevent.
	fed := redirectedInto(device, plan.mirror)
	for _, subject := range want.Subjects {
		var limits Limits
		for _, key := range subject.Keys {
			prefix := key.Prefix.Masked()
			if rate, ok := down[prefix]; ok {
				limits.DownBps = int64(rate) * 8
			}
			if rate, ok := up[prefix]; ok && fed[prefix] {
				limits.UpBps = int64(rate) * 8
			}
		}
		if limits != (Limits{}) {
			out[subject.ID] = limits
		}
	}
	return out, planErr
}

// redirectedInto names every prefix the device still hands to the mirror. It is
// the half of the upload path that lives on the device the core owns.
func redirectedInto(snap Snapshot, mirror string) map[netip.Prefix]bool {
	out := map[netip.Prefix]bool{}
	for _, filter := range snap.Filters {
		if filter.Parent == ingressBlock && filter.Redirect == mirror && filter.Prefix.IsValid() {
			out[filter.Prefix] = true
		}
	}
	return out
}

// readRates resolves the kernel's own prefix -> class -> rate chain on one device.
// An absent device reports nothing rather than failing: it is shaping nobody.
func readRates(snap Snapshot, match MatchField) map[netip.Prefix]uint64 {
	out := map[netip.Prefix]uint64{}
	if !snap.Exists {
		return out
	}
	rates := map[uint16]uint64{}
	for _, class := range snap.Classes {
		if minor, ok := classMinor(class.Handle); ok && class.Parent == rootHandle {
			rates[minor] = class.RateBytesPerSec
		}
	}
	for _, filter := range snap.Filters {
		if filter.Parent != rootHandle || !ours(filter, match) {
			continue
		}
		if minor, ok := classMinor(filter.ClassID); ok && minor != defaultMinor {
			if rate, known := rates[minor]; known {
				out[filter.Prefix] = rate
			}
		}
	}
	return out
}

// devices is the part of one snapshot this panel owns, indexed for the diff.
// Ownership is structural, so a correct object an earlier process wrote is
// adopted rather than rebuilt.
type devices struct {
	device  string
	rooted  bool
	classes map[uint16]ClassSpec
	leaves  map[uint32]QdiscSpec
	filters []FilterSpec
}

/*
ownedView splits one device's tree into what this panel owns and what it must not
touch.

A root qdisc that is not exactly ours is an error and is left alone: the plane has
no ChangeQdisc, so "repairing" it would mean deleting an operator's whole tree. A
filter AHEAD of ours is an error for the sharper reason — it silently eats the
packets ours was installed to classify, and a shaper that classifies nothing while
reporting success is the worst outcome available.
*/
func ownedView(snap Snapshot, device string, match MatchField) (devices, error) {
	view := devices{
		device:  device,
		classes: map[uint16]ClassSpec{},
		leaves:  map[uint32]QdiscSpec{},
	}
	want := rootQdisc(device)
	for _, qdisc := range snap.Qdiscs {
		switch qdisc.Parent {
		case rootParent:
			switch {
			case qdisc == want:
				view.rooted = true
			// A handle of zero is the implicit qdisc every bare device carries —
			// noqueue, pfifo_fast, mq. Nobody wrote it, so replacing it is not a theft.
			case qdisc.Handle == 0:
			default:
				return devices{}, fmt.Errorf("%w: %s is the root qdisc on %s and this panel did not write it",
					ErrForeignObject, qdisc, device)
			}
		case clsactParent:
		default:
			view.leaves[qdisc.Parent] = qdisc
		}
	}
	if !view.rooted {
		return view, nil
	}

	for _, class := range snap.Classes {
		if minor, ok := classMinor(class.Handle); ok && class.Parent == rootHandle {
			view.classes[minor] = class
		}
	}
	for _, filter := range snap.Filters {
		switch {
		case filter.Parent != rootHandle:
		case ours(filter, match):
			view.filters = append(view.filters, filter)
		case filter.Priority <= filterPrioV6:
			return devices{}, fmt.Errorf("%w: %s is ahead of this panel's own filters on %s and would classify their packets first",
				ErrForeignObject, filter, device)
		default:
			logger.Debugf("shaping: %s sits behind this panel's filters on %s, leaving it alone", filter, device)
		}
	}
	return view, nil
}

// ours is the shape this package writes and nothing else does: a host prefix, on
// the family's own priority, selecting a class and redirecting nowhere.
func ours(filter FilterSpec, match MatchField) bool {
	return filter.Prefix.IsValid() &&
		filter.Priority == ourPriority(filter.Prefix) &&
		filter.Match == match &&
		filter.Redirect == ""
}

/*
assignMinors decides which class each subject gets, from the kernel and nothing
else.

A minor is never derived from an id and never persisted: the kernel's own filter
dump answers selector -> class, so the panel adopts what is already installed and
only allocates for a subject that has none. Persisting the map would be a second
source of truth that can rot.
*/
func assignMinors(subjects []shaped, view devices) map[string]uint16 {
	byPrefix := map[netip.Prefix]uint16{}
	for _, filter := range view.filters {
		if minor, ok := classMinor(filter.ClassID); ok {
			byPrefix[filter.Prefix] = minor
		}
	}

	// A minor that more than one wanted subject's selectors point at is CONTESTED,
	// and adopting it would hand one user the class another is being billed for.
	claimants := map[uint16]map[string]bool{}
	for _, subject := range subjects {
		for _, prefix := range subject.prefixes {
			if minor, ok := byPrefix[prefix]; ok {
				if claimants[minor] == nil {
					claimants[minor] = map[string]bool{}
				}
				claimants[minor][subject.key()] = true
			}
		}
	}

	out := make(map[string]uint16, len(subjects))
	claimed := map[uint16]bool{defaultMinor: true}
	for _, subject := range subjects {
		candidates := make([]uint16, 0, len(subject.prefixes))
		for _, prefix := range subject.prefixes {
			if minor, ok := byPrefix[prefix]; ok && !claimed[minor] && len(claimants[minor]) == 1 {
				candidates = append(candidates, minor)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		minor := slices.Min(candidates)
		out[subject.key()] = minor
		claimed[minor] = true
	}

	next := firstMinor
	for _, subject := range subjects {
		if _, adopted := out[subject.key()]; adopted {
			continue
		}
		for claimed[next] || view.classes[next] != (ClassSpec{}) {
			next++
		}
		out[subject.key()] = next
		claimed[next] = true
	}
	return out
}

func rootQdisc(device string) QdiscSpec {
	return QdiscSpec{Device: device, Type: QdiscHTB, Handle: rootHandle, Parent: rootParent, Default: defaultMinor}
}

func clsactQdisc(device string) QdiscSpec {
	return QdiscSpec{Device: device, Type: QdiscClsact, Handle: clsactHandle, Parent: clsactParent}
}

func sortSubjects(in []shaped) {
	slices.SortFunc(in, func(a, b shaped) int { return strings.Compare(a.key(), b.key()) })
}

// comparePrefix orders v4 before v6 and then by address, so an op log is
// comparable across passes and a claim never depends on map iteration.
func comparePrefix(a, b netip.Prefix) int {
	return a.Addr().Compare(b.Addr())
}

// settledDel absorbs the answers that already mean the wanted end state. An add
// answered EEXIST is NOT benign: something the diff could not claim holds it.
func settledDel(err error) bool {
	return errors.Is(err, ErrNotInstalled) || errors.Is(err, ErrNoDevice)
}
