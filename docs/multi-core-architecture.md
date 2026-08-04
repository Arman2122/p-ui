# Penhoon UI — Multi-Core Architecture

> Design document for turning Penhoon UI from an Xray-core panel into a multi-core VPN
> panel (OpenVPN, IKEv2/IPsec, L2TP, OpenConnect, WireGuard/AmneziaWG, SSTP, SSH, …)
> with cross-protocol egress and one unified per-user quota.
>
> Status: **in progress** — P-1 through P2 are implemented; see the roadmap in §12 for what
> each phase landed and what is still a proposal. Companion to `docs/architecture.md`, which
> describes the system as it is today.
>
> Every measurement in §2 was taken against the working tree at `02bc8dcc`, before any phase
> landed, and is kept as the baseline the refactor is judged against.

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
    caps.go                       #   the optional capability interfaces (7 of the 9 below)
    bind.go                       #   Bound + bind() — the ONLY place assertions live
    traffic.go                    #   ClientTraffic (moved from internal/xray), TrafficDelta
    registry.go                   #   Registry, Register, Cores()
    requirement.go                #   per-core packaging/version facts, one table
    coretest/                     #   RunAdapterSuite — the conformance suite
internal/cores/                   # wiring only
    cores.go                      #   one import + one Register line per core. ONE file.
    internal/xray/                # concrete cores — importable ONLY from internal/cores/...
    internal/mtproto/
    internal/openvpn/
    internal/ocserv/
    ...
```

The nested `internal/` is the enforcement. Per `cmd/go`, code below a directory named
`internal` is importable only from the tree rooted at that directory's parent — so
`internal/web/service` importing `internal/cores/internal/openvpn` is a **compile error**.
It cannot be `//nolint`-ed, cannot rot, and costs **zero configuration lines per core**.

> Rejected: a `depguard` deny-list naming each core. One line per core, silently incomplete
> forever after the first omission — precisely the marginal-cost problem being solved.

### 3.2 Three mandatory methods, nine optional capabilities

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

| Capability | Purpose | Cores lacking it |
|---|---|---|
| `Supervisor` | `Reconcile(desired []Instance)`, `Spec()` | — (mandatory in practice) |
| `HotApplier` | apply user changes without dropping sessions | pptpd |
| `UserProvisioner` | add/remove/update one user live | — |
| `TrafficSource` | `CollectTraffic() ([]TrafficDelta, []string)` | ssh (no native counters) |
| `OnlineReporter` | who is connected right now | wireguard (handshake age only) |
| `QuotaEnforcer` | push a byte budget *into* the daemon | xray, ocserv |
| `RateLimiter` | per-user bandwidth cap | openvpn |
| `LinkRenderer` | produce the client's config/URI | — |
| `EgressProvider` / `EgressConsumer` | offer / consume a tunnel | varies |

Seven of these exist in `caps.go` today. `RateLimiter` and the egress pair are deliberately
absent until a core needs them — an interface with no implementor is a guess, and adding one
later costs a nil field, which is the whole point of `Bound`.

**Only `Reconcile` is genuinely mandatory.** A core that cannot reconcile desired state
cannot self-heal after a crash, so every panel restart becomes a correctness event.
`AddUser`/hot-apply are optimisations layered on top — which is exactly why the mtg sidecar
recovers for free today (`manager.go:366`).

`CollectTraffic` is **not** mandatory: SSH-VPN has no per-user byte counter worth the name,
and forcing it produces the `return nil` stub that `runtime/local.go:116` and `:125`
already demonstrate as a failure mode.

### 3.3 `Bound` — the step that actually removes the dispatch

Interface segregation *alone* just relocates `switch protocol` into scattered
`if h, ok := c.(HotApplier); ok` assertions. The fix is to probe **once**:

```go
// internal/core/bind.go — the ONLY file permitted to type-assert a capability.
type Bound struct {
    Core      Core
    Supervise Supervisor      // nil if unsupported
    HotApply  HotApplier
    Users     UserProvisioner
    Traffic   TrafficSource
    Online    OnlineReporter
    Quota     QuotaEnforcer
    Rate      RateLimiter
    Link      LinkRenderer
    Egress    EgressProvider
    Ingress   EgressConsumer
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
func Register(reg *core.Registry) {
    must(reg.Register(xray.New()))
    must(reg.Register(mtproto.New()))
    must(reg.Register(openvpn.New()))
}
```

Duplicate registration **panics** at boot (`database/sql`'s discipline, not sing-box's
silent overwrite).

The decisive argument is repo-specific: `make gen-check` is the **first step** of
`make verify` (`Makefile:30`, `:84`) and fails on a dirty tree. With `init()`-based
registration the registered set depends on the transitive import graph of whichever `main`
the generator links — so a dropped blank import **silently shrinks the generated frontend
schema** and `gen-check` still passes. Secondary: `make test-go` runs `-shuffle=on`
(`Makefile:49`), and package-global mutable registration turns order dependence into flakes.

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
signals. `xray/api.go` is P3's.

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

**Keep `Reset_: false`** on the Xray stats query — `api.go:748` already does this and it is
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
- **Baselines are never expired automatically.** A key absent from one reading is ambiguous —
  removed, or a partial scrape mid-reload — and dropping it bills a live subject its whole
  counter again. `Counter.Forget` is the only way a baseline is dropped, and it is the
  panel's job to call it when *the panel* removes a user.

### 4.3 Enforcement

`disableInvalidClients` (`service/inbound_disable.go:97`) is already the right *shape* —
it resolves email → set of `(inbound, node)` targets and revokes. Two changes:

- It calls `s.xrayApi.RemoveUser` **directly** for local targets, which both violates the
  `runtime.Runtime` layering rule and silently no-ops for an MTProto inbound (the tag isn't
  in Xray, so it returns "User not found"). Route through the registry instead.
- Add pushdown: for cores with `QuotaEnforcer`, push `min(remaining, band)` into the daemon
  (mtg `[secret-limits]` already does this; accel-ppp/RADIUS `Session-Octets-Limit`; ocserv
  per-user config) with band hysteresis so it does not flap.

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
| **B — marked egress** | xray → {openvpn, ikev2, amneziawg, ppp} | allocate `(mark, table)`; run the client daemon with `route-noexec`; install `default dev X` + `blackhole default` in the table from the daemon's up-hook; set `sockopt.mark` on the Xray outbound. |
| **C — L3 bridge** | any L3 inbound → anything | `ip rule from <pool-or-/32> table T`; T's default is another kernel tunnel, or a tun2socks / Xray-`tun` device fronting an L7 proxy. Plus MASQUERADE and MSS clamp. |

Everything else in the matrix is a parameterisation of A, B or C.

Two neat details worth knowing: strongSwan's `set_mark_in` (≥5.7.0) stamps inbound-decrypted
packets with a netfilter mark, making it **the cleanest per-inbound hook of any L3 core**;
and `openconnect --script-tun --script "ocproxy -D 127.0.0.1:P"` gives a SOCKS port with no
root and no routing at all, which makes xray→ocserv trivially Pattern A.

### 5.3 Resource allocation (put in the DB; nothing else touches these ranges)

| Resource | Range | Formula |
|---|---|---|
| Egress id | 1…999 | DB primary key |
| fwmark (data) | `0x0e000001`… | `0x0e000000 \| id` |
| fwmark (tunnel's own outer socket) | `0x0e0f0001`… | `0x0e0f0000 \| id` |
| fwmark mask | `0xff00ffff` | constant |
| Routing table | 30001…30999 | `30000 + id` |
| `ip rule` priority | 31001…31999 (data), 30001…30999 (outer) | |
| Tunnel device | `peg1`…`peg999` | ≤15 chars |
| Local SOCKS port | 21001…21999 | bound to `127.0.0.1` only |

All marks are ≤ `0x7FFFFFFF` so they fit Xray's `int32` `mark` field. Assert at startup
that the band is unused and that tables 30001–30999 are empty; check for collisions with
`wg-quick`'s 51820+ and sing-box's defaults.

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

**Freeze the `clients` column set.** `model.Client` is *already* a 10-credential union
(`ID`, `Security`, `Password`, `Flow`, `Auth`, `PrivateKey`, `PublicKey`, `PreSharedKey`,
`Secret`, `AdTag`) out of 26 fields; WireGuard alone cost five columns on `ClientRecord`.
openapigen emits this to the frontend, so the TS type for a VLESS client already carries
`preSharedKey?`. At 11 cores that is a ~60-optional-field type.

The real cost is not the columns — it is hand-written O(fields) code that **fails silently**.
`MergeClientRecord` (`model.go:1179`) is one `if existing.X != incoming.X && incoming.X != ""`
branch per field, on the node-sync path. Forgetting a field does not error; the merge drops
it and the symptom is "works on the master, not on the node". `ToRecord` and `ToClient`
repeat the mapping twice more.

```sql
CREATE TABLE client_credentials (
    client_id  int    NOT NULL,
    core_id    text   NOT NULL,
    version    int    NOT NULL DEFAULT 1,
    payload    jsonb  NOT NULL,
    PRIMARY KEY (client_id, core_id)
);
```

No FK (the repo runs `DisableForeignKeyConstraintWhenMigrating: true` at `db.go:1872` and
actively drops FKs). **No GIN index on `payload`, ever** — credentials are never queried by
content; every access is by PK or `core_id`. When a core needs real uniqueness, use a partial
expression index in that core's own migration. **The client-list endpoint must never join
this table.**

**Store credentials, do not derive them.** Derivation is *impossible* for ocserv (salted
one-way hash), OpenVPN (a CA signature; revocation needs CRL state) and MTProto (the secret
embeds the **inbound's** FakeTLS domain — `model.go:597` — so the same client's secret
differs per inbound). Add exactly one core-agnostic `clients.secret_seed bytea` as a default
*generator*, never as an invariant.

**An inbound whose core is unknown is quarantined** — never started, never deleted, never
re-marshalled, badged in the UI. The column is a plain varchar with no CHECK constraint, so
an old binary can already `SELECT` a row with `protocol = 'ocserv'`; the danger is the
*write* path, where `db.go:665-782` and `:1393-1620` do unmarshal→mutate→marshal round-trips.
Guard: `TestUnknownCoreRoundTripsByteForByte`. **Never remove or reuse a `Kind` constant** —
it is persisted on installs you will never see.

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

A requirement nothing enforces is not a requirement. Each of these is a test in
`make test-go` or `npm test`.

| Guard | Prevents | Seeded at |
|---|---|---|
| Nested `internal/` for cores | service layer importing a concrete core | compile error, 0 config |
| `TestModelImportsNoCores` | the model→xray coupling returning | 0 (after §2.2 fix) |
| **Dispatch ratchet, bidirectional** | regrowth of `switch protocol` | **109** |
| `TestCapabilityAssertionsOnlyInBind` | assertions becoming the new switch | 0 |
| `TestDescriptorMatchesInterfaces` | the descriptor becoming a lie | 0 |
| `TestEveryCoreKindIsAcceptedByTheValidator` | the `oneof` tag rotting | **fails today on `tun`** |
| `TestClientsTableHasNoPerCoreColumns` | the wide row | 0 |
| `TestJobCountDoesNotGrowPerCore` | 11 cores × 3 cron jobs | 0 |
| `TestUnknownCoreRoundTripsByteForByte` | downgrade data loss | 0 |
| Capability golden fixture (Go ↔ vitest) | the fourth `canEnableTlsFlow` | 0 |
| `TestSuiteCatchesBrokenAdapters` | the conformance suite decaying to a no-op | 0 |

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

| Where | Files | Lines |
|---|---|---|
| `descriptor.go`, `settings.go`, `config.go`, `driver.go`, `traffic.go`, `driver_test.go` | 6, **all new, one directory** | ~590, of which **~450 is irreducible daemon-specific logic** |
| `internal/cores/cores.go` | shared | 2 |
| `internal/core/requirement.go` | shared | ~8 |
| `en-US.json` + `fa-IR.json` | shared | 12 + 12 |
| `tools/openapigen/main.go` (`StructAllow`) | shared | 2 |
| **TypeScript** | **0** | **0** |
| **New API routes / DB migrations** | **0** | **0** |

**5 shared files, ~36 lines.** Compare to mtproto's ~30 files. Each of the 25 eliminated
files is eliminated by a *specific* guard above — not by optimism.

Files explicitly **not** touched: any `.tsx`, anything under `frontend/src/`,
`internal/web/service/*`, `internal/sub/*`, `runtime/local.go`, `runtime/remote.go`,
`internal/web/job/*`, `internal/database/db.go`, `model/model.go`, `endpoints.ts`,
`release.yml`.

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
| **P1** ✅ | Capability rules collapsed onto one table in `internal/core/capability.go`, generated into `frontend/src/generated/capabilities.ts` by openapigen and replayed through the TS evaluator by a Go-generated golden fixture. `tls` and `reality` are now enforced server-side too. Dispatch ratchet **109 → 106** | low | −168 / +72 in the deduplicated files |
| **P2** ✅ | **mtproto ported.** `internal/cores/internal/mtproto/` passes `RunAdapterSuite` with every invariant intact. The contract needed **one** correction (§12.1), and the engine's delta arithmetic was replaced by `core.Counter`, which removed the restart bug in §4. Not yet wired into the service layer — that is P4 | medium | +150 |
| **P3** | Port **xray** (stresses the contract in the opposite direction: one process, many inbounds, hot-apply via gRPC). `runtime.Local`'s 7 MTProto branches → 0. `Remote` untouched and wire-compatible | medium | −120 |
| **P4** | Registry-backed validator (kills the `oneof` tag). One traffic job for all cores; `mtproto_job` merges in. 2 jobs → 1 | low | −150 |
| **P5** | `client_credentials` + descriptor-driven UI + `GET /panel/api/cores` | medium | +700 |
| **P6** | **WireGuard — the measurement, not a step.** If its diff touches `internal/web/service/`, **stop and fix the abstraction** before cores #4–#11 land on it | — | ~600 |

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
2. **`openapigen` drops const values.** `tools/openapigen/walker.go:45,65` collects only
   `ast.TypeSpec` and never reads `token.CONST`, so `Protocol`'s constants are dropped and
   `generated/zod.ts` degrades to `z.string()` — which is *why*
   `frontend/src/schemas/primitives/protocol.ts` hand-duplicates the Go constants. Harvesting
   ValueSpecs and emitting `z.enum([…])` deletes the duplicate and gives the frontend a
   compiler-checked core list. **~30 lines, worth its own commit before P5.**
3. **SoftEther vs from-source accel-ppp** for the SSTP/L2TP/PPTP family — packaged and
   opaque, versus unpackaged with a DKMS tax. Deferrable until phase 6+.
4. **Whether SSH ships at all** without a Go SSH server to give it real accounting.
5. **Does p-ui support Xray `tun` inbounds?** Surfaced by P-1's parity guard and currently
   pinned in `knownProtocolDivergence`. `tun` is accepted by the `oneof=` validator tag and
   fully offered by the frontend (enum, schema arm, `createDefaultTunInboundSettings`), but
   has **no Go `Protocol` constant** — so every Go-side switch falls through to `default`
   for a tun inbound. Meanwhile `frontend/src/schemas/primitives/protocol.ts:21-23` claims
   "the Go backend's validator no longer accepts it", which is **false**. Either add the
   constant (declare it supported) or drop `tun` from the tag and keep the frontend's
   read-only rendering path (declare it legacy). Not a mechanical call — it changes whether
   existing tun inbounds can be edited.
