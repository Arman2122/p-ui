//go:build !linux

package shaping

import "context"

// unsupportedPlane refuses every operation. The seam exists for determinism, not
// for portability: tc has no stand-in on the platforms this is not.
type unsupportedPlane struct{}

func hostPlane() Plane { return unsupportedPlane{} }

func (unsupportedPlane) Probe(context.Context) error { return ErrPlatformUnsupported }

func (unsupportedPlane) Snapshot(context.Context, string) (Snapshot, error) {
	return Snapshot{}, ErrPlatformUnsupported
}

func (unsupportedPlane) Links(context.Context) ([]string, error) {
	return nil, ErrPlatformUnsupported
}

func (unsupportedPlane) EnsureIFB(context.Context, string) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DeleteIFB(context.Context, string) error { return ErrPlatformUnsupported }

func (unsupportedPlane) AddQdisc(context.Context, QdiscSpec) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DelQdisc(context.Context, QdiscSpec) error { return ErrPlatformUnsupported }

func (unsupportedPlane) AddClass(context.Context, ClassSpec) error    { return ErrPlatformUnsupported }
func (unsupportedPlane) ChangeClass(context.Context, ClassSpec) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DelClass(context.Context, ClassSpec) error    { return ErrPlatformUnsupported }

func (unsupportedPlane) AddFilter(context.Context, FilterSpec) error { return ErrPlatformUnsupported }
func (unsupportedPlane) DelFilter(context.Context, FilterSpec) error { return ErrPlatformUnsupported }
