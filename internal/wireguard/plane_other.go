//go:build !linux

package wireguard

import (
	"context"
	"net/netip"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// unsupportedPlane refuses every operation. The seam is about determinism, not
// compilation: wgctrl.New succeeds on Windows and drives a WireGuardNT device.
type unsupportedPlane struct{}

func hostPlane() Plane { return unsupportedPlane{} }

func (unsupportedPlane) Probe(context.Context) error { return ErrPlatformUnsupported }

func (unsupportedPlane) Snapshot(context.Context, string) (Snapshot, error) {
	return Snapshot{}, ErrPlatformUnsupported
}

func (unsupportedPlane) EnsureLink(context.Context, LinkSpec) (LinkState, error) {
	return LinkState{}, ErrPlatformUnsupported
}

func (unsupportedPlane) DeleteLink(context.Context, string) error { return ErrPlatformUnsupported }

func (unsupportedPlane) Configure(context.Context, string, wgtypes.Config) error {
	return ErrPlatformUnsupported
}

func (unsupportedPlane) AddAddr(context.Context, string, netip.Prefix) error {
	return ErrPlatformUnsupported
}

func (unsupportedPlane) DelAddr(context.Context, string, netip.Prefix) error {
	return ErrPlatformUnsupported
}

func (unsupportedPlane) AddRoute(context.Context, string, netip.Prefix) error {
	return ErrPlatformUnsupported
}

func (unsupportedPlane) DelRoute(context.Context, string, netip.Prefix) error {
	return ErrPlatformUnsupported
}
