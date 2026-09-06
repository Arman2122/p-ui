//go:build !linux

package banset

import "context"

// unsupportedPlane refuses every operation. The seam exists for determinism, not
// for portability: nft has no stand-in on the platforms this is not.
type unsupportedPlane struct{}

func hostPlane() Plane { return unsupportedPlane{} }

func (unsupportedPlane) Probe(context.Context) error         { return ErrPlatformUnsupported }
func (unsupportedPlane) Apply(context.Context, string) error { return ErrPlatformUnsupported }
