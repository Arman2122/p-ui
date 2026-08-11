package shaping

import "testing"

/*
TestKernelBytesPerSecTruncates pins the anti-churn boundary.

HTB stores bytes per second. A diff that compares the requested bits against the
readback differs forever on any rate that is not a multiple of eight, and issues
a ClassChange every pass — the perpetual-churn defect class this codebase already
treats as a bug.
*/
func TestKernelBytesPerSecTruncates(t *testing.T) {
	cases := []struct {
		name string
		bps  int64
		want uint64
	}{
		{"zero is unlimited and installs nothing", 0, 0},
		{"a negative rate is not a rate", -1, 0},
		{"ten megabit", 10_000_000, 1_250_000},
		{"the rate that churns if it is not truncated once", 12_345_678, 1_543_209},
		{"unlimited canonicalises like any other rate", UnlimitedBps, 125_000_000_000},
		{"a rate below one byte per second floors to zero", 7, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KernelBytesPerSec(tc.bps); got != tc.want {
				t.Fatalf("KernelBytesPerSec(%d) = %d, want %d", tc.bps, got, tc.want)
			}
		})
	}
}

// TestKernelBytesPerSecIsIdempotentThroughTheKernelUnit is the property the diff
// rests on: canonicalising the readback again must not move it.
func TestKernelBytesPerSecIsIdempotentThroughTheKernelUnit(t *testing.T) {
	for _, bps := range []int64{1, 999, 12_345_678, 10_000_000, 2_000_000, UnlimitedBps} {
		once := KernelBytesPerSec(bps)
		twice := KernelBytesPerSec(int64(once) * 8)
		if once != twice {
			t.Fatalf("KernelBytesPerSec(%d) = %d but re-canonicalising its readback gives %d", bps, once, twice)
		}
	}
}

/*
TestQuantumStaysInsideTheKernelsOwnClamp keeps the default class off the kernel
log.

Measured: a class at UnlimitedBps with no explicit quantum makes the kernel print
"sch_htb: quantum of class 1FFFF is big. Consider r2q change." on every start.
Passing the value it would have clamped to silences it without changing behaviour.
*/
func TestQuantumStaysInsideTheKernelsOwnClamp(t *testing.T) {
	cases := []struct {
		name  string
		bytes uint64
		want  uint32
	}{
		{"a tiny rate floors at the kernel's own minimum", 100, 1000},
		{"ten megabit divides by r2q", 1_250_000, 125_000},
		{"unlimited clamps at the kernel's own ceiling", KernelBytesPerSec(UnlimitedBps), 200_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quantumFor(tc.bytes); got != tc.want {
				t.Fatalf("quantumFor(%d) = %d, want %d", tc.bytes, got, tc.want)
			}
		})
	}
}

/*
TestUnlimitedClearsRealHardware is the regression for a default class that
throttles a user with no policy at all.

Measured on 6.8.0-111: an unshaped user on a device whose default class sat at
10 Gbit achieved 9434 Mbit/s where the bare device carried 12256 — the explicit
default was binding, a 23% penalty on somebody with no limit. The bound is stated
against shipping hardware rather than against the box it was measured on, because
the value that bound was itself chosen against one machine's ceiling.
*/
func TestUnlimitedClearsRealHardware(t *testing.T) {
	const fastestShippingNIC = 400_000_000_000
	if UnlimitedBps <= fastestShippingNIC {
		t.Fatalf("UnlimitedBps = %d does not clear %d bit/s hardware, so the default class binds and throttles an unshaped user",
			UnlimitedBps, fastestShippingNIC)
	}
}
