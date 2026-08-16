package core

import (
	"context"
	"net/netip"
)

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

/*
WholeSetUserProvisioner provisions by re-applying its entire user set, the way
mtg rewrites its [secrets] section, so Instance.Users must be the set as it now
stands: a client missing from it is a client revoked.

A plain UserProvisioner alters the named user alone and reads no other client.
The distinction is what lets the caller stop rebuilding a whole inbound — heal
its settings, project every client — for a single client edit.
*/
type WholeSetUserProvisioner interface {
	UserProvisioner
	ProvisionsWholeUserSet()
}

/*
CredentialDeclarer names the credential fields one kind's clients carry, so the
client form renders from what a core declares instead of from its own protocol
branches. Names come from the closed vocabulary in credentials.go.

It is per-kind and not part of Descriptor because Descriptor is core-grained:
Xray is one core answering ten kinds, and vless, wireguard and shadowsocks need
different fields. Returning nil for a kind declares nothing, and the form keeps
the fields it has always shown, so an unknown inbound stays editable.
*/
type CredentialDeclarer interface {
	ClientCredentials(kind Kind) []string

	/*
		MintClientCredentials returns fresh values for the credentials a client
		is missing — or holds in a form the kind cannot serve, which is why the
		current values are passed in: a shadowsocks key of the wrong size for the
		inbound's method is replaced, not kept. Keys are vocabulary names; only
		declared names may come back, and minting over its own output returns
		nothing. Settings may be empty — mint what can be minted without it.
	*/
	MintClientCredentials(kind Kind, settings string, have map[string]string) (map[string]string, error)

	/*
		ValidateClient refuses a client this kind cannot serve, with the reason
		as the operator reads it. Required-ness lives here rather than in a
		declaration: what "required" means is protocol knowledge — shadowsocks
		needs the email that identifies the client in the config, wireguard a
		public key no panel can invent — and only the core can word the refusal.
	*/
	ValidateClient(kind Kind, settings, email string, have map[string]string) error

	// ClientIdentity names the field that identifies a client of this kind
	// inside a rendered config: a vocabulary name, or "email".
	ClientIdentity(kind Kind) string
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

// Selector is the kind of kernel key a core's users carry. A closed vocabulary,
// like credentials.go's: an unknown selector reads as "cannot shape", not as "fine".
type Selector string

const (
	// SelectorNone is the honest answer for every L7 proxy: its users share one
	// process and one socket set, so no kernel hook can tell them apart.
	SelectorNone Selector = ""
	// SelectorInnerIP is a post-decap host prefix on the core's own device.
	SelectorInnerIP Selector = "innerIP"
	// SelectorFwmark is a mark a core stamps on its own sockets. Declared and
	// unimplemented: nothing in this tree stamps one yet.
	SelectorFwmark Selector = "fwmark"
)

// SubjectKey is one user's kernel identity on one device: Prefixes or Mark,
// never both.
type SubjectKey struct {
	// Prefixes are EVERY host prefix this user answers to, one per family: a v4-only
	// key leaves that user's v6 flows unshaped. Each must hold a single address.
	Prefixes []netip.Prefix
	Mark     uint32
}

// ShapingTarget is one instance's shapeable surface as it stands right now.
type ShapingTarget struct {
	// Device is the interface this instance's decrypted traffic crosses, and it MUST
	// be named from one of the panel's own device namespaces (see shaping.Owns): the
	// plane installs nothing on an interface it cannot prove it owns. Empty means the
	// core is not hosting it now — shaping goes quiet and retries, it does not fail.
	Device   string
	Selector Selector
	// Keys maps email to kernel identity. An email absent from it is a client this
	// core cannot distinguish; it is left unshaped rather than shaped as a guess.
	Keys map[string]SubjectKey
}

/*
ShapingHost declares that this core puts a user's traffic on a kernel device
under an identity the kernel can match, so the panel can attach a limit to it.

It is an enforcement PRIMITIVE and never a policy: a core answers "a@x is
10.8.0.4 on pwg7" and never learns what limit that user has, what tier they are
in, or that tiers exist. A core whose users share one socket set answers
SelectorNone, and that is a correct answer rather than a gap.
*/
type ShapingHost interface {
	// ShapingSelector is per-kind and static, so a form can gate a field before any
	// instance exists. Per-kind for CredentialDeclarer's reason: one core, ten kinds.
	ShapingSelector(kind Kind) Selector

	// ShapingTargets is per-instance and dynamic, because the device is per-instance
	// and can be absent at any moment by design.
	ShapingTargets(ctx context.Context, inst Instance) (ShapingTarget, error)
}

// Session is one client's live connection as its core can see it.
type Session struct {
	Email string
	// Source is where the client connected FROM: for an L7 core its own address, for
	// an L3 core the peer's outer endpoint — the one that roams when a key is shared.
	Source netip.Addr
	// Local is what the client answers to INSIDE the tunnel, one per family. Empty is
	// honest for an L7 core; a routed subnet behind a peer is somebody else's network.
	Local []netip.Addr
	// LastSeenUnixMilli is load-bearing. A core refreshes it only on new activity, so
	// a frozen value is a dead connection, not a reconnect. Zero means it cannot say.
	LastSeenUnixMilli int64
}

/*
SessionReporter names each live connection together with the source it came from.

Separate from OnlineReporter because the two answer different questions and not
every core can answer this one: mtg reports a secret in use and no address at
all, and widening OnlineReporter would force it to invent one.

A snapshot of now: no history, no closed sessions, no aggregation. Deduplication,
recency and the cap itself are the panel's rules and never travel here.
*/
type SessionReporter interface {
	Sessions(ctx context.Context) ([]Session, error)
}

/*
CounterLossDeclarer answers whether removing a user destroys the byte counters
the panel bills from, so a soft product rule is never enforced by losing money.

Measured for wgkernel: a peer remove ZEROES its counters while an allowed-IP,
keepalive or preshared-key edit preserves them. The panel cannot see that from
outside, and it must not be guessed from some other property — "can this core
give its users a kernel identity" is a different question that happens to have
the same answer for the one core that does both today.

Per-kind for CredentialDeclarer's reason: Descriptor is core-grained and one core
answers ten kinds. Not implementing it means removal costs nothing, which is the
right default: every daemon that keeps its counters outside the user object.
*/
type CounterLossDeclarer interface {
	RemovalLosesCounters(kind Kind) bool
}

// QuotaEnforcer pushes a byte budget into the daemon so overshoot is bounded by
// the daemon rather than by the panel's polling interval.
type QuotaEnforcer interface {
	ResetQuota(ctx context.Context, email string) error
}

/*
VersionManager lets the panel see and change which build of a core's daemon is
installed, without knowing where that daemon comes from.

Today this exists once, for Xray, hardcoded in the web layer — GitHub release
listing, architecture mapping, download, unpack, restart. mtg has none of it and
core #3 would need a third copy. That is the per-core cost this contract exists
to remove: a core knows its own release channel, and the panel only asks.

Installed() answers even when the binary is missing or unreadable, because "not
installed" is a normal state the UI has to render. Available() is allowed to be
slow and to fail — it usually crosses the network — so callers cache it and fall
back to showing only what is installed.

Install replaces the binary on disk. It does NOT restart the daemon: that is
Supervisor's job, and keeping them separate is what lets the panel stage an
upgrade and apply it when the traffic allows.
*/
type VersionManager interface {
	Installed(ctx context.Context) (string, error)
	Available(ctx context.Context) ([]string, error)
	Install(ctx context.Context, version string) error
}

// LinkRenderer produces what an end user needs to connect. Kind distinguishes a
// URI ("link") from a config file ("file") such as .ovpn or wg0.conf. host is
// the endpoint address the CALLER resolved — which hostname reaches this panel
// is delivery policy (public host settings, node addresses, the request's own
// Host), and a core must not learn it.
type LinkRenderer interface {
	RenderClient(inst Instance, user User, host string) (Share, error)
}

// Share is one deliverable client config.
type Share struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename,omitempty"`
	Body     string `json:"body"`
}

/*
IngressSelector is how a core's traffic is selected for routing, per kind and
static so a form can gate a field before any instance exists.

A closed vocabulary like Selector's: an unknown value reads as "cannot route",
which is the fail-closed answer. An L7 proxy is its own router and answers
IngressInternal; a core whose decrypted traffic crosses a kernel interface
answers IngressDevice, and the panel routes it by that interface.
*/
type IngressSelector string

const (
	IngressNone     IngressSelector = ""
	IngressInternal IngressSelector = "internal"
	IngressDevice   IngressSelector = "device"
)

// IngressHandle is one instance's routable surface as it stands right now.
type IngressHandle struct {
	// Device is the interface the decrypted traffic crosses. Empty is not a failure:
	// the core is not hosting it now, so routing goes quiet and retries.
	Device string
	// Tag is the inbound tag an internal ingress already answers to. Empty with
	// BlockedKey set means the daemon exposes no tag today.
	Tag string
	// BlockedKey is an i18n key naming why this instance cannot be routed now. It
	// is a key rather than a sentence because the panel ships two locales.
	BlockedKey string
}

// RoutableIngress declares that a rule may name this core's inbounds as a source.
// Without it a core's traffic is not routable and the editor says so.
type RoutableIngress interface {
	IngressSelector(kind Kind) IngressSelector
	IngressHandle(ctx context.Context, inst Instance) (IngressHandle, error)
}

// ExitHandleKind is what an exit offers the router. Closed vocabulary as above.
type ExitHandleKind string

const (
	ExitNone         ExitHandleKind = ""
	ExitDevice       ExitHandleKind = "device"
	ExitSocksPort    ExitHandleKind = "socksPort"
	ExitXrayOutbound ExitHandleKind = "xrayOutbound"
)

/*
SourceOwner says who rewrites the source address once traffic leaves an exit.

Load-bearing rather than descriptive: a kernel forward keeps the ingress
client's inner source, which every upstream that is not a peer drops. A daemon
that does not NAT its own tunnel needs the panel to, and the panel cannot yet —
egress.Plane has no netfilter object — so SourceOwnerPanel is a refusal.
*/
type SourceOwner string

const (
	SourceOwnerDaemon SourceOwner = "daemon"
	SourceOwnerPanel  SourceOwner = "panel"
)

// ExitHandle is one uplink as it stands right now; exactly one handle is set.
type ExitHandle struct {
	Device    string
	SocksPort int
	XrayTag   string
	Source    SourceOwner
}

/*
Exit is one uplink instance.

Separate from Instance because an exit has no inbound row, no users and no
per-user accounting — the three things Instance exists to carry. Settings is
opaque here, exactly as Instance.Settings is.
*/
type Exit struct {
	ID       int
	Kind     Kind
	Enable   bool
	Settings string
}

// RoutableEgress declares that this core can terminate traffic somebody else
// routed to it, which is the other half of any-core-in to any-core-out.
type RoutableEgress interface {
	ExitKinds() []Kind
	ExitHandleKind(kind Kind) ExitHandleKind
	ExitHandle(ctx context.Context, exit Exit) (ExitHandle, error)
}
