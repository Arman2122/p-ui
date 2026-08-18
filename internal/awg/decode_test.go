package awg

import (
	"testing"
	"time"

	"github.com/mdlayher/netlink"
)

/*
Every obfuscation parameter must survive the round trip.

A reconcile compares what it wants against what the device reports, so a
parameter the DECODER drops reads as "the device does not have it" on every
pass. The panel then rewrites it forever -- and rewriting obfuscation on a live
device rekeys every client on it. A silent decode gap is a reconcile loop.
*/
func TestParametersSurviveTheRoundTrip(t *testing.T) {
	want := Params{
		Jc: 4, Jmin: 40, Jmax: 70,
		S1: 20, S2: 30, S3: 40, S4: 50,
		H1: HeaderRange(10, 19), H2: HeaderRange(20, 29),
		H3: HeaderRange(30, 39), H4: HeaderRange(40, 49),
		I1: "b0xdeadbeef", I2: "c2", I3: "t3", I4: "r4", I5: "b5",
		HeaderProtectionKey: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", ContentPaddingAddition: TimerRange(0, 32),
		RekeyAfterTime: TimerRange(100, 140), RekeyTimeout: TimerRange(4, 6),
		RejectAfterTime: TimerRange(170, 190), KeepaliveTimeout: TimerRange(20, 30),
		MaxHandshakeAttempts: TimerRange(3, 7),
		RandomTrailers:       true, DisableCookies: true,
	}

	attrs, err := encodeConfig("awg0", Config{Params: want})
	if err != nil {
		t.Fatalf("encodeConfig: %v", err)
	}
	data, err := netlink.MarshalAttributes(attrs)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	device, err := decodeDevice(data)
	if err != nil {
		t.Fatalf("decodeDevice: %v", err)
	}
	if device.Params != want {
		t.Fatalf("params did not survive:\n got %+v\nwant %+v", device.Params, want)
	}
}

/*
A peer that has never completed a handshake must decode as the ZERO time.

The kernel sends an all-zero timespec for one, and reading that as time.Unix(0,0)
turns "never connected" into "last seen in 1970" -- which the panel shows to an
operator, and which any "stale peer" rule would then act on.
*/
func TestNeverHandshakenDecodesAsZeroTime(t *testing.T) {
	if got := decodeTimespec(make([]byte, 16)); !got.IsZero() {
		t.Fatalf("an all-zero timespec decoded as %v, want the zero Time", got)
	}
	// A real handshake still decodes.
	raw := make([]byte, 16)
	raw[0] = 1
	if got := decodeTimespec(raw); got.IsZero() {
		t.Fatal("a non-zero timespec must decode to a real time")
	}
	if got := decodeTimespec(nil); !got.IsZero() {
		t.Fatalf("a short timespec decoded as %v, want the zero Time", got)
	}
}

// A peer that has never been contacted carries no endpoint, which is normal
// rather than a fault, so a short or unknown sockaddr is nil and not an error.
func TestAbsentEndpointIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"nothing at all", nil},
		{"a truncated sockaddr", make([]byte, 4)},
		{"an unknown family", []byte{0xff, 0xff, 0, 0, 0, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeSockaddr(tc.data); got != nil {
				t.Fatalf("decoded %v, want nil", got)
			}
		})
	}
}

// Keepalive is seconds on the wire and a Duration in the shape the reconcile
// compares, so the units have to change on the way through.
func TestKeepaliveDecodesAsSeconds(t *testing.T) {
	encoder := netlink.NewAttributeEncoder()
	encoder.Uint32(peerPersistentKeepaliveInterval, 25)
	raw, err := encoder.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	peer, err := decodePeer(raw)
	if err != nil {
		t.Fatalf("decodePeer: %v", err)
	}
	if peer.PersistentKeepaliveInterval != 25*time.Second {
		t.Fatalf("keepalive = %v, want 25s", peer.PersistentKeepaliveInterval)
	}
}
