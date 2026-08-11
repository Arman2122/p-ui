package shaping

import "testing"

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
			if got := Owns(tc.device); got != tc.owned {
				t.Fatalf("Owns(%q) = %v, want %v", tc.device, got, tc.owned)
			}
		})
	}
}

// TestIFBDeviceRoundTrips is the other half: a name the panel did not derive is
// never deleted by the mirror GC, however much it looks like one.
func TestIFBDeviceRoundTrips(t *testing.T) {
	for _, id := range []int{1, 7, 999, 100000} {
		name := IFBDevice(id)
		back, mine := ownedIFBID(name)
		if !mine || back != id {
			t.Fatalf("ownedIFBID(%q) = %d, %v; want %d, true", name, back, mine, id)
		}
	}
	for _, name := range []string{"pifb007", "pifb0", "pifb", "pifbx", "ifb0", "pwg7"} {
		if _, mine := ownedIFBID(name); mine {
			t.Fatalf("ownedIFBID(%q) claimed a device this panel never derived", name)
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
