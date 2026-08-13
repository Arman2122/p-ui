package egress

import (
	"context"
	"fmt"
	"net/netip"
)

// Family is the address family an object belongs to. The v4 and v6 namespaces are
// independent, so every object has a twin: v4-only leaks every v6 flow silently.
type Family uint8

const (
	FamilyV4 Family = 4
	FamilyV6 Family = 6
)

// Families is the set every egress object is installed in, in a fixed order so
// an op log is comparable across passes.
var Families = [...]Family{FamilyV4, FamilyV6}

func (f Family) String() string {
	if f == FamilyV6 {
		return "v6"
	}
	return "v4"
}

// DefaultRoute is the destination every route this package installs carries.
// netlink refuses a route with no destination, so "default" is written out.
func (f Family) DefaultRoute() netip.Prefix {
	if f == FamilyV6 {
		return netip.PrefixFrom(netip.IPv6Unspecified(), 0)
	}
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{}), 0)
}

// RuleSpec is one `ip rule iif <dev> lookup <table> priority <prio>`. Selection is
// by ingress device: cryptokey routing has already proven the peer's identity.
type RuleSpec struct {
	Family   Family
	Priority int
	Iif      string
	Table    int
	// Mark selects by the fwmark a socket carries instead of by ingress device,
	// which is how traffic an L7 core originates on this host reaches an exit.
	// Zero means "no mark", so an iif rule is unchanged by its presence.
	Mark uint32
}

func (r RuleSpec) String() string {
	if r.Mark != 0 {
		return fmt.Sprintf("%s prio %d fwmark %#x lookup %d", r.Family, r.Priority, r.Mark, r.Table)
	}
	return fmt.Sprintf("%s prio %d iif %s lookup %d", r.Family, r.Priority, r.Iif, r.Table)
}

// RouteType separates the entries an egress table holds. Blackhole is part of the
// table's identity: a table with no match falls through to main and leaks.
type RouteType uint8

const (
	RouteUnicast RouteType = iota
	RouteBlackhole
	// RouteOther is anything else the kernel reports from a reserved table. It is
	// named by preflight and never touched, because the panel did not write it.
	RouteOther
)

func (t RouteType) String() string {
	switch t {
	case RouteBlackhole:
		return "blackhole"
	case RouteUnicast:
		return "unicast"
	}
	return "other"
}

// The metrics that order an egress table. The front wins while it exists; the
// kernel purges it with the device, leaving the blackhole and no leak.
const (
	FrontMetric     = 100
	BlackholeMetric = 4096
)

// RouteSpec is one route in one table. Dst is always the family's default here,
// but it is carried so a foreign route in a reserved table can be named exactly.
type RouteSpec struct {
	Family Family
	Table  int
	Type   RouteType
	Dst    netip.Prefix
	Device string
	Metric int
}

func (r RouteSpec) String() string {
	dst := "default"
	if r.Dst.IsValid() && r.Dst.Bits() != 0 {
		dst = r.Dst.String()
	}
	if r.Type == RouteBlackhole {
		return fmt.Sprintf("%s table %d blackhole %s metric %d", r.Family, r.Table, dst, r.Metric)
	}
	return fmt.Sprintf("%s table %d %s dev %s metric %d", r.Family, r.Table, dst, r.Device, r.Metric)
}

// AddrSpec is one address and the device carrying it. The device is what tells a
// front holding the gateway its own id derives from a squatter on the same /32.
type AddrSpec struct {
	Prefix netip.Prefix
	Device string
}

func (a AddrSpec) String() string {
	if a.Device == "" {
		return a.Prefix.String()
	}
	return fmt.Sprintf("%s on %s", a.Prefix, a.Device)
}

// Snapshot is the whole reserved band as the kernel holds it right now, read fresh
// on every pass: a fingerprint of this process's own writes cannot see damage.
type Snapshot struct {
	// Rules and Routes are the whole reserved band, foreign objects included: deciding
	// what is owned is the manager's job, and it cannot decide about what it never saw.
	Rules  []RuleSpec
	Routes []RouteSpec
	// Links names every interface on the host. The front is created by somebody
	// else (Xray, strongSwan), so its absence is a normal state, not a fault.
	Links []string
	// Addrs is every address the host holds, so preflight can refuse a gateway
	// base that overlaps something already on the box.
	Addrs []AddrSpec
}

// Plane is the host network stack as this manager uses it, an interface so
// convergence is table-testable off Linux. It deals in device-name strings only.
type Plane interface {
	// Probe reports whether this host can be policy-routed by this panel at all.
	Probe(ctx context.Context) error

	// Snapshot reads the reserved band. It is the only read: the manager holds no
	// cached view of anything it wrote.
	Snapshot(ctx context.Context) (Snapshot, error)

	// AddRule installs one rule. It is deliberately not an Ensure: measured, the kernel
	// answers EEXIST on an exact duplicate, so the diff must not issue one.
	AddRule(ctx context.Context, spec RuleSpec) error

	// DelRule removes one matching rule. Removing the rule is what stops traffic,
	// so it is always the first thing a teardown does.
	DelRule(ctx context.Context, spec RuleSpec) error

	AddRoute(ctx context.Context, spec RouteSpec) error
	DelRoute(ctx context.Context, spec RouteSpec) error

	// Sysctl reads one dotted key. An absent key is an error: the per-device
	// knobs only exist while the device does.
	Sysctl(ctx context.Context, key string) (string, error)
	SetSysctl(ctx context.Context, key, value string) error
}

// HostPlane is the real stack on Linux and a refusing stub everywhere else.
func HostPlane() Plane { return hostPlane() }
