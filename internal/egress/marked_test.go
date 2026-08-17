package egress_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

/*
A probe that sets a mark nothing catches measures the host's DIRECT path and
reports the number under the uplink's name.

That is the one failure this guard exists for: it does not look like a failure.
The request succeeds, the latency is plausible, and the operator reads a working
Surfshark exit off a row whose traffic never entered the tunnel. So "routed" is
read from the host, and a half-converged egress refuses to answer rather than
answering wrongly.
*/
func TestMarkedExitRefusesWhenNothingCatchesTheMark(t *testing.T) {
	kernel := egtest.New()
	manager := newManager(t, kernel, &fakeUplink{kernel: kernel})

	_, err := manager.MarkedExit(context.Background(), 1)
	if !errors.Is(err, egress.ErrNotRouted) {
		t.Fatalf("MarkedExit on a host with no rules = %v, want ErrNotRouted", err)
	}
}

// A table holding only its blackhole is contained: the mark is caught, so
// nothing leaks, but there is no exit to time either.
func TestMarkedExitReportsNoDeviceWhileContained(t *testing.T) {
	kernel := egtest.New()
	driver := &fakeUplink{kernel: kernel}
	manager := newManager(t, kernel, driver)
	ctx := context.Background()

	if err := manager.Ensure(ctx, uplinkRow(1)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	kernel.DelLink(egress.Uplink(1))
	if err := manager.Ensure(ctx, uplinkRow(1)); err != nil {
		t.Fatalf("Ensure with the device gone: %v", err)
	}

	routed, err := manager.MarkedExit(ctx, 1)
	if err != nil {
		t.Fatalf("MarkedExit while contained = %v, want nil", err)
	}
	if routed.Device != "" {
		t.Fatalf("MarkedExit = %q, want empty: the device is gone, so only the blackhole is in the table", routed.Device)
	}
}

/*
The probe must dial only the families the egress actually carries.

Measured on the test host: a Surfshark uplink holding one v4 address gets a v4
route and a v6 blackhole, and a probe left to Go's happy-eyeballs picked the
AAAA, found no v6 source to select and failed with "connect: invalid argument".
A working exit, reported as broken, in the language of a syscall.
*/
func TestMarkedExitNetworkFollowsTheFamiliesCarried(t *testing.T) {
	for _, tc := range []struct {
		name        string
		route       egress.MarkedRoute
		wantNetwork string
		wantRouted  bool
	}{
		{"a v4-only uplink", egress.MarkedRoute{Device: "pux1", V4: true}, "tcp4", true},
		{"a v6-only uplink", egress.MarkedRoute{Device: "pux1", V6: true}, "tcp6", true},
		{"dual stack", egress.MarkedRoute{Device: "pux1", V4: true, V6: true}, "tcp", true},
		{"caught but contained", egress.MarkedRoute{}, "", false},
		// A device with no family routed drops everything inside the table, so
		// there is nothing to time and nothing to dial.
		{"a device with neither family", egress.MarkedRoute{Device: "pux1"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.route.Network(); got != tc.wantNetwork {
				t.Fatalf("Network() = %q, want %q", got, tc.wantNetwork)
			}
			if got := tc.route.Routed(); got != tc.wantRouted {
				t.Fatalf("Routed() = %v, want %v", got, tc.wantRouted)
			}
		})
	}
}

// With the device present the answer is the device itself, which is what the
// caller compares against the uplink it meant to probe.
func TestMarkedExitNamesTheUplinkDevice(t *testing.T) {
	kernel := egtest.New()
	manager := newManager(t, kernel, &fakeUplink{kernel: kernel})
	ctx := context.Background()

	// Twice: the first pass plans against a host where the driver has not made
	// its device yet, so the route into it lands on the pass that can see it.
	for range 2 {
		if err := manager.Ensure(ctx, uplinkRow(1)); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
	}
	routed, err := manager.MarkedExit(ctx, 1)
	if err != nil {
		t.Fatalf("MarkedExit: %v", err)
	}
	if routed.Device != egress.Uplink(1) {
		t.Fatalf("MarkedExit = %q, want %q", routed.Device, egress.Uplink(1))
	}
}
