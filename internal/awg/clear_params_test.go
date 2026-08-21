package awg

import (
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
		{"jc", devJc, 4}, {"jmin", devJmin, 40}, {"jmax", devJmax, 70},
		{"s1", devS1, 0}, {"s2", devS2, 0}, {"s3", devS3, 0}, {"s4", devS4, 0},
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

	// Not the panel's to clear: the module refuses four overlapping zeros, so
	// their cleared state is WireGuard's 1/2/3/4 and that is F098's job.
	for _, kind := range []struct {
		name string
		kind uint16
	}{{"h1", devH1}, {"h2", devH2}, {"h3", devH3}, {"h4", devH4}} {
		if hasAttr(attrs, kind.kind) {
			t.Errorf("%s was written as zero; the module refuses four of those at once", kind.name)
		}
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
