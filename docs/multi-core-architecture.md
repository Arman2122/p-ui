# Penhoon UI — Multi-Core Architecture

> Design document for turning Penhoon UI from an Xray-core panel into a multi-core VPN
> panel (OpenVPN, IKEv2/IPsec, L2TP, OpenConnect, WireGuard/AmneziaWG, SSTP, SSH, …)
> with cross-protocol egress and one unified per-user quota.
>
> Status: **in progress** — P-1 through P5 are implemented; see §12 for what each phase
> landed, what it deliberately did not, and what is still a proposal. Companion to
> `docs/architecture.md`, which describes the system as it is today.
>
> Every measurement in §2 was taken against the working tree at `02bc8dcc`, before any phase
> landed, and is kept as the baseline the refactor is judged against. Everything else was
> re-verified against `e2478640`. §11 is the one section written as a **target**; it says so.

---

## 1. Thesis

The panel is much closer to multi-core than it looks, because **three of the four hard
problems already have working single-instance solutions in the tree**. They were built for
MTProto and never generalised. The refactor is mostly *promotion of existing patterns to
contracts*, not invention.

What already works:

| Problem | Existing proof | Where |
|---|---|---|
| A non-Xray core feeding the shared quota | `mtproto_job` scrapes mtg's HTTP `/stats`, converts to the same delta type Xray produces, and calls the same `AddTraffic` | `internal/web/job/mtproto_job.go:44-77` |
| One quota shared across inbounds of different cores | `client_traffics` is **email-keyed and `UNIQUE`** — one row per client across *all* inbounds | `internal/xray/client_traffic.go:9`, `service/inbound_traffic.go:110-122` |
| A non-Xray core egressing through an Xray outbound | `injectMtprotoEgress` injects a loopback SOCKS inbound tagged with the sidecar's tag, prepends a routing rule, points the sidecar at `socks5://127.0.0.1:P` | `service/xray.go:583-643` |
| Turning a monotonic counter into an idempotent delta | `StatsLastValues` + baseline pass + backwards-counter re-baseline + stale-key pruning | `internal/xray/api.go:736-780` |
| Declarative reconcile of a sidecar core | `Reconcile(desired)` → `structuralFingerprint` vs `secretsFingerprint` → `ensureActionFor` picks *noop / hot-apply / restart* | `internal/mtproto/manager.go:86-366` |

What it cost to build that *without* an abstraction: **MTProto was added by special-casing
~30 files**, including 7 branches in `runtime/local.go`, a dedicated cron job, and a
`MTProtoDomain` field on the shared `InboundOption`. That is the per-core price today, and
it is superlinear — each core also has to be considered by every subsequent core's author.

**The design goal is therefore not "make OpenVPN work". It is "make core #11 cost 5 shared
files and ~36 lines".** Everything below is subordinate to that.

---

## 2. Measured baseline

Protocol identity is a bare `type Protocol string` (`model/model.go:17`) with ten
constants and **zero methods**. Behaviour is re-derived from that string by hand:

Counts below are produced by the detector in `internal/arch/dispatch_ratchet_test.go`, which
is also what seeds the ratchet. They are the **baseline**; the ratchet only ever moves down
from here, and its current value lives in `dispatchTotal`, not in this table.

| Surface | Count |
|---|---|
| Non-test Go references to a `model.<Protocol>` constant | **107** across 19 files |
| Untyped string-literal comparisons against `.Protocol` | **2** (`client_inbound_apply.go:478`, `:860`) |
| **Total dispatch sites (the ratchet seed)** | **109** |
| — frozen by design (historical migrations, a core naming its own kind) | 8 |
| — migratable to the registry | **101** |
| Frontend non-test protocol comparisons | 41 (20 leaked into `src/pages/`, 12 in `OutboundFormModal.tsx`) |
| Independent third implementation of link generation | `docs/lib/xray/` (own CI, fires only on `docs/**`) |
| MTProto-specific guards in non-test Go | 26 |

Densest files: `service/inbound.go` (15), `service/xray.go` (10),
`service/client_inbound_apply.go` (10+2), `service/tgbot/tgbot_inbound.go` (8),
`runtime/local.go` (7), `sub/service.go` (7).

### 2.1 The abstraction has already failed once — twice, measurably

1. **The validator allow-list has rotted.** `model.go:64` carries
   `validate:"required,oneof=vmess vless trojan shadowsocks wireguard hysteria http mixed
   tunnel tun mtproto"` — **eleven** values. The `const` block at `:24-35` defines **ten**.
   There is no `TUN` constant anywhere. The tag accepts a protocol the codebase does not
   define. This is not a predicted failure at core #4; it already happened at core #10.

   **RESOLVED in P4.** The tag is now `validate:"required,protocol"`, and the rule is built
   from `cores.Kinds()` — the accepted set and the servable set are one list. Codegen reads
   the `const` block (this tool is `go run` on a possibly-non-Linux workstation, and a core
   is built from Linux-only pieces), which `TestProtocolSourcesAgree` pins to the registry.

2. **Capability rules are triplicated and one-sided.** "May this VLESS client carry
   `flow=xtls-rprx-vision`" is implemented three times in three shapes —
   `service/inbound_protocol.go:32` (raw JSON), `sub/service.go:768` (parsed maps),
   `frontend/src/lib/xray/protocol-capabilities.ts:52` (form object) — **253 lines for
   nine boolean rules**, with 164 lines of test that each certify one copy and stay green
   when another changes. Two rules (`canEnableTls`, `canEnableReality`) exist **only in
   TypeScript**: the REST API and the Telegram bot can create configurations the UI
   forbids, and nothing rejects them.

The comments in those files literally point at each other ("mirrors
`canEnableTlsFlow()` from the frontend"). **No test crosses the boundary.** Discipline has
already been tried and has already lost; only a mechanical guard changes this.

**PARTLY resolved — and P1's roadmap row overclaimed it.** The triplication is gone: one
table in `internal/core/capability.go`, generated into TypeScript, cross-checked by a golden
fixture. But the row said `tls` and `reality` were "now enforced server-side too", and they
are not. Repo-wide, the only non-test callers of `core.Can` evaluate `CapTLSFlow` (twice) and
`CapFallbacks` (once):

```
internal/sub/service.go:751                  CapTLSFlow
internal/web/service/inbound_protocol.go:30  CapTLSFlow
internal/web/service/inbound_protocol.go:39  CapFallbacks
```

`CapTLS`, `CapReality`, `CapStream` and `CapSniffing` are in the table and evaluated by the
frontend, and **by nothing on any write path**. So the original hole is still open: the REST
API and the Telegram bot can still create a configuration the UI forbids.

Closing it is a *behaviour* change, not a refactor — a stricter save path can reject inbounds
an admin already has, so it needs its own phase and a survey of existing rows first. It is not
free, which is exactly why the row should not have claimed it was already done.

### 2.2 The one coupling that blocks everything

`internal/database/model/model.go:13` imports `internal/xray`, used at `:59`
(`ClientStats []xray.ClientTraffic`), `:328` and `:367` (`GenXrayInboundConfig`).

Every future core therefore transitively imports the Xray core through the model layer, so
**no import fence is expressible at all** until this is broken. And `xray.ClientTraffic` is
not an Xray type — it is the email-keyed cross-core traffic row, sitting in the wrong
package.

Both fences are otherwise **clean today** (verified: nothing outside `internal/xray/`
imports `xtls/xray-core`; neither core package imports `internal/web`). That will never be
cheaper to freeze than it is now.

---

## 3. The core contract

### 3.1 Package layout — the fence is the compiler, not a linter

```
internal/core/                    # the contract. Importable by everyone.
    core.go                       #   Core, Kind, Descriptor
    caps.go                       #   the optional capability interfaces (17 today)
    bind.go                       #   Bound + Bind() — the ONLY place assertions live
    counter.go                    #   the ONE cumulative→delta engine
    traffic.go                    #   ClientTraffic (moved from internal/xray), TrafficDelta
    inbound.go                    #   InboundConfig (moved from internal/xray)
    registry.go                   #   Registry, Register, Cores()
    capability.go                 #   the ONE rule table: may this inbound do X
    credentials.go                #   the closed credential-name vocabulary (P5)
    coretest/                     #   RunAdapterSuite — the conformance suite
internal/cores/                   # wiring only
    cores.go                      #   one import + one Register line per core. ONE file.
    internal/xray/                # concrete cores — importable ONLY from internal/cores/...
    internal/mtproto/
    internal/wireguard/
```

The nested `internal/` is the enforcement. Per `cmd/go`, code below a directory named
`internal` is importable only from the tree rooted at that directory's parent — so
`internal/web/service` importing `internal/cores/internal/openvpn` is a **compile error**.
It cannot be `//nolint`-ed, cannot rot, and costs **zero configuration lines per core**.

> Rejected: a `depguard` deny-list naming each core. One line per core, silently incomplete
> forever after the first omission — precisely the marginal-cost problem being solved.

### 3.2 Three mandatory methods, seventeen optional capabilities

```go
// internal/core/core.go — as implemented
type Kind string

type Core interface {
    Describe() Descriptor   // ID, i18n title key, declared capabilities
    Kinds() []Kind          // one driver may serve several (accel-ppp: l2tp, pptp, sstp)
    Preflight(ctx context.Context) error // binary present? kernel module? version ok?
}
```

> This proposed `type Kind = model.Protocol`, to alias a defined type rather than `string`.
> It is impossible: `model` imports `internal/core` for `ClientTraffic`, so the alias is an
> import cycle. `Kind` is its own type and `internal/arch` pins it to the protocol constants
> three ways instead.

Everything else is an **optional** interface:

| Capability | Purpose | Implemented today by |
|---|---|---|
| `Supervisor` | `Reconcile(desired []Instance)`, `StopAll` | all three (mandatory in practice) |
| `InstanceApplier` | apply/drop one inbound without disturbing the rest | all three |
| `HotApplier` | classify a change so the caller can avoid a restart | all three |
| `UserProvisioner` | add/remove one user against a running instance | all three |
| `WholeSetUserProvisioner` | provisions by re-applying its whole user set — `Instance.Users` is the set as it now stands, a missing client is a revoked one | mtproto (mtg rewrites `[secrets]`) |
| `CredentialDeclarer` | declare, mint and validate a kind's client credentials; name the identity field (§3.2a) | all three |
| `TrafficSource` | `CollectTraffic() ([]TrafficDelta, error)` — deltas, each core normalising its own counter semantics | all three |
| `TagTrafficSource` | inbound/outbound totals, not per user (§12.3) | all three |
| `OnlineReporter` | who is connected right now | all three |
| `SessionReporter` | each live connection with its source address | xray, wgkernel — not mtg, which sees a secret in use and no address |
| `QuotaEnforcer` | a byte budget the daemon itself enforces (today: `ResetQuota`, §4.3) | mtproto |
| `CounterLossDeclarer` | does removing a user destroy the counters the panel bills from | wgkernel (a peer remove zeroes them — measured) |
| `ShapingHost` | a user's kernel identity on a device the panel owns, for rate limits | wgkernel |
| `RoutableIngress` | may a routing rule name this core's inbounds as a source (§3.2b) | all three |
| `RoutableEgress` | may this core terminate a route: exit kinds and handles (§3.2b) | wgkernel (wg-client uplinks) |
| `VersionManager` | install/list the daemon's own releases (§15) | no implementer yet |
| `LinkRenderer` | render the client's config; `host` resolved by the caller (§3.2a) | wgkernel (.conf) |

All seventeen exist in `caps.go`; `VersionManager` is now the only declared slot with no
implementer. An earlier revision here kept `RateLimiter` and an egress pair deliberately
**absent** "until a core needs them — adding one later costs a nil field". Both needs
arrived and the claim priced correctly: shaping added `ShapingHost` and routing added
`RoutableIngress`/`RoutableEgress` as nil fields on `Bound`, with no edit to any other
core. `coretest` refuses the hollow version of each — a core implementing `RoutableIngress`
whose every kind answers `IngressNone` "can route nothing and declares nothing"
(`bind.go:121`).

**Only `Reconcile` is genuinely mandatory.** A core that cannot reconcile desired state
cannot self-heal after a crash, so every panel restart becomes a correctness event.
`AddUser`/hot-apply are optimisations layered on top — which is exactly why the mtg sidecar
recovers for free today (`manager.go:366`).

`CollectTraffic` is **not** mandatory: SSH-VPN has no per-user byte counter worth the name,
and forcing it produces the `return nil` stub that `runtime/local.go:116` and `:125`
already demonstrate as a failure mode.
### 3.2a Credentials and links are protocol knowledge, and the engines host it

`CredentialDeclarer` grew from one method to four (`caps.go:75`). `ClientCredentials`
still names a kind's fields from `credentials.go`'s closed vocabulary. `MintClientCredentials`
returns fresh values for what a client is missing — or holds in a form the kind cannot
serve, which is why the current values are passed in: a shadowsocks key of the wrong size
for the inbound's method is replaced, not kept. `ValidateClient` refuses with the words the
operator reads — required-ness is protocol knowledge (shadowsocks needs the email that
identifies the client in the config, wireguard a public key no panel can invent), and only
the core can word the refusal, so `coretest` and the arch guard pin the *exact* strings.
`ClientIdentity` names the field that identifies a client inside a rendered config.

The knowledge itself lives in the ENGINES, once each: `internal/xray/shadowsocks.go` (SS2022
keys are raw AEAD keys with an exact byte size per method; legacy methods take any
passphrase) and `internal/mtproto/secrets.go` (a FakeTLS secret embeds the inbound's
fronting domain), so the core adapter and the panel's settings-healing pass read one
implementation. Migrating the service layer's client-credential switches onto these
methods took the dispatch ratchet from 125 to 101 (`dispatch_ratchet_test.go:66`).

`LinkRenderer` is implemented and consumed. The wgkernel core renders the `.conf` itself
(`cores/internal/wireguard/share.go`): a WireGuard client is configured by a FILE, and the
`wireguard://` URI the subscription emits is a lossy transport the apps rebuild a `.conf`
from, so the file is the true deliverable. The sub server serves it at
`GET :subid/file/:inboundId` (`sub/controller.go:288`) — the subscription token already
authenticates the client for links, so it authorises the file too: same secret, same
audience, one more representation. `RenderClient(inst, user, host)` takes `host` as an
argument the CALLER resolved: which hostname reaches this panel is delivery policy — public
host settings, node addresses, the request's own Host — and it never enters a core.

### 3.2b The routing pair and its closed vocabularies

`RoutableIngress` says a rule may name this core's inbounds as a source.
`IngressSelector(kind)` is static per kind, so a form can gate a field before any instance
exists; `IngressHandle` is per-instance and dynamic, because the surface can be absent at
any moment by design. The vocabulary is closed the way `credentials.go`'s is:
`IngressInternal` — an L7 proxy is its own router, so the handle is a tag (xray; mtproto
once its loopback bridge hands plaintext to Xray, and a `BlockedKey` i18n key names why
not when the bridge is off) — or `IngressDevice`, decrypted traffic crossing a kernel
interface the panel routes by (wgkernel). An unknown value reads as "cannot route", which
is the fail-closed answer.

`RoutableEgress` is the other half of any-core-in to any-core-out: `ExitKinds` feeds the
Outbounds page's picker, `ExitHandleKind` (closed: `device` / `socksPort` /
`xrayOutbound`) tells the router how to reach an exit, `ExitHandle` tells it where, right
now. The handle carries a `SourceOwner`, and that field is load-bearing rather than
descriptive: a kernel forward keeps the ingress client's inner source address, which every
upstream that is not a peer drops. A daemon that does not NAT its own tunnel needs the
panel to, and the panel cannot yet — `egress.Plane` has no netfilter object — so
`SourceOwnerPanel` is a refusal, not a plan (`caps.go:335`).

### 3.3 `Bound` — the step that actually removes the dispatch

Interface segregation *alone* just relocates `switch protocol` into scattered
`if h, ok := c.(HotApplier); ok` assertions. The fix is to probe **once**:

```go
// internal/core/bind.go — the ONLY file permitted to type-assert a capability.
type Bound struct {
    Core Core

    Supervise   Supervisor      // nil if unsupported
    Apply       InstanceApplier
    HotApply    HotApplier
    Users       UserProvisioner
    UserSet     WholeSetUserProvisioner
    Creds       CredentialDeclarer
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
```

Call sites read `if b.HotApply != nil`. Enforced by `TestCapabilityAssertionsOnlyInBind`,
an AST walk asserting no `TypeAssertExpr` naming a capability interface appears outside
`bind.go`. Seeded at zero violations, so it can never regress.

The shape is Grafana's `backend.ServeOpts`; the "tiny mandatory interface + optional
interfaces probed by assertion" split is `database/sql`'s (`Queryer`, `ExecerContext`,
`SessionResetter`).

### 3.4 Registration is explicit, never `init()`

```go
// internal/cores/cores.go — the whole "which cores exist" answer, greppable, +2 lines/core
func Register(reg *core.Registry, deps Deps) error {
    if err := reg.Register(mtproto.New()); err != nil {
        return err
    }
    if err := reg.Register(wireguard.New()); err != nil {
        return err
    }
    return reg.Register(xray.New(xray.Deps{BaseConfig: deps.XrayBaseConfig}))
}
```

A duplicate kind is **refused at boot** (`database/sql`'s discipline, not sing-box's
silent overwrite). `Deps` carries the panel-side facts a core cannot derive for itself —
today the Xray base config — passed in rather than reached for, so a core still cannot
import the web layer.

The decisive argument is repo-specific: `make gen-check` is the **first step** of
`make verify` (`Makefile:30`, `:84`) and fails on a dirty tree. With `init()`-based
registration the registered set depends on the transitive import graph of whichever `main`
the generator links — so a dropped blank import **silently shrinks the generated frontend
schema** and `gen-check` still passes. Secondary: `make test-go` runs `-shuffle=on`
(`Makefile:49`), and package-global mutable registration turns order dependence into flakes.
**One registry, learned the hard way.** The panel builds its registry once at boot
(`web.go:530`) and hands it to the facade with `cores.Use` (`cores.go:115`). Every
deps-free helper — `ServedByXray`, `ClientCredentials`, `ClientShare`,
`IngressSelectorFor`, … — answers from that WIRED registry: those are the adapter
instances the jobs drive and the supervisor restarts, and an answer from a second set of
instances is an answer about cores nothing is running — the facade and the jobs used to
disagree exactly that way. The `Deps{}`-built fallback survives only for processes that
never wire a runtime: the CLI, codegen, and tests that call a facade helper directly
(`kindOwners`, `cores.go:106`).

### 3.5 One capability evaluator, cross-checked across the language boundary

Replace the 253 triplicated lines with a bounded DNF evaluator: **exactly three operators**
(`in`, `set`, `prefix`), depth one, two addressable roots (`settings.*`, `stream.*`).
~55 lines of Go, ~35 of TypeScript, containing **no protocol names**. All nine existing
predicates fit; a rule needing a fourth operator is *code* — set `Grant.Dynamic = true` and
defer to the server.

The guard is the important half: **a Go test writes
`frontend/src/test/golden/fixtures/capabilities/resolve.json`; a vitest twin reads the same
file** and must reproduce every answer. Change a clause in Go, leave the TS evaluator alone,
watch vitest go red. This is already an in-house idiom —
`service/golden_fixtures_xray_test.go:37` resolves that exact fixture directory — not
imported ceremony.

---

## 4. Unified accounting

The highest-severity subsystem in the refactor. Because `client_traffics` is email-keyed
and shared, **a delta bug in core #7 does not corrupt core #7's accounting — it corrupts
the shared quota of users who never touched core #7.**

The delta engine was written twice (`xray/api.go:736-780` and `mtproto/manager.go`) and
**the second copy had a real bug**: its negative clamp dropped every byte moved since an mtg
restart. Eleven cores parsing OpenVPN status files, `occtl show users`, `wg show transfer`
and `accel-cmd show sessions` will produce eleven copies, and every one will be subtly wrong
at least once. **There must be exactly one.**

P2 deleted the mtproto copy: the manager now feeds raw cumulative readings to `core.Counter`
and the bug is gone, with `TestCollectTrafficSurvivesAnMtgRestart` covering both restart
signals. The xray copy followed (dc375f93, 2026-08-04): `XrayAPI` holds a `core.Counter`
(`api.go:58`) and `GetTraffic` feeds it raw cumulative stats (`api.go:780`). Xray's stats
carry no incarnation token, so a panel-caused restart arrives out of band through
`NoteCoreRestart` and a reset counter is caught by the backwards-counter backstop. The one
engine now has exactly one implementation and every core as its caller.

### 4.1 Four rules

1. **Normalise every source to absolute-cumulative-within-epoch at the edge.** A reading on
   the wire is `(subject, epoch_token, absolute_up, absolute_down, seq)`. **Never put a
   delta on the wire** — deltas are not idempotent; absolute readings are. Sources that
   only offer destructive reads get a node-local, fsync'd accumulator that converts them
   before anything leaves the box.
2. **Compute the delta in exactly one place: inside the Postgres transaction that owns the
   cursor.** The cursor row *is* the idempotency key; a compare-and-swap advance
   (`WHERE seq < :new`) collapses at-least-once delivery into exactly-once application with
   no dedupe table.
3. **Drain before destroy.** Any operation that annihilates a counter — removing an Xray
   user, removing a WG peer, restarting a core, deleting an mtg secret, rewriting a config —
   must be preceded by a final read that is durably committed. A hard contract in the source
   interface, enforced in `runtime.Runtime`, not a convention. This converts "every restart
   loses a poll" into "only crashes lose".
4. **Enforcement is a pushdown problem, not a polling problem.** Polling bounds *detection*
   latency; overshoot is `peak_rate × (poll + ingest + enforce)`. At 1 Gbit/s with a 60 s
   poll that is **7.5 GB**. Only a limit pushed *into* the daemon bounds overshoot
   independently of the control plane — and it is the only thing that still works when a
   node is partitioned.
Status, measured 2026-08-16. Rule 1 is real at process scope: every adapter hands raw
cumulative readings to `core.Counter`, and the destructive tag-stats read is banked in the
adapter and drained exactly once per poll — the traffic job loops over capabilities, not
protocols (`job/core_traffic.go:43`). Rule 2 is still a target: deltas are computed in the
adapter's memory and applied additively, not inside a Postgres transaction owning a cursor,
so a panel crash between `Observe` and commit loses that poll. Rule 3 is built where it was
measured to matter — the WireGuard manager banks every peer's reading before any write that
destroys one (`wireguard/manager.go:177`, `TestRemovePeerBanksItsFinalReading`) — but as a
manager habit, not yet the contract-level obligation this rule demands. Rule 4 holds for
mtg, whose `[secret-limits]` maps `totalGB`/`expiryTime` into the daemon at config render,
and for nothing else; §4.3 has what `QuotaEnforcer` does today.

Where correctness is genuinely unobtainable (bytes between the last read and an unobserved
crash), **record `bytes_known_lost` and undercount**. Never estimate into the enforced
number. Undercounting costs bandwidth; overcounting costs trust.

### 4.2 Counter taxonomy → adapter obligation

| Class | Sources | Adapter must |
|---|---|---|
| Monotonic cumulative | Xray stats, `wg show transfer`, RADIUS gigawords | emit raw + epoch token |
| Reset-on-read | Xray with `reset=true`, Hysteria `?clear=1` | **never use** — at-most-once vs the panel |
| Per-session | OpenVPN `bytecount`, RADIUS per-session | accumulate per session id, sum live + closed |
| Event-push | RADIUS Interim-Update | node-local accumulator, then Class A |

**Keep `Reset_: false`** on the Xray stats query — `api.go:764` already does this and it is
correct. `Set(0)` is an `atomic.SwapInt64`: atomic with respect to the data plane, but
at-most-once with respect to you.

Epoch token = `boot_id : process_start_nonce [: object_generation]`. It detects every reset
class that is detectable; the backwards-counter check stays only as a backstop. mtproto uses
`/stats` `started_at`, and P2 proved the two are **independently sufficient** — either alone
bills a restart correctly, so a test that drives one leaves the other unexercised.

Two rules the epoch and the baseline map obey, both found by review after P2 and both now
pinned by a test in `counter_test.go`:

- **An empty epoch means *unknown*, never *new*.** A source that answers without its start
  token must not be read as having restarted: the epoch would flip away and back, and each
  flip wipes every baseline, so the next reading is billed in full. Unknown keeps the last
  known epoch and leaves the work to the backstop.
- **Baselines expire only on sustained absence — and this rule has been wrong in both
  directions.** The old prune dropped a baseline after one missing reading, and a partial
  scrape mid-reload re-billed a live subject its whole counter; this section then
  over-corrected to "never expire automatically", which grows the map with every subject
  ever seen. As shipped (7d10dda2, 2026-08-04): a baseline is dropped after
  `baselineGrace = 10` consecutive readings without its key (`counter.go:30`) — 50 s at
  Xray's poll, 100 s at mtg's, far longer than any reload — and a reading with no subjects
  at all is evidence about nothing, so it counts no absences. `Counter.Forget` stays the
  immediate path when *the panel* removes a subject; drain the final reading first.

### 4.3 Enforcement

`disableInvalidClients` (`service/inbound_disable.go:120`) is already the right *shape* —
it resolves email → set of `(inbound, node)` targets and revokes. Two changes were
prescribed here; one resolved sideways, one half-landed:

- It still calls `s.xrayApi.RemoveUser` **directly** for local targets
  (`inbound_disable.go:182`), which violates the `runtime.Runtime` layering rule; routing
  it through the registry remains open. The MTProto half resolved differently than
  prescribed: `restartCannotFix` (`inbound_disable.go:323`) classifies Xray's "handler not
  found" as nothing-to-remove rather than a restart trigger, and the depleted client is cut
  off by its own core — the mtproto reconcile drops the secret once
  `client_traffics.enable` goes false. Each core revoking its own depleted clients through
  reconcile is the durable shape; the direct call is the residue.
- Pushdown exists at both ends for mtg and neither for anyone else. `[secret-limits]`
  carries quota and expiry *into* the daemon at config render. The reverse edge shipped
  2026-08-15 (a996a4e0): auto-renew clears the daemon-side counter through every core
  answering `QuotaEnforcer` (`resetCoreQuotas`, `service/inbound_mtproto.go:114`) — the
  capability's first production consumer — because a renewed client whose daemon keeps
  counting stays blocked however the panel feels about their quota. The
  `min(remaining, band)` push with hysteresis is still the target for cores whose budget
  cannot ride their config (accel-ppp/RADIUS `Session-Octets-Limit`; ocserv per-user
  config).

**An IP-limit bounce is per-CORE, and deliberately best-effort.** `core.Session` carries no
instance id, so a breach is attributed to the core that reported it and never to one of its
inbounds — and it cannot be otherwise: Xray's own online-user stats are keyed by email in a
global namespace (`user>>>email>>>online`) and carry no inbound tag, so the core could not
populate such a field if the contract had one. A client on two inbounds of one core therefore
has the bounce land on whichever of them sorts first. That is acceptable because **the bounce is
not the enforcement**: the fail2ban line is, and the jail bans the ADDRESS at the host firewall,
which covers every inbound and every core at once. Removing and re-adding the user only hurries
along the sessions already established. What must never happen is the bounce landing on a core
that saw nothing, which is a different client's connections being cut — that is why the target is
chosen by observation (`SessionScan.Observers`) and not by a sort order.

### 4.4 Why RADIUS is an adapter, not the architecture

RADIUS is a tempting unifier and it is the wrong backbone here, for one decisive reason:
**the panel's two primary cores cannot speak it at all.**

| | Auth | Acct | CoA/Disconnect |
|---|---|---|---|
| ocserv | yes | yes (gigawords correct) | **no DAE listener** |
| strongSwan `eap-radius` | yes | yes (opt-in) | yes, but matches on **User-Name only** |
| accel-ppp | yes | yes | yes, richest matcher |
| OpenVPN `radiusplugin` | yes | yes | **no** — kill via management interface |
| SoftEther | PAP only | **no** (upstream: no plan) | no |
| **Xray / sing-box / mtg** | **no** | **no** | **no** |

Recommendation: **embed a RADIUS server** (`layeh.com/radius`) on `127.0.0.1:1812/1813`,
as *one ingestion adapter behind the normalised interface*. Do **not** deploy FreeRADIUS —
it adds a second stateful daemon, a second config language, a second schema, and a second
writer to Postgres, for features (proxying, realms, EAP termination) this product does not
need. Enforcement stays a panel-driven fan-out, never "the RADIUS server decides".

> The one thing that flips this: if EAP-MSCHAPv2 or EAP-TLS termination for IKEv2 is a hard
> requirement, `layeh.com/radius` is not an EAP server and FreeRADIUS 3.2 with `rlm_rest`
> (never `rlm_sql`, to preserve single-writer Postgres) becomes correct.

---

## 5. Cross-protocol egress

### 5.1 The mental model

There are only two kinds of thing, and conflating them is the main source of broken designs:

| | Examples | Where a "user" exists | Where egress is chosen |
|---|---|---|---|
| **L7 socket proxies** | Xray, `ssh -D`, mtg | in the daemon, per connection, as a credential | in the daemon: per outbound / routing rule |
| **L3 packet tunnels** | OpenVPN, strongSwan, ocserv, WireGuard, accel-ppp | only as an **IP address** once the packet hits the kernel | in the kernel FIB: routing table, `ip rule`, netns, VRF |

**The accounting invariant:** per-user counters are taken at **ingress**, in the core that
authenticated the user, *before* any egress mechanism. No egress mechanism can restore
identity once lost. Consequence: **Xray's per-user counters are completely independent of
which outbound the traffic took** — every mechanism below preserves Xray accounting
perfectly. Anything measuring bytes on the *egress* interface (`ip -s link show wg0`) is
aggregate-only and must never back a quota.

### 5.2 Forty-nine combinations, three implementations

| Pattern | Covers | Mechanism |
|---|---|---|
| **A — L7 chain** | xray → {xray, ocserv, wireguard, ssh, shadowsocks} | egress daemon exposes a local SOCKS port (or none: Xray's native `wireguard` outbound with `noKernelTun`); emit one outbound + routing rule. **Zero kernel state.** This is the majority of real demand and it is the pattern `injectMtprotoEgress` already implements. |
| **B — marked egress** | xray → {wg uplink, openvpn, ikev2, amneziawg, ppp} | emit a `freedom` outbound whose `sockopt.mark` is `0x0e000000 \| id`; the manager pairs an exact-match `ip rule fwmark` with the id's table and blackhole; a driver brings the uplink device up and fills the table with it. **Built** — the `wg-client` uplink shipped it, dialled by the panel's own engine rather than by a client daemon with `route-noexec` up-hooks. |
| **C — L3 bridge** | any L3 inbound → anything | `ip rule iif <ingress-dev> table T` (`from <pool-or-/32>` where the core gives an inbound no device of its own); T holds `default dev <front>` over `blackhole default`. The front is another kernel tunnel, or a tun2socks / Xray-`tun` device fronting an L7 proxy. |

Everything else in the matrix is a parameterisation of A, B or C.

That sentence used to price Pattern B as a hand-build per daemon; the tree superseded that
with a taxonomy. `internal/egress/driver.go` splits an egress type into `Driver` — what
fills the id's routing table, the only mandatory half — plus `Provisioner` when the PANEL
makes the device and `Injector` when the front is a device the core itself creates. The
shapes map onto who owns the device: `xray-tun`'s front belongs to Xray and appears when
Xray does, so it injects; a WireGuard uplink is dialled by this panel, so it provisions
(`drivers/wgclient` — on the SAME engine that serves `pwg` inbounds, because an uplink and
an inbound must never be two writers to one device namespace); an ikev2 uplink would be
dialled by strongSwan, which makes its own device, so it is a `Driver` alone. Adding openvpn
is choosing one of these three and adding one `Register` line to `newEgressDriverRegistry`
(`internal/web/service/egress.go`) — explicit, never `init()`, for §3.4's reason. A type the
registry does not serve is contained, never let out through the server's own identity.

Three corrections to that table, each measured on 6.8.0-111:

- **MASQUERADE and MSS clamp are front-dependent, not part of Pattern C.** They are needed
  only by a front that *forwards* packets. A front that terminates L4 — Xray `tun` on
  gVisor, tun2socks — re-originates the flow from the host, so the tunnel's inner source
  address never reaches the wire and the path MTU is the host's. `xray-tun` needs neither.
- **A Pattern C table needs `blackhole default` at least as much as a Pattern B one.** Its
  front can be absent at any moment *by design*: a TUN fd carries no `TUNSETPERSIST`, so the
  device dies with the process that made it and the kernel purges the only route out of the
  table. Without the blackhole the `ip rule`'s lookup misses, falls through to `main`, and
  every tunnel user egresses with the server's own address. Install it strictly before any
  rule points at the table and remove it strictly after the last one.
- **A WireGuard device's `FirewallMark` cannot select egress for its own users.** Measured:
  it marks the device's own encapsulated outer UDP (9 marked / 0 unmarked) and never the
  decrypted inner traffic in either direction (0 of 8 each way). That is precisely why
  `wgkernel` → Xray is Pattern C and not Pattern B — there is no mark to match on, which is
  what makes the ingress device the selector.

Two neat details worth knowing: strongSwan's `set_mark_in` (≥5.7.0) stamps inbound-decrypted
packets with a netfilter mark, making it **the cleanest per-inbound hook of any L3 core**;
and `openconnect --script-tun --script "ocproxy -D 127.0.0.1:P"` gives a SOCKS port with no
root and no routing at all, which makes xray→ocserv trivially Pattern A.

### 5.3 Resource allocation (put in the DB; nothing else touches these ranges)

| Resource | Range | Formula | Built? |
|---|---|---|---|
| Egress id | 1…999 | DB primary key | yes |
| Routing table | 30001…30999 | `30000 + id` | yes |
| `ip rule` priority | 31001…31999 | `31000 + id` | yes |
| Front device | `peg1`…`peg999` | `"peg" + id`, ≤15 chars | yes |
| Uplink device | `pux1`…`pux999` | `"pux" + id` | yes |
| Front gateway | `100.127.0.1/32`… | `DefaultGatewayBase` (100.127.0.0/16) + id | yes |
| fwmark (data) | `0x0e000001`…`0x0e0003e7` | `0x0e000000 \| id`, exact match — no mask | yes |
| Local SOCKS port | — | an `ExitSocksPort` handle names the core's own port | band not needed |
| Shaping upload mirror | `pifb1`…`pifb999` | `"pifb" + id`, ≤15 chars | yes |

A front and an uplink are opposite ends — one terminates traffic the panel received, the
other originates traffic the panel sends — which is why `pux` is its own namespace rather
than a `peg` variant, with its own round-trip (`alloc.go:33`). The gateway /32 exists for
the return path alone: an addressless front fails reverse-path filtering, and only there.

**A shapeable core must name its device from a registered namespace.** What the previous
revision said to do "at that point" is done: the three constants became
`shaping.Namespaces` (`internal/shaping/namespaces.go`), which `NewManager` takes
explicitly — a manager that decided its own would be a second opinion on which devices the
panel owns. `pwg` and `peg` are built in because the panel derives them from an id itself;
`pifb` is owned by construction, since this package creates the mirrors; a core that brings
its own device namespace registers a prefix at wiring time. The ownership property survived
the generalisation intact: `Owns` is still a round-tripping predicate and never a prefix
test — an operator's `pwgtest` is somebody else's interface and a tree installed on it
throttles traffic this panel does not serve — and `Register` enforces what keeps the round
trip sound: lower-case letters only, so any device name splits at exactly one place and two
namespaces can never both claim one device (`pwg1`+`2` versus `pwg`+`12`).

All marks are ≤ `0x7FFFFFFF` so they fit Xray's `int32` `mark` field. The asserts this
section used to ask for are real: `Preflight` (`internal/egress/preflight.go`) refuses
anything foreign in the reserved priority and table bands, refuses a gateway-base collision
with an address already on the box, and refuses a strict `net.ipv4.conf.all.rp_filter`
naming the sysctl. The `wg-quick` (51820+) and sing-box defaults sit outside 30001–30999
entirely, so the one band walk subsumes that check. It is deliberately not part of the
reconcile tick — drift repair would either shout the same refusal forever or delete objects
it has no claim to; it answers at save time and on the Outbounds page instead.

**Two mark rows died before shipping, and the corpse is instructive.** The design carried a
second band for the tunnel's own outer socket (`0x0e0f0000 | id`) plus a shared mask, and
the mask was corrected once already — `0xff00ffff` zeroed the only nibble separating the
bands, and because the outer rule sits at the *lower* priority it would have swallowed every
data-marked packet into the underlay table, a silent routing loop. The corrected mask then
never shipped either, because the premise dissolved: the first marked driver's uplink is a
WireGuard device, its outer UDP is kernel-encapsulated and carries a mark only when a
`FirewallMark` is set on the device — §5.2's 9-marked/0-unmarked measurement — and
`wgclient` sets none, so the outer traffic routes via `main` with no second rule and the
loop the outer band existed to break cannot form. One exact-match band won: `ip rule fwmark
Mark(id)` with no mask at all (`alloc.go:87`). A future daemon whose outer socket CAN
inherit the data mark — OpenVPN over its own TCP, say — is the point at which an outer band
gets built, against its real need.

### 5.4 What P6-3 decided (the `xray-tun` type)

**Data model.** One table whose `type` column is the whole generalisation seam:

```
egresses(id PK, type, enable, remark, target, settings JSON, owner,
         ingress_inbound_id UNIQUE NULL, created_at, updated_at)
inbounds.egress_id INT NULL   -- dead; survives as the migration's backfill input
```

`id` is the only allocation ever stored — `Table`, `Prio`, `Device`, `Uplink`, `Mark` and
`Gateway` are pure functions of it (`internal/egress/alloc.go`), and `ownedEgressID` /
`ownedUplinkID` round-trip a device name back, so `peg007` is somebody else's. Two columns
arrived with routing (§5.5): `owner` separates a front the panel provisions from an uplink
an operator typed credentials into, and `ingress_inbound_id` — UNIQUE — names the inbound a
front exists for, which makes one-front-per-ingress the schema's rule rather than a
convention. `target` is the outbound-or-balancer tag a FRONT sends traffic to, resolved by
the same `routingTargetExists`/`routingTagIsBalancer` the three Pattern A bridges use; an
uplink IS the destination, so only a driver that injects is held to one. `settings` buys the
next driver zero migrations.

`egress_id` was the attachment column, and the attachment path is deleted (§5.5): the
column survives only so `migrateRoutingIntent` (`internal/database/routing_migration.go`)
can backfill old attachments into `routing_rules` rows plus panel-owned fronts, clearing
each reference as it goes — it must, because the column now carries `fk_inbounds_egress …
ON DELETE RESTRICT` (`db.go`) and a leftover reference would block the row's deletion. The
third of its original three reasons outlived the column and decides where intent lives
instead: anything rendered into an inbound's `settings`/`streamSettings` drags a REALITY
inbound through `hot_diff.go`'s REALITY rule into a **full process restart**, which is why a
rule is a `routing_rules` row and never a key on the inbound. The id sequence stays exempt
from `resyncPostgresSequences` (`internal/database/db.go`): the resync sets a sequence back
to `MAX(id)`, which after deleting the newest egress would hand the same id — and whatever
of its kernel state survived — to the next one created.

**Selection is `iif` for what the kernel forwards, `fwmark` for what this host originates.**
Cryptokey routing has already proven the peer's identity by the time the packet appears on
`pwg<inboundID>`, so the ingress device is a stronger claim than a source prefix, needs no
pool parsing, and survives an AllowedIPs edit; `from <pool-or-/32>` stays the documented
fallback for a core that gives an inbound no device of its own. The second selector landed
with uplinks: a core's own outbound socket has no input device to match on, so a driver sets
`Fill.Marked` and the manager emits an exact-match `ip rule fwmark Mark(id)` into the same
table — with the table, never on its own, because a mark with no rule to catch it routes via
`main` and leaves with the host's own address. Locally generated traffic has no `iif`, so a
front's own uplink sockets can never match the device rule — the loop hazard upstream's
`proxy/tun/README.md` warns about is structurally impossible here rather than avoided by
careful metrics.

**Lifecycle follows ownership, and the attach control is gone.** An operator uplink's kernel
objects exist while its row is enabled; a panel-owned front's ROW follows the rules that
need it — `ensureFronts` upserts one per device ingress a rule names, `reapFronts` retires
the front of an ingress no rule names any more (`internal/web/service/routing.go`). The reap
is not tidiness: measured, a front row outliving its last rule keeps selecting the device
into a table holding only its blackhole, and deleting the last rule took a working WireGuard
client from the internet to nothing — worse than the state before any rule existed. The
synchronous property survived the attach path's deletion: every rule write passes through
the one `converge`, which reconciles the kernel before returning — a tick that caught up
later would leave a just-saved rule egressing with the server's own identity. A rule whose
device is absent installs, lists as `[detached]` and reattaches by itself, and boot is
fail-closed by *ordering*: one reconcile pass runs before any core is started. Add order is
blackhole → rules → core config → the one `default dev <front>` route; remove order is the
exact reverse, because removing the rule is what stops traffic.

**An unresolvable target means dark, not direct.** Inheriting the three bridges' skip-and-log
means no front is injected while the rules and blackhole stay, so the egress's clients are
*contained*. That is intended, and stated here so nobody "fixes" it into a leak.

**IPv6 is a peer of IPv4, not a follow-up.** The v4 and v6 rule and table namespaces are
wholly independent, so a v4-only implementation leaks every v6 flow silently. Every rule,
blackhole and front route has a twin. Reverse-path filtering is the one v4-only knob —
`/proc/sys/net/ipv6/conf/<dev>/rp_filter` does not exist.

**One host global the panel reports and never owns; two it took ownership of.**
`net.ipv4.conf.all.rp_filter` stays a refusal — the effective value is `max(all, dev)`, so a
strict global cannot be lowered per device and preflight names the sysctl (per-device the
drivers do lower it: `2`, loose, on an uplink, because `0` is overridden on a hardened
host). Forwarding switched sides: `EnsureHostForwarding` turns both families' knobs on the
moment any `IngressDevice` inbound exists, persists them through a sysctl drop-in
(`Plane.PersistSysctl`) so a reboot does not silently darken every tunnel, and never turns
them off — docker or another VPN may depend on the same knob, and neither is the panel's to
break. It owns one nft table too: `EnsureMasquerade` translates the forwarded traffic of L3
ingress devices, because a client's in-tunnel source never survives upstream and a tunnel
that hands out addresses and drops every packet is the alternative; the table is dropped
when the last L3 inbound goes.

**Master-local in v1.** A node inbound (`NodeID != nil`) is simply never a routing subject —
`Subjects` filters on `node_id IS NULL`, and `DesiredInstances` filters node inbounds before
any core is asked, so a core structurally cannot answer for an inbound living somewhere
else. The reason is unchanged: an egress id is a global DB key while every resource it
derives is per-host. Multi-node egress is its own phase, gated behind node sync carrying
egress rows.

**No per-user accounting is added.** By §5.1's invariant the `wgkernel` core's per-peer
counters already bill correctly whatever egress the traffic took. The front's tag `peg<id>`
deliberately matches no inbound row, so the core's `inbound>>>peg<id>>>>traffic` counters are
discarded rather than rolled into an inbound whose bytes were already counted at ingress.
The mtproto bridge made the opposite choice — it reuses the inbound's own tag — and had to
add rollup suppression to stop double-billing.

**Known costs, accepted.** The injections deepen the §12.4 debt: everything `GetXrayConfig`
injects after the inbound list — now including the routing compile's fronts and its
synthesized `pex<id>` outbounds — is invisible to the Xray core's own `Reconcile`. gVisor
termination changes observable client behaviour — a connection to a dead host still
completes a handshake and then RSTs, ICMP is echo-only and answered locally, so traceroute
through the egress is meaningless — and is now DISCLOSED where it is chosen: the rule editor
warns that routing a kernel WireGuard inbound narrows it to TCP, UDP and ICMP
(`RuleFormModal`'s fronted alert, keys `pages.xray.routing.l3Fronted*`). Every kernel fact in §5.2 and §5.3 was
measured on 6.8.0-111 / iproute2 6.1.0 while §6 verifies against Ubuntu 26.04 / kernel 7.0 /
iproute2 6.19; the semantics are long-stable but the gap travels with the numbers.

### 5.5 Routing: intent in `routing_rules`, one pure compile (landed 2026-08)

What §5.4 called attach grew into the general thing and then replaced it. Operator intent is
one table — `routing_rules`: these inbounds, these criteria, this destination — compiled
into BOTH artifacts that realise it, the Xray rules array and the kernel state §5.4's
manager converges. The attach endpoint and service path are deleted, and the Egresses page
went with them: what an operator authors is rules on the Xray page's Routing tab and exits
on the Outbounds page. `inbounds.egress_id` survives only as the migration's input (§5.4).

**The rule row** carries `sort_index` for first-match order, a scope (`selected` plus
inbound IDs, or `all` — expanded at compile time to one rule per routable subject *at the
rule's own position*, which keeps ordering exact across mechanisms), a criteria JSON object,
and one of five destination kinds: `outbound`, `balancer`, `exit`, `direct`, `block`. Rules
name inbounds by id, never by tag: tag-keyed rules are what an inbound rename used to
silently widen to every inbound on the box. The save boundary refuses what the compile could
not realise (`normalizeRule`) and what Xray would misroute: an unknown `outboundTag` does
not fail, it falls back to the *first outbound*, so `destResolves` refuses a rule aimed at a
tag nothing answers to — and `checkNotReferenced` refuses to delete or disable an exit while
a rule routes to it, the same silent-direct hazard from the other side. Deleting an inbound
prunes it from every rule (`PruneInbound`); a rule left naming nobody is disabled and
labelled, never silently removed — the operator wrote it. The prune converges synchronously
because a row is not kernel state: a detached `iif` rule re-attaches the moment a device of
that name reappears, which `resyncPostgresSequences` re-handing inbound ids makes possible.

**The compile** (`internal/routing/plan.go`) is pure by construction — no database, no
netlink, no xray-core types — so a preview and a save must derive the same answer from the
same input. Everything protocol-specific arrives already resolved: the service layer asks
the core registry once, and `Plan` never learns which core it serves. It also never returns
an error: a rule it cannot realise becomes a `Diag`, per (rule, subject) PAIR and never per
row — one rule naming an Xray inbound and a WireGuard inbound is realised by two mechanisms,
and one chip cannot describe both without lying about one of them. The mechanism vocabulary
is closed: `proxy`, `inspected`, `marked`, `inert` — and `kernel`, reserved with zero
emitters, the name held for a rule realised as a pure `ip rule` with no front. `Refusals`
names the diag subset that must gate a save; today the boundary checks above do the gating
and the editor carries the per-subject truth, so surfacing the pair diags there is the
seam's unconsumed half.

**What a core declares** (`internal/core/caps.go`): `RoutableIngress` — a static
`IngressSelector` per kind (`internal`: an L7 proxy is its own router; `device`: decrypted
traffic crosses a kernel interface) plus a live per-instance `IngressHandle` — and
`RoutableEgress` — `ExitKinds`, a static `ExitHandleKind` (`device` / `socksPort` /
`xrayOutbound`) and a live `ExitHandle` carrying a `SourceOwner`. Closed vocabularies like
`Selector`'s: an unknown value reads as "cannot route", the fail-closed answer.
`SourceOwnerPanel` is a refusal today (`KeyNoSnat`) — a kernel forward keeps the ingress
client's inner source, which every upstream that is not a peer drops, and `egress.Plane` has
no netfilter object to rewrite it. `wgkernel` answers `SourceOwnerDaemon` for its uplinks
because the answer is about the path, not the device: what reaches an uplink is a marked
socket this host ORIGINATED, so the kernel picks the uplink's own address at route lookup
and nobody rewrites anything.

**Fronts are provisioned, never authored.** An L3 subject a rule names is always fronted
through Xray's gVisor tun in this phase — the kernel FIB has no domain or port vocabulary,
so criteria can only match inside Xray. `ensureFronts` upserts exactly one panel-owned
`egresses` row per named device ingress (UNIQUE on `ingress_inbound_id`; fronts and exits
draw from ONE id sequence, so the two can never derive the same table, priority or device)
and `reapFronts` retires a front with its last rule — §5.4's measured reason. The compile
stamps a `geoip:private` guard strictly ahead of each front's rules, because a front is
otherwise the one class of forwarded traffic exempt from the template's private block. The
gVisor downgrade — TCP/UDP/ICMP only, ping answered locally — is disclosed in the rule
editor the moment a fronted subject is picked, and the criteria mask says the same thing
structurally: `user`, `domain` and `protocol` are withheld for a fronted subject, because
Xray's tun handler builds a MemoryUser with no Email and an unsniffed packet carries no
name, and a field offered there produces a rule that saves and never matches — the exact
bug class this page exists to remove.

**Exits are authored on the Outbounds page from what the registry declares.**
`CoreView.ExitKinds` mirrors each bound core's `RoutableEgress` answer, so the picker is
built from the registry rather than a list somebody maintains; the frontend keeps one module
per authorable kind (`frontend/src/pages/xray/outbounds/exits/`) — the ONE place the core
KIND and egress TYPE vocabularies meet, after a bug where both were the WireGuard uplink's
and a second exit kind would have saved itself as a `wg-client` for the wrong driver to
dial. A `DestExit` rule compiles by handle kind: `xrayOutbound` and `socksPort` are Pattern
A; `device` is Pattern B and carries `compileRule`'s capitalised invariant — a marked
outbound is only emitted for an exit whose row converges the matching `ip rule` fwmark,
because a marked socket with no rule to catch it routes via `main` and leaves with the
server's own address: direct rather than dark, inverting the one property `internal/egress`
guarantees. The row is the tie — the same enabled egress that answers `ExitDevice` is the
one whose `Fill` sets `Marked` — and `converge` reconciles the kernel before Xray restarts.

The whole seam, priced for core #N: declare `RoutableIngress` and its inbounds appear as
rule subjects; declare `RoutableEgress`, register an egress driver in one of §5.2's three
shapes, and add the one kind→type line in `exitKindFor`, and its uplinks appear as
destinations. Nothing in the compile, the manager or the editor learns the protocol's name.

---

## 6. The cores

Verified on the target OS (Ubuntu 26.04, kernel 7.0). All required kernel modules are
present: `wireguard`, `l2tp_ppp`, `l2tp_netlink`, `ppp_mppe`, `gre`, `xfrm_user`, `esp4`;
`tun` and `ppp_generic` builtin; `nftables 1.1.6`; iproute2 6.19.

| Core | apt (26.04) | Per-user accounting | Hot user add | Enforcement | Priority |
|---|---|---|---|---|---|
| **xray** | vendored | gRPC stats, monotonic | `AlterInbound` | RemoveUser | shipped |
| **mtproto** (mtg-multi) | bundled binary | HTTP `/stats` | `PUT /secrets` | `[secret-limits]` | shipped |
| **wireguard / amnezia** | `wireguard-tools 1.0.2025` | `wg show transfer` (per-peer, monotonic) | `ConfigureDevice(ReplacePeers=false)` | remove peer | **1st** |
| **ocserv** | `1.3.0` | `occtl -j show users` (RX/TX) | append to `ocpasswd`, immediate | `occtl disconnect user` | **2nd** |
| **openvpn** | `2.7.0` | management `bytecount` / `status 3` | `--management-client-auth`, CCD | `client-kill` | **3rd** |
| **strongswan (IKEv2)** | `6.0.4` | `list-sas` bytes-in/out per CHILD_SA | `swanctl --load-creds` | `terminate` | **4th** |
| **L2TP/IPsec** | `strongswan` + `xl2tpd 1.3.20` | RADIUS acct | chap-secrets reload | RADIUS DM | 5th |
| **SSTP** | **SoftEther `5.01`** | SoftEther JSON-RPC | RPC | RPC | 6th |
| **ssh** | `openssh` / custom Go server | **none natively** | — | kill sessions | last |
| **pptp** | **absent** | — | — | — | not recommended |

### 6.1 Packaging notes that change the plan

- **`accel-ppp` is in no Debian or Ubuntu release, ever.** The tidy "one accel-ppp core
  covers PPTP + L2TP + SSTP" answer costs a from-source build **and DKMS maintenance on
  every kernel update**. That is a recurring tax, not a one-off.
- **`pptpd` is in Debian (1.4.0/1.5.0) but not Ubuntu 24.04 or 26.04** (Ubuntu 22.04 only).
  PPTP's MS-CHAPv2 is offline-crackable; Ubuntu dropped it deliberately. Ship it as a
  checkbox at most.
- **SoftEther is packaged (5.01)** and covers SSTP + L2TP/IPsec + OpenVPN + SSL-VPN in one
  daemon with a JSON-RPC API and per-user counters. For the legacy PPP family it is the
  strongest *packaged* answer — at the cost of a large opaque dependency and PAP-only RADIUS.
- **CRITICAL — sequencing.** WireGuard and ocserv are first not because they are the most
  wanted, but because WG has the cleanest per-peer counters (`wgctrl`, pure Go, no daemon)
  and ocserv has the cleanest management CLI. They stress the contract in opposite
  directions for the least implementation risk.
- **Market note:** OpenVPN, L2TP, PPTP and plain IKEv2 are heavily DPI-blocked inside Iran.
  They serve other markets, enterprise and domestic use — they do not replace Xray/REALITY
  for censored users. Rank accordingly; the architecture is indifferent.

### 6.2 SSH is the honest exception

`sshd` provides **no per-user byte accounting**. The options are per-uid `nftables`
counters, per-user cgroup/netns, or **replacing sshd with a Go SSH server**
(`golang.org/x/crypto/ssh`) that counts bytes itself. The last is the only one that yields
first-class accounting, and it is a real subsystem, not an adapter. Until then, SSH should
declare **no** `TrafficSource` capability rather than fake one — which is exactly what the
optional-capability design exists to permit.

---

## 7. Supervision and deployment

**Cores must stop being children of the panel.** `update.sh` restarts the panel at 12 sites
(`:222, :290, :489, :490, :494, :500, :501, :569, :663, :723, :757, :855`). With one proxy
core that is a reconnect blip; with IKEv2 and L2TP parented to `p-ui` it is **a full VPN
outage per ACME renewal**. Also `p-ui.service.debian` has **no `User=`** — the panel runs
as root and every forked child inherits it.

Design: one `internal/supervisor` package, one `Spec` value type, backends behind one
interface. **The `Spec` uses systemd's exact vocabulary** (`Restart=`, `RestartSec=`,
`RestartSteps=`, `StartLimitIntervalSec=`, `StartLimitBurst=`) from day one — even if the
systemd backend never ships — because retrofitting it is a breaking change to every adapter.
`p-ui.service.debian` already uses that vocabulary.

Ship `exec` as the only backend through the early phases (a pure hoist of
`mtproto/process.go` and most of `xray/process.go`), then systemd as opt-in
(`PUI_SUPERVISOR=auto|systemd|exec`, one knob, never per-core), and flip the default only
after field data. The point of the interface is that this choice can be made later **without
touching a single core adapter**.

Two rules that are already violated today:

- **Health must not be `IsRunning()`.** Xray stays alive with every inbound failing to bind
  and the panel shows green. Liveness restarts; readiness only reports.
- **Config must be validated before it is written.** Every candidate core has a checker
  (`xray run -test`, `ocserv -t`, `swanctl` dry-run). Validate against a temp path and leave
  the running config untouched on failure. Current failure mode: bad config lands, core
  dies, panel restarts it, core dies.

Also fix `release.yml:152` — `MTG_MULTI_VER=$(curl … releases/latest)` is a **floating pin**,
so two builds of the same panel tag are today different artifacts. Every bundled dependency
gets an exact tag *and* a sha256 the build verifies. (The panel already verifies Xray's
`.dgst` at *runtime* in `server.go:939-973` while the build that ships it verifies nothing.)

---

## 8. Data model

**The `clients` column set is frozen — enforced, not aspirational.**
`TestClientRecordColumnsAreFrozen` (`arch/client_columns_test.go`) pins `ClientRecord` to
its measured field list, so adding `ocserv_password` requires deleting a line from the
freeze — which is the review signal this design depends on. The union it froze is Xray's
plus WireGuard's (uuid, password, auth, security, flow, and the five WG columns);
MTProto's `secret` and `ad_tag` have already *left* the table for `client_credentials`,
dropped from `clients` and backfilled by the one-time `ClientCredentials` seeder
(`db.go:752`). openapigen still emits the union to the frontend, so the TS type for a
VLESS client carries `preSharedKey?` — the frozen width is the accepted cost; unbounded
growth was the danger.

The real cost is not the columns — it is hand-written O(fields) code that **fails silently**.
`MergeClientRecord` (`model.go:1297`) is one `if existing.X != incoming.X && incoming.X != ""`
branch per field, on the node-sync path. Forgetting a field does not error; the merge drops
it and the symptom is "works on the master, not on the node". `ToRecord` and `ToClient`
repeat the mapping twice more.

```sql
-- Built: db.go:646, model.ClientCredential. MTProto is the only tenant so far
-- (keys "secret" and "adTag"), plus idx_client_credentials_inbound_id for the
-- inbound-side lookups.
CREATE TABLE client_credentials (
    client_id  integer NOT NULL,
    inbound_id integer NOT NULL,
    key        text    NOT NULL,
    value      text    NOT NULL,
    PRIMARY KEY (client_id, inbound_id, key)
);
```

**Keyed per inbound, not per core — corrected 2026-08-06.** This section previously specified
`PRIMARY KEY (client_id, core_id)`, which cannot hold the first credential it would be asked
to hold: the paragraph below already says an MTProto secret embeds the *inbound's* FakeTLS
domain, so a client on two mtproto inbounds needs two values. `client_inbounds.flow_override`
(`model.go:1055`) is the repo's existing precedent and is already per-(client, inbound) and
already authoritative over the column on `clients`. Keying per core would have left the table
with no tenant it could actually serve.

No FK on this table — but "the repo has no FKs" stopped being true when egress landed.
GORM-driven constraints stay off (`DisableForeignKeyConstraintWhenMigrating: true`,
`db.go:2162`); where a dangling reference is a silent *leak* rather than a stale id, the
panel now writes the constraint by hand. `fk_inbounds_egress` is `ON DELETE RESTRICT`
(`db.go:299`) because an inbound pointing at a deleted egress emits no rule and egresses
with the server's own identity while the panel still reports it attached — RESTRICT rather
than SET NULL because a silent detach is that same leak with the database agreeing to it.
The same reasoning exempts `egresses` from the boot-time sequence resync
(`sequenceMustNotRewind`, `db.go:160`): an egress id names host-global kernel state (table
`30000+id`, rule priority `31000+id`, device `peg<id>`), and a rewound sequence would hand
a deleted egress's id — and whatever of its kernel state survived — to the next one
created. Credentials carry neither risk: never queried by content, every access by PK or
`(client_id, inbound_id)`; when a core needs real uniqueness, use a partial expression
index in that core's own migration. **The client-list endpoint must never join this
table.**

**Store credentials, do not derive them.** Derivation is *impossible* for ocserv (salted
one-way hash), OpenVPN (a CA signature; revocation needs CRL state) and MTProto (the secret
embeds the **inbound's** FakeTLS domain — `GenerateFakeTLSSecret`, `model.go:691` — so the
same client's secret differs per inbound). The generator question resolved better than the
`clients.secret_seed` column this section once proposed: minting moved into the core that
owns the format (`CredentialDeclarer.MintClientCredentials`, §3.2a), so the panel asks
rather than derives, and no seed column was ever added.

**Every byte of durable core state rides a DB row — the invariant nothing had written
down.** A WireGuard server key lives in the inbound's `settings` JSON, peer keys in the
frozen `clients` columns, an mtg secret in `client_credentials`; the only file any core
writes in production is its rendered config, regenerated from rows on every reconcile
(`mtproto/manager.go:653` is the one such write under either engine). That is what makes
`pg_dump` a *complete* backup of a panel: binaries reinstall, configs regenerate, rows
restore. The first core that keeps a state directory — an OpenVPN CA and its CRL are the
obvious candidate — breaks restore silently: everything works until the box dies. Such
state goes in rows (settings JSON, `client_credentials`, or a table of its own), or the
core must say loudly in review that backup now has a second half.

**An inbound whose core is unknown is quarantined** — never started, never deleted, never
re-marshalled, badged in the UI (`reasonUnknownCore`). The column is a plain varchar with
no CHECK constraint, so an old binary can already `SELECT` a row with
`protocol = 'ocserv'`; the danger is the *write* path, where db.go's healer and backfill
passes do unmarshal→mutate→marshal round-trips. The guard is written:
`TestUnknownCoreRoundTripsByteForByte` (`arch/unknown_core_test.go:52`) drives an unknown
kind through the render path — which must *omit* it, nil config and nil error, never mangle
it — and its second half pins quarantine as a loud refusal: a state-changing op against an
unknown kind must fail with the exact refusal string, because a silent success is a delete
that never happened. `Registry.For`'s false result means "quarantine", never "delete"
(`registry.go:54`). **Never remove or reuse a `Kind` constant** — it is persisted on
installs you will never see.

---

## 9. Frontend

The frontend writes **zero lines per core**. A core declares its UI in `ui:` struct tags on
the same field that already carries `validate:`; labels are i18n **keys**; a ~300-line
AntD + RHF renderer turns the descriptor into a form; any field may name a **slot** for
genuinely bespoke UI (keypair generation, REALITY derivation, cert upload), lazily
code-split. Widget vocabulary is **frozen at 14 kinds**, budget-enforced.

**The i18n test settles where keys live, and it is not a matter of taste.**
`frontend/src/test/i18n-dead-keys.test.ts:44` *excludes* the `generated/` directory when
collecting reference sources, while `:60` scans **all of `internal/**/*.go`** with a regex
that matches dotted tokens **inside Go struct tags**. So a key written as
`ui:"…,label=cores.openvpn.field.port"` in Go is *already* "referenced in the same commit" —
whereas the same key generated into `frontend/src/generated/core-ui.ts` would report **dead**.

Corollary: **descriptor *types* are codegen; descriptor *instances* are runtime.** openapigen
emits the types and Zod schemas for cores this build knows; a `GET /panel/api/cores` endpoint
serves the actual instances — including a core reported by a sub-node running a newer panel,
which codegen structurally cannot describe.

Two hard constraints:
- **The descriptor form merges into the raw settings object, never rebuilds it.**
  `formValuesToWirePayload` rebuilds per protocol today, which is safe *only* because every
  field is known. Under partial knowledge, rebuilding silently deletes every key the
  master's descriptor did not mention.
- **Never narrow the generated Zod protocol enum to enabled cores.** It validates data read
  *out of* the DB; narrowing makes disabling a core break its existing inbounds client-side.
  Filter the picker, not the validator.

Rename `frontend/src/lib/xray/` → `src/lib/cores/xray/` in the first frontend PR — that
directory name is the key both the ESLint boundary rule and the ratchet's exempt regex use.

---

## 10. The guards

A requirement nothing enforces is not a requirement. Names below were re-verified against
the tree; three had been listed under names that were never the ones in the code, and two
were listed for four phases before anyone wrote them.

| Guard | Where | Prevents |
|---|---|---|
| Nested `internal/` for cores | compile error, 0 config | service layer importing a concrete core |
| `TestXrayCoreVendorIsFenced` | `arch/imports_test.go:48` | xray-core leaking outside `internal/xray` |
| `TestCoreEnginesDoNotImportTheWebLayer` | `arch/imports_test.go:75` | a core reaching up into the service layer |
| `TestDataLayerDoesNotImportACore` | `arch/imports_test.go:109` | the model→xray coupling returning (§2.2) |
| **`TestProtocolDispatchRatchet`, bidirectional** | `arch/dispatch_ratchet_test.go` | regrowth of `switch protocol` |
| `TestCapabilityAssertionsOnlyInBind` | `arch/capability_assertions_test.go:74` | assertions becoming the new switch |
| `descriptor/capabilities-match` | `core/coretest/suite.go:130` | the descriptor becoming a lie |
| `TestSuiteCatchesBrokenAdapters` | `core/coretest/suite_test.go:291` | the conformance suite decaying to a no-op |
| `TestInboundProtocolIsValidatedByTheRegistry` | `arch/protocol_sources_test.go:85` | a hand-typed `oneof` allow-list growing back |
| `TestProtocolSourcesAgree` | `arch/protocol_sources_test.go:124` | const block or frontend enum drifting from the registry |
| `TestProtocolConstantNamesAreUnique` | `arch/protocol_sources_test.go:183` | two constants claiming one kind |
| `TestClientRecordColumnsAreFrozen` | `arch/client_columns_test.go:54` | the wide row |
| Capability golden fixture (Go ↔ vitest) | `frontend/src/test/capabilities.test.ts` | the fourth `canEnableTlsFlow` |
| `TestHotApplyRendersWhatARestartWouldApply` | `service/xray_render_unified_test.go:46` | a hot apply drifting from what a restart applies |
| `TestLocalRuntimeIsWiredToRender` | `arch/local_deps_test.go:25` | web.go dropping an optional-at-compile-time dep |
| `xray_inbounds.golden` + `TestGetXrayConfigIsStableAcrossCalls` | `service/xray_config_golden_test.go:251` | a silent reformat restarting Xray on a timer |

Sixteen exist. **Two more were named here long before they were written:**

| Late arrival | Prevents | Landed |
|---|---|---|
| `TestJobCountDoesNotGrowPerCore` | 11 cores × 3 cron jobs | `arch/job_growth_test.go` |
| `TestUnknownCoreRoundTripsByteForByte` | an unserved kind reaching Xray, or being rewritten | `arch/unknown_core_test.go` |

Both were declared here for four phases while matching nothing in the tree, so §8's
quarantine rule and §12.3's "a second job is fine" argument rested on prose alone the whole
time. §12.3's argument turned out to be wrong, and the guard that would have said so was the
one missing: supervision never became registry-driven until the guard was finally written.

**Three renames** — the old names were never in the tree and cost a reader a grep each:
`TestModelImportsNoCores` is `TestDataLayerDoesNotImportACore`;
`TestClientsTableHasNoPerCoreColumns` is `TestClientRecordColumnsAreFrozen`;
`TestDescriptorMatchesInterfaces` is not a test at all — it is the
`descriptor/capabilities-match` invariant raised inside `RunAdapterSuite`, which every core's
`driver_test.go` runs and `TestSuiteCatchesBrokenAdapters` keeps honest.

**The ratchet's number is `dispatchTotal`, not this table.** It is **94** after P5:
109 seeded (P-1) → 106 (P1) → 99 (P3) → 94 (`e5cc0bc5`, which took `runtime/local.go` to
zero) → 95 (`e2478640`, a one-off migration in `db.go`, a file already frozen as historical)
→ 94 (`6fb1f026`, `RenderInbound` asking the registry instead of naming mtproto).

**That number was wrong until the detector was fixed.** It matched a `model.<Const>`
selector and a comparison against an expression ending in `.Protocol`, so a `case "vless":`
arm of a `switch inbound.Protocol` counted as **zero** — and that is how the largest tables
in the tree are written, `sub/service.go`'s share-link dispatch among them. Twenty-nine
sites were invisible, a 23% undercount concentrated in exactly the layer the ratchet exists
to guard.

Teaching it that shape moved the total to **123** with nothing having regressed. The honest
reading of P5 is therefore that it removed five dispatch sites out of 123, not out of 99 —
the direction is right and the remaining distance is larger than the old number implied.

**The ratchet must fail in both directions.** If it only fails upward, slack accumulates
every time someone deletes a site and the guard dies quietly. Failing when the count is
*below* budget forces whoever removed a site to lower the number in the same PR — exactly
what `routes_contract_test.go:109`/`:123` does, and why that test still works after years.

**Guard the guard:** if the total parsed count is 0, `t.Fatal`. The model is
`routes_contract_test.go:78-80`. Without it, renaming `Protocol` turns every budget green
and the test certifies nothing for a year.

**The detector must be name-based, not type-based.** `client_inbound_apply.go:478` and
`:860` are `if oldInbound.Protocol == "shadowsocks"` — both operands have static type
`string`, so a `go/analysis` type-based analyzer sees nothing. A `go/ast` name-based walk
catches all three shapes, uses only stdlib (house rule), and needs no new `go.mod` dependency.

---

## 11. Cost of core #11

> **This section is a TARGET, not a measurement.** It was written in the present tense with
> no marker and read as fact for four phases. §11.1 is what is achieved at `e2478640`;
> §11.2 audits what a core costs today and **§11.2a is the compiled measurement from P6**;
> §11.3 is the target and what must land to reach it. The old table was optimistic by roughly
> an order of magnitude and named a file,
> `internal/core/requirement.go`, that **has never existed in this repo** —
> `find . -name 'requirement*.go'` is empty at every commit. The nearest real thing is
> `internal/core/capability.go`'s rule table, which answers a different question.

### 11.1 ACHIEVED

- **The protocol enum costs no hand-written TypeScript.** `openapigen` emits the closed union;
  `frontend/src/schemas/primitives/protocol.ts` is a re-export and `Protocols` is a mapped
  type over the generated list. Pinned by `TestProtocolSourcesAgree` (§13, decision 2).
- **The client credential form costs no TypeScript** (P5, `dbcdf012`). A core declares, per
  kind, which credentials its clients carry (`core.CredentialDeclarer`); `GET /panel/api/cores`
  serves the manifest and `frontend/src/lib/cores/client-credentials.ts` takes the union over
  the inbounds a client is on. A kind nothing declares falls back to `uuid/password/auth`, so
  an unknown core stays editable. A name outside `internal/core/credentials.go`'s closed
  vocabulary fails `RunAdapterSuite`.
- **The clients table costs no columns.** `client_credentials (client_id, inbound_id, key)`
  ships with MTProto as a real tenant, and `TestClientRecordColumnsAreFrozen` makes adding
  `ocserv_password` require deleting a line from a guard.
- **The capability rules cost one table entry**, not three implementations (P1).
- **Registration is genuinely two lines** in `internal/cores/cores.go`, and `Kinds()` derives
  the validator from it (P4).

### 11.2 The audited cost of the next real core

> This section is an **audit, read from source**. §11.2a is the **measurement, compiled** —
> what core #3 actually cost. Where they disagree, §11.2a wins; it found three sites this
> enumeration missed and twice the TypeScript.

Counted for `ocserv` — a daemon core with its own users, its own credential, and a config
**file** rather than a URI, i.e. the cheapest realistic non-Xray core. **24 shared files, of
which exactly 2 are the registration lines**, plus **two** directories rather than one
(`internal/cores/internal/ocserv/` for the adapter and `internal/ocserv/` for the daemon
engine — the split `mtproto` already has), plus ~11 TypeScript files.

**Fails loudly if forgotten — 7 files across 6 rows.** These are the ones §11.3 is already
true for.

| File | Cost | Caught by |
|---|---|---|
| `internal/cores/cores.go` | import + `Register` | compile |
| `internal/database/model/model.go` | one `Protocol` constant | `TestProtocolSourcesAgree` |
| `internal/web/translation/{en-US,fa-IR}.json` | 2 files | `i18n-dead-keys.test.ts` |
| `tools/openapigen/main.go` | `StructAllow` entry | `build-openapi.mjs` |
| `internal/arch/dispatch_ratchet_test.go` | budget + total, both ways | itself |
| `internal/core/credentials.go` | only if a credential leaves the vocabulary | `RunAdapterSuite` |

**Fails silently if forgotten — 17 files.** Every one still carries per-protocol logic; none
errors when a new kind is missing from it. Site counts are mtproto-shaped branches, i.e. the
ones a second non-Xray core must be added to; the ratchet's per-file budget is larger because
it counts every protocol constant.

| File | What it decides for a kind |
|---|---|
| `internal/core/capability.go` | whether the kind may use tls / reality / stream / sniffing |
| `internal/web/service/inbound.go` | 7 sites: settings validation, desired instances, protocol change |
| `internal/web/service/client_inbound_apply.go` | 4 sites: which core to hot-apply after a client edit |
| `internal/web/service/xray.go` | 2 sites shaped `Protocol != model.MTProto`, i.e. "assume Xray" |
| `internal/web/service/client_crud.go` | which credential to mint (`switch`, silent `default`) |
| `internal/web/service/inbound_clients.go` | the same switch again, for copy-to-inbound |
| `internal/web/service/client_credential.go` | hand-written `…CredentialRows` / `apply…Credentials` pair |
| `internal/web/service/client_link.go`, `client_portable.go`, `client_traffic.go`, `inbound_traffic.go` | share link, node-sync payload, quota reset |
| `internal/web/service/port_conflict.go` | TCP/UDP bits behind the inbound tag |
| `internal/sub/service.go`, `clash_service.go`, `json_service.go` | 15 sites across the three subscription formats |
| `internal/web/service/tgbot/tgbot_inbound.go` | 8 sites |
| `internal/web/web.go` | cadence + `AddJob` + `StopAll` for a core with its own sidecar |

Plus a per-core service file in a shared directory (`inbound_ocserv.go`, mirroring
`inbound_mtproto.go`'s 26 references) and, until supervision merges, a per-core reconcile job
(`job/ocserv_job.go`, mirroring `mtproto_job.go`).

**TypeScript is ~11 files, not 0**, and this is the sharpest gap between §11.3 and the tree.
The §9 `ui:`-tag descriptor renderer **does not exist anywhere**: `grep -rn 'ui:"'` over
`--include=*.go` matches nothing, and `frontend/src/lib/cores/` holds only `capabilities.ts`
and `client-credentials.ts`. The **inbound** form is still a hand-written ladder —
`InboundFormModal.tsx` has 25 `protocol === Protocols.X` comparisons, 9 of them shaped
`{protocol === Protocols.X && <XFields/>}`.

| File | Kind |
|---|---|
| `src/schemas/protocols/inbound/ocserv.ts` | new — the settings Zod schema |
| `src/pages/inbounds/form/protocols/ocserv.tsx` | new — the fields component |
| `src/schemas/protocols/inbound/index.ts` | edit — the discriminated union `fillProtocolSettingsDefaults` reads |
| `src/pages/inbounds/form/protocols/index.ts` | edit — export |
| `src/pages/inbounds/form/InboundFormModal.tsx` | edit — ladder, picker list, transport/security gating |
| `src/lib/xray/inbound-defaults.ts` | edit — `createDefaultInboundSettings`, ends `default: return null` |
| `src/lib/xray/inbound-form-adapter.ts` | edit — `clientSchemaForProtocol`, ends `default: return null` |
| `src/lib/xray/inbound-tag.ts` | edit — `inboundTransports`, the mirror of `port_conflict.go` |
| `src/pages/inbounds/info/helpers.ts` | edit — `LINK_PROTOCOLS` / `hasShareLink` |
| `src/pages/inbounds/list/helpers.ts` | edit — `isInboundMultiUser`, ends `default: return false` |
| `src/models/dbinbound.ts` | edit — protocol flags |

**None of them fails loudly.** Every TS dispatcher takes `protocol: string` and ends in a
silent `default:` — `return null`, `return false`, or nothing rendered. The failure mode is a
blank form or a missing share button on a working inbound, found by a user rather than CI.
There is no frontend equivalent of the dispatch ratchet.

### 11.2a MEASURED: what core #3 actually cost (P6, `wgkernel`)

The audit above is an estimate. This is the count, taken from the tree, for a core with a
**client-side artefact** — kernel WireGuard, whose product is a `.conf` file rather than a
URI. It is the first number in this document that was compiled rather than read.

**Ratchet: 123 → 116 → 126.** P6-2a's abstraction fix removed 7 sites; registering the kind
added 10. **Net +3 across all of P6**, against a plan that estimated +2. The residual the
fix could not absorb is **+10, not the estimated +8**:

| File | Budget | Why it could not be absorbed |
|---|---|---|
| `internal/web/service/client_inbound_apply.go` | 17 → **19** | two keyless-ID arms, not one: the add-time credential switch *and* `UpdateInboundClient`'s `newClientId` |
| `internal/web/service/tgbot/tgbot_inbound.go` | 8 → **10** | two copies of the same `excludedProtocols` map |
| `internal/sub/service.go` | 14 → **16** | `GetLink` arm + `genWireguardLink` guard |
| `internal/web/service/inbound.go` | 17 → **18** | `AddInbound`'s own copy of the credential switch |
| `internal/web/service/port_conflict.go` | 7 → **8** | UDP, per §11.2's row |
| `internal/sub/clash_service.go` | 6 → **7** | `buildProxy` |
| `internal/sub/json_service.go` | 8 → **9** | `getConfig` outbound switch |

**Three sites §11.2's enumeration missed**, all found by hand after the plan called the
enumeration complete: `inbound.go`'s `AddInbound` credential switch, `client_inbound_apply.go`'s
`UpdateInboundClient` id resolution — both `default:` arms that demand `client.ID != ""` from a
kind that has no id — and `json_service.go`'s `"protocol": string(inbound.Protocol)`, which
emitted a kind name Xray has no outbound for. Budget core #4 for arms of that shape wherever a
`default:` assumes a UUID.

**Files, counted.** Registering the kind edited **12 shared Go files and added none**, and
needed 5 Go test files. The core itself is the two directories §11.2 predicts —
`internal/wireguard/` (8 files incl. the `wgtest` fake, 2 test files) and
`internal/cores/internal/wireguard/` (2 files, 2 test files) — which is the irreducible
daemon-specific half and is not the architecture's cost. Then **10 i18n entries** (5 English
keys × 2 locales), **21 TypeScript files — 3 new and 18 edited** — 11 frontend test files, and
6 generated files that `make gen` rewrote.

**TypeScript was supposed to be 0.** §11.3 budgets **zero** TS files for a new core; the
measured cost is 21, roughly double §11.2's own ~11-file estimate, because a core with a
client-side artefact also pays the `.conf` renderer, the QR modal, the info modal, the three
`INBOUND_PROTOCOL_COLORS` copies and the online-rollup list. **The §9 descriptor renderer is
the single largest unpaid debt in this document, and P6 raised its price rather than lowering
it.** Nothing here is close to §11.3's "5 shared files, ~29 lines".

**Which guards actually fired.** Honestly split, because the ratio is the finding:

- **Fired, and could not be silenced:** `TestProtocolSourcesAgree` (the `model.Protocol`
  constant, the registry and the generated TS union must agree three ways), the compile error
  from `cores.Register`, `i18n-dead-keys.test.ts` in both directions, `gen-check` on the
  regenerated `frontend/src/generated/` + `openapi.json`, and `capabilities.test.ts` via the
  Go-generated golden fixture.
- **Fired only as a count.** `TestProtocolDispatchRatchet` reports "*… has 17 protocol
  dispatch sites but the budget still says 19 — lower it in this PR*", which is the message an
  author silences by lowering the number. Measured, before the tests below existed: collapsing all three
  `case "wireguard", "wgkernel":` arms left every package test green and the ratchet was the
  only complaint. It guards the count, not the behaviour.
- **Did not fire at all.** `internal/sub/service.go:474`'s hardcoded
  `protocol in ('vmess',…)` SQL list — a string literal the detector cannot see, and without
  the kind in it every subscription in all three formats returns an empty body. And **every**
  TypeScript dispatcher, all of which end in a silent `default:`.

The three unguarded classes each now have a test that fails without its arm
(`TestGetInboundsBySubIdIncludesWireguard`, `TestWgkernelClientSurvivesTheCredentialSwitches`,
`wgkernel-inbound.test.ts`), but writing one per site per core does not scale — that is the
argument for items 1 and 3 of §11.3, not a substitute for them.

### 11.3 The TARGET, and what it still needs

| Where | Files | Lines |
|---|---|---|
| `descriptor.go`, `settings.go`, `config.go`, `driver.go`, `traffic.go`, `driver_test.go` | 6, **all new, one directory** | ~590, of which **~450 is irreducible daemon-specific logic** |
| `internal/cores/cores.go` | shared | 2 |
| `en-US.json` + `fa-IR.json` | shared | 12 + 12 |
| `tools/openapigen/main.go` (`StructAllow`) | shared | 2 |
| `model/model.go` (one `Protocol` constant) | shared | 1 |
| **TypeScript** | **0** | **0** |
| **New API routes / DB migrations** | **0** | **0** |

**5 shared files, ~29 lines.** The measured figure for core #3 is 12 shared Go files plus 21
TypeScript ones (§11.2a), so this target is off by roughly an order of magnitude in the
direction that matters. Reaching it from §11.2's 24 needs, in rough order of payoff:

1. **The §9 descriptor renderer**, which is the whole TypeScript column — estimated at ~11
   files, measured at 21 for core #3. Nothing of it exists; P5 built the *client* half of the
   same idea and proved the shape works.
2. **The supervision merge** (§12.3), which removes `web.go` and the per-core job.
3. **`LinkRenderer` with an implementer** (§13, decision 1), which removes the three
   `internal/sub/` files and `tgbot_inbound.go`.
4. **Registry-driven credential minting**, which removes the `client_crud.go` /
   `inbound_clients.go` / `client_credential.go` trio — `CredentialDeclarer` already knows
   the answer, and nothing on the Go side asks it yet.

The `model.Protocol` constant is a mirror, not a list: `TestProtocolSourcesAgree` fails until
it matches the registry, so forgetting it is a red test rather than a protocol that
half-exists. It is not removable — Go needs to *name* a protocol without a bare literal. The
frontend's copy **was** removable and is gone: `protocol.ts` re-exports the generated union.

The soft number is `driver.go` (~250 LOC): larger for strongSwan (VICI) and accel-ppp
(kernel preflight), smaller for SSH. **That variance is the actual difficulty of the daemon,
not a cost the architecture imposes.** The architecture's contribution is the 5 shared files,
and *that* is the number to hold the design to.

---

## 12. Roadmap

| Phase | Content | Risk | Net LOC |
|---|---|---|---|
| **P-1** ✅ | **Guards only, zero behaviour change.** Landed in `internal/arch/`: both import fences (clean), dispatch ratchet seeded at **109**, model→core coupling pin, frozen `ClientRecord` columns, three-way protocol-source parity (`tun` pinned, see §13.5) | none | +740 |
| **P0** ✅ | `ClientTraffic` and `InboundConfig` → `internal/core` (**precondition, done**); `internal/xray` keeps aliases so 390 call sites are untouched. `internal/core` contract + `Counter` + `internal/cores/` + `coretest` + `TestSuiteCatchesBrokenAdapters`. Nothing calls the contract yet. **Measured effect: `internal/mtproto`'s dependency graph fell from 708 packages to 205, and from 142 xray-core packages to 0.** | none | +1100 |
| **P1** ✅ | Capability rules collapsed onto one table in `internal/core/capability.go`, generated into `frontend/src/generated/capabilities.ts` by openapigen and replayed through the TS evaluator by a Go-generated golden fixture. Dispatch ratchet **109 → 106**. ⚠️ It moved the rule *table* into Go but **not the enforcement** — see §2.1's correction | low | −168 / +72 in the deduplicated files |
| **P2** ✅ | **mtproto ported.** `internal/cores/internal/mtproto/` passes `RunAdapterSuite` with every invariant intact. The contract needed **one** correction (§12.1), and the engine's delta arithmetic was replaced by `core.Counter`, which removed the restart bug in §4. Not yet wired into the service layer — that is P4 | medium | +150 |
| **P3** ✅ | Port **xray** (stresses the contract in the opposite direction: one process, many inbounds, hot-apply via gRPC). `runtime.Local` dispatches every inbound add/update/delete through the registry; its MTProto branches went **9 → 2** (the two left are the per-user calls, see P4). Ratchet **106 → 99**. `Remote` untouched and wire-compatible. What the port found: §12.2 | medium | −120 |
| **P4** ✅ | ✅ Registry-backed validator: `Inbound.Protocol` carries `validate:"required,protocol"` and the rule is built from `cores.Kinds()`, so the accepted set *is* the servable set. The `oneof=` list is gone and `TestInboundProtocolIsValidatedByTheRegistry` stops it coming back. ✅ One traffic job bills every core: it loops the registry over TrafficSource + TagTrafficSource + OnlineReporter, and mtproto_job keeps only its reconcile. Supervision deliberately did NOT merge — see §12.3 | low | −150 |
| **P5** ✅ | `client_credentials` keyed `(client_id, inbound_id, key)` with MTProto as its first tenant; user ops routed through `bound.Users`; per-kind credential manifest served by `GET /panel/api/cores` and rendered by the client form. Ratchet **99 → 94**, then **94 → 95** on a frozen migration file. The **inbound** form was not touched — see §12.4 | medium | +2332 / −372 excluding generated files |
| **P6** ✅ | **Kernel WireGuard — the measurement, not a step.** Landed as three commits: the engine + adapter unregistered, then the abstraction fix alone (ratchet **123 → 116**, six protocol-string gates replaced by two registry questions), then the kind registered (**116 → 126**). **Net +3, worse than the +2 planned, and the residual was +10 against an estimated +8.** Its diff touches `internal/web/service/` in 5 files, so the roadmap's own trigger fired — the fix landed first and separately, which is why the reduction is measurable at all. Cost, in full: **21 TypeScript files against §11.3's budget of 0**, 12 shared Go files, 10 i18n entries, and 3 dispatch sites §11.2's audit had missed. Numbers and which guards fired: **§11.2a** | medium | +3702 core, +839 / −97 shared |

**P-1 is the whole bet.** Three of its four guards have zero violations *today*. Land them
while that is still true — after the refactor starts, every one becomes a negotiation.

**`runtime.Runtime` is never merged with the registry.** The registry answers *which
engine*; `internal/web/runtime` answers *which machine* (Local vs Remote over mTLS).
Separated, that is 2+N. Merged, it is a 2×N matrix — and `local.go` is already halfway into
that trap at N=2.

### 12.1 What porting core #1 corrected in the contract

The point of porting the smaller core first was to find out where the contract was wrong
while it was still cheap. It was wrong in exactly one place, and the fix strengthened the
suite rather than weakening it.

**`Check` stopped the core before it measured it.** `checkSupervisor` ended with `StopAll`,
so by the time `checkTraffic` ran there was no daemon left to scrape. The fake core in
`suite_test.go` never noticed, because its counters were a map in the test process — but
*every real core* keeps its counters inside the daemon. mtproto would have reported zero
bytes and the suite would have blamed the adapter. Teardown moved to a `checkTeardown` that
runs last, and `TestTrafficIsCheckedWhileTheCoreRuns` — a fake whose `CollectTraffic`
returns nothing while stopped — now fails if anyone reorders it back.

Three things the contract got right, confirmed under mutation:

- **`Bound` with nil-able fields.** The adapter declares five capabilities and implements
  six interfaces; not one `if _, ok := c.(X)` was needed outside `bind.go`.
- **`Counter` as the single delta engine.** Deleting mtproto's copy was a net *removal*, and
  the epoch and the backwards-counter backstop turn out to be independently sufficient —
  each alone still bills a restart correctly, which is why both stay.
- **Descriptor honesty is mechanically checkable.** Flipping one `core.No()` to `core.Yes()`
  fails `descriptor/capabilities-match` without any test naming that capability.

An adversarial review of the port then found that **the suite certified capabilities it never
called**. `Check` exercised `Reconcile`, `PlanChange` and `CollectTraffic`; `AddUser`,
`RemoveUser`, `OnlineEmails` and `ResetQuota` were verified at the type level only. A
`UserProvisioner` returning bare `nil` — the exact stub `caps.go` was written to prevent —
passed. It also asserted byte *totals* and never which client they were billed to, so a core
charging the right bytes to the wrong user passed too, on the one data structure the whole
design rests on. Both are now checked, against the daemon rather than the adapter, via a new
`Rig.ServedUsers`; five more entries in `TestSuiteCatchesBrokenAdapters` keep them honest.

The same review found two defects in `Counter` (§4.2), each able to double-bill a user's
entire cumulative usage. Neither was reachable before P2 because nothing called `Counter` yet.

Two obligations the port revealed, both now written down rather than discovered later:

- **A destructive read cannot serve two capabilities.** mtg returns bytes and online status
  from one `/stats` scrape, and scraping twice would advance the byte counters and discard
  the second delta. `OnlineEmails` replays the last scrape instead. Any core whose
  `TrafficSource` and `OnlineReporter` share a source owes the same treatment.
- **`Kinds()` is not `Describe().ID`.** They coincide for mtproto and will not for accel-ppp,
  which answers `l2tp`, `pptp` and `sstp` from one descriptor.

### 12.2 What porting core #2 corrected, and what it found

The contract itself needed one change: `Instance` carries the inbound's three JSON sections
as **plain strings**, not one blob and not a struct of `json.RawMessage`. Both alternatives
reformat. `encoding/json` compacts a `RawMessage` on the way out, `WireguardClientsToPeers`
indents what it emits, and `InboundConfig.Equals` compares bytes — so a conversion that
changes nothing semantically still reads as a config change and restarts Xray under live
connections. The same reasoning killed the adapter's rebuild of `settings.clients` from
`Instance.Users`: a `map[string]string` cannot hold wireguard `allowedIPs` or vless
`testseed`, and re-marshalling sorts the keys.

So for Xray the stored blob is the authority and `Users` is the read model. mtproto renders
*from* `Users`, because its `SecretEntry` is a small typed struct that loses nothing.
`instanceOf` keeps the two in sync, and each core reads whichever is lossless for it.

**What P3 found and left, now RESOLVED.** `XrayService.GetXrayConfig` did not merely call
`GenXrayInboundConfig`: it rebuilt `settings.clients` from the **clients table**, filtered by
traffic and expiry state, attached fallbacks, and preprocessed `streamSettings` (xhttp
session-key lifting, finalmask/REALITY stripping, XMC mask validation, `externalProxy`
deletion). So the panel had **two** inbound renderers that disagreed, and P3 deliberately
kept `Local`'s output byte-identical rather than widening the port mid-phase.

They are one renderer now. The block is `XrayService.RenderInbound`, and `runtime.Local`
reaches it through `LocalDeps.RenderInbound` — injected the way `XrayBaseConfig` already
reaches the cores registry, so `runtime` still does not import the service layer. A nil
result means the local Xray does not serve that inbound and the stored sections stand, which
is how an mtproto inbound reaches its own core untouched.

**What the divergence actually cost**, and the reason this was not cosmetic: an inbound
edited while Xray ran kept clients the quota job had already disabled, lost its fallbacks,
and carried a finalmask that panics Xray-core under REALITY. Then it compounded —
`InboundConfig.Equals` compares bytes, so after any hot apply the running inbound no longer
matched the generator and every restart check afterwards read a pending change.

It landed golden-fixture-first, as required above: `testdata/xray_inbounds.golden` pinned the
output, the extraction had to leave it unchanged, and
`TestHotApplyRendersWhatARestartWouldApply` then compared both paths byte for byte.
`RenderInbound` reads the quota-disabled emails itself rather than trusting the row's
preloaded `ClientStats`, which only `GetAllInbounds` populates — otherwise the render would
depend on how the caller loaded the row.

### 12.2b The Xray restart signal goes to a counter nothing bills from

Found by auditing P-1..P4, **left alone deliberately** after an attempted fix broke billing.

The counter that bills lives on the xray *adapter's* `XrayAPI`. The only thing that pushes a
restart into it is `Core.noteRestart`, called from `Core.Reconcile` — which has **no
production caller**. Real restarts go through `XrayService.RestartXray`, which calls
`NoteCoreRestart` on `s.xrayAPI`: a different handle whose counter is read only by
`XrayService.GetXrayTraffic`, and that method is now **dead** (the traffic job stopped calling
it when it became registry-driven). So the out-of-band restart signal reaches nothing.

**Why this is nearly harmless.** `api.go` passes an empty epoch and says so: the design is
"NoteCoreRestart, with the backwards-counter backstop behind it". A restarted Xray counts from
zero, so the first post-restart reading is *below* every stale baseline and the backstop
re-baselines correctly. The signal only matters when a reading comes back at or above its old
baseline, which needs a subject to move more in one 5s poll than it had moved in its entire
prior life. The backstop is doing the work, and it is sufficient.

**The fix that did not work — and why its evidence is void.** Detecting the restart in
`connect()` — re-priming when `c.mgr.Current()` returns a different `*Process` — reads
correctly and passes the whole suite, and then billed **nothing at all** on a live rig: two
controlled A/B runs, zero delta with it and ~204 KB with it reverted.

That result proves nothing. The rig shared its Xray API port with another panel's core (§14),
so roughly half of every run's reads were served by a process the change never touched. The
reasoning at the time was still sound — `NoteSourceRestart` only clears `last`, so the failure
mode should have been over-billing, not silence — and that unexplained gap is why it was
reverted rather than shipped. Re-run the A/B on an isolated rig before concluding anything.
Whoever picks this up should also delete `XrayService.GetXrayTraffic` and the `NoteCoreRestart`
call in `RestartXray`; both are wired to nothing.

### 12.3 What the job merge needed from the contract

The two jobs look mergeable — both cores implement `Supervisor`, `TrafficSource` and
`OnlineReporter`, so a single job could loop over `registry.Cores()`. Reading them says
otherwise. `TrafficDelta` is **per-user only** (`Email`, `Tag`, `Up`, `Down`), and
`xray_traffic_job` consumes something the contract cannot express: `[]*xray.Traffic` rows
carrying **inbound and outbound totals** with an `IsInbound` flag, used by
`outboundService.AddTraffic`, the external-inform POST, and the WebSocket frame.

`activeInboundTags` is fine — it derives from the deltas' `Tag`. Outbound accounting is not.

**RESOLVED: `TrafficSource` stays per-user; a separate optional `TagTrafficSource` reports
tagged subjects.** What settled it was reading the counters rather than the jobs. Xray's
stats API exposes `inbound>>>`, `outbound>>>` and `user>>>` as **three independent
families** — an inbound's total is not the sum of its users', and a `dokodemo` or `tunnel`
inbound has no users at all. Widening `TrafficSource` would therefore have made every core
return a heterogeneous list, and forced every consumer to filter it.

The split falls out of that:

| subject | who can answer | in the contract |
|---|---|---|
| per-user bytes | every core | `TrafficSource` — the one universal question, and what the unified quota is built on |
| the core's own inbounds | Xray must report them; a core whose inbound totals *are* the sum of its users' can derive them | `TagTrafficSource`, optional |
| egress | only a core that meters its own outbounds | same interface — see below |

Egress does **not** get a capability of its own. A core either meters its own outbounds
(Xray) or routes through one that does — mtproto's `routeThroughXray` bridge — and in the
second case the bytes are already counted by the first. That is exactly why `mtproto_job`
skips the inbound rollup for routed tags today.

mtproto deliberately does **not** implement it: its inbound totals are the sum of its
clients', so the job derives them, which is the case the interface's doc comment describes.

One trap the implementation had to dodge, and the reason this is not two calls to the same
API: **the stats read is destructive.** Xray's tag deltas arrive on the same scrape as the
per-user ones, so `CollectTraffic` banks them and `CollectTagTraffic` drains them —
accumulating between drains so a caller that polls users more often than tags loses nothing,
and clearing on drain so the same bytes are never billed twice.

**What the job merge still has to decide — the cadences differ.** `cadenceXrayTraffic` is
`@every 5s` and `cadenceMtproto` is `@every 10s` (`web.go:289-290`), so one loop means
picking one. Neither choice is free: 5s doubles the `/stats` scrape rate against every mtg
sidecar, and 10s halves the dashboard's traffic resolution and doubles the window a client
can overshoot its quota in. The per-core polling interval may need to be a fact the core
states rather than a constant the panel picks.

The second question is shape. `mtproto_job` also **reconciles** — it restarts sidecars that
died — and that is supervision, not accounting. Folding it into the traffic job couples the
two; keeping it separate but registry-driven would satisfy `TestJobCountDoesNotGrowPerCore`
just as well, because what that guard forbids is a job *per core*, not a second job. **That
guard does not exist yet** (§10), so nothing currently stops the next core shipping a third job.

### 12.4 What P5 landed, and what it deliberately did not

Five commits, `8afd5ed7..e2478640`:

| Commit | What |
|---|---|
| `8afd5ed7` | `GET /panel/api/cores` — id, i18n title key, the kinds each core serves, tri-state capabilities. Both lists sorted, because `Registry.Cores` is in registration order and an unsorted view made `gen-check` flap. No UI consumed it yet |
| `c3d17d10` | `client_credentials (client_id, inbound_id, key)`, migration in `db.go`, MTProto's `secret` and `ad_tag` moved onto it and off `ClientRecord` — so the frozen-column guard **ratcheted down by two** |
| `e5cc0bc5` | `Local.AddUser`/`RemoveUser` resolve `bound.Users` off the registry, so `core.UserProvisioner` finally has a production caller; `local.go` no longer imports `internal/xray` (`remote.go` still does, for the `xray.ClientTraffic` alias on the mTLS wire). Five hand-built credential maps deleted. Ratchet `local.go` 2 → entry gone, `client_bulk.go` 1 → gone, `client_inbound_apply.go` 12 → 10, total **99 → 94** |
| `dbcdf012` | `core.CredentialDeclarer` + `internal/core/credentials.go`'s closed vocabulary + the descriptor-driven **client** form. `auth` had been rendered for every client on every protocol; it is Hysteria-only |
| `e2478640` | Backfill restricted to mtproto inbounds. Found on the live panel's real data: a client on both a VLESS and an MTProto inbound had an MTProto secret stored against the VLESS one. `db.go` 5 → 6, total **94 → 95** |

Two fixes fell out of the port and are worth knowing about: `9b303d63` (bulk-add could not
see mtproto inbounds) and `62c5b91c` (one email must be one live registration on key-indexed
inbounds).

**What P5 did NOT do.** Each was a decision, not an oversight, and P6 should plan around it:

- **The other 11 per-protocol columns stay on `clients`** — `uuid`, `password`, `auth`,
  `flow`, `security`, `reverse`, and WireGuard's five (`wg_private_key`, `wg_public_key`,
  `wg_allowed_ips`, `wg_pre_shared_key`, `wg_keep_alive`). They are Xray's legacy debt and
  they ride the node-sync wire format and the subscription path, so moving them buys nothing
  today. The point was to ship the table **with a real tenant** rather than as speculative
  architecture; `TestClientRecordColumnsAreFrozen` holds the line.
- **Credential *storage* generalised; credential *minting* did not.**
  `client_credential.go` still holds a hand-written `mtprotoCredentialRows` /
  `applyMtprotoCredentials` pair, and `client_crud.go` and `inbound_clients.go` still mint
  credentials from a `switch` with a silent `default`. **Partly RESOLVED in P6-2a:**
  `cores.ClientCredentials` now backs the four WireGuard key gates, so `CredentialDeclarer`
  has a Go caller; minting itself still switches on the protocol string.
- **Supervision did not merge.** `mtproto_job` still reconciles sidecars on its own 10s
  cadence beside the traffic job's 5s (`web.go:289-290`), and `web.go` still calls
  `mtproto.GetManager().StopAll()` by name. §12.3 is still the open design.
- **The inbound form is untouched.** No P5 commit touches `frontend/src/pages/inbounds/form/`
  or `frontend/src/lib/xray/`. The descriptor idea was proven on the *client* form only, and
  §9's `ui:`-tag renderer remains unwritten — which is the whole of §11.2's TypeScript column.
- **The enforcement hole from §2.1 is still open.** `CapTLS`, `CapReality`, `CapStream` and
  `CapSniffing` are still evaluated by nothing on any write path.

**RESOLVED for supervision, and it kept both cadences.** `mtproto_job` is gone;
`core_supervise` loops `registry.Cores()` over `Supervisor.Reconcile` at `@every 10s`, and
shutdown loops the same registry over `StopAll`, so a core is converged and stopped because
it is registered. The cadence question turned out to be two questions, not one: the `@every
1s` job is a **liveness** check (`DidXrayCrash()` is two atomic loads) while a reconcile
rebuilds a core's desired set from the database. Merging them would either slow crash
recovery 10x or multiply the DB work by 10, so liveness stays at 1s and convergence stays at
the 10s the one reconciled core already ran at. No core states an interval, because no core
needs a different one yet.

**What did not merge: Xray, and it is not the job's fault.** `Deps.XrayBaseConfig` is *not*
the whole panel-owned config — `GetXrayConfig` injects subscription outbounds, the panel
egress, node egresses and the mtproto SOCKS bridges **after** the inbound list, and
`Config.Equals` compares inbounds positionally while the core sorts them by tag. Calling the
xray core's `Reconcile` from a timer would therefore restart Xray every 10 seconds *and*
drop every node egress and mtproto bridge, and it would also resurrect a manually stopped
Xray (`isManuallyStopped` lives in `XrayService`, not in the core). So `cores.PanelConvergedCore`
names the one core the panel still converges itself. Removing it needs the base config to
become complete and the manual-stop fact to move into the engine manager — real work, not
wiring, and the last thing standing between core #11 and free supervision.

---

## 13. Open decisions

1. **The fourth link-generation implementation.** There are three today (`internal/sub/`,
   `frontend/src/lib/xray/`, `docs/lib/xray/`); the docs copy drifts silently because
   `docs-ci.yml` fires only on `docs/**`. Either accept the drift with a golden cross-check,
   or make the Go `LinkRenderer` the sole producer and have the frontend and docs consume
   rendered output. The second is more work now and is the only one that stops being a bug
   source at 11 cores. *Mitigating fact:* new cores emit config **files** (`.ovpn`,
   `wg0.conf`, `.sswan`, `.p12`), not URIs — so `Share.Kind == "file"` plus a backend
   endpoint means zero frontend link logic per core. Do not let an `.ovpn` template appear
   anywhere under `frontend/src/`.
2. ~~**`openapigen` drops const values.**~~ **RESOLVED.** A named string type with constants
   is now emitted as a closed set everywhere: `types.ts` gets the union, `zod.ts` gets
   `PROTOCOL_VALUES` plus `z.enum(PROTOCOL_VALUES)`. The rule is general, not a protocol
   special case — `ProcessState` became a union in the same change.
   `frontend/src/schemas/primitives/protocol.ts` is now a re-export, and `Protocols` is a
   mapped type over the generated list, so it is exhaustive by construction. **Core #11
   touches no TypeScript at all.**
3. **SoftEther vs from-source accel-ppp** for the SSTP/L2TP/PPTP family — packaged and
   opaque, versus unpackaged with a DKMS tax. Deferrable until phase 6+.
4. **Whether SSH ships at all** without a Go SSH server to give it real accounting.
5. ~~**Does p-ui support Xray `tun` inbounds?**~~ **RESOLVED: yes, supported.** `model.Tun`
   now exists, the xray core claims the kind, and `knownProtocolDivergence` is empty — all
   three protocol sources agree for the first time. P3 forced the decision by making an
   unknown protocol an error: `tun` had no constant, so the kind list built from the
   constants omitted it and tun inbounds stopped being served. Keeping it was the only
   option that did not break inbounds admins had already created.

   The lesson generalises: whenever a guard compares against "the protocols", ask *which*
   list it is really checking. P4 removed the question — the validator asks the registry, so
   the accepted set and the servable set are one list and cannot disagree.

---

## 14. Resolved: a hot-added client went unbilled — two Xray cores shared one API port

Found while verifying quota enforcement on a live rig (2026-08-05), and resolved the same day.
It was never an accounting defect. The collector, the delta engine and the write path were
correct throughout; they were being fed by **two different Xray processes at random**.

**Root cause.** The config template ships a fixed Xray gRPC API port (`62789`). A second p-ui
on the same host renders the same port, and Xray sets `SO_REUSEPORT` on its listeners — so the
second bind **succeeds silently** and the kernel load-balances connections across both
processes. From then on every `AddUser`, `RemoveUser` and `QueryStats` lands on a coin-flip:
clients are provisioned into one core while traffic is read from the other.

The rig and the box's production panel had both picked `62789`:

```
LISTEN 127.0.0.1:62789  pid=346980   rig xray      (cwd=/root/smoke)
LISTEN 127.0.0.1:62789  pid=52795    prod xray     (cwd=/usr/local/p-ui)
```

**What settled it.** Polling the API repeatedly returned two disjoint answer-sets, alternating:

```
18 counters   in-30443-tcp, live-reality2-30446, smoke-in-20001, live-plain-30445   + fresh@x, hotadd@x
14 counters   live-reality-30443, smoke-in-20002, live-plain-30445                  + quota-test@x
```

Production's on-disk config declared only the `api` inbound, so every inbound and user in its
answer-set had been injected into it by the *other* panel over the shared port. Production's
Xray was even listening on the rig's test ports, load-balancing real user connections with it.

**Why three explanations were wrong.** Each was built on a measurement that silently sampled a
different core than the one being reasoned about. A counter "vanished" (the other process
answered), moved **backwards** (ditto), and a client's traffic was "dropped by the panel" (its
counter only ever existed on one of the two). A restart-signal fix that "zero-billed" in an A/B
test had half its reads served by the wrong process. The lesson is narrow and worth keeping:
when a measurement contradicts code that reads correct, first prove the measurement addresses
one subject.

**Symptoms this explains** — all reported, none an accounting bug:

| symptom | actual cause |
| --- | --- |
| hot-added client billed 0 | its counter exists on only one of the two cores |
| a share link times out | half the connections reach a core without that client |
| counters move backwards | consecutive reads answered by different cores |
| user removed but still connects | `RemoveUser` applied to the other core |

**The fix** (`internal/xray/process.go`). `Start` dials the API port before exec'ing and
refuses when anything already answers, because Xray itself will not complain. A non-Xray holder
of the port fails the bind loudly on its own, so only the Xray-on-Xray case needs catching.
Failing closed is deliberate: a panel that cannot tell which core it is talking to must not run.
The same applies to an orphaned Xray left by a crashed panel — previously the panel would
silently share with its own zombie.

Not fixed by randomising the port: that lowers the odds of a collision without removing the
failure, and leaves every existing install on `62789`. Not fixed by identifying the core after
connecting either — with `SO_REUSEPORT` the assignment is per connection, so a one-time check
proves nothing about the next call.

**Consequence for the accounting code: leave it alone.** `Counter`, `collectCoreTraffic` and
`GetTraffic` were verified correct on live data throughout this investigation, including the
cross-core sum (389.4 MB xray + 156.6 MB mtg = 546.0 MB on one row, to the byte).

---

## 15. Updating a core's daemon — `VersionManager`

The panel can already switch Xray builds: list GitHub releases, map the architecture,
download, unpack, restart. All of it lives in `ServerService`, keyed to Xray, reachable at
`POST /panel/api/server/installXray/:version`.

**mtg has none of it.** Its binary arrives once, at release build time, fetched by
`release.yml`; the panel cannot see its version or change it. Core #3 would need a third
implementation, and each one drags a GitHub client, an arch table and an unpack routine into
the web layer — where none of them belong. This is the per-core cost the contract exists to
remove, and it is the capability the project needs most after accounting: a core's daemon
outlives the panel release that shipped it, and a CVE in one should not wait for the other.

`core.VersionManager` is the slot:

```go
Installed(ctx) (string, error)
Available(ctx) ([]string, error)
Install(ctx, version) error
```

Three properties are deliberate.

**A core owns its release channel.** Xray's is `XTLS/Xray-core`, mtg's is
`mhsanaei/mtg-multi`, and a future core's may be an apt repository or a vendor tarball. The
panel asks *which versions* and *install this one*; it never learns where they come from. That
is what keeps the GitHub client out of `ServerService` and stops core #11 adding a fourth
copy.

**Install does not restart.** Replacing the binary and reloading the daemon are separate
steps, so the panel can stage an upgrade and apply it when traffic allows — and so a failed
download never leaves a core stopped. Restarting stays `Supervisor`'s job.

**`Installed` answers when the binary is missing.** "Not installed" is a normal state for a
core the admin has never used, and the UI has to render it rather than error.

### 15.1 What lands, in order

1. `xray` implements it by moving the existing `ServerService` logic behind the interface —
   behaviour-preserving, and `installXray/:version` keeps working by delegating.
2. `mtproto` implements it against the `mtg-multi` releases the release workflow already
   consumes, which is the first time that binary becomes updatable at all.
3. One pair of endpoints replaces the per-core one: `GET /panel/api/cores/:id/versions` and
   `POST /panel/api/cores/:id/install/:version`, with `Bound.Versions == nil` meaning the UI
   hides the control for that core.
4. `coretest` gains a conformance case, so a core that reports a version it cannot install —
   or an `Available` list not containing `Installed` — fails its adapter suite.

Until step 1 lands the interface has no implementer, which is the same state `LinkRenderer`
is in: a declared slot the registry can already resolve, so adding it later is one method on
one core rather than a change to the contract.

