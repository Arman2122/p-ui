package shaping

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
)

/*
Namespaces is the set of device namespaces this panel may install a tree on.

Registration is explicit and never init(): with init() the owned set would depend
on whichever main links the package, and a device this panel does not own is one
whose traffic it must not throttle.

A namespace is a PREFIX, never a core: this package may name a device and must
not learn which protocol produced it. wgkernel's pwg and the egress band's peg
are built in because the panel itself derives them from an id; a core that brings
its own device namespace registers it at wiring time.
*/
type Namespaces struct {
	mu        sync.RWMutex
	shapeable []string
}

// DefaultNamespaces is what the panel derives on its own: L3 ingress devices and
// the fronts the egress band carves. The mirror namespace is not among them —
// this package creates those itself, so ownership is never in question.
func DefaultNamespaces() *Namespaces {
	return &Namespaces{shapeable: []string{wireguardPrefix, amneziawgPrefix, egressPrefix}}
}

/*
Register claims one device-name prefix.

Letters only, and that is load-bearing rather than tidy: an id is all digits, so
a letters-only prefix splits any device name at exactly one place and two
distinct namespaces can never both round-trip the same device. A prefix ending
in a digit would make pwg1+2 and pwg+12 the same string with two owners.

A duplicate is an error rather than a silent accept, matching core.Registry and
egress.Registry — a second claim on one namespace means two managers believe they
own one device's tree.
*/
func (n *Namespaces) Register(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("shaping: Register(\"\")")
	}
	for _, r := range prefix {
		if r < 'a' || r > 'z' {
			return fmt.Errorf("%w: %q must be lower-case letters only, so a name splits at one place",
				ErrBadNamespace, prefix)
		}
	}
	// The shortest name a prefix can produce must still fit, or every device it
	// could ever name is refused by the kernel rather than by us.
	if len(prefix)+1 > maxDeviceName {
		return fmt.Errorf("%w: %q leaves no room for an id within %d characters",
			ErrBadNamespace, prefix, maxDeviceName)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if slices.Contains(n.shapeable, prefix) {
		return fmt.Errorf("%w: %q", ErrDuplicateNamespace, prefix)
	}
	n.shapeable = append(n.shapeable, prefix)
	return nil
}

// Shapeable lists the namespaces whose devices carry client traffic — the ones a
// tree and a mirror are built for. Sorted, so an op log is comparable.
func (n *Namespaces) Shapeable() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := slices.Clone(n.shapeable)
	slices.Sort(out)
	return out
}

/*
Owns reports whether this panel may install objects on device.

A round-tripping predicate over the panel's own names, not a string prefix test:
an operator's "pwgtest" shares the prefix and is somebody else's interface, and a
tree installed on it would throttle traffic the panel does not serve.
*/
func (n *Namespaces) Owns(device string) bool {
	_, mine := n.DeviceID(device)
	return mine
}

// DeviceID reads the inbound id out of an owned device name, whichever namespace
// it belongs to — including the mirror band, which is owned by construction.
func (n *Namespaces) DeviceID(device string) (int, bool) {
	if id, mine := ownedID(device, ifbPrefix); mine {
		return id, true
	}
	for _, prefix := range n.Shapeable() {
		if id, mine := ownedID(device, prefix); mine {
			return id, true
		}
	}
	return 0, false
}

// parentOf names the shapeable device an id would have in prefix. Used to find
// the tree that feeds a mirror, which is the only evidence the mirror is live.
func parentOf(prefix string, id int) string { return prefix + strconv.Itoa(id) }
