package core

import "context"

/*
Optional capability interfaces.

A core implements only what its daemon can actually do. The alternative — one fat
interface — is already visible in internal/web/runtime: Runtime has 14 methods and
Local's AddUser and RemoveUser are bare `return nil` stubs that read like
implementations. At eleven cores that shape produces ~150 no-op methods, and a
no-op is indistinguishable from a bug.

These are asserted in exactly one place, Bind, and never at a call site.
*/

// Supervisor runs the core's processes. Reconcile is the only genuinely
// mandatory capability: a core that cannot converge on desired state cannot
// self-heal after a crash, so every panel restart becomes a correctness event.
type Supervisor interface {
	Reconcile(ctx context.Context, desired []Instance) error
	StopAll(ctx context.Context) error
}

// InstanceApplier applies one instance without disturbing the others, so an
// edit to one inbound cannot drop a different inbound's connections. A core
// without it is converged by reconciling the whole desired set, which is
// correct but heavier: for a single-process core that means rebuilding the
// config every time one client changes.
type InstanceApplier interface {
	ApplyInstance(ctx context.Context, inst Instance) error
	DropInstance(ctx context.Context, inst Instance) error
}

// HotApplier classifies a change so the caller can avoid dropping connections.
// A core without it is always restarted.
type HotApplier interface {
	PlanChange(before, after Instance) Action
}

// UserProvisioner adds and removes one user against a running instance.
// Without it, a user change is applied by reconciling the whole instance.
type UserProvisioner interface {
	AddUser(ctx context.Context, inst Instance, user User) error
	RemoveUser(ctx context.Context, inst Instance, email string) error
}

// TrafficSource reports per-user usage. Deltas only: a core normalises its own
// counter semantics — cumulative, per-session, or reset-on-read — before
// returning, using Counter where the source is cumulative.
type TrafficSource interface {
	CollectTraffic(ctx context.Context) ([]TrafficDelta, error)
}

/*
TagTrafficSource reports usage that belongs to a tag rather than to a user: the
core's own inbounds, and any egress it meters itself.

Separate from TrafficSource because the two answer different questions and not
every core can answer this one. An inbound's total is not the sum of its users'
— Xray counts inbound, outbound and user as three independent families, and a
dokodemo or tunnel inbound has no users at all — so a core that cannot separate
them leaves this unimplemented rather than returning a guess.

Egress lives here too, not in a capability of its own: a core either meters its
own outbounds (Xray) or routes through one that does (mtproto's routeThroughXray
bridge), and in the second case the bytes are already counted by the first.
*/
type TagTrafficSource interface {
	CollectTagTraffic(ctx context.Context) ([]TagDelta, error)
}

// OnlineReporter names the clients with a live connection right now.
type OnlineReporter interface {
	OnlineEmails(ctx context.Context) ([]string, error)
}

// QuotaEnforcer pushes a byte budget into the daemon so overshoot is bounded by
// the daemon rather than by the panel's polling interval.
type QuotaEnforcer interface {
	ResetQuota(ctx context.Context, email string) error
}

// LinkRenderer produces what an end user needs to connect. Kind distinguishes a
// URI ("link") from a config file ("file") such as .ovpn or wg0.conf.
type LinkRenderer interface {
	RenderClient(inst Instance, user User) (Share, error)
}

// Share is one deliverable client config.
type Share struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename,omitempty"`
	Body     string `json:"body"`
}
