//go:build !linux

package egress

import "context"

// unsupportedPlane refuses every operation. The seam exists for determinism, not
// for portability: policy routing has no stand-in on the platforms this is not.
type unsupportedPlane struct{}

func hostPlane() Plane { return unsupportedPlane{} }

func (unsupportedPlane) Probe(context.Context) error { return ErrPlatformUnsupported }

func (unsupportedPlane) Snapshot(context.Context) (Snapshot, error) {
	return Snapshot{}, ErrPlatformUnsupported
}

func (unsupportedPlane) AddRule(context.Context, RuleSpec) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DelRule(context.Context, RuleSpec) error { return ErrPlatformUnsupported }

func (unsupportedPlane) AddRoute(context.Context, RouteSpec) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DelRoute(context.Context, RouteSpec) error { return ErrPlatformUnsupported }

func (unsupportedPlane) Sysctl(context.Context, string) (string, error) {
	return "", ErrPlatformUnsupported
}

func (unsupportedPlane) SetSysctl(context.Context, string, string) error {
	return ErrPlatformUnsupported
}

func (unsupportedPlane) PersistSysctl(context.Context, string, string) error {
	return ErrPlatformUnsupported
}
