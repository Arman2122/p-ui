package shaping

/*
UnlimitedBps is what the explicit default class is created at. It sits above the
measured ~10.3-10.4 Gbit/s single-qdisc ceiling, so it can never bind.

Leaving `htb default` pointing at a guessed rate was measured to throttle an
UNSHAPED peer from 4608 to 960 Mbit/s — a 4.8x penalty on a user with no policy
at all, which is the opposite of what shaping's fail-open rule requires.
*/
const UnlimitedBps int64 = 10_000_000_000

/*
KernelBytesPerSec canonicalises a rate into the unit HTB actually stores.

The kernel holds bytes per second, so a requested 12345678 bit/s comes back as
12345672 bit/s and a diff comparing requested bits against the readback differs
forever, issuing a ClassChange every pass. Canonicalise once at the boundary,
then store, push and diff in the kernel's own unit.
*/
func KernelBytesPerSec(bitsPerSec int64) uint64 {
	if bitsPerSec <= 0 {
		return 0
	}
	return uint64(bitsPerSec) / 8
}

// r2q is HTB's rate-to-quantum divisor, at the kernel's own default. Reproducing
// its arithmetic here is what keeps the class off the "quantum is big" warning.
const r2q = 10

// quantumFor is the value the kernel would have computed and then clamped. Passing
// it explicitly silences the clamp warning a 10 Gbit default class otherwise logs.
func quantumFor(bytesPerSec uint64) uint32 {
	quantum := bytesPerSec / r2q
	switch {
	case quantum < 1000:
		return 1000
	case quantum > 200000:
		return 200000
	}
	return uint32(quantum)
}
