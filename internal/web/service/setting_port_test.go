package service

import (
	"strings"
	"testing"
)

/*
A port the panel cannot bind must be refused where it is stored.

`p-ui setting -port 99999` parsed fine, was stored, and the panel then died on
net.Listen every time it started — and because the installer sets the port and
the admin credentials in a single invocation, a mistyped port took the
credentials with it: main.go builds the setting command with flag.ExitOnError,
so an unparseable value exits before updateSetting runs at all, and install.sh
has no set -e to notice. The box is left reachable on admin/admin while the
installer prints the random credentials it never applied.

Guarded in Go rather than only in the shell, because this also protects the
panel's own settings page, and because a shell check cannot stop the CLI.
*/
func TestTheWebPortMustBeBindable(t *testing.T) {
	initTestDB(t)
	svc := &SettingService{}

	for _, port := range []int{0, -1, 65536, 99999} {
		if err := svc.SetPort(port); err == nil {
			t.Fatalf("port %d was accepted; the panel cannot bind it and will crash-loop on every start", port)
		} else if !strings.Contains(err.Error(), "65535") {
			t.Fatalf("the refusal for %d does not say what the range is: %v", port, err)
		}
	}

	for _, port := range []int{1, 2053, 65535} {
		if err := svc.SetPort(port); err != nil {
			t.Fatalf("port %d is bindable and was refused: %v", port, err)
		}
	}

	// And the stored value is the last accepted one, not a refused one.
	got, err := svc.GetPort()
	if err != nil {
		t.Fatalf("GetPort: %v", err)
	}
	if got != 65535 {
		t.Fatalf("stored port = %d, want 65535", got)
	}
}
