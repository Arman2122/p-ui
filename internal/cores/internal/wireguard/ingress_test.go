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
