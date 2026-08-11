package egress

import "errors"

// The failures an operator has to be able to tell apart. Every Plane error is
// mapped onto one of these before it leaves the manager.
var (
	ErrPlatformUnsupported = errors.New("egress: policy-routed egress is available on Linux only")
	ErrPermission          = errors.New("egress: managing routing rules needs CAP_NET_ADMIN")
	ErrNoDevice            = errors.New("egress: the front device is gone")
	// ErrFamilyDisabled is one family switched off on the front device itself. The
	// rule and the blackhole still install, so the flow is contained, not leaking.
	ErrFamilyDisabled  = errors.New("egress: the address family is disabled on the front device")
	ErrIDOutOfRange    = errors.New("egress: the id is outside the reserved band")
	ErrGatewayBase     = errors.New("egress: the gateway base cannot serve the id band")
	ErrUnknownDriver   = errors.New("egress: no driver is registered for this type")
	ErrDuplicateDriver = errors.New("egress: a driver is already registered for this type")
	ErrForeignResource = errors.New("egress: an object in the reserved band belongs to somebody else")
	ErrStrictRPFilter  = errors.New("egress: strict reverse-path filtering kills the front's return path")

	// The kernel's two answers that mean it already agrees with the diff. The errno
	// differs by object; the manager's response to them does not.
	ErrAlreadyInstalled = errors.New("egress: the object is already installed")
	ErrNotInstalled     = errors.New("egress: the object is not installed")
)
