package wireguard

import (
	"context"
	"net/netip"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Plane is the host network stack as this engine uses it. It is an interface so
// the convergence logic runs under test on any platform; the real one is Linux.
type Plane interface {
	// Probe reports whether kernel WireGuard is usable on this host.
	Probe(ctx context.Context) error

	// Snapshot reads one device's live state. Exists is false without an error
	// when the interface is simply not there.
	Snapshot(ctx context.Context, name string) (Snapshot, error)

	// EnsureLink creates the device when it is missing and brings it up. Created
	// reports that the device is new, so every setting has to be pushed again.
	EnsureLink(ctx context.Context, spec LinkSpec) (LinkState, error)

	// DeleteLink removes the device. A device that is already gone is not an error.
	DeleteLink(ctx context.Context, name string) error

	// Configure applies one wgctrl configuration to the device.
	Configure(ctx context.Context, name string, cfg wgtypes.Config) error

	AddAddr(ctx context.Context, name string, addr netip.Prefix) error
	DelAddr(ctx context.Context, name string, addr netip.Prefix) error
	AddRoute(ctx context.Context, name string, dst netip.Prefix) error
	DelRoute(ctx context.Context, name string, dst netip.Prefix) error
}

// LinkSpec is the device as the link layer sees it. An MTU of zero leaves the
// kernel default in place rather than asking for a zero-byte interface.
type LinkSpec struct {
	Name string
	MTU  int
}

// LinkState is what the link layer reports back. Created forces a full push: a
// device that has just been made has no key, no addresses and no peers.
type LinkState struct {
	Index   int
	MTU     int
	Up      bool
	Created bool
}

// Snapshot is everything needed to diff one device against desired state. It is
// read on every Ensure, which is what lets an out-of-band change be repaired.
type Snapshot struct {
	Exists bool
	Link   LinkState
	Device wgtypes.Device
	Addrs  []netip.Prefix
	Routes []netip.Prefix
}
