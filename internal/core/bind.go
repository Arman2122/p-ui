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

	Supervise     Supervisor
	Apply         InstanceApplier
	HotApply      HotApplier
	Users         UserProvisioner
	UserSet       WholeSetUserProvisioner
	Creds         CredentialDeclarer
	Validate      InstanceValidator
	Transports    TransportDeclarer
	Traffic       TrafficSource
	TagTraffic    TagTrafficSource
	Online        OnlineReporter
	Quota         QuotaEnforcer
	Version       VersionReporter
	Versions      VersionManager
	Link          LinkRenderer
	Shape         ShapingHost
	Rate          RateEnforcer
	Deferred      DeferredApplier
	FileShare     FileShareDeclarer
	Outliver      PanelOutliver
	Mint          InstanceMinter
	Devices       DeviceNamespacer
	Ingress       RoutableIngress
	Egress        RoutableEgress
	ExitSupervise ExitSupervisor
	Sessions      SessionReporter
	CounterLoss   CounterLossDeclarer
	Cut           SessionCutter
	Admits        LoginAdmitter
	Health        HealthReporter
	Logs          LogTailer
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
	if v, ok := c.(InstanceValidator); ok {
		b.Validate = v
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
	if v, ok := c.(VersionReporter); ok {
		b.Version = v
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
	if v, ok := c.(RateEnforcer); ok {
		b.Rate = v
	}
	if v, ok := c.(DeferredApplier); ok {
		b.Deferred = v
	}
	if v, ok := c.(FileShareDeclarer); ok {
		b.FileShare = v
	}
	if v, ok := c.(PanelOutliver); ok {
		b.Outliver = v
	}
	if v, ok := c.(InstanceMinter); ok {
		b.Mint = v
	}
	if v, ok := c.(DeviceNamespacer); ok {
		b.Devices = v
	}
	if v, ok := c.(RoutableIngress); ok {
		b.Ingress = v
	}
	if v, ok := c.(RoutableEgress); ok {
		b.Egress = v
	}
	if v, ok := c.(ExitSupervisor); ok {
		b.ExitSupervise = v
	}
	if v, ok := c.(SessionReporter); ok {
		b.Sessions = v
	}
	if v, ok := c.(CounterLossDeclarer); ok {
		b.CounterLoss = v
	}
	if v, ok := c.(SessionCutter); ok {
		b.Cut = v
	}
	if v, ok := c.(LoginAdmitter); ok {
		b.Admits = v
	}
	if v, ok := c.(HealthReporter); ok {
		b.Health = v
	}
	if v, ok := c.(LogTailer); ok {
		b.Logs = v
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
	if b.Rate != nil && !ratesSomeKind(b.Core, b.Rate) {
		problems = append(problems, "RateEnforcer: implemented, but every kind answers EnforcesRates false; a core that can throttle nobody declares nothing")
	}
	if b.Rate != nil && b.Shape != nil {
		problems = append(problems, "RateEnforcer and ShapingHost: a core throttles inside itself or hands the panel a kernel identity, never both")
	}
	// Same shape as the shaping and routing rules: a core that provisions users
	// for no kind it serves is claiming a capability it never applies.
	if b.Users != nil && !provisionsSomeKind(b.Core, b.Users) {
		problems = append(problems, "UserProvisioner: implemented, but every kind answers ProvisionsUsers false; a core that can provision nobody declares nothing")
	}
	if b.Ingress != nil && !routesSomeKind(b.Core, b.Ingress) {
		problems = append(problems, "RoutableIngress: implemented, but every kind answers IngressNone; a core that can route nothing declares nothing")
	}
	// A routable kind that names no criterion is a subject no rule can be
	// written against, which is the same silence as not routing it at all. F060.
	if b.Ingress != nil && routesSomeKind(b.Core, b.Ingress) && !namesSomeCriterion(b.Core, b.Ingress) {
		problems = append(problems, "RoutableIngress: implemented, but every kind names no criterion; a subject no rule can match declares nothing")
	}
	problems = append(problems, unmechanizedCriteria(b)...)
	if b.Egress != nil && len(b.Egress.ExitKinds()) == 0 {
		problems = append(problems, "RoutableEgress: implemented, but ExitKinds is empty; a core that offers no exit declares nothing")
	}
	if b.CounterLoss != nil && !losesCountersOnSomeKind(b.Core, b.CounterLoss) {
		problems = append(problems, "CounterLossDeclarer: implemented, but every kind answers false; not implementing it says the same thing")
	}
	if b.Cut != nil && b.Sessions == nil {
		problems = append(problems, "SessionCutter: implemented without SessionReporter; a cut is aimed by a session the core must be able to name")
	}
	// LoginAdmitter has no all-false rule on purpose: false is the honest answer
	// of a core wired without an AdmitFunc, and a registry built that way is valid.
	return problems
}

/*
unmechanizedCriteria names every kind declaring a criterion the panel cannot
produce for that kind's ingress shape.

The promise the criteria mask makes is that a rule the editor lets you write is
a rule that can match. A payload criterion is only answerable where the panel
has somewhere to attach a sniffer -- an inbound the core answers to, or one the
panel synthesises for it -- so a core declaring `domain` for a shape with no
such inbound is writing F060 again, in its own words.

Caught here, in the conformance run the core's own author executes, rather than
by an operator whose rule silently never fired.
*/
func unmechanizedCriteria(b *Bound) []string {
	if b.Ingress == nil {
		return nil
	}
	var problems []string
	for _, kind := range b.Core.Kinds() {
		selector := b.Ingress.IngressSelector(kind)
		if selector == IngressNone || InspectableSelector(selector) {
			continue
		}
		for _, criterion := range b.Ingress.IngressCriteria(kind) {
			if !criterion.NeedsInspection() {
				continue
			}
			problems = append(problems, "RoutableIngress: kind "+string(kind)+" declares criterion "+
				string(criterion)+", which needs the payload read, but its ingress selector "+
				string(selector)+" gives the panel no inbound to attach a sniffer to; "+
				"the rule would save and never match")
		}
	}
	return problems
}

// routesSomeKind reports whether any kind this core serves can be named as a
// routing source, which is the whole of what implementing RoutableIngress claims.
// namesSomeCriterion reports whether any routable kind says what a rule can
// match its traffic on.
func namesSomeCriterion(c Core, ingress RoutableIngress) bool {
	for _, kind := range c.Kinds() {
		if ingress.IngressSelector(kind) == IngressNone {
			continue
		}
		if len(ingress.IngressCriteria(kind)) > 0 {
			return true
		}
	}
	return false
}

// provisionsSomeKind reports whether any kind this core serves can be handed a
// user, which is the whole of what implementing UserProvisioner claims.
func provisionsSomeKind(c Core, u UserProvisioner) bool {
	for _, kind := range c.Kinds() {
		if u.ProvisionsUsers(kind) {
			return true
		}
	}
	return false
}

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
func ratesSomeKind(c Core, r RateEnforcer) bool {
	for _, kind := range c.Kinds() {
		if r.EnforcesRates(kind) {
			return true
		}
	}
	return false
}

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
