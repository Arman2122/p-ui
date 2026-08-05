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

	Supervise  Supervisor
	Apply      InstanceApplier
	HotApply   HotApplier
	Users      UserProvisioner
	Traffic    TrafficSource
	TagTraffic TagTrafficSource
	Online     OnlineReporter
	Quota      QuotaEnforcer
	Link       LinkRenderer
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
	if v, ok := c.(LinkRenderer); ok {
		b.Link = v
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
	return problems
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
