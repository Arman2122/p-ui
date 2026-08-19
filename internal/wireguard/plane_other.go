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

func (unsupportedPlane) Links(context.Context) ([]string, error) {
	return nil, ErrPlatformUnsupported
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

// UnsupportedPlane is the refusing plane, exported so a core for a Linux-only
// module can still be constructed and registered off Linux. Registration must
// not depend on the platform: the panel decides what it can serve through
// Preflight, and a core missing from the registry on a developer's machine is a
// different build rather than the same one honestly reporting a refusal.
func UnsupportedPlane() Plane { return unsupportedPlane{} }
