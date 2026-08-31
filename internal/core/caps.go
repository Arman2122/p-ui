package core

import (
	"context"
	"net/netip"
	"slices"
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

	/*
		ProvisionsUsers scopes the two above per kind, because a core is one
		process serving many: Xray's http, mixed, tunnel and tun inbounds have no
		user list at all, and it answers "proxy is not a UserManager" for them.

		The panel used to learn that from the error string of a gRPC call it
		should never have made. False means the kind holds no users, and every
		user op on it is refused locally with ErrKindHasNoUsers.

		Not derivable from CredentialDeclarer: nil there means "declares nothing,
		keep the form as it was" -- an answer about the FORM, not the protocol --
		so reading it as "has no users" would build a refusal on a documented
		false premise. That the two agree today is a coincidence coretest pins in
		the one direction that is genuinely a contradiction. F314.
	*/
	ProvisionsUsers(kind Kind) bool
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
InstanceValidator refuses an inbound this kind cannot serve, with the reason as
the operator reads it.

The sibling of CredentialDeclarer.ValidateClient, one level up: that one answers
"can this kind serve this client", this one "can it serve this inbound at all".
Both exist for the same reason — what makes a configuration invalid is protocol
knowledge, and only the core that owns the kind can word the refusal.

Without it, an invalid setting is discovered when the device is created. For a
kernel core that means the module answers EINVAL and a ratelimited dmesg line
the operator will never see, so the panel saves the row, reports success, and
the inbound simply does not work.

Optional. A kind whose core does not implement it is saved unvalidated, exactly
as every kind was before.
*/
type InstanceValidator interface {
	ValidateInstance(kind Kind, settings string) error
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

/*
Transports is the L4 footprint one inbound occupies on its port.

Both false is not a valid answer: a core that returned it would silently disable
conflict detection for its own kind, so the port-conflict check reads an empty
answer as TCP. None is how a kind says it genuinely binds nothing — an Xray TUN
device is a kernel interface, not a listener — and it is a separate field for
exactly that reason. Negligence and a deliberate answer must not look alike.

None is exclusive: a kind that binds nothing cannot also bind TCP or UDP, and
coretest fails a core that answers both. F319.
*/
type Transports struct {
	TCP  bool
	UDP  bool
	None bool
}

/*
TransportDeclarer answers what an inbound actually binds, so port-conflict
detection stops deriving it from the protocol string.

It takes the settings because the answer is not static per kind: a shadowsocks
inbound carries its L4 choice in a CSV field that overrides everything, a mixed
inbound adds UDP with a boolean, and Xray's kcp and quic transports move a
stream from TCP to UDP. Only the core that reads those blobs knows which of its
kinds is on which protocol, and a wrong guess either lets two inbounds bind one
port or refuses a pair that never collided.
*/
type TransportDeclarer interface {
	Transports(kind Kind, settings, streamSettings string) Transports
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
	/*
		IngressSubnet is a core whose sessions each get their own transient kernel
		device -- the PPP family and OpenConnect -- so no device name is stable
		enough to route by. The stable handle is the instance's address pool, and
		the kernel rule that selects it is `from <prefix>` rather than `iif <dev>`.

		Declared here so the contract can SAY it before the egress plane can do
		it. Nothing mechanizes it yet, which is exactly why the criteria guard
		below refuses a payload criterion for it: a core shipping this selector is
		routable by address as soon as the plane grows Src, and its rules cannot
		silently claim more in the meantime.
	*/
	IngressSubnet IngressSelector = "subnet"
)

/*
InspectionMechanism is how the panel reads a domain out of one ingress shape.

A criterion is only real if something can produce it. Xray matches a domain from
the sniffer, and the sniffer runs on an Xray INBOUND -- so for every ingress
shape the question is: which inbound would carry the sniffing block?

  - IngressInternal: the inbound the core already answers to, or the loopback
    bridge the panel builds for it.
  - IngressDevice: the xray-tun front the panel synthesises for that device.
  - IngressSubnet: nothing today. A per-session device has no front and no
    bridge, so there is no inbound to attach a sniffer to.

InspectableSelector answers that question once, and coretest and internal/arch
both refuse a core declaring a payload criterion for a shape it answers false
for. That is the whole guard: a core cannot claim `domain` for an ingress the
panel has no way to read one from, so a rule that saves is a rule that can
match. The inverse of that promise is the bug F060 was. Extend this the day a
new shape gains a mechanism -- and the guard will name any core that was waiting
on it.
*/
func InspectableSelector(selector IngressSelector) bool {
	switch selector {
	case IngressInternal, IngressDevice:
		return true
	default:
		return false
	}
}

/*
PayloadCriteria are the criteria that can only be answered by reading the
connection's payload, rather than from the packet's own addresses.

Separated from CriteriaByAddress because they are what the mechanism question
applies to: an address is in every packet, a name is not.
*/
func PayloadCriteria() []Criterion {
	return []Criterion{CriterionDomain, CriterionProtocol, CriterionAttrs}
}

// NeedsInspection reports whether this criterion can only be answered by
// reading the payload.
func (c Criterion) NeedsInspection() bool {
	return slices.Contains(PayloadCriteria(), c)
}

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

/*
Criterion is one thing a routing rule can match traffic on. A closed vocabulary,
like every other in this file: a name outside it is offered by no editor.
*/
type Criterion string

const (
	CriterionIP         Criterion = "ip"
	CriterionPort       Criterion = "port"
	CriterionSourcePort Criterion = "sourcePort"
	CriterionNetwork    Criterion = "network"
	CriterionSource     Criterion = "source"
	CriterionUser       Criterion = "user"
	CriterionDomain     Criterion = "domain"
	CriterionProtocol   Criterion = "protocol"
	CriterionAttrs      Criterion = "attrs"
)

/*
CriteriaByAddress is what every routable ingress can match on: the packet's own
addresses and ports. Nothing here needs an identity or a payload, so it holds
for a device ingress and an internal one alike.
*/
func CriteriaByAddress() []Criterion {
	return []Criterion{CriterionIP, CriterionPort, CriterionSourcePort, CriterionNetwork, CriterionSource}
}

/*
RoutableIngress declares that a rule may name this core's inbounds as a source.
Without it a core's traffic is not routable and the editor says so.

IngressCriteria is part of the declaration because the selector does not imply
it. Xray and mtproto both answer IngressInternal, and the service layer inferred
the vocabulary from that — so an mtproto subject was offered `user`, `domain`,
`protocol` and `attrs`, none of which can match: its plaintext reaches Xray over
a loopback SOCKS bridge that carries no user identity and does no sniffing. A
rule written on any of them saved and never matched, which is the whole class of
bug the routing editor exists to remove. F060.
*/
type RoutableIngress interface {
	IngressSelector(kind Kind) IngressSelector
	IngressHandle(ctx context.Context, inst Instance) (IngressHandle, error)
	IngressCriteria(kind Kind) []Criterion
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
