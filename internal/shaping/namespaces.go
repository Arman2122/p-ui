package shaping

import (
	"fmt"
	"slices"
	"strings"
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

Two kinds. A plain namespace names one device per inbound, <prefix><id>. A
per-session FAMILY names one per live session, <prefix><id>-<n>, every one of
them shaped as that inbound's. The prefix rules are the same, and one prefix is
one kind or the other.
*/
type Namespaces struct {
	mu        sync.RWMutex
	shapeable []string
	families  []string
}

// DefaultNamespaces is what the panel derives on its own: L3 ingress devices and
// the fronts the egress band carves. The mirror namespace is not among them —
// this package creates those itself, so ownership is never in question.
func DefaultNamespaces() *Namespaces {
	return &Namespaces{shapeable: []string{wireguardPrefix, amneziawgPrefix, egressPrefix}}
}

/*
Register claims one per-inbound device-name prefix.

A duplicate is an error rather than a silent accept, matching core.Registry and
egress.Registry — a second claim on one namespace means two managers believe they
own one device's tree.
*/
func (n *Namespaces) Register(prefix string) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.claimedLocked(prefix) {
		return fmt.Errorf("%w: %q", ErrDuplicateNamespace, prefix)
	}
	n.shapeable = append(n.shapeable, prefix)
	return nil
}

// RegisterFamily claims a per-session prefix, whose devices are named
// <prefix><inbound id>-<session index>. Refused on the same grounds as Register.
func (n *Namespaces) RegisterFamily(prefix string) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.claimedLocked(prefix) {
		return fmt.Errorf("%w: %q", ErrDuplicateNamespace, prefix)
	}
	n.families = append(n.families, prefix)
	return nil
}

func (n *Namespaces) claimedLocked(prefix string) bool {
	return slices.Contains(n.shapeable, prefix) || slices.Contains(n.families, prefix)
}

/*
checkPrefix: letters only, and that is load-bearing rather than tidy: an id is
all digits, so a letters-only prefix splits any device name at exactly one place
and two distinct namespaces can never both round-trip the same device. A prefix
ending in a digit would make pwg1+2 and pwg+12 the same string with two owners.
*/
func checkPrefix(prefix string) error {
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
	return nil
}

// Shapeable lists the per-inbound namespaces whose devices carry client traffic
// — the ones a tree and a mirror are built for. Sorted, so an op log is comparable.
func (n *Namespaces) Shapeable() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := slices.Clone(n.shapeable)
	slices.Sort(out)
	return out
}

// Families lists the per-session namespaces, sorted like Shapeable.
func (n *Namespaces) Families() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := slices.Clone(n.families)
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
	if id, mine := ownedFamilyID(device, ifbPrefix); mine {
		return id, true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, prefix := range n.shapeable {
		if id, mine := ownedID(device, prefix); mine {
			return id, true
		}
	}
	for _, prefix := range n.families {
		if id, mine := ownedFamilyID(device, prefix); mine {
			return id, true
		}
	}
	return 0, false
}

// MirrorOf names the upload mirror a shaped device derives: the mirror band plus
// the device's own suffix, so two sessions of one inbound never share a mirror.
func (n *Namespaces) MirrorOf(device string) (string, bool) {
	suffix, ok := n.suffixOf(device)
	if !ok {
		return "", false
	}
	return mirrorDevice(suffix), true
}

// suffixOf is what follows the owning prefix: "7" for pwg7, "2-3" for poc2-3. A
// mirror has none, since nothing builds a tree on a mirror's mirror.
func (n *Namespaces) suffixOf(device string) (string, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, prefix := range n.shapeable {
		if _, mine := ownedID(device, prefix); mine {
			return strings.TrimPrefix(device, prefix), true
		}
	}
	for _, prefix := range n.families {
		if _, mine := ownedFamilyID(device, prefix); mine {
			return strings.TrimPrefix(device, prefix), true
		}
	}
	return "", false
}

// parentsOf names every device that could feed a mirror carrying suffix, one per
// namespace; the caller reads only those that exist.
func (n *Namespaces) parentsOf(suffix string) []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.shapeable)+len(n.families))
	for _, prefix := range n.shapeable {
		out = append(out, prefix+suffix)
	}
	for _, prefix := range n.families {
		out = append(out, prefix+suffix)
	}
	slices.Sort(out)
	return out
}
