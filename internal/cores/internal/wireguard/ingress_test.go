package wireguard

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
This is the resolver that used to live in the service layer as a protocol
comparison, and the dispatch ratchet dropped by one when it moved here. The
device name is derived from the inbound id, so it is stable across restarts.
*/
func TestIngressHandleNamesTheDevice(t *testing.T) {
	got, err := (&Core{}).IngressHandle(context.Background(), core.Instance{ID: 7, Kind: Kind})
	if err != nil {
		t.Fatalf("IngressHandle: %v", err)
	}
	if got.Device != "pwg7" {
		t.Errorf("Device = %q, want %q", got.Device, "pwg7")
	}
	if got.Tag != "" || got.BlockedKey != "" {
		t.Errorf("a device ingress answers with a device alone, got tag=%q blocked=%q", got.Tag, got.BlockedKey)
	}
}

func TestWireguardIsADeviceIngress(t *testing.T) {
	c := &Core{}
	if got := c.IngressSelector(Kind); got != core.IngressDevice {
		t.Errorf("IngressSelector(%q) = %q, want %q", Kind, got, core.IngressDevice)
	}
	// Xray's own userspace wireguard is a different kind served by a different
	// core, and answering for it here would claim a device that does not exist.
	if got := c.IngressSelector("wireguard"); got != core.IngressNone {
		t.Errorf("IngressSelector(wireguard) = %q, want %q", got, core.IngressNone)
	}
}

/*
The same engine serves an inbound and dials an uplink, so this core answers on
both sides of a rule. What it must NOT do is claim the source address is handled.

WireGuard performs no address translation, so a packet forwarded into an uplink
still carries the ingress client's inner source and the far side drops it. Saying
SourceOwnerPanel is what makes the compile refuse this exit until the panel can
write a MASQUERADE rule -- a refusal beats a route that looks healthy and
delivers nothing.
*/
func TestExitHandleAdmitsThePanelOwnsTheSource(t *testing.T) {
	c := &Core{}

	if kinds := c.ExitKinds(); len(kinds) != 1 || kinds[0] != Kind {
		t.Fatalf("ExitKinds = %v, want [%s]", kinds, Kind)
	}
	if got := c.ExitHandleKind(Kind); got != core.ExitDevice {
		t.Errorf("ExitHandleKind(%s) = %q, want %q", Kind, got, core.ExitDevice)
	}
	if got := c.ExitHandleKind("vless"); got != core.ExitNone {
		t.Errorf("a kind this core does not serve = %q, want %q", got, core.ExitNone)
	}

	handle, err := (&Core{}).ExitHandle(context.Background(), core.Exit{ID: 5, Kind: Kind, Enable: true})
	if err != nil {
		t.Fatalf("ExitHandle: %v", err)
	}
	if handle.Device != "pwg5" {
		t.Errorf("Device = %q, want %q", handle.Device, "pwg5")
	}
	if handle.Source != core.SourceOwnerPanel {
		t.Fatalf("Source = %q, want %q: claiming the daemon NATs would let the compile "+
			"emit a route that silently drops every packet", handle.Source, core.SourceOwnerPanel)
	}
}
