package shaping

import (
	"strconv"
	"testing"
)

// TestOwnsRoundTrips pins the predicate every write is gated on. A prefix test
// would adopt an operator's own pwgtest and install a tree on their interface.
func TestOwnsRoundTrips(t *testing.T) {
	cases := []struct {
		device string
		owned  bool
	}{
		{"pwg7", true},
		{"peg7", true},
		{"pifb7", true},
		{"pwg1", true},
		{"pwg100000", true},
		{"pwgtest", false},
		{"pifb007", false},
		{"pifb0", false},
		{"pwg0", false},
		{"pwg-1", false},
		{"pwg", false},
		{"eth0", false},
		{"", false},
		{"pifb+7", false},
		// IFNAMSIZ is 16, so a name this long cannot exist and must not be claimed.
		{"pwg1234567890123", false},
	}
	for _, tc := range cases {
		t.Run(tc.device, func(t *testing.T) {
			if got := DefaultNamespaces().Owns(tc.device); got != tc.owned {
				t.Fatalf("Owns(%q) = %v, want %v", tc.device, got, tc.owned)
			}
		})
	}
}

// TestMirrorNamesRoundTrip is the other half: a name the panel did not derive is
// never deleted by the mirror GC, however much it looks like one.
func TestMirrorNamesRoundTrip(t *testing.T) {
	for _, id := range []int{1, 7, 999, 100000} {
		name := IFBDevice(id)
		back, mine := ownedMirror(name)
		if !mine || back != strconv.Itoa(id) {
			t.Fatalf("ownedMirror(%q) = %q, %v; want %q, true", name, back, mine, strconv.Itoa(id))
		}
	}
	// A session's mirror carries the session's suffix, so its parent is named back
	// exactly and two sessions of one inbound never derive one mirror.
	for _, suffix := range []string{"2-0", "2-3", "100-12"} {
		back, mine := ownedMirror(mirrorDevice(suffix))
		if !mine || back != suffix {
			t.Fatalf("ownedMirror(%q) = %q, %v; want %q, true", mirrorDevice(suffix), back, mine, suffix)
		}
	}
	foreign := []string{
		"pifb007", "pifb0", "pifb", "pifbx", "ifb0", "pwg7",
		"pifb2-03", "pifb02-3", "pifb0-1", "pifb2-", "pifb-3", "pifb2-3-1", "pifb2--1",
	}
	for _, name := range foreign {
		if _, mine := ownedMirror(name); mine {
			t.Fatalf("ownedMirror(%q) claimed a device this panel never derived", name)
		}
	}
}

// TestAFamilyDeviceRoundTrips: a per-session device is owned only when both of
// its numbers rebuild the name, so a near miss stays somebody else's interface.
func TestAFamilyDeviceRoundTrips(t *testing.T) {
	ns := DefaultNamespaces()
	if err := ns.RegisterFamily("poc"); err != nil {
		t.Fatalf("RegisterFamily: %v", err)
	}
	for _, device := range []string{"poc2-0", "poc2-3", "poc100-12"} {
		if !ns.Owns(device) {
			t.Errorf("Owns(%q) = false for a session device of a registered family", device)
		}
	}
	if id, mine := ns.DeviceID("poc2-3"); !mine || id != 2 {
		t.Errorf("DeviceID(poc2-3) = %d, %v; want 2, true", id, mine)
	}
	nearMisses := []string{
		"poc2", "poc2-", "poc-3", "poc02-3", "poc2-03", "poc2--1",
		"poc2-3-1", "poctest", "poc2-3x", "poc0-1", "poc2-+1",
	}
	for _, device := range nearMisses {
		if ns.Owns(device) {
			t.Errorf("Owns(%q) = true; a near miss is somebody else's interface", device)
		}
	}
	// The plain namespaces do not adopt a session name, nor the family a plain one.
	if ns.Owns("pwg2-3") || DefaultNamespaces().Owns("poc2-3") {
		t.Error("a session name was owned by a per-inbound namespace, or by an unregistered family")
	}
}

func TestMirrorOfNamesTheDeviceSuffix(t *testing.T) {
	ns := DefaultNamespaces()
	if err := ns.RegisterFamily("poc"); err != nil {
		t.Fatalf("RegisterFamily: %v", err)
	}
	for device, want := range map[string]string{"pwg7": "pifb7", "peg12": "pifb12", "poc2-3": "pifb2-3"} {
		if got, ok := ns.MirrorOf(device); !ok || got != want {
			t.Errorf("MirrorOf(%q) = %q, %v; want %q, true", device, got, ok, want)
		}
	}
	// A mirror derives no mirror, and neither does a foreign device.
	for _, device := range []string{"pifb7", "pifb2-3", "eth0", "pwgtest"} {
		if got, ok := ns.MirrorOf(device); ok {
			t.Errorf("MirrorOf(%q) = %q; nothing builds a tree on it", device, got)
		}
	}
}

func TestClassHandleRoundTrips(t *testing.T) {
	for _, minor := range []uint16{firstMinor, 0x11, defaultMinor} {
		back, ok := classMinor(classHandle(minor))
		if !ok || back != minor {
			t.Fatalf("classMinor(classHandle(%#x)) = %#x, %v", minor, back, ok)
		}
	}
	// Major 2 is nothing this package writes, and minor 0 is the qdisc itself.
	for _, handle := range []uint32{0x00020010, rootHandle, 0} {
		if _, ok := classMinor(handle); ok {
			t.Fatalf("classMinor(%#x) claimed a handle this package never writes", handle)
		}
	}
}
