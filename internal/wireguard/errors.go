package wireguard

import "errors"

// The failures an operator has to be able to tell apart. Every Plane error is
// mapped onto one of these before it leaves the engine.
var (
	ErrPlatformUnsupported = errors.New("wireguard: kernel WireGuard is available on Linux only")
	ErrNoKernelSupport     = errors.New("wireguard: this kernel has no WireGuard support")
	ErrPermission          = errors.New("wireguard: managing a WireGuard device needs CAP_NET_ADMIN")
	ErrNoDevice            = errors.New("wireguard: the device is gone")
	ErrNotWireguardLink    = errors.New("wireguard: the interface exists and is not a WireGuard device")
)
