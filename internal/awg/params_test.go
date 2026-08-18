package awg

import (
	"errors"
	"testing"
)

/*
Every rule here is one the kernel also enforces, and that is the point.

The module answers an invalid combination with EINVAL and a ratelimited dmesg
line nobody reads, so without these checks the operator's only symptom is "the
device could not be configured" -- for a mistake as specific as one padding
value being four bytes short.
*/
func TestValidateMirrorsTheModulesOwnRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params Params
		want   error
	}{
		{"no obfuscation at all is plain WireGuard", Params{}, nil},
		{"a normal junk range", Params{Jc: 4, Jmin: 40, Jmax: 70}, nil},
		{"an inverted junk range", Params{Jc: 4, Jmin: 70, Jmax: 40}, ErrJunkRange},
		// Equal is not inverted: one junk size is a legitimate configuration.
		{"a junk range of exactly one size", Params{Jc: 4, Jmin: 50, Jmax: 50}, nil},
		{"distinct header ranges", Params{H1: HeaderRange(10, 19), H2: HeaderRange(20, 29), H3: HeaderRange(30, 39), H4: HeaderRange(40, 49)}, nil},
		{"two header ranges on the same value", Params{H1: HeaderRange(10, 10), H2: HeaderRange(10, 10)}, ErrHeaderOverlap},
		{"header ranges that touch at one end", Params{H1: HeaderRange(10, 20), H2: HeaderRange(20, 30)}, ErrHeaderOverlap},
		// Zero means "leave it alone", so unset headers cannot collide with
		// each other -- otherwise every partial configuration would be refused.
		{"unset headers do not collide", Params{H1: HeaderRange(10, 19)}, nil},
		{"padding without header protection is free", Params{S1: 4}, nil},
		{"padding too small for the nonce", Params{HeaderProtectionKey: "k", S1: 4}, ErrPaddingTooSmall},
		{"padding exactly the nonce size", Params{HeaderProtectionKey: "k", S1: HeaderProtectionNonceSize}, nil},
		// Zero padding is "unset", not "too small": a protected device that pads
		// only the handshake messages is normal.
		{"unset padding beside header protection", Params{HeaderProtectionKey: "k", S1: 20}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// A zero Params must round-trip as "plain WireGuard", because that is what the
// kernel does with absent attributes and what a stored empty config means.
func TestZeroParamsIsPlainWireGuard(t *testing.T) {
	if !(Params{}).IsZero() {
		t.Fatal("an empty Params must report itself as carrying no obfuscation")
	}
	if (Params{Jc: 1}).IsZero() {
		t.Fatal("a Params with junk packets is not plain WireGuard")
	}
}

/*
A header written as a bare number is the trap this encoding exists to avoid.

src/type.h stores a header as (hi<<32 | lo), so the bare value N reads as the
range [N, 0] -- an upper bound BELOW its lower bound, which no other range can
intersect. The kernel's overlap check then never fires, and two message types
can be configured onto the same value: the far end cannot tell a handshake
initiation from a transport packet, and nothing anywhere reports why.
*/
func TestABareHeaderValueDefeatsTheOverlapCheck(t *testing.T) {
	bare := Params{H1: 10, H2: 10}
	if err := bare.Validate(); err != nil {
		t.Fatalf("bare values encode empty ranges, so the kernel sees no overlap: %v", err)
	}
	ranged := Params{H1: HeaderRange(10, 10), H2: HeaderRange(10, 10)}
	if err := ranged.Validate(); err == nil {
		t.Fatal("the same two headers, encoded as ranges, must be refused")
	}
}
