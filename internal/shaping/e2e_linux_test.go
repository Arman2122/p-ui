//go:build linux

package shaping

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

/*
Drives the real plane against a real kernel. Everything else in this package runs
against shapetest, and a fake cannot catch what makes tc surprising: the kernel
keys a filter chain on (protocol, priority) and refuses a v6 filter beside a v4
one at the same priority, and a class's rate survives a round trip only in bytes
per second. Both were measured, and both look perfect in the fake.

Run it inside a private network namespace — `ip netns exec <ns> ./shaping.test` —
because it creates devices and qdiscs the host would otherwise keep.
*/
const (
	e2eDevice = "pwg901"
	e2eMirror = "pifb901"
)

var (
	e2eV4 = netip.MustParsePrefix("10.9.0.2/32")
	e2eV6 = netip.MustParsePrefix("fd00::2/128")
)

func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("PUI_SHAPING_E2E") != "1" {
		t.Skip("set PUI_SHAPING_E2E=1 to run against the real kernel (needs root and a private netns)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if !confinedToOwnNetns("/proc/self/ns/net", "/proc/1/ns/net") {
		t.Skip("run inside a private network namespace: these tests create devices and rewrite qdiscs")
	}
}

// confinedToOwnNetns reports whether this process left init's network namespace.
// Unreadable is not confined: the only safe answer is the one that skips.
func confinedToOwnNetns(selfPath, initPath string) bool {
	self, err := os.Readlink(selfPath)
	if err != nil {
		return false
	}
	init, err := os.Readlink(initPath)
	if err != nil {
		return false
	}
	return self != init
}

// liveManager builds the device the core would own and guarantees the namespace
// is left with nothing of this panel's on it, however the test ends.
func liveManager(t *testing.T) (*Manager, Plane) {
	t.Helper()
	e2e(t)
	plane := hostPlane()
	sh(t, "ip", "link", "del", e2eDevice)
	sh(t, "ip", "link", "del", e2eMirror)
	mustSh(t, "ip", "link", "add", e2eDevice, "type", "dummy")
	mustSh(t, "ip", "link", "set", e2eDevice, "up")
	t.Cleanup(func() {
		sh(t, "ip", "link", "del", e2eDevice)
		sh(t, "ip", "link", "del", e2eMirror)
		sh(t, "ip", "link", "del", ProbeDevice)
	})
	return NewManager(plane, DefaultNamespaces()), plane
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, _ := exec.Command(name, args...).CombinedOutput()
	return string(out)
}

func mustSh(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v %s", name, strings.Join(args, " "), err, out)
	}
}

func e2eWant(down, up int64, prefixes ...netip.Prefix) []DeviceWant {
	keys := make([]Key, 0, len(prefixes))
	for _, prefix := range prefixes {
		keys = append(keys, Key{Prefix: prefix})
	}
	return []DeviceWant{{Device: e2eDevice, Subjects: []Subject{
		{ID: "alice", Keys: keys, Limits: Limits{DownBps: down, UpBps: up}},
	}}}
}

/*
TestLiveConvergeIsIdempotent is the anti-churn property against real netlink.

What the research measured was tc's TEXT output; whether netlink's own readback in
bytes per second equals the pushed value byte for byte was inferred. This is where
a residual surfaces, and the fix is the canonicalisation — never a tolerance, which
would hide the residual and issue a ClassChange every pass forever.
*/
func TestLiveConvergeIsIdempotent(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	// 12345678 bit/s is 1543209 bytes/s and reads back as 12345672 bit/s.
	want := e2eWant(12_345_678, 7_654_321, e2eV4, e2eV6)
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("first Converge: %v", err)
	}

	before := liveTree(t, plane, e2eDevice) + liveTree(t, plane, e2eMirror)
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("second Converge: %v", err)
	}
	after := liveTree(t, plane, e2eDevice) + liveTree(t, plane, e2eMirror)
	if before != after {
		t.Fatalf("a second pass moved the kernel\nbefore\n%s\nafter\n%s", before, after)
	}

	enforced, err := m.Enforced(ctx, want[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	// The readback is in the kernel's own unit, so the bits come back truncated.
	if got := (Limits{DownBps: 12_345_672, UpBps: 7_654_320}); enforced["alice"] != got {
		t.Fatalf("Enforced = %+v, want %+v", enforced["alice"], got)
	}
}

// TestLiveBothFamiliesClassifyIntoOneClass is the v6-is-a-peer requirement, and
// the reason the two filters sit at their own priorities: measured, the kernel
// answers EINVAL to a second protocol at a priority another already holds.
func TestLiveBothFamiliesClassifyIntoOneClass(t *testing.T) {
	m, plane := liveManager(t)
	if err := m.Converge(context.Background(), e2eWant(10_000_000, 0, e2eV4, e2eV6)); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	snap, err := plane.Snapshot(context.Background(), e2eDevice)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var classes, filters int
	target := map[netip.Prefix]uint32{}
	for _, class := range snap.Classes {
		if minor, ok := classMinor(class.Handle); ok && minor != defaultMinor {
			classes++
		}
	}
	for _, filter := range snap.Filters {
		if filter.Parent == rootHandle && filter.Prefix.IsValid() {
			filters++
			target[filter.Prefix] = filter.ClassID
		}
	}
	if classes != 1 || filters != 2 {
		t.Fatalf("got %d classes and %d filters, want one class selected by two", classes, filters)
	}
	if target[e2eV4] == 0 || target[e2eV4] != target[e2eV6] {
		t.Fatalf("the families landed on %v; a dual-stack user shares one budget", target)
	}
	if !liveHasDefaultAt(t, plane, e2eDevice, KernelBytesPerSec(UnlimitedBps)) {
		t.Fatalf("the default class is not explicit at UnlimitedBps: %s", liveTree(t, plane, e2eDevice))
	}
}

/*
TestLiveClassChangePreservesCounters is what makes a tier crossing free.

Measured on real traffic: the class keeps its byte counters across a change, so a
customer crossing a threshold is slowed rather than disconnected. A
delete-and-re-add implementation zeroes them and sheds the in-flight window.
*/
func TestLiveClassChangePreservesCounters(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	if err := m.Converge(ctx, e2eWant(100_000_000, 0, e2eV4)); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	before := liveFilters(t, plane, e2eDevice)

	if err := m.Converge(ctx, e2eWant(10_000_000, 0, e2eV4)); err != nil {
		t.Fatalf("retune: %v", err)
	}
	after := liveFilters(t, plane, e2eDevice)
	if before != after {
		t.Fatalf("a rate change moved the filters\nbefore %s\nafter  %s", before, after)
	}

	enforced, err := m.Enforced(ctx, e2eWant(10_000_000, 0, e2eV4)[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if enforced["alice"].DownBps != 10_000_000 {
		t.Fatalf("Enforced after the change = %d, want 10000000", enforced["alice"].DownBps)
	}
}

// TestLiveMirrorSurvivesItsParentAndIsCollected: measured, deleting the core's
// device takes its clsact qdisc and mirred filters but leaves the ifb standing.
func TestLiveMirrorSurvivesItsParentAndIsCollected(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	if err := m.Converge(ctx, e2eWant(0, 10_000_000, e2eV4)); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !liveHasLink(t, plane, e2eMirror) {
		t.Fatal("the upload limit created no mirror device")
	}

	mustSh(t, "ip", "link", "del", e2eDevice)
	if !liveHasLink(t, plane, e2eMirror) {
		t.Fatal("the mirror died with its parent; the fake models it as surviving")
	}
	if err := m.Converge(ctx, nil); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if liveHasLink(t, plane, e2eMirror) {
		t.Fatal("a stranded mirror was not collected")
	}
}

// TestLiveForeignRootQdiscIsReportedAndNotDeleted: the panel never deletes an
// operator's tree, and the plane has no ChangeQdisc that could repair one.
func TestLiveForeignRootQdiscIsReportedAndNotDeleted(t *testing.T) {
	m, plane := liveManager(t)
	mustSh(t, "tc", "qdisc", "add", "dev", e2eDevice, "root", "handle", "9:", "fq_codel")

	err := m.Converge(context.Background(), e2eWant(10_000_000, 0, e2eV4))
	if !errors.Is(err, ErrForeignObject) {
		t.Fatalf("Converge = %v, want ErrForeignObject", err)
	}
	if tree := liveTree(t, plane, e2eDevice); !strings.Contains(tree, "fq_codel handle 9:0") {
		t.Fatalf("the operator's root qdisc was replaced: %s", tree)
	}
}

// TestLivePreflightLeavesTheHostAsItFoundIt: shaping is disabled rather than the
// panel stopped, so the probe must be reversible on a host that passes it.
func TestLivePreflightLeavesTheHostAsItFoundIt(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	before, err := plane.Links(ctx)
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if report := m.Preflight(ctx); !report.OK() {
		t.Fatalf("Preflight on a host with every module loaded: %v", report.Err())
	}
	after, err := plane.Links(ctx)
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Fatalf("preflight changed the host\nbefore %v\nafter  %v", before, after)
	}
}

// TestLiveTeardownLeavesNothing is the cleanliness contract this whole namespace
// discipline exists for: converging to nothing must leave nothing.
func TestLiveTeardownLeavesNothing(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	if err := m.Converge(ctx, e2eWant(10_000_000, 10_000_000, e2eV4, e2eV6)); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if err := m.Converge(ctx, []DeviceWant{{Device: e2eDevice}}); err != nil {
		t.Fatalf("teardown: %v", err)
	}

	if liveHasLink(t, plane, e2eMirror) {
		t.Fatalf("the mirror survived a teardown")
	}
	snap, err := plane.Snapshot(ctx, e2eDevice)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Classes) != 0 || len(snap.Filters) != 0 {
		t.Fatalf("objects survived the teardown: %s", liveTree(t, plane, e2eDevice))
	}
	for _, qdisc := range snap.Qdiscs {
		if qdisc.Handle != 0 {
			t.Fatalf("a qdisc this panel wrote survived the teardown: %s", qdisc)
		}
	}
}

func liveTree(t *testing.T, plane Plane, device string) string {
	t.Helper()
	snap, err := plane.Snapshot(context.Background(), device)
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", device, err)
	}
	if !snap.Exists {
		return device + ": absent\n"
	}
	var out []string
	for _, qdisc := range snap.Qdiscs {
		out = append(out, qdisc.String())
	}
	for _, class := range snap.Classes {
		out = append(out, class.String())
	}
	for _, filter := range snap.Filters {
		out = append(out, filter.String())
	}
	slices.Sort(out)
	return strings.Join(out, "\n") + "\n"
}

// liveFilters is the classification alone, so a rate change can be proven not to
// have touched it — the filter's classid is what a re-add would move.
func liveFilters(t *testing.T, plane Plane, device string) string {
	t.Helper()
	snap, err := plane.Snapshot(context.Background(), device)
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", device, err)
	}
	var out []string
	for _, filter := range snap.Filters {
		out = append(out, fmt.Sprintf("%s handle %d", filter, filter.Handle))
	}
	slices.Sort(out)
	return strings.Join(out, " | ")
}

func liveHasLink(t *testing.T, plane Plane, name string) bool {
	t.Helper()
	links, err := plane.Links(context.Background())
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	return slices.Contains(links, name)
}

func liveHasDefaultAt(t *testing.T, plane Plane, device string, rate uint64) bool {
	t.Helper()
	snap, err := plane.Snapshot(context.Background(), device)
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", device, err)
	}
	for _, class := range snap.Classes {
		if class.Handle == classHandle(defaultMinor) {
			return class.RateBytesPerSec == rate && class.CeilBytesPerSec == rate
		}
	}
	return false
}
