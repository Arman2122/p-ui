package amneziawg

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
Every name this core answers with must be its OWN device.

Reported from the panel as "I connect but nothing resolves". The core derived
its ingress device name from a package default instead of asking the manager
that creates it, so an AmneziaWG inbound reported pwg4 for a device called
pawg4. Everything keyed on that name then pointed at an interface nobody had
created: the panel installed its NAT rule on pwg4, the real pawg4 got none, and
clients completed a handshake and could reach nothing at all.

Nothing failed. The rule installed, the device existed, the tunnel came up.
*/
func TestEveryNameBelongsToThisCoresNamespace(t *testing.T) {
	c := New()
	inst := core.Instance{ID: 4, Kind: Kind, Tag: "awg-in"}

	handle, err := c.IngressHandle(context.Background(), inst)
	if err != nil {
		t.Fatalf("IngressHandle: %v", err)
	}
	if handle.Device != DevicePrefix+"4" {
		t.Fatalf("ingress device = %q, want %q -- NAT, egress selection and shaping all key on this", handle.Device, DevicePrefix+"4")
	}
	// The specific wrong answer, named so a regression says what it regressed to.
	if strings.HasPrefix(handle.Device, "pwg") {
		t.Fatalf("ingress device %q is kernel WireGuard's namespace, not this core's", handle.Device)
	}

	exit, err := c.ExitHandle(context.Background(), core.Exit{ID: 5, Kind: Kind, Enable: true})
	if err != nil {
		t.Fatalf("ExitHandle: %v", err)
	}
	if !strings.HasPrefix(exit.Device, "paux") {
		t.Fatalf("exit device = %q, want this module's uplink namespace", exit.Device)
	}
}
