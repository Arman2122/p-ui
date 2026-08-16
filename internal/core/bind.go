package core

/*
The only file permitted to type-assert a capability interface.

Interface segregation on its own does not remove protocol dispatch, it relocates
it: `if h, ok := c.(HotApplier); ok` scattered over the service layer is the same
problem as `switch inbound.Protocol`, just harder to grep. Binding once turns
every call site into a nil check on a field.

internal/arch enforces this: TestCapabilityAssertionsOnlyInBind fails on a
capability assertion anywhere else, seeded at zero so it cannot regress.
*/

// Bound is a core with its capabilities resolved. A nil field means the core
// does not have that capability — callers check the field, never assert.
type Bound struct {
	Core Core

	Supervise   Supervisor
	Apply       InstanceApplier
	HotApply    HotApplier
	Users       UserProvisioner
	UserSet     WholeSetUserProvisioner
	Creds       CredentialDeclarer
	Transports  TransportDeclarer
	Traffic     TrafficSource
	TagTraffic  TagTrafficSource
	Online      OnlineReporter
	Quota       QuotaEnforcer
	Versions    VersionManager
	Link        LinkRenderer
	Shape       ShapingHost
	Ingress     RoutableIngress
	Egress      RoutableEgress
	Sessions    SessionReporter
	CounterLoss CounterLossDeclarer
}

// Bind resolves every optional capability of c exactly once.
func Bind(c Core) *Bound {
	b := &Bound{Core: c}
	if v, ok := c.(Supervisor); ok {
		b.Supervise = v
	}
	if v, ok := c.(InstanceApplier); ok {
		b.Apply = v
	}
	if v, ok := c.(HotApplier); ok {
		b.HotApply = v
	}
	if v, ok := c.(UserProvisioner); ok {
		b.Users = v
	}
	if v, ok := c.(WholeSetUserProvisioner); ok {
		b.UserSet = v
	}
	if v, ok := c.(CredentialDeclarer); ok {
		b.Creds = v
	}
	if v, ok := c.(TransportDeclarer); ok {
		b.Transports = v
	}
	if v, ok := c.(TrafficSource); ok {
		b.Traffic = v
	}
	if v, ok := c.(TagTrafficSource); ok {
		b.TagTraffic = v
	}
	if v, ok := c.(OnlineReporter); ok {
		b.Online = v
	}
	if v, ok := c.(QuotaEnforcer); ok {
		b.Quota = v
	}
	if v, ok := c.(VersionManager); ok {
		b.Versions = v
	}
	if v, ok := c.(LinkRenderer); ok {
		b.Link = v
	}
	if v, ok := c.(ShapingHost); ok {
		b.Shape = v
	}
	if v, ok := c.(RoutableIngress); ok {
		b.Ingress = v
	}
	if v, ok := c.(RoutableEgress); ok {
		b.Egress = v
	}
	if v, ok := c.(SessionReporter); ok {
		b.Sessions = v
	}
	if v, ok := c.(CounterLossDeclarer); ok {
		b.CounterLoss = v
	}
	return b
}

// DeclaredMatchesImplemented reports capabilities a core claims but does not
// implement, and vice versa. coretest calls it so a descriptor cannot become a
// lie — the failure mode that turned OCI's features.md into fiction.
func (b *Bound) DeclaredMatchesImplemented() []string {
	d := b.Core.Describe()
	var problems []string
	check := func(name string, declared *bool, implemented bool) {
		if declared == nil {
			problems = append(problems, name+": not declared; every capability must be answered Yes() or No()")
			return
		}
		if *declared != implemented {
			problems = append(problems, name+": descriptor says "+boolWord(*declared)+" but the interface is "+boolWord(implemented))
		}
	}
	check("UserHotAdd", d.Caps.UserHotAdd, b.Users != nil)
	check("PerUserStats", d.Caps.PerUserStats, b.Traffic != nil)
	check("QuotaPushdown", d.Caps.QuotaPushdown, b.Quota != nil)
	check("OnlineUsers", d.Caps.OnlineUsers, b.Online != nil)
	check("ShareLink", d.Caps.ShareLink, b.Link != nil)
	// ShapingHost declares per kind rather than in Caps, because Descriptor is
	// core-grained and one core answers ten kinds; so its claim is read from those.
	if b.Shape != nil && !shapesSomeKind(b.Core, b.Shape) {
		problems = append(problems, "ShapingHost: implemented, but every kind answers SelectorNone; a core that can shape nothing declares nothing")
	}
	if b.Ingress != nil && !routesSomeKind(b.Core, b.Ingress) {
		problems = append(problems, "RoutableIngress: implemented, but every kind answers IngressNone; a core that can route nothing declares nothing")
	}
	if b.Egress != nil && len(b.Egress.ExitKinds()) == 0 {
		problems = append(problems, "RoutableEgress: implemented, but ExitKinds is empty; a core that offers no exit declares nothing")
	}
	if b.CounterLoss != nil && !losesCountersOnSomeKind(b.Core, b.CounterLoss) {
		problems = append(problems, "CounterLossDeclarer: implemented, but every kind answers false; not implementing it says the same thing")
	}
	return problems
}

// routesSomeKind reports whether any kind this core serves can be named as a
// routing source, which is the whole of what implementing RoutableIngress claims.
func routesSomeKind(c Core, r RoutableIngress) bool {
	for _, kind := range c.Kinds() {
		if r.IngressSelector(kind) != IngressNone {
			return true
		}
	}
	return false
}

// shapesSomeKind reports whether any kind this core serves carries a kernel
// identity, which is the whole of what implementing ShapingHost claims.
func shapesSomeKind(c Core, h ShapingHost) bool {
	for _, kind := range c.Kinds() {
		if h.ShapingSelector(kind) != SelectorNone {
			return true
		}
	}
	return false
}

// losesCountersOnSomeKind reports whether any kind this core serves actually
// pays the removal cost, which is the whole of what implementing it claims.
func losesCountersOnSomeKind(c Core, d CounterLossDeclarer) bool {
	for _, kind := range c.Kinds() {
		if d.RemovalLosesCounters(kind) {
			return true
		}
	}
	return false
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
