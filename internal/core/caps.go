package core

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"time"
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

/*
HealthReporter answers "how is this daemon" from the daemon's side rather than
from a flag the panel set: the supervisor's state, and above it whether the
daemon answers on its own control surface — the readiness §7 asks for, since a
process that holds a PID and binds nothing is running and useless.

Ready is the one question the panel can put to every core alike: does the
control surface answer. It does not prove each listener bound (a sing-box whose
TUIC port failed to open still answers stats), so Detail carries what the daemon
said, and a core that knows more says it there.

A core with one process fills the top level and leaves Instances nil; a core
with one daemon per inbound fills Instances and leaves the top level zero, and
Overall derives it — in one place, so the page, the event pass and the
conformance suite agree on what degraded means.
*/
type HealthState string

const (
	HealthRunning  HealthState = "running"
	HealthDegraded HealthState = "degraded"
	HealthStopped  HealthState = "stopped"
	HealthFailed   HealthState = "failed"
	// HealthIdle is a per-inbound core with nothing to run: not a fault.
	HealthIdle HealthState = "idle"
)

// InstanceHealth is one daemon's state. SinceUnixMilli is when this life of it
// began; Restarts counts the ones the supervisor made on its own.
type InstanceHealth struct {
	InstanceID     int
	Tag            string
	State          HealthState
	Ready          bool
	Detail         string
	PID            int
	Restarts       uint32
	SinceUnixMilli int64
}

type Health struct {
	State          HealthState
	Ready          bool
	Detail         string
	PID            int
	Restarts       uint32
	SinceUnixMilli int64
	Instances      []InstanceHealth
}

// Overall is the one derivation of a core's state from its instances'.
func (h Health) Overall() (HealthState, bool) {
	if len(h.Instances) == 0 {
		if h.State == "" {
			return HealthIdle, true
		}
		return h.State, h.Ready
	}
	running, failed, ready := 0, 0, 0
	for _, i := range h.Instances {
		switch i.State {
		case HealthRunning:
			running++
			if i.Ready {
				ready++
			}
		case HealthFailed:
			failed++
		}
	}
	switch {
	case running == len(h.Instances):
		return HealthRunning, ready == running
	case running == 0 && failed > 0:
		return HealthFailed, false
	case running == 0:
		return HealthStopped, false
	default:
		return HealthDegraded, false
	}
}

type HealthReporter interface {
	Health(ctx context.Context) (Health, error)
}

// LogTailer returns the last n lines the daemon wrote: for a unit that died at
// once its stderr is the only place the reason is. instanceID 0 = one process.
type LogTailer interface {
	TailLog(ctx context.Context, instanceID, n int) ([]string, error)
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
InstanceMinter fills the secrets an inbound needs that no operator can type
and no file may hold: a server certificate and key, a tls-crypt-v2 server key.
Called on add and on every update; previous is what the row held before the
edit (empty on add). The core carries its minted fields forward from previous
when the new settings lack them, then mints only what is still missing, so a
save that arrives without them (an API client sending the fields it knows, a
form rebuilt from defaults) never rotates what clients already pin.
*/
type InstanceMinter interface {
	MintInstance(kind Kind, settings, previous string) (string, error)
}

// DeviceNamespace is the name space a core's kernel interfaces are drawn from,
// so the shaping plane can own trees on them. Prefix is letters only (shaping.Register).
type DeviceNamespace struct {
	Prefix string
	// PerSession: devices are <Prefix><inbound id>-<n>, one per live session, all
	// shaped as that inbound's — OpenConnect's shape, and PPP's.
	PerSession bool
}

// DeviceNamespacer names the device namespace a core's kernel interfaces use.
type DeviceNamespacer interface {
	DeviceNamespace() DeviceNamespace
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

// SourceCredentialResult contains only missing source values. NodeDerived fields
// still need node-side fulfillment and never establish forwarding readiness.
type SourceCredentialResult struct {
	Values      map[string]string
	NodeDerived []string
}

// SourceCredentialClaim identifies an authentication lookup key without exposing
// it. Persistent ownership and safe retirement belong to the source transaction.
type SourceCredentialClaim struct {
	Scheme string   `json:"-"`
	Digest [32]byte `json:"-"`
}

// SourceCredentialDeclarer is pure: no host files, daemon calls or supplied-value
// replacement. Validation covers source credentials, not node readiness.
type SourceCredentialDeclarer interface {
	SourceClientCredentials(kind Kind) []string
	MintSourceClientCredentials(kind Kind, settings string, have map[string]string) (SourceCredentialResult, error)
	ValidateSourceClientCredentials(kind Kind, settings, email string, have map[string]string) error
	SourceClientCredentialClaims(kind Kind, settings, email string, have map[string]string) ([]SourceCredentialClaim, error)
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

// ShapingTarget is one device of an instance's shapeable surface, as it stands
// right now.
type ShapingTarget struct {
	/* Device is the interface this target's decrypted traffic crosses, and it MUST
	   be named from one of the panel's own device namespaces (see shaping.Owns): the
	   plane installs nothing on an interface it cannot prove it owns. A core not
	   hosting the instance answers with no target at all, never one with no device. */
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

	// ShapingTargets is per-instance and dynamic: one target per device the
	// instance's traffic crosses right now, and none while the core is not hosting it.
	ShapingTargets(ctx context.Context, inst Instance) ([]ShapingTarget, error)
}

// RateLimit is what one client may push right now, per direction, in bits per
// second from the client's side; 0 is unlimited. The mirror of policy.Limits.
type RateLimit struct{ UpBps, DownBps int64 }

/*
RateEnforcer applies a per-user rate INSIDE the core, for a core whose users
share one socket set and so can never be shaped on a device (SelectorNone).

The complement of ShapingHost, not an alternative to it: a core answers one or
the other. Like ShapingHost it is an enforcement primitive and never a policy —
the panel evaluates the ladder and hands over the number. An email absent from
limits is unlimited, so a cleared plan is one more call, not a special case.
*/
type RateEnforcer interface {
	// EnforcesRates is per-kind and static, so a form can gate its speed fields.
	EnforcesRates(kind Kind) bool
	EnforceRates(ctx context.Context, inst Instance, limits map[string]RateLimit) error
	// EnforcedRates reads back what the core holds each client to right now, from
	// the objects that throttle: a limit pushed and lost must not read as enforced.
	EnforcedRates(ctx context.Context, inst Instance) (map[string]RateLimit, error)
}

/*
DeferredApplier declares that this core applies a user or instance edit on its
next reconcile pass rather than at once: sing-box has no hot user add, so an
edit is a config rewrite and a reload, coalesced. Such a core's AddUser,
RemoveUser and ApplyInstance record the wish and return nil; ApplyDelay is what
the operator is told at save time — "takes effect within this long".
*/
type DeferredApplier interface {
	ApplyDelay(kind Kind) time.Duration
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
	// StartedUnixMilli is when the connection opened, for a core that knows. Zero
	// means it cannot say, and the panel counts from when it first saw the address.
	StartedUnixMilli int64
}

/*
SessionReporter names each live connection together with the source it came from.

Separate from OnlineReporter because the two answer different questions and not
every core can answer this one: a core that reports a user in use and no address
(mtg, before it tracked addresses) would have had to invent one.

A snapshot of now: no history, no closed sessions, no aggregation. Deduplication,
recency and the cap itself are the panel's rules and never travel here.
*/
type SessionReporter interface {
	Sessions(ctx context.Context) ([]Session, error)
}

// HeldSources is the distinct addresses a client's sessions come from, sorted:
// what a core hands the panel when asking whether to admit one more.
func HeldSources(sessions []Session, email string) []netip.Addr {
	seen := map[netip.Addr]struct{}{}
	var out []netip.Addr
	for _, s := range sessions {
		if s.Email != email || !s.Source.IsValid() {
			continue
		}
		if _, dup := seen[s.Source]; dup {
			continue
		}
		seen[s.Source] = struct{}{}
		out = append(out, s.Source)
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out
}

/*
SessionCutter ends a client's live sessions from one source address: how a device
cap is enforced on a core that admits at the transport rather than at a login the
panel can refuse. By (email, source) — the unit the cap is decided in — never by
email alone, or the devices inside the cap would fall with the one over it. It
answers how many it ended; a source with no session left is nothing to end, not
an error. A core that cannot end a session (Xray; a kernel WireGuard peer, whose
removal also zeroes its counters) leaves this out, and the panel falls back to
the host firewall.
*/
type SessionCutter interface {
	CutSessions(ctx context.Context, email string, source netip.Addr) (int, error)
}

/*
LoginAdmitter is a core that asks the panel before admitting a login, so a
device cap is refused at the door rather than cut after the fact. Answered per
kind, and DERIVED by the core from whether it was wired with an AdmitFunc at all:
a core built without one admits everyone and says so. That is what keeps the
answer honest — a declaration could be made and the callback never called, and
nothing but a login would show it.
*/
type LoginAdmitter interface {
	AdmitsAtLogin(kind Kind) bool
}

/*
Admission is one login a core asks the panel about. Held is every source this
core itself currently has sessions from for the client, so the panel adds only
what it knows from elsewhere; LimitIP is the cap the core's own user carries.
Whether to admit is the panel's rule, never the core's.
*/
type Admission struct {
	Kind       Kind
	InstanceID int
	Email      string
	Source     netip.Addr
	Held       []netip.Addr
	LimitIP    int
}

// AdmitFunc answers an Admission. A non-nil error refuses the login, and its
// text is the reason the client and the panel's auth-failure event both see.
type AdmitFunc func(ctx context.Context, a Admission) error

// ErrDeviceLimit is the refusal a full cap produces; the panel wraps it with the
// count so a core can match it and a client can read it.
var ErrDeviceLimit = errors.New("device limit reached")

/*
CounterLossDeclarer answers whether removing a user destroys the byte counters
the panel bills from. It is why no product rule is ever enforced by removing and
re-adding a user: a cap is enforced by SessionCutter or the firewall, and a core
that loses its counters on removal is exactly the one that must not be "bounced".

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

// VersionReporter names the daemon build on disk. ErrNotInstalled is a normal
// answer the UI renders, never a fault: a core the admin never used has none.
type VersionReporter interface {
	Installed(ctx context.Context) (string, error)
}

/*
VersionManager lets the panel change which build of a core's daemon is
installed, without knowing where that daemon comes from.

This existed once, for Xray, hardcoded in the web layer — GitHub release
listing, architecture mapping, download, unpack, restart. mtg had none of it and
every further core would have needed its own copy. That is the per-core cost
this contract exists to remove: a core knows its own release channel, and the
panel only asks.

Available() is allowed to be slow and to fail — it usually crosses the network —
so callers cache it and fall back to showing only what is installed. Install
replaces the binary on disk and refuses a version Available did not list. It
does NOT restart the daemon: that is Supervisor's job, and keeping them separate
is what lets the panel stage an upgrade and apply it when the traffic allows.

Reporting and managing are separate: system and embedded cores report versions,
while downloadable engines validate compatible releases before installation.
*/
type VersionManager interface {
	VersionReporter
	Available(ctx context.Context) ([]string, error)
	Install(ctx context.Context, version string) error
}

// ErrNotInstalled is Installed's answer for a binary that is not there.
var ErrNotInstalled = errors.New("core: daemon not installed")

// ErrUnknownVersion refuses an Install of a version Available did not list, so
// the binary directory only ever receives what the core's own channel publishes.
var ErrUnknownVersion = errors.New("core: version not in the release list")

// LinkRenderer produces what an end user needs to connect. Kind distinguishes a
// URI ("link") from a config file ("file") such as .ovpn or wg0.conf. host is
// the endpoint address the CALLER resolved — which hostname reaches this panel
// is delivery policy (public host settings, node addresses, the request's own
// Host), and a core must not learn it.
type LinkRenderer interface {
	RenderClient(inst Instance, user User, host string) (Share, error)
}

// FileShareDeclarer says whether a kind's share is a file the subscription
// serves by URL, or only a link; a LinkRenderer without it is taken as a file.
type FileShareDeclarer interface {
	SharesFile(kind Kind) bool
}

// PanelOutliver says the core's daemons keep serving through a panel restart
// (a supervisor holds them), so shutdown must not stop them; StopAll still does.
type PanelOutliver interface {
	OutlivesPanel() bool
}

// Share is one deliverable client config.
type Share struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename,omitempty"`
	Body     string `json:"body"`
	// URI is the link a subscription list can carry for this share when the
	// client apps have one (ssh://); empty for a share that is only a file.
	URI string `json:"uri,omitempty"`
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

		The plane grew Src, so this is mechanized: the pool selects into the same
		table, behind the same front, as a device does. One thing it needs that a
		device does not is an exception for the pool itself -- the host holds an
		address inside it and its own replies to clients would be caught -- which
		the egress manager plans with the rule.
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
  - IngressSubnet: the same front. A source-prefix rule sends that traffic into
    the same table a device rule does, so the sniffer has the same inbound to
    attach to; only the selector on the way in is different.

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
	case IngressInternal, IngressDevice, IngressSubnet:
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
	// Sources are the address prefixes this instance's traffic carries, for a
	// core whose sessions each get a transient device no rule could name. Empty
	// is not a failure, for the same reason Device empty is not.
	Sources []netip.Prefix
	// BlockedKey is an i18n key naming why this instance cannot be routed now. It
	// is a key rather than a sentence because the panel ships two locales.
	BlockedKey string
}

/*
Fronted reports whether this handle names traffic that reaches the panel across
the kernel rather than through an Xray tag, so it needs a synthesised front.

One predicate, asked everywhere, because the answer used to be spelled
`Device != ""` in four places and a second kernel shape would have had to find
all four.
*/
func (h IngressHandle) Fronted() bool { return h.Device != "" || len(h.Sources) > 0 }

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
	// TCPOnly is an exit that carries no UDP (an SSH forward); the compiler
	// narrows rules to TCP rather than let routed UDP vanish into it.
	TCPOnly bool
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

// ExitSocksPortBase is where a core-owned SOCKS exit listens: base + row id,
// loopback only. Fixed so a panel restart compiles the same Xray config.
const ExitSocksPortBase = 41000

// ExitSocksPortFor is the loopback port an ExitSocksPort exit serves on.
func ExitSocksPortFor(exitID int) int { return ExitSocksPortBase + exitID }

// IsExitSocksPort reports whether port sits in the band the exits own.
func IsExitSocksPort(port int) bool { return port > ExitSocksPortBase && port <= ExitSocksPortBase+999 }

/*
ExitSupervisor runs the exits a core owns: the SSH client behind a SOCKS port,
one day an OpenVPN client. Supervisor's mirror for the outbound half — the
panel hands over the enabled rows of the core's kinds and the core converges,
so ExitHandle stays pure and a compile or a preview never dials anything.
*/
type ExitSupervisor interface {
	ValidateExit(exit Exit) error
	ReconcileExits(ctx context.Context, desired []Exit) error
}
