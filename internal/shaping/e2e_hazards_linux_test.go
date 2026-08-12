//go:build linux

package shaping

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

/*
The four ways a converged tree can look perfect and enforce nothing.

Every one of them was measured on this kernel with iperf3 and every one of them
returned nil from Converge, so none is reachable through the fake alone: three
depend on objects the panel does not write (a foreign filter, the clsact's other
block) and the fourth on a device outliving the pass that wanted it.
*/

// TestLiveAForeignIngressFilterAheadOfOursIsAnError.
//
// Measured: with a matchall at prio 1 the client achieved 7029 Mbit/s against a
// contracted 10, because tcf_classify returns on the first filter with a verdict.
func TestLiveAForeignIngressFilterAheadOfOursIsAnError(t *testing.T) {
	m, _ := liveManager(t)
	ctx := context.Background()
	want := e2eWant(0, 10_000_000, e2eV4)
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	mustSh(t, "tc", "filter", "add", "dev", e2eDevice, "ingress",
		"protocol", "ip", "prio", "1", "matchall", "action", "pass")

	err := m.Converge(ctx, want)
	if !errors.Is(err, ErrForeignObject) {
		t.Fatalf("Converge = %v, want ErrForeignObject: a shaper that classifies nothing while reporting success is the worst outcome available", err)
	}
	// Read with tc: the panel's own vocabulary has no matchall, so its rendering
	// of a foreign rule cannot say whether the right one survived.
	if filters := sh(t, "tc", "filter", "show", "dev", e2eDevice, "ingress"); !strings.Contains(filters, "matchall") {
		t.Fatalf("the operator's filter was deleted rather than reported: %q", filters)
	}
}

/*
TestLiveEnforcedNeedsTheRedirect.

Deleting the clsact is exactly what the kernel leaves behind when the core
recreates its device: measured, the hook and every mirred filter die with pwg
while the ifb mirror survives with its whole HTB tree intact. Reading only the
mirror then reports a contracted upload rate that nothing feeds.
*/
func TestLiveEnforcedNeedsTheRedirect(t *testing.T) {
	m, _ := liveManager(t)
	ctx := context.Background()
	want := e2eWant(10_000_000, 10_000_000, e2eV4)
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	before, err := m.Enforced(ctx, want[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if got := (Limits{DownBps: 10_000_000, UpBps: 10_000_000}); before["alice"] != got {
		t.Fatalf("Enforced = %+v, want %+v", before["alice"], got)
	}

	mustSh(t, "tc", "qdisc", "del", "dev", e2eDevice, "clsact")
	after, err := m.Enforced(ctx, want[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if after["alice"].UpBps != 0 {
		t.Fatalf("Enforced.UpBps = %d with nothing redirecting into the mirror, want 0", after["alice"].UpBps)
	}
	if after["alice"].DownBps != 10_000_000 {
		t.Fatalf("the download half is untouched and must still report: got %d", after["alice"].DownBps)
	}
}

/*
TestLiveAnOperatorsEgressFilterKeepsTheSharedHook.

One clsact carries ffff:fff2 and ffff:fff3, and deleting it takes both. Measured:
without reading the egress block the panel removed an administrator's qdisc and
their tc-egress filter on a routine move to a download-only tier, and returned nil.
*/
func TestLiveAnOperatorsEgressFilterKeepsTheSharedHook(t *testing.T) {
	m, _ := liveManager(t)
	ctx := context.Background()
	mustSh(t, "tc", "qdisc", "add", "dev", e2eDevice, "clsact")
	mustSh(t, "tc", "filter", "add", "dev", e2eDevice, "egress",
		"protocol", "ip", "prio", "1", "matchall", "action", "pass")

	if err := m.Converge(ctx, e2eWant(0, 10_000_000, e2eV4)); err != nil {
		t.Fatalf("Converge with an upload limit: %v", err)
	}
	if err := m.Converge(ctx, e2eWant(10_000_000, 0, e2eV4)); err != nil {
		t.Fatalf("Converge down to download only: %v", err)
	}

	if hooks := sh(t, "tc", "qdisc", "show", "dev", e2eDevice); !strings.Contains(hooks, "clsact") {
		t.Fatalf("the administrator's hook was deleted with ours: %s", hooks)
	}
	if filters := sh(t, "tc", "filter", "show", "dev", e2eDevice, "egress"); !strings.Contains(filters, "matchall") {
		t.Fatalf("the administrator's egress filter went with the hook: %q", filters)
	}
}

/*
TestLiveAMirrorIsNotCollectedUnderALiveRedirect.

Reachable by disabling an inbound: its device leaves the want at once and is torn
down by a different job on its own schedule. The kernel answers TC_ACT_SHOT to a
redirect at a departed device, so reaping the mirror early does not leave the
client unshaped — measured, it left them at 100% packet loss.
*/
func TestLiveAMirrorIsNotCollectedUnderALiveRedirect(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()
	if err := m.Converge(ctx, e2eWant(0, 10_000_000, e2eV4)); err != nil {
		t.Fatalf("Converge: %v", err)
	}

	// The device is simply absent from this pass's want, which is all the
	// disable path does before the engine gets round to removing it.
	if err := m.Converge(ctx, nil); err != nil {
		t.Fatalf("Converge with the device left out: %v", err)
	}
	if !liveHasLink(t, plane, e2eMirror) {
		t.Fatal("the mirror was reaped while a live mirred filter still pointed at it")
	}
	if filters := sh(t, "tc", "filter", "show", "dev", e2eDevice, "ingress"); !strings.Contains(filters, e2eMirror) {
		t.Fatalf("the redirect must still name a device that exists, got %q", filters)
	}

	mustSh(t, "ip", "link", "del", e2eDevice)
	if err := m.Converge(ctx, nil); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if liveHasLink(t, plane, e2eMirror) {
		t.Fatal("keeping a pinned mirror must be a delay and never a leak")
	}
}

/*
TestLiveASteadyStatePassAtScaleWritesNothing.

The anti-churn property at the scale the design budgeted for, against real
netlink rather than the fake. The duration is logged rather than asserted — a
wall-clock bound is a flaky test — but it is the number that says whether a pass
still fits inside the job's ten-second period.
*/
func TestLiveASteadyStatePassAtScaleWritesNothing(t *testing.T) {
	m, plane := liveManager(t)
	ctx := context.Background()

	const users = 2000
	subjects := make([]Subject, 0, users)
	for i := range users {
		addr := netip.AddrFrom4([4]byte{10, 9, byte(i >> 8), byte(i)})
		subjects = append(subjects, Subject{
			ID:     "user" + string(rune('a'+i%26)) + netip.PrefixFrom(addr, 32).String(),
			Keys:   []Key{{Prefix: netip.PrefixFrom(addr, 32)}},
			Limits: Limits{DownBps: 10_000_000, UpBps: 10_000_000},
		})
	}
	want := []DeviceWant{{Device: e2eDevice, Subjects: subjects}}

	build := time.Now()
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("build %d subjects: %v", users, err)
	}
	t.Logf("cold build of %d shaped users: %v", users, time.Since(build))

	before := liveTree(t, plane, e2eDevice)
	settled := time.Now()
	if err := m.Converge(ctx, want); err != nil {
		t.Fatalf("steady-state pass: %v", err)
	}
	t.Logf("steady-state pass over %d shaped users: %v", users, time.Since(settled))
	if after := liveTree(t, plane, e2eDevice); after != before {
		t.Fatal("a converged pass changed the kernel")
	}
}
