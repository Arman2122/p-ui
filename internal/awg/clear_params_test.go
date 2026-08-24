package awg

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mdlayher/netlink"
)

func attrValue(t *testing.T, attrs []netlink.Attribute, kind uint16) (uint16, bool) {
	t.Helper()
	for _, attr := range attrs {
		if attr.Type == kind {
			if len(attr.Data) < 2 {
				t.Fatalf("attribute %d carries %d bytes, want 2", kind, len(attr.Data))
			}
			return uint16(attr.Data[0]) | uint16(attr.Data[1])<<8, true
		}
	}
	return 0, false
}

// attrValue64 reads a header, which is a packed (hi<<32|lo) range rather than a
// scalar, so the 2-byte reader above would answer the wrong number silently.
func attrValue64(t *testing.T, attrs []netlink.Attribute, kind uint16) (uint64, bool) {
	t.Helper()
	for _, attr := range attrs {
		if attr.Type == kind {
			if len(attr.Data) != 8 {
				t.Fatalf("attribute %d carries %d bytes, want 8", kind, len(attr.Data))
			}
			return binary.LittleEndian.Uint64(attr.Data), true
		}
	}
	return 0, false
}

func hasAttr(attrs []netlink.Attribute, kind uint16) bool {
	for _, attr := range attrs {
		if attr.Type == kind {
			return true
		}
	}
	return false
}

/*
A cleared parameter has to be expressible.

Zero-means-unset made "back to none" the same message as "leave it alone", so the
kernel kept the old padding while every .conf the panel handed out had dropped
it — and a tunnel where one side pads and the other does not never completes a
handshake, on either end, silently.
*/
func TestPanelOwnedParametersAreWrittenEvenWhenZero(t *testing.T) {
	attrs := encodeParams(Params{Jc: 4, Jmin: 40, Jmax: 70}, true)

	for _, kind := range []struct {
		name  string
		kind  uint16
		value uint16
	}{
		{"jc", devJc, 4},
		{"jmin", devJmin, 40},
		{"jmax", devJmax, 70},
		{"s1", devS1, 0},
		{"s2", devS2, 0},
		{"s3", devS3, 0},
		{"s4", devS4, 0},
	} {
		got, present := attrValue(t, attrs, kind.kind)
		if !present {
			t.Errorf("%s is not in the message, so the device keeps whatever it had", kind.name)
			continue
		}
		if got != kind.value {
			t.Errorf("%s = %d, want %d", kind.name, got, kind.value)
		}
	}

	/*
		The headers are the panel's to clear too, since F098 — but never as zero.
		The module refuses four overlapping zeros, so a cleared header goes out as
		WireGuard's own message type, which is what a client with no h1..h4 in its
		.conf falls back to. Measured: setting 1/2/3/4 is accepted and awg-tools
		then reports no headers at all (RUNTIME-RESULTS §12).
	*/
	for _, header := range []struct {
		name  string
		kind  uint16
		value uint64
	}{
		{"h1", devH1, HeaderRange(1, 1)},
		{"h2", devH2, HeaderRange(2, 2)},
		{"h3", devH3, HeaderRange(3, 3)},
		{"h4", devH4, HeaderRange(4, 4)},
	} {
		got, present := attrValue64(t, attrs, header.kind)
		if !present {
			t.Errorf("%s is not in the message, so a device that had a custom header keeps it while every .conf has dropped it", header.name)
			continue
		}
		if got != header.value {
			t.Errorf("%s = %d, want WireGuard's own %d", header.name, got, header.value)
		}
	}
}

// A custom header still goes out as itself; only an unset one is substituted.
func TestACustomHeaderIsNotOverwrittenByItsClearedValue(t *testing.T) {
	attrs := encodeParams(Params{H1: HeaderRange(10, 19)}, true)

	got, present := attrValue64(t, attrs, devH1)
	if !present {
		t.Fatal("h1 is not in the message")
	}
	if want := HeaderRange(10, 19); got != want {
		t.Fatalf("h1 = %d, want the operator's own %d", got, want)
	}
	// And its three cleared siblings do not collide with it.
	if err := (Params{H1: HeaderRange(10, 19)}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

/*
The substitution creates an overlap the bare fields never showed: a custom range
covering 1, 2, 3 or 4 collides with the cleared siblings the encoder is about to
send, and the module refuses the merged state.
*/
func TestACustomHeaderThatCoversAClearedOneIsRefused(t *testing.T) {
	err := Params{H1: HeaderRange(1, 5)}.Validate()
	if err == nil {
		t.Fatal("h1 = 1-5 was accepted beside three cleared headers; the module refuses that merged state")
	}
	if !errors.Is(err, ErrHeaderOverlap) {
		t.Fatalf("Validate() = %v, want %v", err, ErrHeaderOverlap)
	}
}

/*
And the other half of the same rule: a call that is not the panel pushing
obfuscation must not touch it. The wireguard plane configures peers and keys on
an AmneziaWG device through this same encoder, carrying no parameters — writing
zeros there would wipe the obfuscation on every peer add.
*/
func TestACallThatCarriesNoParametersChangesNone(t *testing.T) {
	attrs := encodeParams(Params{}, false)
	if len(attrs) != 0 {
		t.Fatalf("a parameterless call emitted %d attribute(s): %+v", len(attrs), attrs)
	}

	// The same message shape the peer path builds.
	cfg, err := encodeConfig("pawg9", Config{Params: Params{}})
	if err != nil {
		t.Fatalf("encodeConfig: %v", err)
	}
	for _, kind := range []struct {
		name string
		kind uint16
	}{{"jc", devJc}, {"s1", devS1}, {"s2", devS2}} {
		if hasAttr(cfg, kind.kind) {
			t.Errorf("a peer-only configuration carries %s, which would clear it on the device", kind.name)
		}
	}
}
