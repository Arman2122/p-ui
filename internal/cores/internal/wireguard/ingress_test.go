package wireguard

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/wireguard/wgtest"
)

/*
This is the resolver that used to live in the service layer as a protocol
comparison, and the dispatch ratchet dropped by one when it moved here. The
device name is derived from the inbound id, so it is stable across restarts.
*/
func TestIngressHandleNamesTheDevice(t *testing.T) {
	// A real manager, because the device name comes from the one that MAKES it --
	// deriving it a second time is how a core serving another module ended up
	// naming pwg4 for a device called pawg4, and everything keyed on that name
	// then pointed at an interface nobody had created.
	c := NewFor(Kind, engine.NewManager(wgtest.New()), nil)
	got, err := c.IngressHandle(context.Background(), core.Instance{ID: 7, Kind: Kind})
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
	c := NewFor(Kind, nil, nil)
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
both sides of a rule.

The uplink is a device the panel dials, in its own namespace: an inbound and an
uplink numbered the same are two devices, never one. Source is the daemon's
because every ingress reaches an exit through a front, so what arrives at the
uplink is a socket Xray re-originated -- the kernel picks the uplink's own
address at route lookup and nothing has to translate it. A packet merely
FORWARDED in would keep the client's inner source and need a MASQUERADE, which
is why this answer is tied to the fronted path rather than asserted in general.
*/
func TestExitHandleIsAnUplinkTheDaemonSources(t *testing.T) {
	// The uplink manager names the dialled device, so it has to be a real one
	// for the same reason the ingress manager does.
	c := NewFor(Kind, engine.NewManager(wgtest.New()), engine.NewNamedManager(wgtest.New(), engine.UplinkPrefix))

	if kinds := c.ExitKinds(); len(kinds) != 1 || kinds[0] != Kind {
		t.Fatalf("ExitKinds = %v, want [%s]", kinds, Kind)
	}
	if got := c.ExitHandleKind(Kind); got != core.ExitDevice {
		t.Errorf("ExitHandleKind(%s) = %q, want %q", Kind, got, core.ExitDevice)
	}
	if got := c.ExitHandleKind("vless"); got != core.ExitNone {
		t.Errorf("a kind this core does not serve = %q, want %q", got, core.ExitNone)
	}

	handle, err := c.ExitHandle(context.Background(), core.Exit{ID: 5, Kind: Kind, Enable: true})
	if err != nil {
		t.Fatalf("ExitHandle: %v", err)
	}
	// Named by THIS core's uplink manager, not the process-wide one: a core
	// serving another module dials in another namespace.
	if want := engine.NewNamedManager(wgtest.New(), engine.UplinkPrefix).Name(5); handle.Device != want {
		t.Errorf("Device = %q, want %q", handle.Device, want)
	}
	if handle.Device == engine.InterfaceName(5) {
		t.Fatal("uplink 5 must not name inbound 5's device: two writers, one device")
	}
	if handle.Source != core.SourceOwnerDaemon {
		t.Fatalf("Source = %q, want %q: Xray re-originates through the front, so the "+
			"kernel picks the uplink's own source", handle.Source, core.SourceOwnerDaemon)
	}
}
