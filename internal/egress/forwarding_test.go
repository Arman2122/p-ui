package egress_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

/*
A fresh install from GitHub ships net.ipv4.ip_forward=0, so the first WireGuard
inbound anyone creates completes handshakes and forwards nothing. The panel used
to only report that; reporting it means every install has a manual step its
operator discovers from user complaints.
*/
func TestEnsureForwardingTurnsBothFamiliesOn(t *testing.T) {
	plane := egtest.New()
	plane.SetSysctlValue(egress.IPForwardKey, "0")
	plane.SetSysctlValue(egress.IPForward6Key, "0")
	manager := egress.New(plane, egress.NewRegistry())

	if err := manager.EnsureForwarding(context.Background()); err != nil {
		t.Fatalf("EnsureForwarding: %v", err)
	}

	for _, key := range []string{egress.IPForwardKey, egress.IPForward6Key} {
		got, err := plane.Sysctl(context.Background(), key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if got != "1" {
			t.Errorf("%s = %q, want \"1\": an L3 inbound is inert without it", key, got)
		}
	}

	/*
		The drop-in goes through the plane like every other host write.

		It used to be a package function calling os.WriteFile, so a test holding a
		fake plane still wrote to the real /etc/sysctl.d — which passes as root and
		fails everywhere else, and CI is everywhere else.
	*/
	body, written := plane.DropIn(egress.ForwardingDropIn)
	if !written {
		t.Fatalf("nothing was persisted to %s, so the setting dies at the next reboot", egress.ForwardingDropIn)
	}
	for _, key := range []string{egress.IPForwardKey, egress.IPForward6Key} {
		if !strings.Contains(body, key) {
			t.Errorf("the drop-in does not carry %s:\n%s", key, body)
		}
	}
}

// Free on every pass after the first: the reconcile job calls this every ten
// seconds, and a knob already on must not be rewritten.
func TestEnsureForwardingWritesNothingWhenAlreadyOn(t *testing.T) {
	plane := egtest.New()
	plane.SetSysctlValue(egress.IPForwardKey, "1")
	plane.SetSysctlValue(egress.IPForward6Key, "1")
	manager := egress.New(plane, egress.NewRegistry())

	before := len(plane.Ops())
	if err := manager.EnsureForwarding(context.Background()); err != nil {
		t.Fatalf("EnsureForwarding: %v", err)
	}
	if got := len(plane.Ops()); got != before {
		t.Fatalf("performed %d ops, want %d: an unchanged knob must not be rewritten", got, before)
	}
}

/*
A kernel without a family reports no key at all. That is a host fact, not a
failure to fix, and it must not stop the other family being turned on.
*/
func TestEnsureForwardingSurvivesAMissingFamily(t *testing.T) {
	plane := egtest.New()
	plane.SetSysctlValue(egress.IPForwardKey, "0")
	manager := egress.New(plane, egress.NewRegistry())

	if err := manager.EnsureForwarding(context.Background()); err != nil {
		t.Fatalf("a kernel with no v6 is not an error, got: %v", err)
	}
	got, err := plane.Sysctl(context.Background(), egress.IPForwardKey)
	if err != nil || got != "1" {
		t.Fatalf("v4 = %q (err %v), want \"1\": one absent family must not block the other", got, err)
	}
}
