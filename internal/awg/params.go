/*
Package awg carries AmneziaWG's obfuscation parameters and, on Linux, drives the
kernel module that implements them.

The parameters are the whole point of the protocol: WireGuard's handshake has a
fixed shape that a censor can fingerprint, and AmneziaWG hides it behind junk
packets, custom message headers and padding. Every one of them has to be
IDENTICAL on both ends -- a peer that pads differently is not a peer that works
slightly worse, it is a peer whose packets the other side cannot recognise at
all. So these belong to a client's configuration, never to a server-side knob an
operator can tune in isolation.
*/
package awg

import (
	"errors"
	"fmt"
)

// The refusals an operator has to be able to tell apart, each mirroring a check
// the kernel module itself makes.
var (
	ErrJunkRange       = errors.New("awg: jmin must not exceed jmax")
	ErrHeaderOverlap   = errors.New("awg: the message headers must not overlap")
	ErrPaddingTooSmall = errors.New("awg: header protection needs room for its nonce")
)

// HeaderProtectionNonceSize is src/header_protection.h's own constant. Padding
// smaller than this leaves no room for the nonce, and the kernel refuses the
// device rather than silently disabling protection.
const HeaderProtectionNonceSize = 12

/*
Params is one device's obfuscation configuration, named as a .conf file names
each field so an operator pasting from an Amnezia client recognises every one.

Zero means "leave it alone" throughout, which is what the kernel does with an
absent attribute -- so a Params{} is a plain WireGuard device, and adding a field
later cannot change the meaning of a stored one.
*/
type Params struct {
	// Junk packets sent before the real handshake: how many, and the size range
	// each is drawn from.
	Jc   uint16 `json:"jc,omitempty"`
	Jmin uint16 `json:"jmin,omitempty"`
	Jmax uint16 `json:"jmax,omitempty"`

	// Padding prepended to each of the four message types, so their lengths stop
	// being the constants a classifier matches on.
	S1 uint16 `json:"s1,omitempty"`
	S2 uint16 `json:"s2,omitempty"`
	S3 uint16 `json:"s3,omitempty"`
	S4 uint16 `json:"s4,omitempty"`

	// The message type headers themselves. WireGuard's are 1, 2, 3 and 4, which
	// is the single most recognisable thing about it on the wire.
	H1 uint64 `json:"h1,omitempty"`
	H2 uint64 `json:"h2,omitempty"`
	H3 uint64 `json:"h3,omitempty"`
	H4 uint64 `json:"h4,omitempty"`

	// Signature-carrying junk packets, described by upstream's own tag language
	// (b/c/t/r). Passed through rather than parsed here: the kernel validates the
	// format, and a second parser in the panel would be a second dialect.
	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`
	I3 string `json:"i3,omitempty"`
	I4 string `json:"i4,omitempty"`
	I5 string `json:"i5,omitempty"`

	// HeaderProtectionKey encrypts the header itself. Its presence is what makes
	// the S1..S4 minimum below apply.
	HeaderProtectionKey string `json:"headerProtectionKey,omitempty"`

	ContentPaddingAddition uint16 `json:"contentPaddingAddition,omitempty"`
	RandomTrailers         bool   `json:"randomTrailers,omitempty"`
	DisableCookies         bool   `json:"disableCookies,omitempty"`

	// Timer overrides, in seconds. WireGuard's defaults are themselves a
	// fingerprint, so a deployment that changes them must change them on both
	// ends like everything else here.
	RekeyAfterTime       uint16 `json:"rekeyAfterTime,omitempty"`
	RekeyTimeout         uint16 `json:"rekeyTimeout,omitempty"`
	RejectAfterTime      uint16 `json:"rejectAfterTime,omitempty"`
	KeepaliveTimeout     uint16 `json:"keepaliveTimeout,omitempty"`
	MaxHandshakeAttempts uint16 `json:"maxHandshakeAttempts,omitempty"`
}

// IsZero reports a device with no obfuscation at all, which is plain WireGuard
// carried by the AmneziaWG module.
func (p Params) IsZero() bool { return p == Params{} }

/*
Validate applies the module's own rules before the device is ever created.

Every check here mirrors one in src/netlink.c. Doing it in the panel is not
duplication for its own sake: the kernel answers a failed check with EINVAL and
a ratelimited dmesg line the operator will never see, so without this an invalid
combination reaches the UI as "device could not be configured".
*/
func (p Params) Validate() error {
	if p.Jmin > p.Jmax {
		return fmt.Errorf("%w: jmin %d, jmax %d", ErrJunkRange, p.Jmin, p.Jmax)
	}

	// Each header occupies a u32 range, and two that overlap make one message
	// type indistinguishable from another -- to the far end, not just to a censor.
	headers := []struct {
		name  string
		value uint64
	}{{"h1", p.H1}, {"h2", p.H2}, {"h3", p.H3}, {"h4", p.H4}}
	for i, left := range headers {
		if left.value == 0 {
			continue
		}
		for _, right := range headers[i+1:] {
			if right.value == 0 {
				continue
			}
			if headersOverlap(left.value, right.value) {
				return fmt.Errorf("%w: %s and %s both cover %d", ErrHeaderOverlap, left.name, right.name, left.value)
			}
		}
	}

	if p.HeaderProtectionKey != "" {
		for _, padding := range []struct {
			name  string
			value uint16
		}{{"s1", p.S1}, {"s2", p.S2}, {"s3", p.S3}, {"s4", p.S4}} {
			if padding.value > 0 && padding.value < HeaderProtectionNonceSize {
				return fmt.Errorf("%w: %s is %d, and the nonce needs %d",
					ErrPaddingTooSmall, padding.name, padding.value, HeaderProtectionNonceSize)
			}
		}
	}
	return nil
}

// headersOverlap mirrors u32_range_overlap in src/type.h. A header is a RANGE
// encoded as (hi<<32 | lo), not a bare value, so two of them collide when their
// bounds intersect -- and a range built with HeaderRange is what the kernel
// compares. Getting the halves backwards refuses configurations that are fine.
func headersOverlap(left, right uint64) bool {
	leftLo, leftHi := uint32(left), uint32(left>>32)
	rightLo, rightHi := uint32(right), uint32(right>>32)
	return leftLo <= rightHi && rightLo <= leftHi
}

// HeaderRange encodes one message-type header the way src/type.h's
// u32_range_init does. A single value is the range [value, value]: written as a
// bare number instead, its high half reads as an upper bound of zero and the
// kernel's overlap check silently never fires.
func HeaderRange(lo, hi uint32) uint64 { return uint64(hi)<<32 | uint64(lo) }
