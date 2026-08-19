//go:build !linux

package awg

import (
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/wireguard"
)

// Driver off Linux refuses everything, so a core built here reports honestly
// rather than being absent from the registry entirely.
type Driver struct{}

func NewPlane() wireguard.Plane { return wireguard.UnsupportedPlane() }

func (Driver) Probe() error { return ErrPlatformUnsupported }

func (Driver) Device(string) (*wgtypes.Device, error) { return nil, ErrPlatformUnsupported }

func (Driver) ConfigureDevice(string, wgtypes.Config) error { return ErrPlatformUnsupported }

// ConfigureParams matches the Linux signature so callers compile unchanged.
func ConfigureParams(string, Params) error { return ErrPlatformUnsupported }
