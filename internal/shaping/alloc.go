/*
Package shaping installs and converges the kernel objects that hold one user to a
contracted rate: an HTB class per shaped user, an sfq leaf under it, and a
cls_flower filter per address family selecting it.

It is the mechanism and nothing else. It has no email, no protocol, no tier, no
quota and no plan — a subject is an opaque id, a set of selectors and two numbers
in bits per second. The rules that produce those numbers live in internal/policy,
which this package neither imports nor is imported by.
*/
package shaping

import (
	"net/netip"
	"strconv"
	"strings"
)

// The device namespaces this panel owns. They are re-declared here rather than
// imported: this package may name a device, never a core.
const (
	wireguardPrefix = "pwg"
	egressPrefix    = "peg"
	// ifbPrefix names the upload mirror device. It is created by this package
	// outright, so its whole lifecycle — including the GC — is ours.
	ifbPrefix = "pifb"

	// MinID is the first inbound id a device name can carry. There is no upper
	// bound but the name's: an inbound id is a database key and outgrows any band.
	MinID = 1
	// maxDeviceName is IFNAMSIZ-1. A longer name is refused by the kernel at
	// creation, so it must be refused here rather than discovered at add time.
	maxDeviceName = 15
)

// IFBDevice is the upload mirror for one inbound. A pure function of the id, so
// nothing derived is stored and the GC can decide ownership from the name alone.
func IFBDevice(id int) string { return ifbPrefix + strconv.Itoa(id) }

// ownedIFBID reads the id back out of a mirror device name. It round-trips
// through IFBDevice, so a near miss like pifb007 or pifb0 is somebody else's.
func ownedIFBID(name string) (int, bool) { return ownedID(name, ifbPrefix) }

// ownedID is the shared round trip: the name must rebuild itself exactly from the
// id it claims, which is what rejects a leading zero and a trailing letter alike.
func ownedID(name, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || len(name) > maxDeviceName {
		return 0, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id < MinID || prefix+strconv.Itoa(id) != name {
		return 0, false
	}
	return id, true
}

// The one tree this package builds, as handles. Major 1 is the root qdisc on
// every device it owns; a minor is one shaped user and is never persisted.
const (
	rootMajor  uint16 = 1
	rootHandle uint32 = uint32(rootMajor) << 16
	// rootParent is what the kernel reports for a qdisc attached at the root.
	rootParent uint32 = 0xffffffff

	// defaultMinor is the class unclassified traffic falls into. It is created
	// explicitly at UnlimitedBps: measured, a guessed default throttles an
	// unshaped peer 4.8x, and shaping must fail open.
	defaultMinor uint16 = 0xffff
	// firstMinor leaves the low minors free so a hand-written 1:1 or 1:10 from an
	// operator's own experiment is never mistaken for this panel's.
	firstMinor uint16 = 0x10

	/*
		The two priorities this package writes filters at, one per address family.

		Measured: the kernel keys a filter chain on (protocol, priority) and answers
		EINVAL to a second protocol at a priority another already holds, so a v6
		filter beside a v4 one at 100 is refused outright. Anything foreign AHEAD of
		these silently eats the packets ours were installed to classify.
	*/
	filterPrioV4 uint16 = 100
	filterPrioV6 uint16 = 101
)

// ourPriority is the priority one selector's filter belongs at. It is a function
// of the family and never of a counter, so a filter round-trips across passes.
func ourPriority(prefix netip.Prefix) uint16 {
	if prefix.Addr().Is6() {
		return filterPrioV6
	}
	return filterPrioV4
}

// classHandle is the full handle of one user's class. Minors are discovered from
// the snapshot at add time, never derived from an id and never stored.
func classHandle(minor uint16) uint32 { return rootHandle | uint32(minor) }

// classMinor reads the minor back out of a class handle, and reports whether the
// handle is one this package could have written at all.
func classMinor(handle uint32) (uint16, bool) {
	major, minor := uint16(handle>>16), uint16(handle)
	return minor, major == rootMajor && minor != 0
}
