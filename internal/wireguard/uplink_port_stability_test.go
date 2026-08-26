package wireguard

import (
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
A dialling uplink's source port must survive a reconcile.

An inbound configures its own listen port. An uplink does not — it dials out,
and which source port it leaves from is the kernel's business. So Provision
builds an Instance with Port left at zero.

diffDevice read that zero as a value to write rather than an absence, and to the
kernel a listen port of 0 does not mean "leave it alone": it means "assign a
fresh one". The egress reconcile runs every ten seconds, so the device got a new
source port six times a minute.

That makes a WireGuard handshake impossible to complete. The initiation goes out
from port A; the far end replies to port A; by the time the reply lands the
device is on port B and the packet is dropped. Measured on a live pair: the
initiator sent 9.39 KiB and received 0 B, while the responder had a peer entry
whose last handshake was 36 minutes old — from the window between the device
being created and the first reconcile tick.

Every kernel WireGuard and AmneziaWG uplink was affected, which is every exit
the panel dials.
*/

func TestAnUplinkKeepsThePortTheKernelGaveIt(t *testing.T) {
	var key wgtypes.Key
	current := wgtypes.Device{PrivateKey: key, ListenPort: 50455, FirewallMark: 0xe000001}

	// Port 0 is an uplink: nothing configured one.
	delta := diffDevice(current, key, 0, 0xe000001, false)

	if delta.ListenPort != nil {
		t.Fatalf("the reconcile rewrote the listen port to %d; the kernel reads 0 as \"assign a fresh one\", so the device changes source port every pass and no handshake can complete",
			*delta.ListenPort)
	}
	if !delta.empty() {
		t.Fatalf("an uplink whose device already matches produced work to do: %#v", delta)
	}
}

// An inbound always carries a port, and a device on the wrong one must still be
// corrected — that is the case this guard must not break.
func TestAnInboundStillHasItsPortCorrected(t *testing.T) {
	var key wgtypes.Key
	current := wgtypes.Device{PrivateKey: key, ListenPort: 111, FirewallMark: 0}

	delta := diffDevice(current, key, 51888, 0, false)

	if delta.ListenPort == nil {
		t.Fatal("an inbound on the wrong port was left there; its clients dial the port the panel published")
	}
	if *delta.ListenPort != 51888 {
		t.Fatalf("listen port = %d, want the inbound's own 51888", *delta.ListenPort)
	}
}

// And a device already on its configured port needs no write at all.
func TestAMatchingInboundPortIsLeftAlone(t *testing.T) {
	var key wgtypes.Key
	current := wgtypes.Device{PrivateKey: key, ListenPort: 51888, FirewallMark: 0}

	if delta := diffDevice(current, key, 51888, 0, false); delta.ListenPort != nil {
		t.Fatalf("rewrote a port that already matched: %d", *delta.ListenPort)
	}
}

/*
On a device the panel has just created, every setting is written rather than
diffed against an empty device — but a zero port still must not be written,
because the kernel has already chosen one and writing zero would replace it.
*/
func TestAFreshUplinkDeviceIsNotGivenPortZero(t *testing.T) {
	var key wgtypes.Key
	current := wgtypes.Device{ListenPort: 43503}

	delta := diffDevice(current, key, 0, 0xe000001, true)

	if delta.ListenPort != nil {
		t.Fatalf("a freshly created uplink had its port rewritten to %d", *delta.ListenPort)
	}
	// The rest of a full write still happens.
	if delta.PrivateKey == nil || delta.FirewallMark == nil {
		t.Fatal("the full write dropped the key or the mark along with the port")
	}
}
