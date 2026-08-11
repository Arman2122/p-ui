// Package shapetest is an in-memory stand-in for the host traffic-control stack,
// honest about what the kernel answers: every errno here was measured on 6.8.
package shapetest

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/Arman2122/p-ui/internal/shaping"
)

var _ shaping.Plane = (*Kernel)(nil)

// The kernel's own parent sentinels, not this panel's. They are here rather than
// imported because what they gate is kernel behaviour the stand-in has to model.
const (
	rootParent   uint32 = 0xffffffff
	clsactParent uint32 = 0xfffffff1
	ingressBlock uint32 = 0xfffffff2
)

/*
Kernel is the fake stack.

The op log is there so a test can assert ORDER, which is the half of this
mechanism the kernel enforces in both directions: a class a filter still points
at answers EBUSY, and a filter naming an absent class is refused outright.
*/
type Kernel struct {
	mu      sync.Mutex
	links   []string
	qdiscs  []shaping.QdiscSpec
	classes []shaping.ClassSpec
	filters []shaping.FilterSpec

	// The two handles the kernel assigns rather than the caller. A filter's handle
	// counts PER CHAIN, so the v4 and v6 rules of one user both come back as 0x1 —
	// modelled exactly, because a diff that keyed on it would pass a global counter.
	nextLeaf   uint16
	nextFilter map[string]uint32

	ops       []string
	snapshots int

	// ProbeErr is what Probe answers, so a refused host can be driven.
	ProbeErr error
	// SnapshotErr fails the one read a device's convergence starts with, keyed by
	// device so a partial-view abort can be driven for exactly one of them.
	SnapshotErr map[string]error
	// LinksErr fails the enumeration the mirror GC depends on.
	LinksErr error
	// Fail maps an op string, exactly as Ops records it, onto the error that op
	// answers with — which is how a partial apply is driven mid-pass.
	Fail map[string]error
}

func New() *Kernel {
	return &Kernel{nextLeaf: 0x8000, nextFilter: map[string]uint32{}}
}

// chain is the (device, parent, priority) tuple the kernel keys a filter list on.
func chain(spec shaping.FilterSpec) string {
	return fmt.Sprintf("%s/%d/%d", spec.Device, spec.Parent, spec.Priority)
}

// takeHandle is the kernel's own per-chain assignment, starting at 1.
func (k *Kernel) takeHandle(spec shaping.FilterSpec) uint32 {
	k.nextFilter[chain(spec)]++
	return k.nextFilter[chain(spec)]
}

// sameRule is the flower key the kernel refuses a duplicate on. The handle is
// excluded because the kernel assigns it, so an add never carries one.
func sameRule(a, b shaping.FilterSpec) bool {
	a.Handle, b.Handle = 0, 0
	return a == b
}

// family is what a chain carries. Measured: the kernel answers EINVAL to a second
// protocol at a priority another already holds, which is why v4 and v6 differ.
func family(spec shaping.FilterSpec) int {
	if spec.Prefix.Addr().Is6() {
		return 6
	}
	return 4
}

func (k *Kernel) Probe(context.Context) error { return k.ProbeErr }

func (k *Kernel) Links(context.Context) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.LinksErr != nil {
		return nil, k.LinksErr
	}
	return slices.Clone(k.links), nil
}

func (k *Kernel) Snapshot(_ context.Context, device string) (shaping.Snapshot, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.snapshots++
	if err := k.SnapshotErr[device]; err != nil {
		return shaping.Snapshot{}, err
	}
	snap := shaping.Snapshot{Exists: slices.Contains(k.links, device), Links: slices.Clone(k.links)}
	if !snap.Exists {
		return snap, nil
	}
	for _, qdisc := range k.qdiscs {
		if qdisc.Device == device {
			snap.Qdiscs = append(snap.Qdiscs, qdisc)
		}
	}
	for _, class := range k.classes {
		if class.Device == device {
			snap.Classes = append(snap.Classes, class)
		}
	}
	for _, filter := range k.filters {
		if filter.Device == device {
			snap.Filters = append(snap.Filters, filter)
		}
	}
	return snap, nil
}

// op records the attempt and answers the injected failure, if any. It records
// before failing on purpose: a test asserting order must see what was tried.
func (k *Kernel) op(kind string, subject fmt.Stringer) error {
	entry := kind + " " + subject.String()
	k.ops = append(k.ops, entry)
	return k.Fail[entry]
}

// present is the absent-device answer every write shares. The device belongs to
// the core, so it disappearing mid-pass is a normal state and not a fault.
func (k *Kernel) present(device string) error {
	if !slices.Contains(k.links, device) {
		return fmt.Errorf("%w: %s", shaping.ErrNoDevice, device)
	}
	return nil
}

func (k *Kernel) EnsureIFB(_ context.Context, name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ops = append(k.ops, "ifb+ "+name)
	if err := k.Fail["ifb+ "+name]; err != nil {
		return err
	}
	k.addLink(name)
	return nil
}

// addLink brings a device up carrying the implicit root qdisc the kernel gives
// every interface — noqueue, pfifo_fast or fq, all at handle 0 and nobody's work.
func (k *Kernel) addLink(name string) {
	if slices.Contains(k.links, name) {
		return
	}
	k.links = append(k.links, name)
	slices.Sort(k.links)
	k.qdiscs = append(k.qdiscs, shaping.QdiscSpec{Device: name, Type: "noqueue", Parent: rootParent})
}

// DeleteIFB models the whole device going away: measured, the kernel takes its
// qdiscs, classes and filters with it, and every OTHER device survives untouched.
func (k *Kernel) DeleteIFB(_ context.Context, name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ops = append(k.ops, "ifb- "+name)
	if err := k.Fail["ifb- "+name]; err != nil {
		return err
	}
	if !slices.Contains(k.links, name) {
		return fmt.Errorf("%w: %s", shaping.ErrNotInstalled, name)
	}
	k.delLink(name)
	return nil
}

func (k *Kernel) AddQdisc(_ context.Context, spec shaping.QdiscSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("qdisc+", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	at := slices.IndexFunc(k.qdiscs, func(q shaping.QdiscSpec) bool {
		return q.Device == spec.Device && q.Parent == spec.Parent
	})
	// Measured: a root add over the implicit qdisc succeeds and replaces it, while
	// one over a qdisc somebody wrote answers EEXIST.
	if at >= 0 && !(k.qdiscs[at].Parent == rootParent && k.qdiscs[at].Handle == 0) {
		return fmt.Errorf("%w: %s", shaping.ErrAlreadyInstalled, spec)
	}
	if at >= 0 {
		k.qdiscs = slices.Delete(k.qdiscs, at, at+1)
	}
	// A leaf's handle is the kernel's to pick, and it is picked from a rising
	// counter — nothing in the diff may depend on the value.
	if spec.Handle == 0 {
		spec.Handle = uint32(k.nextLeaf) << 16
		k.nextLeaf++
	}
	k.qdiscs = append(k.qdiscs, spec)
	return nil
}

func (k *Kernel) DelQdisc(_ context.Context, spec shaping.QdiscSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("qdisc-", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	at := slices.IndexFunc(k.qdiscs, func(q shaping.QdiscSpec) bool {
		return q.Device == spec.Device && q.Parent == spec.Parent
	})
	if at < 0 {
		return fmt.Errorf("%w: %s", shaping.ErrNotInstalled, spec)
	}
	// Deleting the root takes the whole tree, which is why a teardown that walks
	// the classes first must still find nothing left to do afterwards.
	if k.qdiscs[at].Parent == rootParent {
		k.dropTree(spec.Device)
		// The kernel puts the implicit qdisc back the moment ours is gone.
		k.qdiscs = append(k.qdiscs, shaping.QdiscSpec{Device: spec.Device, Type: "noqueue", Parent: rootParent})
		return nil
	}
	k.qdiscs = slices.Delete(k.qdiscs, at, at+1)
	return nil
}

func (k *Kernel) AddClass(_ context.Context, spec shaping.ClassSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("class+", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	if k.classAt(spec.Device, spec.Handle) >= 0 {
		return fmt.Errorf("%w: %s", shaping.ErrAlreadyInstalled, spec)
	}
	k.classes = append(k.classes, spec)
	return nil
}

func (k *Kernel) ChangeClass(_ context.Context, spec shaping.ClassSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("class~", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	at := k.classAt(spec.Device, spec.Handle)
	if at < 0 {
		return fmt.Errorf("%w: %s", shaping.ErrNotInstalled, spec)
	}
	// The live-edit path: the class keeps its identity and its byte counters, and
	// only the two rates move. A delete-and-re-add here would zero them.
	k.classes[at] = spec
	return nil
}

func (k *Kernel) DelClass(_ context.Context, spec shaping.ClassSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("class-", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	at := k.classAt(spec.Device, spec.Handle)
	if at < 0 {
		return fmt.Errorf("%w: %s", shaping.ErrNotInstalled, spec)
	}
	if slices.ContainsFunc(k.filters, func(f shaping.FilterSpec) bool {
		return f.Device == spec.Device && f.ClassID == spec.Handle
	}) {
		return fmt.Errorf("%w: %s is still selected by a filter", shaping.ErrInUse, spec)
	}
	k.classes = slices.Delete(k.classes, at, at+1)
	// The leaf goes with its class, which is why a teardown that already removed
	// it must not then be told the qdisc is missing.
	k.qdiscs = slices.DeleteFunc(k.qdiscs, func(q shaping.QdiscSpec) bool {
		return q.Device == spec.Device && q.Parent == spec.Handle
	})
	return nil
}

func (k *Kernel) AddFilter(_ context.Context, spec shaping.FilterSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("filter+", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	if spec.Redirect != "" {
		if err := k.present(spec.Redirect); err != nil {
			return err
		}
	}
	for _, have := range k.filters {
		switch {
		case have.Device != spec.Device || have.Parent != spec.Parent:
		case have.Priority == spec.Priority && family(have) != family(spec):
			return fmt.Errorf("%w: %s at prio %d", shaping.ErrPriorityInUse, spec.Device, spec.Priority)
		case sameRule(have, spec):
			return fmt.Errorf("%w: %s", shaping.ErrAlreadyInstalled, spec)
		}
	}
	// Measured: a filter naming a class that does not exist is ACCEPTED, so the
	// build order here is the panel's choice and never the kernel's constraint.
	spec.Handle = k.takeHandle(spec)
	k.filters = append(k.filters, spec)
	return nil
}

func (k *Kernel) DelFilter(_ context.Context, spec shaping.FilterSpec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.op("filter-", spec); err != nil {
		return err
	}
	if err := k.present(spec.Device); err != nil {
		return err
	}
	at := slices.IndexFunc(k.filters, func(f shaping.FilterSpec) bool { return f == spec })
	if at < 0 {
		return fmt.Errorf("%w: %s", shaping.ErrNotInstalled, spec)
	}
	k.filters = slices.Delete(k.filters, at, at+1)
	return nil
}

func (k *Kernel) classAt(device string, handle uint32) int {
	return slices.IndexFunc(k.classes, func(c shaping.ClassSpec) bool {
		return c.Device == device && c.Handle == handle
	})
}

// dropTree is what deleting the root qdisc takes with it. The clsact hook hangs
// off a different parent, so it and its ingress filters survive.
func (k *Kernel) dropTree(device string) {
	k.qdiscs = slices.DeleteFunc(k.qdiscs, func(q shaping.QdiscSpec) bool {
		return q.Device == device && q.Parent != clsactParent
	})
	k.classes = slices.DeleteFunc(k.classes, func(c shaping.ClassSpec) bool { return c.Device == device })
	k.filters = slices.DeleteFunc(k.filters, func(f shaping.FilterSpec) bool {
		return f.Device == device && f.Parent != ingressBlock
	})
}

func (k *Kernel) delLink(name string) {
	k.links = slices.DeleteFunc(k.links, func(l string) bool { return l == name })
	k.qdiscs = slices.DeleteFunc(k.qdiscs, func(q shaping.QdiscSpec) bool { return q.Device == name })
	k.classes = slices.DeleteFunc(k.classes, func(c shaping.ClassSpec) bool { return c.Device == name })
	k.filters = slices.DeleteFunc(k.filters, func(f shaping.FilterSpec) bool { return f.Device == name })
}

// AddLink brings a device somebody else owns onto the host: the core's own
// interface, which this package installs objects on but never creates.
func (k *Kernel) AddLink(names ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, name := range names {
		k.addLink(name)
	}
}

/*
DelLink models the core's device going away under the panel.

Measured: the kernel takes that device's clsact qdisc and its mirred filters with
it, and the mirror device it redirected to SURVIVES — which is the whole reason
the GC enumerates links rather than reading a snapshot.
*/
func (k *Kernel) DelLink(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.delLink(name)
}

// Seed plants objects the panel did not write: an operator's own tc, or what an
// earlier process left behind.
func (k *Kernel) Seed(qdiscs []shaping.QdiscSpec, classes []shaping.ClassSpec, filters []shaping.FilterSpec) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.qdiscs = append(k.qdiscs, qdiscs...)
	k.classes = append(k.classes, classes...)
	for _, filter := range filters {
		if filter.Handle == 0 {
			filter.Handle = k.takeHandle(filter)
		}
		k.filters = append(k.filters, filter)
	}
}

// Retarget re-points one selector's filter at another class, which is the shape
// of out-of-band damage that shapes one user as another.
func (k *Kernel) Retarget(prefix netip.Prefix, classID uint32) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for i, filter := range k.filters {
		if filter.Prefix == prefix && filter.ClassID != 0 {
			k.filters[i].ClassID = classID
		}
	}
}

// Retune moves a class's rate behind the panel's back, so a readback can be
// proven to report the kernel rather than what the panel last pushed.
func (k *Kernel) Retune(device string, handle uint32, bytesPerSec uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if at := k.classAt(device, handle); at >= 0 {
		k.classes[at].RateBytesPerSec = bytesPerSec
		k.classes[at].CeilBytesPerSec = bytesPerSec
	}
}

// Tree is everything the stand-in holds now, sorted so a test compares against a
// fixed list rather than against insertion order.
func (k *Kernel) Tree() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, len(k.qdiscs)+len(k.classes)+len(k.filters))
	for _, qdisc := range k.qdiscs {
		out = append(out, qdisc.String())
	}
	for _, class := range k.classes {
		out = append(out, class.String())
	}
	for _, filter := range k.filters {
		out = append(out, filter.String())
	}
	slices.Sort(out)
	return out
}

// Devices is every link the stand-in holds, so the mirror GC can be observed.
func (k *Kernel) Devices() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.links)
}

// Ops is every write attempted, in order. Order is the point: a class must never
// be deleted while a filter still selects it, and never added after one does.
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

// Snapshots is how many device reads have happened. A converged pass writes
// nothing, so this is the only way to observe that one ran at all.
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
