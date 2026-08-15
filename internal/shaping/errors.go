package shaping

import "errors"

// The failures an operator has to be able to tell apart. Every Plane error is
// mapped onto one of these before it leaves the manager.
var (
	ErrPlatformUnsupported = errors.New("shaping: kernel shaping is available on Linux only")
	ErrPermission          = errors.New("shaping: managing qdiscs needs CAP_NET_ADMIN")
	// ErrNoDevice is retryable, not a failure: the core owns the device and it is
	// absent between a restart and the next reconcile.
	ErrNoDevice = errors.New("shaping: the device is gone")
	// ErrNotOwned is a device outside Owns(). It is refused rather than shaped,
	// because installing a tree on somebody else's interface is unrecoverable.
	ErrNotOwned = errors.New("shaping: the device is not this panel's to shape")
	// ErrBadNamespace is a device prefix that cannot round-trip an id unambiguously.
	ErrBadNamespace = errors.New("shaping: the device namespace is not usable")
	// ErrDuplicateNamespace is one prefix claimed twice, which would leave two
	// managers believing they own one device's tree.
	ErrDuplicateNamespace = errors.New("shaping: the device namespace is already registered")
	// ErrForeignObject is an object on an owned device that the panel did not write.
	// It is reported and left alone: a reconciler that guesses deletes an operator's work.
	ErrForeignObject = errors.New("shaping: an object on the device belongs to somebody else")
	// ErrDuplicateKey is one selector claimed by two subjects. Both go unshaped:
	// shaping one user as another is the failure a customer cannot detect.
	ErrDuplicateKey = errors.New("shaping: two subjects claim the same selector")
	// ErrModuleMissing is a kernel that cannot carry the mechanism at all. It
	// disables shaping and never stops the panel.
	ErrModuleMissing = errors.New("shaping: the kernel does not carry a module this mechanism needs")

	// The kernel's answers that mean it already agrees with the diff. The errno
	// differs by object; the manager's response to them does not.
	ErrAlreadyInstalled = errors.New("shaping: the object is already installed")
	ErrNotInstalled     = errors.New("shaping: the object is not installed")
	// ErrInUse is EBUSY: measured, deleting a class a filter still points at is
	// refused, which is why teardown order is filter -> leaf qdisc -> class.
	ErrInUse = errors.New("shaping: the object is still in use")
	// ErrPriorityInUse is EINVAL on a filter add: measured, the kernel keys a filter
	// chain on (protocol, priority) and holds one address family per priority.
	ErrPriorityInUse = errors.New("shaping: another address family already holds that filter priority")
)
