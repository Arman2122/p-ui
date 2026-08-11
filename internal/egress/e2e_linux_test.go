//go:build linux

package egress

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

/*
Drives the real plane against a real kernel. Everything else in this package
runs against egtest, and a fake cannot catch what makes netlink surprising: a
device route the kernel stores at SCOPE_LINK is not the one a delete sent at
SCOPE_UNIVERSE finds, and a route dump omits every non-main table unless the
table filter is set. Both were measured, and both look perfect in the fake.

Run it inside a private network namespace — `ip netns exec <ns> ./egress.test` —
because it writes rules and routes the host would otherwise keep.
*/

const e2eID = 901

func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("PUI_EGRESS_E2E") != "1" {
		t.Skip("set PUI_EGRESS_E2E=1 to run against the real kernel (needs root and a private netns)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
}

type e2eDriver struct{}

func (e2eDriver) Type() string { return "e2e" }

func (e2eDriver) Fill(e Egress) (Fill, error) {
	device := Device(e.ID)
	return Fill{Device: device, Sysctls: map[string]string{"net.ipv4.conf." + device + ".rp_filter": "0"}}, nil
}

func liveManager(t *testing.T) *Manager {
	t.Helper()
	e2e(t)
	registry := NewRegistry()
	if err := registry.Register(e2eDriver{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	m := New(hostPlane(), registry)
	t.Cleanup(func() {
		_ = m.Remove(context.Background(), e2eID)
		_ = exec.Command("ip", "link", "del", Device(e2eID)).Run()
	})
	return m
}

func liveRow() Egress {
	return Egress{ID: e2eID, Type: "e2e", Enable: true, Ingress: []string{"pwg77"}}
}

// addFront creates the device the row's table points at. A dummy routes exactly
// as the tun does; what is under test here is netlink, not gVisor.
func addFront(t *testing.T) {
	t.Helper()
	run(t, "ip", "link", "add", Device(e2eID), "type", "dummy")
	run(t, "ip", "link", "set", Device(e2eID), "up")
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

// band is everything the kernel holds for this egress, as the manager sees it.
func band(t *testing.T, m *Manager) []string {
	t.Helper()
	snap, err := m.plane.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var out []string
	for _, rule := range snap.Rules {
		if rule.Priority == Prio(e2eID) {
			out = append(out, rule.String())
		}
	}
	for _, route := range snap.Routes {
		if route.Table == Table(e2eID) {
			out = append(out, route.String())
		}
	}
	slices.Sort(out)
	return out
}

func assertBand(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("band =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func fullBand() []string {
	return []string{
		"v4 prio 31901 iif pwg77 lookup 30901",
		"v4 table 30901 blackhole default metric 4096",
		"v4 table 30901 default dev peg901 metric 100",
		"v6 prio 31901 iif pwg77 lookup 30901",
		"v6 table 30901 blackhole default metric 4096",
		"v6 table 30901 default dev peg901 metric 100",
	}
}

func containedBand() []string {
	return []string{
		"v4 prio 31901 iif pwg77 lookup 30901",
		"v4 table 30901 blackhole default metric 4096",
		"v6 prio 31901 iif pwg77 lookup 30901",
		"v6 table 30901 blackhole default metric 4096",
	}
}

// TestLiveConvergence is the one that matters: a second pass reporting no error
// proves the snapshot reads back exactly what the plan asked for.
func TestLiveConvergence(t *testing.T) {
	m := liveManager(t)
	addFront(t)
	ctx := context.Background()

	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	assertBand(t, band(t, m), fullBand())

	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("second Ensure re-issued writes the kernel already had: %v", err)
	}
	assertBand(t, band(t, m), fullBand())

	knob, err := m.plane.Sysctl(ctx, "net.ipv4.conf."+Device(e2eID)+".rp_filter")
	if err != nil {
		t.Fatalf("read the knob: %v", err)
	}
	if knob != "0" {
		t.Fatalf("rp_filter = %q, want 0 — the front's return path is filtered away", knob)
	}
}

// TestLiveAdoptionOfHandMadeObjects is the scope bug, pinned: iproute2 gives a v4
// device route SCOPE_LINK, and a plane writing SCOPE_UNIVERSE would churn forever.
func TestLiveAdoptionOfHandMadeObjects(t *testing.T) {
	m := liveManager(t)
	addFront(t)
	table, prio, device := "30901", "31901", Device(e2eID)

	run(t, "ip", "route", "add", "blackhole", "default", "table", table, "metric", "4096")
	run(t, "ip", "route", "add", "default", "dev", device, "table", table, "metric", "100")
	run(t, "ip", "-6", "route", "add", "blackhole", "default", "table", table, "metric", "4096")
	run(t, "ip", "-6", "route", "add", "default", "dev", device, "table", table, "metric", "100")
	run(t, "ip", "rule", "add", "iif", "pwg77", "lookup", table, "priority", prio)
	run(t, "ip", "-6", "rule", "add", "iif", "pwg77", "lookup", table, "priority", prio)

	before := band(t, m)
	assertBand(t, before, fullBand())
	if err := m.Ensure(context.Background(), liveRow()); err != nil {
		t.Fatalf("Ensure over hand-made state: %v", err)
	}
	assertBand(t, band(t, m), before)
}

// TestLiveFailClosed is the kernel invariant the design rests on: the device dies
// with the core process and takes the only route out of the table with it.
func TestLiveFailClosed(t *testing.T) {
	m := liveManager(t)
	addFront(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	run(t, "ip", "link", "del", Device(e2eID))
	assertBand(t, band(t, m), containedBand())
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure with the front gone: %v", err)
	}
	assertBand(t, band(t, m), containedBand())

	addFront(t)
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure once the front is back: %v", err)
	}
	assertBand(t, band(t, m), fullBand())
}

// TestLiveContainmentBeforeTheFrontExists pins the boot ordering property: a rule
// installs with its device absent and attaches itself when the device appears.
func TestLiveContainmentBeforeTheFrontExists(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure with no device at all: %v", err)
	}
	assertBand(t, band(t, m), containedBand())

	addFront(t)
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure once the front appeared: %v", err)
	}
	assertBand(t, band(t, m), fullBand())
}

func TestLiveTeardownEmptiesTheBand(t *testing.T) {
	m := liveManager(t)
	addFront(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := m.Remove(ctx, e2eID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertBand(t, band(t, m), nil)
	if err := m.Remove(ctx, e2eID); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

// TestLiveTwoEgressesDoNotCollide is the case the plan called unproven: two
// fronts and two tables in one process, each reaching only its own device.
func TestLiveTwoEgressesDoNotCollide(t *testing.T) {
	m := liveManager(t)
	second := e2eID + 1
	t.Cleanup(func() {
		_ = m.Remove(context.Background(), second)
		_ = exec.Command("ip", "link", "del", Device(second)).Run()
	})
	addFront(t)
	run(t, "ip", "link", "add", Device(second), "type", "dummy")
	run(t, "ip", "link", "set", Device(second), "up")

	rows := []Egress{liveRow(), {ID: second, Type: "e2e", Enable: true, Ingress: []string{"pwg78"}}}
	ctx := context.Background()
	if err := m.Reconcile(ctx, rows); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	assertBand(t, band(t, m), fullBand())

	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var other []string
	for _, route := range snap.Routes {
		if route.Table == Table(second) {
			other = append(other, route.String())
		}
	}
	slices.Sort(other)
	assertBand(t, other, []string{
		"v4 table 30902 blackhole default metric 4096",
		"v4 table 30902 default dev peg902 metric 100",
		"v6 table 30902 blackhole default metric 4096",
		"v6 table 30902 default dev peg902 metric 100",
	})

	// The row that is dropped from the desired set takes its own objects and
	// nothing else: a reconciler that deletes by band would empty both tables.
	if err := m.Reconcile(ctx, rows[:1]); err != nil {
		t.Fatalf("Reconcile without the second row: %v", err)
	}
	assertBand(t, band(t, m), fullBand())
	snap, err = m.plane.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, route := range snap.Routes {
		if route.Table == Table(second) {
			t.Fatalf("the stranded table still holds %s", route)
		}
	}
}

/*
TestLivePreflightAcceptsItsOwnFronts is the steady state of any host that runs an
egress at all: the front carries the gateway its id derives, because an addressless
front fails reverse-path filtering on the return path.

Counting that address as a foreign collision refuses every attach from the moment
the first front exists — which is every panel with an enabled egress.
*/
func TestLivePreflightAcceptsItsOwnFronts(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	addFront(t)
	gateway, err := Gateway(DefaultGatewayBase, e2eID)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	run(t, "ip", "addr", "add", gateway.String(), "dev", Device(e2eID))

	if refused := gatewayRefusals(m.Preflight(ctx, DefaultGatewayBase)); len(refused) != 0 {
		t.Fatalf("preflight refused the panel's own front: %v", refused)
	}

	// The same band on a device the panel does not own is still a real collision:
	// something else would answer for the front's return traffic.
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "phsquat0").Run() })
	run(t, "ip", "link", "add", "phsquat0", "type", "dummy")
	run(t, "ip", "addr", "add", "100.127.0.5/32", "dev", "phsquat0")
	refused := gatewayRefusals(m.Preflight(ctx, DefaultGatewayBase))
	if len(refused) != 1 || !strings.Contains(refused[0], "100.127.0.5/32 on phsquat0") {
		t.Fatalf("preflight refusals = %v, want exactly the squatter named", refused)
	}
}

// gatewayRefusals is the half of the report this test is about, so a host whose
// rp_filter or band is unusable for other reasons fails on those, not on this.
func gatewayRefusals(report Report) []string {
	var out []string
	for _, refusal := range report.Refusals {
		if errors.Is(refusal, ErrGatewayBase) {
			out = append(out, refusal.Error())
		}
	}
	return out
}

/*
A host that switched IPv6 off on the front device refuses the v6 front route with
EACCES — indistinguishable from a missing CAP_NET_ADMIN without asking the device.

Measured on 6.8.0-111: the v6 rule and the v6 blackhole install regardless, so v6
is contained. Reporting it would return an error on every pass forever.
*/
func TestLiveFamilyDisabledOnTheFrontIsContained(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	addFront(t)
	knob := "net.ipv6.conf." + Device(e2eID) + ".disable_ipv6"
	run(t, "sysctl", "-qw", knob+"=1")

	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure with IPv6 disabled on the front: %v", err)
	}
	assertBand(t, band(t, m), []string{
		"v4 prio 31901 iif pwg77 lookup 30901",
		"v4 table 30901 blackhole default metric 4096",
		"v4 table 30901 default dev peg901 metric 100",
		"v6 prio 31901 iif pwg77 lookup 30901",
		"v6 table 30901 blackhole default metric 4096",
	})

	run(t, "sysctl", "-qw", knob+"=0")
	if err := m.Ensure(ctx, liveRow()); err != nil {
		t.Fatalf("Ensure once IPv6 came back: %v", err)
	}
	assertBand(t, band(t, m), fullBand())
}

// TestLiveForeignObjectsSurvive is the ownership rule against the real kernel:
// the reconciler walks the whole band and must leave what it did not write.
func TestLiveForeignObjectsSurvive(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_ = exec.Command("ip", "rule", "del", "priority", "31905").Run()
		_ = exec.Command("ip", "route", "del", "prohibit", "192.168.77.0/24", "table", "30906").Run()
	})
	run(t, "ip", "rule", "add", "iif", "pwg99", "lookup", "30777", "priority", "31905")
	run(t, "ip", "route", "add", "prohibit", "192.168.77.0/24", "table", "30906")

	if err := m.Reconcile(ctx, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var survived []string
	for _, rule := range snap.Rules {
		survived = append(survived, rule.String())
	}
	for _, route := range snap.Routes {
		survived = append(survived, route.String())
	}
	slices.Sort(survived)
	assertBand(t, survived, []string{
		"v4 prio 31905 iif pwg99 lookup 30777",
		"v4 table 30906 192.168.77.0/24 dev  metric 0",
	})
}
