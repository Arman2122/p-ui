package shaping

import (
	"context"
	"fmt"
	"net/netip"
)

// The three qdisc types this package installs, as the kernel names them. Strings
// because that is what a dump returns, so a foreign one can be named in an error.
const (
	QdiscHTB     = "htb"
	QdiscFqCodel = "fq_codel"
	QdiscClsact  = "clsact"
)

/*
The handles the ingress hook lives at, measured rather than taken from a constant
name.

The clsact qdisc reports handle ffff: parent ffff:fff1, but a filter on its
ingress side attaches at ffff:fff2 — netlink's HANDLE_CLSACT is the qdisc's
parent, not the filter's, and using it silently installs nothing.
*/
const (
	clsactHandle uint32 = 0xffff0000
	clsactParent uint32 = 0xfffffff1
	ingressBlock uint32 = 0xfffffff2
)

// QdiscSpec is one qdisc. A leaf's Handle is assigned by the kernel and read back,
// so a leaf is identified by its Parent and never by a handle the panel picked.
type QdiscSpec struct {
	Device string
	Type   string
	Handle uint32
	Parent uint32
	// Default is the minor unclassified traffic falls into, on an htb root only.
	Default uint16
}

func (q QdiscSpec) String() string {
	return fmt.Sprintf("qdisc %s %s handle %s parent %s", q.Device, q.Type, handleStr(q.Handle), handleStr(q.Parent))
}

// ClassSpec is one shaped budget. The rates are bytes per second because that is
// what HTB stores: canonicalising anywhere else leaves a residual that churns.
type ClassSpec struct {
	Device          string
	Handle          uint32
	Parent          uint32
	RateBytesPerSec uint64
	CeilBytesPerSec uint64
}

func (c ClassSpec) String() string {
	return fmt.Sprintf("class %s %s rate %d ceil %d", c.Device, handleStr(c.Handle), c.RateBytesPerSec, c.CeilBytesPerSec)
}

// MatchField is the header field a filter keys on. Download selects on the
// destination, upload on the source, and the same prefix serves both.
type MatchField uint8

const (
	MatchDst MatchField = iota
	MatchSrc
)

func (m MatchField) String() string {
	if m == MatchSrc {
		return "src"
	}
	return "dst"
}

/*
FilterSpec is one cls_flower rule. It either selects a class or redirects to the
upload mirror, never both.

Handle is assigned by the kernel and is the only way to delete a filter, so it is
carried out of the snapshot and deliberately excluded from every comparison.
*/
type FilterSpec struct {
	Device   string
	Parent   uint32
	Priority uint16
	Match    MatchField
	// Prefix carries the family: flower needs an eth_type, and a v4-only filter
	// list leaves every v6 flow of that user unshaped.
	Prefix   netip.Prefix
	ClassID  uint32
	Redirect string
	Handle   uint32
}

func (f FilterSpec) String() string {
	target := "classid " + handleStr(f.ClassID)
	if f.Redirect != "" {
		target = "redirect " + f.Redirect
	}
	return fmt.Sprintf("filter %s parent %s prio %d %s_ip %s %s",
		f.Device, handleStr(f.Parent), f.Priority, f.Match, f.Prefix, target)
}

// same reports whether two filters are the same rule. The kernel-assigned handle
// is excluded on purpose: including it would make every readback a difference.
func (f FilterSpec) same(other FilterSpec) bool {
	f.Handle, other.Handle = 0, 0
	return f == other
}

// handleStr renders a handle the way tc does, so an error message can be pasted
// into a tc command.
func handleStr(handle uint32) string {
	if handle == 0 {
		return "none"
	}
	return fmt.Sprintf("%x:%x", handle>>16, uint16(handle))
}

// Snapshot is one device's whole tree as the kernel holds it right now, read
// fresh on every pass: a fingerprint of this process's own writes cannot see
// damage done by anything else.
type Snapshot struct {
	// Exists separates "the device is absent" from "the device is bare". The
	// first is retryable and normal; the second is a tree the panel must build.
	Exists bool
	// Qdiscs, Classes and Filters are the whole device, foreign objects included:
	// deciding what is owned is the manager's job and it cannot decide about
	// what it never saw.
	Qdiscs  []QdiscSpec
	Classes []ClassSpec
	Filters []FilterSpec
	// Links names every interface on the host. Required, not incidental: measured,
	// a pifb device SURVIVES the deletion of the pwg it mirrors.
	Links []string
}

// Plane is the host traffic-control stack as this manager uses it, an interface
// so convergence is table-testable off Linux. It deals in device names only.
type Plane interface {
	// Probe reports whether this host can be shaped by this panel at all.
	Probe(ctx context.Context) error

	// Snapshot reads one device's tree. It is the only read the diff consumes:
	// the manager holds no cached view of anything it wrote.
	Snapshot(ctx context.Context, device string) (Snapshot, error)

	// Links enumerates every interface, so the GC can find a mirror device whose
	// parent is long gone and which no snapshot would otherwise reach.
	Links(ctx context.Context) ([]string, error)

	EnsureIFB(ctx context.Context, name string) error
	DeleteIFB(ctx context.Context, name string) error

	AddQdisc(ctx context.Context, spec QdiscSpec) error
	DelQdisc(ctx context.Context, spec QdiscSpec) error

	AddClass(ctx context.Context, spec ClassSpec) error
	// ChangeClass is the 15us live-edit path and the reason this is not an Ensure:
	// add-on-existing answers EEXIST, so the diff must choose between them.
	ChangeClass(ctx context.Context, spec ClassSpec) error
	DelClass(ctx context.Context, spec ClassSpec) error

	AddFilter(ctx context.Context, spec FilterSpec) error
	DelFilter(ctx context.Context, spec FilterSpec) error
}

// HostPlane is the real stack on Linux and a refusing stub everywhere else.
func HostPlane() Plane { return hostPlane() }
