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

/*
Turning this host into a router must not cost it its own IPv6.

accept_ra defaults to 1, which the kernel reads as "accept unless forwarding is
enabled". EnsureForwarding enables forwarding, so on a host whose IPv6 default
route comes from a Router Advertisement -- AWS, Vultr, Linode; not Hetzner,
which is why neither box we can look at shows it -- the panel silently costs the
host its own connectivity, at the next reboot.
*/
func TestEnsureForwardingKeepsRouterAdvertisementsOnTheDeviceThatUsesThem(t *testing.T) {
	plane := egtest.New()
	plane.SetSysctlValue(egress.IPForwardKey, "0")
	plane.SetSysctlValue(egress.IPForward6Key, "0")
	plane.SetSysctlValue("net.ipv6.conf.eth0.accept_ra", "1")
	plane.RADevices = []string{"eth0"}

	if err := egress.New(plane, nil).EnsureForwarding(context.Background()); err != nil {
		t.Fatalf("EnsureForwarding: %v", err)
	}

	if value, _ := plane.Sysctl(context.Background(), "net.ipv6.conf.eth0.accept_ra"); value != "2" {
		t.Fatalf("eth0 accept_ra = %q, want 2 so the RA still lands once forwarding is on", value)
	}

	body, ok := plane.DropIn(egress.AcceptRADropIn)
	if !ok || !strings.Contains(body, "net.ipv6.conf.eth0.accept_ra=2") {
		t.Fatalf("drop-in %s = %q, want it to carry eth0 so a reboot keeps the route", egress.AcceptRADropIn, body)
	}

	// Ordered, not merely both done: between forwarding going on and accept_ra
	// going to 2 the interface is ignoring RAs, and a reboot applies the files
	// in name order, which is why this one is 98 and forwarding is 99.
	ops := strings.Join(plane.Ops(), "\n")
	raAt := strings.Index(ops, "sysctl net.ipv6.conf.eth0.accept_ra=2")
	fwdAt := strings.Index(ops, "sysctl "+egress.IPForward6Key+"=1")
	if raAt < 0 || fwdAt < 0 || raAt > fwdAt {
		t.Fatalf("ops = %v, want accept_ra raised before forwarding was enabled", plane.Ops())
	}
}

/*
And it must never be raised on a device a client can send packets to.

accept_ra=2 on a tunnel would let any VPN client advertise itself as this host's
default gateway. The guard is that the device has to be carrying a route the
KERNEL attributes to an RA, which a wg device never is -- rather than a list of
name prefixes, which would start admitting tunnels the day a new core invents one.
*/
func TestEnsureForwardingNeverRaisesAcceptRAOnATunnel(t *testing.T) {
	plane := egtest.New()
	plane.SetSysctlValue(egress.IPForwardKey, "0")
	plane.SetSysctlValue(egress.IPForward6Key, "0")
	plane.SetSysctlValue("net.ipv6.conf.pwg7.accept_ra", "1")
	plane.SetSysctlValue("net.ipv6.conf.eth0.accept_ra", "0")
	// The host is statically addressed: nothing here is configured by an RA.
	plane.RADevices = nil

	if err := egress.New(plane, nil).EnsureForwarding(context.Background()); err != nil {
		t.Fatalf("EnsureForwarding: %v", err)
	}

	if value, _ := plane.Sysctl(context.Background(), "net.ipv6.conf.pwg7.accept_ra"); value != "1" {
		t.Fatalf("pwg7 accept_ra = %q, want it untouched: a client must not be able to become this host's gateway", value)
	}
	// An operator's explicit 0 is a refusal, not a default to be improved on.
	if value, _ := plane.Sysctl(context.Background(), "net.ipv6.conf.eth0.accept_ra"); value != "0" {
		t.Fatalf("eth0 accept_ra = %q, want the operator's 0 left alone", value)
	}
	// And nothing written at all, so one momentary read of zero RA devices cannot
	// delete the line an earlier pass wrote to survive the next boot.
	if body, ok := plane.DropIn(egress.AcceptRADropIn); ok {
		t.Fatalf("drop-in %s was written as %q on a host with no RA device", egress.AcceptRADropIn, body)
	}
}
