package shaping

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRegisterRefusesANamespaceThatCannotRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		want   error
	}{
		{"empty", "", nil},
		/* A prefix ending in a digit makes one device name two names: pawg1+id 2
		   and pawg+id 12 are both "pawg12", with two managers owning one tree. */
		{"trailing digit", "pawg1", ErrBadNamespace},
		{"digits only", "12", ErrBadNamespace},
		{"upper case", "PAWG", ErrBadNamespace},
		{"punctuation", "p-awg", ErrBadNamespace},
		// IFNAMSIZ-1 is 15, so a 15-character prefix can never carry an id.
		{"no room for an id", strings.Repeat("a", maxDeviceName), ErrBadNamespace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := DefaultNamespaces().Register(tc.prefix)
			if err == nil {
				t.Fatalf("Register(%q) was accepted", tc.prefix)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Register(%q) = %v, want %v", tc.prefix, err, tc.want)
			}
		})
	}
}

func TestRegisterRefusesASecondClaimOnOneNamespace(t *testing.T) {
	ns := DefaultNamespaces()
	if err := ns.Register("pfake"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := ns.Register("pfake"); !errors.Is(err, ErrDuplicateNamespace) {
		t.Fatalf("second Register = %v, want %v", err, ErrDuplicateNamespace)
	}
	if err := ns.Register(wireguardPrefix); !errors.Is(err, ErrDuplicateNamespace) {
		t.Fatalf("re-registering a built-in = %v, want %v", err, ErrDuplicateNamespace)
	}
}

/*
A registered namespace has to be owned everywhere the hardcoded list used to be
consulted, or a core's devices are shapeable by one code path and foreign to
another — which is how a tree gets built and then never collected.
*/
func TestARegisteredNamespaceIsOwnedEverywhere(t *testing.T) {
	ns := DefaultNamespaces()
	if err := ns.Register("pfake"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !ns.Owns("pawg7") {
		t.Error("a registered namespace's device is not owned")
	}
	if id, mine := ns.DeviceID("pawg7"); !mine || id != 7 {
		t.Errorf("DeviceID(pawg7) = %d, %v; want 7, true", id, mine)
	}
	if !slices.Contains(ns.Shapeable(), "pfake") {
		t.Errorf("Shapeable() = %v, missing the registered namespace", ns.Shapeable())
	}

	// The round trip still holds: sharing the prefix is not owning the device.
	for _, foreign := range []string{"pawgtest", "pawg0", "pawg007", "pfake", "awg7"} {
		if ns.Owns(foreign) {
			t.Errorf("Owns(%q) = true; a near miss is somebody else's interface", foreign)
		}
	}

	// A namespace nobody registered stays foreign, which is the default that
	// keeps the panel off an operator's own interfaces.
	// Not pawg: that one IS a default now, being the AmneziaWG core's devices.
	if DefaultNamespaces().Owns("pzzz7") {
		t.Error("an unregistered namespace is owned by a fresh set")
	}
}

// The mirror band is owned by construction: this package creates those devices
// itself, so it is not registered and must never depend on registration.
func TestTheMirrorBandIsOwnedWithoutRegistration(t *testing.T) {
	ns := &Namespaces{}
	if !ns.Owns(IFBDevice(9)) {
		t.Fatalf("%s is not owned by an empty set", IFBDevice(9))
	}
	if slices.Contains(ns.Shapeable(), ifbPrefix) {
		t.Error("the mirror band must not be shapeable: nothing builds a tree on a mirror's mirror")
	}
}

// A family is refused on exactly the grounds a plain namespace is: the split at
// one place is what lets poc2-3 name its inbound and nothing else.
func TestRegisterFamilyRefusesWhatRegisterRefuses(t *testing.T) {
	for _, prefix := range []string{"", "pawg1", "12", "PAWG", "p-awg", strings.Repeat("a", maxDeviceName)} {
		if err := DefaultNamespaces().RegisterFamily(prefix); err == nil {
			t.Errorf("RegisterFamily(%q) was accepted", prefix)
		}
	}
}

// One prefix is one kind: claimed as both, the same device name would be read two
// ways, and the second reading would install a tree the first never collects.
func TestAPrefixIsOneKindOfNamespace(t *testing.T) {
	ns := DefaultNamespaces()
	if err := ns.RegisterFamily("poc"); err != nil {
		t.Fatalf("RegisterFamily: %v", err)
	}
	if err := ns.Register("poc"); !errors.Is(err, ErrDuplicateNamespace) {
		t.Fatalf("Register after RegisterFamily = %v, want %v", err, ErrDuplicateNamespace)
	}
	if err := ns.RegisterFamily("poc"); !errors.Is(err, ErrDuplicateNamespace) {
		t.Fatalf("second RegisterFamily = %v, want %v", err, ErrDuplicateNamespace)
	}
	if err := ns.RegisterFamily(wireguardPrefix); !errors.Is(err, ErrDuplicateNamespace) {
		t.Fatalf("RegisterFamily on a built-in = %v, want %v", err, ErrDuplicateNamespace)
	}
	if got := ns.Families(); !slices.Equal(got, []string{"poc"}) {
		t.Errorf("Families() = %v, want [poc]", got)
	}
	if slices.Contains(ns.Shapeable(), "poc") {
		t.Error("a family is listed among the per-inbound namespaces")
	}
}
