# Penhoon UI — Architecture & Code Map

> Navigation map for contributors and AI coding agents (referenced from `CLAUDE.md`).
> Goal: jump to the right file in one hop instead of grepping the whole tree.
> Tracks the `main` branch — paths reflect the latest changes, so verify against the live
> tree rather than a pinned release (Go module `github.com/Arman2122/p-ui`).
>
> **How to use this file:** read "Mental model" + "Request lifecycle" first, then
> use the **Symptom → File index** to locate work. Respect the **Layering rules**
> when adding code. Verify with the commands in **Build / Test / Lint**.

---

## 1. Mental model (the 30-second version)

Penhoon UI is a **web control panel for [Xray-core](https://github.com/XTLS/Xray-core)**. The Go
backend is the source of truth: it stores inbounds/clients/settings in a DB, renders an
Xray JSON config from that state, supervises the Xray child process, and exposes a REST +
WebSocket API. A React SPA (built by Vite, embedded into the Go binary) is the UI. A second,
separate HTTP server serves **subscription links** to end users.

The panel supervises **two managed child processes**: Xray-core itself and — when MTProto
inbounds exist — the `mtg-proxy` Telegram-proxy binary (`github.com/Arman2122/mtg-proxy`, a
multi-secret fork built from source; `internal/mtproto/`). One process per inbound serves
every attached client's FakeTLS secret through the fork's `[secrets]` section, plus optional
per-client sponsored-channel ad-tags via `[secret-ad-tags]`. A client or ad-tag edit is
hot-applied via the fork's management API (`PUT /secrets`, guarded by a per-process bearer
token), with a process restart as the fallback on older binaries.

Servers and processes, all launched from `main.go`:

| Server / process | Package | Purpose | Default port |
|---|---|---|---|
| **Panel** | `internal/web` | Admin REST/WS API + serves the embedded SPA | 2053 |
| **Subscription** | `internal/sub` | Public endpoint that hands out client configs (raw / JSON / Clash) | `subPort` setting |
| **Xray-core** | supervised via `internal/xray` | The actual proxy engine; a child process, not Go code | `inbounds[].port` |
| **mtg-proxy** | supervised via `internal/mtproto` | MTProto proxy child process for MTProto inbounds (multi-secret) | per inbound |

Two key ideas that explain most of the complexity:

1. **The DB → Xray config pipeline.** Inbounds/clients live in the DB. On every change the
   backend regenerates the Xray config and applies it — preferring a *hot diff* (live gRPC
   API mutation) over a full process restart. See §5.1.
2. **The Runtime abstraction (multi-node).** A panel can manage remote "nodes" (other Penhoon UI
   instances). Every state-changing inbound/client operation is dispatched through a
   `runtime.Runtime` interface that is either **`Local`** (this box's Xray gRPC API) or
   **`Remote`** (HTTPS call to a child node, with `verify`/`skip`/`pin`/`mtls` TLS modes).
   This is the single most important abstraction in the project. See §5.2.

---

## 2. Tech stack

**Backend (Go 1.27):**
- Web framework: **Gin** (`gin-gonic/gin`) + sessions (cookie store), gzip.
- ORM: **GORM** on **PostgreSQL** — the only backend (`PUI_DB_DSN`, required).
- Scheduler: **robfig/cron/v3** (seconds-precision) for all background jobs.
- Xray: **xtls/xray-core** vendored as a library; the panel talks to the running core over
  its **gRPC API** and also shells out to manage the process.
- Telegram bot: **mymmrac/telego**. i18n: **nicksnyder/go-i18n**.
- Misc: gorilla/websocket, gopsutil (system stats), go-qrcode, gotp (2FA TOTP).

**Frontend (`frontend/`):**
- **React 19** + **Ant Design 6** + **Vite 8** + **TypeScript**.
- Data layer: **TanStack Query** (`@tanstack/react-query`) over the native **Fetch API**; **Zod 4** schemas.
- Router: **react-router 8**. Charts: **uPlot** (`frontend/src/components/viz/Sparkline.tsx`). Editor: **CodeMirror 6**.
- **Build output goes to `internal/web/dist/`** (see `vite.config.js` → `outDir`) and is
  embedded into the Go binary with `go:embed`. Three HTML entries: `index.html` (panel SPA),
  `login.html`, `subpage.html`. The Go server serves the SPA; there is no separate frontend
  deployment.

**Important:** the legacy Go-template UI and `web/assets/` are **gone**. All HTML/JS comes
from the embedded Vite `dist/`. Don't look for `.html` templates in `internal/web`.

---

## 3. Request lifecycle (follow the data)

### 3.1 Admin API request (e.g. "add a client")

```
Browser (React, fetch)
  → POST {basePath}/panel/api/...
    → Gin engine (internal/web/web.go: initRouter)
      → middleware chain: SecurityHeaders → MaxBodyBytes (10 MiB; importDB exempt)
                          → [DomainValidator, if webDomain set] → gzip → sessions("p-ui")
                          → base-path/cache-control context → Localizer
                          → API routes add: ConfigEnvelope (zstd + SHA-256) → CSRF
        → Controller (internal/web/controller/*.go)   // HTTP concerns only: bind, validate, respond
          → Service (internal/web/service/*.go)        // business logic + transactions
            → GORM → DB (internal/database)            // persistence
            → runtime.Runtime dispatch                 // apply to Xray (Local) or node (Remote)
              → Local:  internal/xray (gRPC API or config regen + restart)
              → Remote: internal/web/runtime/remote.go → HTTPS → child node's API
```

The controller layer is thin. **Business logic lives in services.** When something is wrong
with *behavior*, the bug is almost always in a service file, not a controller.

### 3.2 Subscription request (end-user fetching their config)

```
End user → GET {subPath}/{subId}   (separate server, internal/sub)
  → internal/sub/controller.go (routes: raw / JSON / Clash variants, feature-flagged)
    → internal/sub/service.go (~2.5k lines — the link/config builder)
      → reads inbounds+clients+hosts from DB, renders per-protocol share links /
        Clash YAML / JSON (Host rows can override address/SNI/path per inbound)
```

### 3.3 Background work (cron jobs)

Scheduled in `internal/web/web.go` → `startTask()`. Each job is a struct in
`internal/web/job/`. Examples: poll Xray traffic every 5s, check client IP limits every 10s,
node heartbeat every 5s, periodic traffic resets (hourly/daily/weekly/monthly). See §5.4.

---

## 4. Directory map (what lives where)

```
p-ui/
├── main.go                     # Entry point: CLI (run / migrate / setting / cert),
│                               #   bootstrap, signal handling, restart loop
├── go.mod / go.sum             # Go deps (module path ends in /v3)
│
├── internal/                   # ALL backend Go code (private packages)
│   ├── arch/                   # ⭐ Architecture guards. No runtime code — every file is a
│   │                           #   test that parses the repo and fails on erosion: the
│   │                           #   protocol-dispatch ratchet, the core import fences, the
│   │                           #   frozen clients columns. Pure stdlib, so it runs on any OS.
│   ├── core/                   # ⭐ The core contract (multi-core refactor). capability.go holds
│   │                           #   the ONE table answering "may this inbound do X" (tls, reality,
│   │                           #   tlsFlow, fallbacks, sniffing, stream, ss2022) — generated into
│   │                           #   frontend/src/generated/capabilities.ts and cross-checked by
│   │                           #   frontend/src/test/capabilities.test.ts. Core is 3 methods;
│   │                           #   caps.go holds the optional capability interfaces, resolved
│   │                           #   once by bind.go into Bound. Also ClientTraffic (the
│   │                           #   email-keyed cross-core usage row), InboundConfig, and
│   │                           #   Counter — the single monotonic-counter→delta engine.
│   │                           #   coretest/ is the conformance suite every core must pass.
│   ├── cores/                  # Wiring only: one Register line per core, explicit not init().
│   │                           #   Concrete cores live under cores/internal/ so the compiler
│   │                           #   stops the service layer importing one directly.
│   │   └── internal/mtproto/   #   First core ported to the contract: adapts internal/mtproto.
│   │                           #   driver_test.go runs the conformance suite against a
│   │                           #   stand-in mtg that serves the real management API.
│   ├── config/                 # Env-var config: paths, DB DSN, log level, version
│   │                           #   Every PUI_* env var is read here (config.go)
│   ├── database/
│   │   ├── db.go               # InitDB: connect, AutoMigrate, seeders (~1.4k lines). DB hotspot.
│   │   ├── dialect.go          # Shared PostgreSQL SQL fragments (jsonb, LEAST/GREATEST, …)
│   │   └── model/              # **ALL GORM models** (model.go ~1.1k lines + siblings:
│   │                           #   node_client_traffic.go, node_client_ip.go,
│   │                           #   client_global_traffic.go). ⭐ Start here for data shape.
│   ├── eventbus/               # In-process pub/sub (buffered channel): outbound.down|up,
│   │                           #   xray.crash, node.down|up, cpu.high, memory.high, login.attempt
│   ├── tunnelmonitor/          # Optional tunnel health probe (PUI_TUNNEL_HEALTH_* env vars):
│   │                           #   HTTP probe (default Cloudflare trace); repeated failures
│   │                           #   trigger an Xray restart hook. Independent of panel settings.
│   ├── xray/                   # Xray-core integration (the proxy engine wrapper)
│   │   ├── process.go          # Spawn/supervise the Xray child process (~750 lines)
│   │   ├── api.go              # gRPC client to a running Xray (add/remove user, stats) (~800 lines)
│   │   ├── hot_diff.go         # ⭐ Compute minimal live changes to avoid full restart (~500 lines)
│   │   ├── config.go           # Xray config object model
│   │   ├── inbound.go          # Inbound JSON shaping
│   │   ├── client_traffic.go   # ClientTraffic model (persisted as client_traffics)
│   │   ├── traffic.go          # Traffic type helpers
│   │   └── log_writer.go       # Pipe Xray stdout/stderr into the panel logger
│   │
│   ├── web/                    # The panel server
│   │   ├── web.go              # ⭐ Server bootstrap: initRouter (all routes) + startTask (all cron jobs)
│   │   ├── controller/         # HTTP handlers (thin). One file per resource:
│   │   │   ├── inbound.go      #   /panel/api/inbounds
│   │   │   ├── client.go       #   /panel/api/clients (CRUD + bulk + ips + onlines)
│   │   │   ├── group.go        #   client-group endpoints
│   │   │   ├── node.go         #   /panel/api/nodes   (multi-node management)
│   │   │   ├── host.go         #   /panel/api/hosts   (per-inbound subscription host overrides)
│   │   │   ├── server.go       #   /panel/api/server  (status, xray version, certs, logs, DB import/export)
│   │   │   ├── setting.go      #   /panel/api/setting (settings + API tokens)
│   │   │   ├── xray_setting.go #   /panel/api/xray    (raw Xray config editor, WARP/Nord)
│   │   │   ├── api.go          #   /panel/api gateway (token auth, envelope + CSRF wiring)
│   │   │   ├── index.go        #   login/logout/csrf/2FA
│   │   │   ├── spa.go          #   SPA fallback for /panel UI routes
│   │   │   └── websocket.go    #   WS upgrade endpoint
│   │   ├── service/            # ⭐⭐ Business logic. This is where most real work happens.
│   │   │   ├── inbound.go              # Inbound CRUD core (~1.4k lines)
│   │   │   ├── inbound_node.go         # ⭐ Node sync for inbounds: reconcile, traffic merge (~1.1k lines)
│   │   │   ├── inbound_traffic.go      # Per-client traffic accounting (~1.1k lines)
│   │   │   ├── inbound_clients.go      # Client-within-inbound operations
│   │   │   ├── inbound_sublink.go      # Inbound-level subscription link helpers
│   │   │   ├── inbound_migration.go    # Inbound schema/format migrations
│   │   │   ├── client_crud.go          # Client create/read/update/delete
│   │   │   ├── client_bulk.go          # Bulk client ops (~1.6k lines)
│   │   │   ├── client_inbound_apply.go # ⭐ Apply client changes to runtime (Local/Remote) (~1.2k lines)
│   │   │   ├── client_groups.go        # Client grouping
│   │   │   ├── client_link.go          # Per-client share-link generation
│   │   │   ├── client_external_link.go # External links attached to clients
│   │   │   ├── client_wireguard.go     # WireGuard client specifics
│   │   │   ├── client_paging.go        # Server-side pagination/sort/filter for client lists
│   │   │   ├── node.go                 # ⭐ NodeService: CRUD, probe, heartbeat, dirty-tracking (~1.1k lines)
│   │   │   ├── node_mtls.go            # Node mTLS certificate management (master side)
│   │   │   ├── node_tree.go            # Node hierarchy / descendants
│   │   │   ├── host.go                 # Host rows (subscription output overrides)
│   │   │   ├── server.go               # ServerService: status, certs, xray install, DB ops (~2.2k lines)
│   │   │   ├── setting.go              # SettingService: all panel settings + defaults (~1.3k lines)
│   │   │   ├── setting_mtls.go         # mTLS settings (node hardening)
│   │   │   ├── traffic_writer.go       # Batched persistence of traffic deltas to the DB
│   │   │   ├── xray.go                 # ⭐ XrayService: config gen + restart/hot-apply (~1.2k lines)
│   │   │   ├── xray_setting.go         # Raw Xray config persistence
│   │   │   ├── xray_metrics.go         # Xray observability metrics
│   │   │   ├── metric_history.go       # Historical system/xray metrics
│   │   │   ├── reality_scan.go         # REALITY target scanner
│   │   │   ├── url_safety.go           # Outbound URL validation (SSRF guards)
│   │   │   ├── outbound_subscription.go# Outbound subscription (e.g. Warp/Nord provider configs)
│   │   │   ├── port_conflict.go        # Detect inbound port collisions
│   │   │   ├── fallback.go             # Xray fallback (SNI/ALPN routing on shared port)
│   │   │   ├── email/                  # Email notification service (SMTP)
│   │   │   ├── integration/            # External providers: warp.go (Cloudflare WARP), nord.go (NordVPN)
│   │   │   ├── outbound/               # Outbound config service
│   │   │   ├── panel/                  # Cross-cutting panel services:
│   │   │   │   ├── panel.go            #   panel-level helpers
│   │   │   │   ├── user.go             #   admin user auth (bcrypt)
│   │   │   │   ├── api_token.go        #   API token CRUD (SHA-256 hashed)
│   │   │   │   └── websocket.go        #   WS hub / push service
│   │   │   └── tgbot/                  # Telegram bot command handlers
│   │   ├── runtime/            # ⭐⭐ The Local/Remote node abstraction (see §5.2)
│   │   │   ├── runtime.go      #   the Runtime interface (the contract)
│   │   │   ├── local.go        #   Local impl → this box's Xray gRPC API
│   │   │   ├── remote.go       #   Remote impl → HTTPS calls to a child node
│   │   │   ├── tls_client.go   #   per-node HTTP client: verify / skip / pin / mtls
│   │   │   └── manager.go      #   RuntimeFor(nodeID) → picks Local or Remote
│   │   ├── job/               # Cron job structs (one file per job — see §5.4)
│   │   ├── middleware/        # Gin middleware: security.go (headers/HSTS), bodylimit.go,
│   │   │                      #   domainValidator.go, validate.go (CSRF), config_envelope.go
│   │   ├── global/           # Global singletons: web server + sub server handles, restart hook
│   │   ├── network/          # Custom net listeners (e.g. proxy-protocol aware)
│   │   ├── session/          # Session/cookie helpers
│   │   ├── websocket/        # WS hub implementation
│   │   ├── locale/ + translation/  # i18n middleware + locale JSON catalogs (en-US, fa-IR)
│   │   ├── entity/           # Shared request/response DTOs
│   │   └── dist/             # ⚠️ Vite build output, embedded via go:embed (generated — do not hand-edit)
│   │
│   ├── sub/                   # The subscription server (separate from panel)
│   │   ├── sub.go             #   server bootstrap
│   │   ├── controller.go      #   routes for raw / JSON / Clash subscription formats
│   │   ├── service.go         # ⭐ The link/config builder (~2.5k lines — share-link logic lives here)
│   │   ├── json_service.go    #   JSON subscription format
│   │   ├── clash_service.go   #   Clash/Mihomo YAML format
│   │   ├── clash_external.go  #   external Clash config integration
│   │   ├── external_subscription.go / external_config.go  # external sub import/aggregation
│   │   ├── host_sub.go        #   Host-row overrides applied to subscription output
│   │   ├── endpoint.go        #   subscription endpoint configuration
│   │   ├── vless_route.go     #   VLESS route shaping
│   │   ├── remark_vars.go     #   remark variable expansion
│   │   └── links.go           #   link helpers
│   │
│   ├── mtproto/              # Embedded MTProto (Telegram) proxy: manager.go +
│   │                         #   process.go supervision + orphans.go cleanup
│   ├── logger/              # App logger (op/go-logging + lumberjack rotation)
│   └── util/                # Leaf helpers (no business logic):
│       ├── common/          #   errors, misc
│       ├── crypto/          #   key/cert generation (x25519, ML-KEM/ML-DSA, ECH)
│       ├── link/            #   outbound share-link building primitives
│       ├── wirecodec/ + wireguard/  # WireGuard codec + integration helpers
│       └── random/, json_util/, reflect_util/, sys/, netproxy/, netsafe/, ldap/
│
├── frontend/                 # React SPA (built into internal/web/dist)
│   ├── vite.config.js        # ⭐ Build config: outDir → ../internal/web/dist, dev on :5173
│   │                         #   (strict) proxying to :2053, entries index/login/subpage.html
│   ├── package.json          # scripts: dev / build / preview / lint / typecheck / test / gen
│   └── src/
│       ├── main.tsx / routes.tsx / queryClient.ts   # SPA entry, router, query client
│       ├── entries/          # Extra HTML entry points: login.tsx, subpage.tsx
│       ├── pages/            # ⭐ Route screens. Mirrors the panel's feature areas:
│       │   ├── inbounds/     #   inbound list + the big inbound form (protocols/security/transport)
│       │   ├── clients/      #   client management screens
│       │   ├── nodes/        #   multi-node UI
│       │   ├── hosts/        #   subscription host-override UI
│       │   ├── xray/         #   raw Xray config UI (routing, dns, outbounds, balancers, overrides)
│       │   ├── index/        #   dashboard/home
│       │   └── settings/, groups/, sub/, login/, api-docs/
│       ├── api/              # ⭐ Data layer: http-init, QueryProvider, queryKeys, websocket bridge
│       │   └── queries/      #   TanStack Query hooks (useNodesQuery, useStatusQuery, …)
│       ├── schemas/          # Zod schemas: protocols, forms, api, primitives
│       ├── generated/        # ⚠️ GENERATED from Go (see §5.5): schemas.ts, types.ts, zod.ts, examples.ts
│       ├── components/       # Reusable UI (clients/ form/ ui/ viz/ feedback/ utility/)
│       ├── lib/              # Frontend domain logic (xray/ inbounds/ clients/)
│       ├── hooks/, models/, layouts/, i18n/, utils/, styles/
│       └── test/             # Vitest + golden fixtures (config-generation snapshot tests)
│
├── tools/openapigen/         # ⭐ Go program that emits frontend/src/generated/* from Go types (§5.5)
├── docs/                     # Markdown docs (this file, custom-subscription-templates.md, …)
├── media/                    # README images
│
├── install.sh / update.sh / p-ui.sh                        # VPS install + management CLI
├── p-ui.service.debian                                     # systemd unit (Ubuntu/Debian)
└── .github/workflows/        # CI: ci.yml, codeql.yml, release.yml, smoke.yml,
                              #     mutation.yml, cleanup_caches.yml, claude-bot.yml
```

---

## 5. Cross-cutting subsystems (the parts that span many files)

### 5.1 DB → Xray config pipeline (config generation & application)

The panel never edits Xray's running config directly from controllers. The flow is:

1. A service mutates DB state (inbound/client/setting).
2. `XrayService` (`service/xray.go`) builds a fresh `xray.Config` from DB state
   (`GetXrayConfig`).
3. It tries a **hot apply** (`tryHotApply` → `xray/hot_diff.go`): diff old vs new config and
   push only the deltas over the Xray gRPC API (add/remove inbound, add/remove user) — **no
   process restart**, so live connections survive.
4. If the diff isn't hot-applicable (structural change), it falls back to a **full restart**
   of the Xray process (`xray/process.go`).

Restart is debounced via an atomic "need restart" flag (`SetToNeedRestart` /
`IsNeedRestartAndSetFalse`), consumed by a `@every 30s` cron task registered in `startTask()`
— any number of mutations inside the window causes at most one restart.

**Key files:** `service/xray.go` (orchestration), `xray/hot_diff.go` (the diff algorithm),
`xray/process.go` (process lifecycle), `xray/api.go` (gRPC calls), `xray/config.go` (config model).

### 5.2 Runtime abstraction — Local vs Remote (multi-node) ⭐ most important

A "node" (`model.Node`) is another Penhoon UI instance this panel controls. Every state-changing
inbound/client operation goes through the `runtime.Runtime` interface so the *same service
code* works whether the target is the local Xray or a remote node.

- **Interface:** `internal/web/runtime/runtime.go` — `Name`, `AddInbound`, `DelInbound`,
  `UpdateInbound`, `AddUser`, `RemoveUser`, `UpdateUser`, `DeleteUser`, `AddClient`,
  `RestartXray`, `ResetClientTraffic`, `ResetInboundTraffic`, `ResetAllTraffics`.
- **`Local`** (`local.go`): resolves the inbound's core from the registry and applies through
  it — no protocol branch of its own, and no direct `internal/xray` call.
- **`Remote`** (`remote.go`): serializes the operation and sends it over HTTPS to the child
  node's API.
- **TLS modes** (`tls_client.go`, per-node `TlsVerifyMode`):
  `verify` (system CAs, default) / `skip` (no validation) / `pin` (leaf cert SHA-256 must
  match `PinnedCertSha256`) / `mtls` (master presents a client certificate; node cert checked
  against system roots; API token optional). Master-side cert management:
  `service/node_mtls.go` + `service/setting_mtls.go`.
- **Dispatch:** `manager.go` → `Manager.RuntimeFor(nodeID *int)`; `nil` nodeID → `Local`,
  otherwise a cached/lazy-loaded `Remote`. `InvalidateNode(id)` drops a cached remote client.

**Node identity & attribution (the hard part).** Inbounds carry a `NodeID` *and* an
`OriginNodeGuid`. Because inbounds can be pushed across hops, the panel attributes traffic and
online clients back to the originating panel using **stable GUIDs** rather than local IDs.
Relevant logic: `service/inbound_node.go` (`ReconcileNode`, `SetRemoteTraffic`, GUID merge,
`synthNodeGuid`, `panelGuid`) and `service/node.go` (`effectiveNodeGuid`, heartbeat, dirty
tracking). Node "dirty" flags drive an **anti-entropy reconciliation** so an offline node's
inbound edits converge once it reconnects.

**Where to look for node bugs:**
- Operation not reaching a node → `runtime/remote.go` + `runtime/manager.go`.
- Wrong traffic/online attribution across hops → `service/inbound_node.go` (GUID merge paths).
- Node shown offline / stale status → `job/node_heartbeat_job.go` + `service/node.go` (`Probe`, `UpdateHeartbeat`).
- Edits to an offline node not applying on reconnect → dirty/reconcile logic in `service/inbound_node.go` + `service/node.go` (`MarkNodeDirty`/`ClearNodeDirty`/`NodeSyncState`).
- TLS/mTLS handshake failures → `runtime/tls_client.go`, `service/node_mtls.go`, `service/node.go` (`FetchCertFingerprint`).

### 5.3 Traffic accounting

Per-client and per-inbound up/down counters originate from Xray's stats API and are persisted
to the DB. The Xray traffic job polls the core; node traffic is pulled from child nodes and
merged with GUID-based baselines to avoid double counting after resets.

**Key files:** `service/inbound_traffic.go`, `service/traffic_writer.go`,
`job/xray_traffic_job.go`, `job/node_traffic_sync_job.go`, `service/inbound_node.go`
(`SetRemoteTraffic` / `upsertNodeBaseline`), models `xray.ClientTraffic`,
`model.NodeClientTraffic`, `model.ClientGlobalTraffic` (cross-master totals).
Periodic resets: `job/periodic_traffic_reset_job.go` (keyed off `Inbound.TrafficReset`).

### 5.4 Background jobs (cron)

All registered in `web.go` → `startTask()`. Each is a struct with a `Run()` method in `internal/web/job/`:

| Schedule | Job | Purpose / condition |
|---|---|---|
| `@every 1s` | `check_xray_running_job` | Restart Xray if it died (2 consecutive down checks) |
| `@every 30s` | (inline func in `startTask`) | Debounced Xray restart — consumes the "need restart" flag (§5.1) |
| `@every 5s` | `xray_traffic_job` | Pull traffic stats from Xray (5s start delay); run the reseller budget and group quota pass (`enforceProductQuotas`, §5.10) and cut the sessions of every client either switched off (`service/client_cut.go`) |
| `@every 5s` | `node_heartbeat_job` | Probe child nodes (online/offline) |
| `@every 5s` | `node_traffic_sync_job` | Pull + merge node traffic; push reconciliation; push global usage and each client's resolved speed ladder to nodes |
| `@every 10s` | `core_policy` | Run the plan cycle pass (scheduled traffic resets, depletion notices — §5.9) and the reseller budget pass (§5.10, cutting what it switched off); converge shaping and in-core rates; observe every core's sessions and every node's reported sightings, decide each client's IP cap, cut the over-cap device through the core that saw it or record a `client_bans` row, converge the panel's own nft ban set on the rows, and publish `client.device_limit` (`internal/web/job/core_policy.go`) |
| `@every 10s` | `core_supervise` | Read every registered core's health, publishing `core.crash` once per outage and `core.up` on recovery, then reconcile it on its desired inbounds — a unit the backend reports down is started again (Xray excepted — the panel's own config build converges it; `internal/web/job/core_supervise.go`, `core_health.go`) |
| `@every 10s` | `egress_reconcile` | Drift repair for the egress band (`ip rule`, private table, blackhole). Also runs once synchronously at the top of `startTask()`, before any core is started, so containment exists before any device can carry a packet |
| `@every 5m` | `outbound_subscription_job` | Refresh outbound provider configs |
| `@every 10m` | `clear_logs_job` (`PruneXrayLogsJob`) | Truncate Xray access/error logs once either exceeds 64 MiB |
| `@hourly` | `warp_ip_job`, `periodic_traffic_reset_job("hourly")` | WARP IP rotation; traffic resets |
| `@daily` | `clear_logs_job`, `periodic_traffic_reset_job("daily")`, `periodic_traffic_reset_job("monthly")` | IP-limit and Xray access/error log cleanup; daily resets and due monthly resets |
| `@weekly` | `periodic_traffic_reset_job("weekly")` | Weekly traffic resets |
| default `@every 1m` | `ldap_sync_job` | Only if LDAP enabled; schedule configurable |
| default `@daily` | `stats_notify_job` | Only if TG bot enabled; schedule configurable |
| `@every 2m` | `check_hash_storage` | Only if TG bot enabled; expires bot callback hashes |
| `@every 1m` | `check_cpu_usage` | Only if a CPU alarm is configured (TG or email); publishes `cpu.high` |
| `@every 1m` | `check_memory_usage` | Only if a memory alarm is configured; publishes `memory.high` |
| configurable | `free_os_memory` | Only if `sys.MemoryReleaseIntervalMinutes() > 0`; returns heap to OS |

To change *when* something runs, edit `startTask()`. To change *what* it does, edit the job file.

### 5.5 Type generation (Go → TypeScript) ⚠️ don't hand-edit generated files

The Go backend is the schema source of truth. `tools/openapigen` (a Go program, with a
`StructAllow` allowlist of exported types) emits
`frontend/src/generated/{schemas,types,zod,examples}.ts`. The frontend build runs this first:

- `npm run gen:zod` → `go run ./tools/openapigen` (regenerate from Go)
- `npm run gen:api` → builds the OpenAPI doc (`scripts/build-openapi.mjs`, driven by the
  hand-maintained endpoint registry `src/pages/api-docs/endpoints.ts`)
- `npm run build` runs `gen:api` then `vite build`.

**Implication:** if you change a Go model/DTO that crosses the API boundary, regenerate the
frontend types (`cd frontend && npm run gen`) instead of editing `src/generated/` by hand.

### 5.6 Share-link / subscription generation

Two distinct code paths produce client configs:
- **Per-client links in the panel** (the "copy link" / QR in the UI): `service/client_link.go`
  + `util/link/outbound.go`.
- **Subscription endpoint** (what a client app polls): `internal/sub/service.go` (raw links),
  `internal/sub/json_service.go` (JSON), `internal/sub/clash_service.go` (Clash YAML).
  **`Host` rows** (`model.Host`, edited under /panel/api/hosts) override address/SNI/path/
  security per inbound in subscription output — applied in `sub/host_sub.go`.

Both paths must agree per protocol. A malformed link for a specific protocol/transport combo
(e.g. XHTTP + Reality) is usually a field-lookup mismatch in **`internal/sub/service.go`** (and
its tests `service_test.go` / golden fixtures), or in `util/link/outbound.go`. The frontend
also has protocol schemas under `frontend/src/schemas/protocols/` and `frontend/src/lib/xray/`.

### 5.7 Event bus (in-process pub/sub)

`internal/eventbus/` is a minimal buffered-channel pub/sub. Producers call a non-blocking
`Publish(Event)`; all subscribers receive every event. Event types: `outbound.down|up`,
`xray.crash`, `node.down|up`, `cpu.high`, `memory.high`, `login.attempt`, with structured
payloads (OutboundHealthData, NodeHealthData, LoginEventData, SystemMetricData). Producers
include the CPU/memory jobs, node heartbeat, and login handling; consumers include the
Telegram bot and the email notifier (`service/email/`). Use it for cross-cutting
notifications instead of importing notification services into producers.

### 5.8 Tunnel health monitor

`internal/tunnelmonitor/` is an optional watchdog configured **only via env vars**
(`PUI_TUNNEL_HEALTH_*`, read in `internal/config/`), deliberately independent of panel
settings so it can be enabled from a systemd `EnvironmentFile` even when the panel is
unreachable. It periodically probes an HTTP URL (default: Cloudflare trace endpoint) through
the tunnel; after N successive failures (default 3) it fires a recovery callback wired to an
Xray restart.

### 5.9 Plans (policies)

A plan (`model.Policy`, table `policies`) is a reusable client template: a speed ladder
(`tiers`), and four *managed* fields — quota, duration, IP limit, inbound set — each of which
is NULL when the plan does not manage it and `0` when it manages it as unlimited. Both states
are needed: the first edit of a ladder-only plan would otherwise read `0` and wipe every
member's quota. Admin plans have `owner_user_id IS NULL`; a reseller's are scoped to their id
and are refused `startOnFirstUse` / `autoRenewDays` (`service/plan_catalogue.go`, `SavePlan`).
Names are unique per owner, case-insensitively — that is what every plan picker shows.

**Assignment is a separate row, keyed by email** (`model.ClientPolicy`, `client_policies`), so
it survives the delete-and-recreate a node re-sync performs; a rename moves it by hand
(`movePolicyAssignment`), a client delete drops it (`releasePolicyAssignments`). The FK to the
plan is `ON DELETE SET NULL`, so a deleted plan leaves its members visible as **unresolved**
rather than silently unlimited. The four `override_*` bits record explicit operator intent to
keep the client's own value for that field; the value itself lives on the client.

**Applying is copying, not linking.** `ApplyPlan` (`service/plan_assignment.go`) copies the
managed fields onto `clients` / `client_traffics`, attaches or detaches the plan's inbounds,
pushes native quotas through the runtime, and only then stamps `applied_rev = policies.rev`.
`rev` bumps only when a managed field or the inbound set changes (`managedPlanEqual`), so
editing the ladder alone lights no **pending** badge: the ladder is evaluated live on every
policy pass (`service/policy.go`, `factsFor`), whereas quota/duration/IP edits wait for
**Reapply**, which preserves overrides unless told to clear them and keeps the original term
start (`planExpiry`). Client creation with a plan (`CreateWithPlan`) writes the prepared client
and its assignment in one transaction before any runtime call, and rolls the identity back on
provisioning failure.

**The cycle pass** (`ConvergePlanCycles`, `service/plan_cycles.go`, from `core_policy` every
10 s) reads every assignment on a reset cycle or a `notify` plan in a few chunked queries,
resets traffic when `policy.NextReset` from the last reset (or the term start) has passed, and
emits one `client.plan_depleted` notice per depletion, re-armed once usage drops under quota.
A client reset in a pass is not judged for depletion until the next one. `onDepletion:
throttle` keeps a depleted client connected at 1 MiB/s by appending a rung to their ladder
(`policy.DepletionLadder`) and is excluded from the quota-disable predicate
(`PlanQuotaDisableSQL`); both continuation modes are refused at save time on inbounds whose
core enforces a hard quota of its own. On a sub-node the ladder arrives pre-resolved from the
master (`client_policies.resolved_tiers`, §5.2) and no plan row exists there.

### 5.10 Reseller accounts

A reseller is a `users` row with `role = 'reseller'` plus a `reseller_accounts` row
(`model.ResellerAccount`) that carries the budget — traffic limit, client cap, term — the
five permission flags, and the two rules: `on_depletion` (`disable` cuts the owned clients,
`block` keeps them online and only refuses new provisioning, top-ups and re-enables) and
`reset_cycle` (`none` / `weekly` / `monthly`, anchored on `period_started_at`, computed with
the same `policy.NextReset` the plans use). A client belongs to a reseller through
`clients.owner_user_id`; NULL is the administrator. Which inbounds a reseller may attach to
is `reseller_inbound_grants`, enforced twice: in Go before every write, and by the
`pui_reseller_*` triggers in `internal/database/reseller_constraints.sql` so no path around
the service can attach an ungranted inbound or re-own a client whose inbounds the new owner
lacks. `chk_reseller_no_global_group` keeps owned clients out of global groups.

**Usage is counted by trigger, not by job** (`reseller_usage.sql`): every write to
`client_traffics` or `client_global_traffics` for an owned email runs
`pui_reseller_usage_observe`, which keeps a per-source cursor (`reseller_usage_sources`) and a
per-client peak (`reseller_client_usages`) and adds only the growth to
`reseller_accounts.traffic_used`. That is why a client reset or delete never refunds the
account, why replicated traffic is not counted twice, and why an ownership change is
prospective — the owner-changed trigger re-baselines the cursors so the old bytes stay with
the old owner. `reseller_allocations` is an append-only ledger of quota handed out (the
`pui_reseller_quota_allocated` trigger writes it on create, quota increase and re-own); the
dashboard's "sales" chart is that ledger.

**Enforcement is one level-triggered pass**, `ResellerService.EnforceBudgets`
(`service/reseller_budget.go`), run from both `xray_traffic_job` (5 s) and `core_policy`
(10 s); it takes the per-account advisory lock (`WithAccountLock`, `pg_advisory_xact_lock`)
only for accounts that need it. Under the lock, `convergeBudgetLocked` runs a due cycle reset,
flips `budget_blocked` first (the owner gate every re-enable reads, `ValidateClientOwnerEnable`),
then pauses or restores clients through `ClientService.Update` — never a direct core call —
remembering what it paused in `reseller_paused_clients` so a top-up restores exactly those.
The emails it switched off come back to the job, which feeds them through
`service.CutDisabledClients` (`service/client_cut.go`), and `Suspend` calls the same helper
after releasing the lock: the same `restartXrayOnClientDisable` gate as per-client depletion,
forced because a hot user removal never closes an Xray tunnel that is already up. Only a
traffic depletion honours `block`; a lapsed term and a suspension always cut, and once the
reason to pause is gone the paused clients come back even while `block` still refuses
provisioning. Every account transition — ninety percent of traffic, depletion, seven days before the
term ends, expiry, restoration, a period reset — is announced once, by a conditional update on
the `*_warned_at` stamps or the `budget_blocked` flip, into the reseller's activity feed
(administrators read every audience) and onto the bus as `reseller.budget` (Telegram and email templates
under `tgbot.messages.eventReseller*`).

**Lock discipline:** the lock transaction never holds a client row. Every write inside
`WithAccountLock` runs on a fresh `database.GetDB()` connection, so the traffic writer's
trigger path is never queued behind an account operation
(`reseller_reservations_test.go` fails if this slips). Transfers between resellers
(`service/reseller_transfer.go`) take both locks in ascending user id. Deleting an account
deletes or re-owns its clients first (runtime-first, `BulkDelete`), then drops the private
plans, the ledger, the account's activity and the login; a client the runtime cannot delete
keeps the account for a retry.

---

## 6. Data model cheat-sheet

GORM models in `internal/database/model/` (main file `model.go` + siblings); all registered
for AutoMigrate in `internal/database/db.go`.

| Model | Table role | Notable fields |
|---|---|---|
| `User` | Admin login | bcrypt password, `LoginEpoch` (invalidates sessions) |
| `Inbound` | An Xray inbound | `Tag` (unique), `Port`, `Protocol`, `Settings`/`StreamSettings`/`Sniffing` (JSON), `Enable`, `TrafficReset`, `NodeID`, **`OriginNodeGuid`**, `ClientStats` (assoc) |
| `Client` | In-memory client view | UUID/email/flow/limits (parsed from inbound JSON; not persisted) |
| `ClientRecord` | Persisted client (`clients`) | `Email` (unique), `SubID`, `UUID`, `TotalGB`, `ExpiryTime`, `LimitIP`, `Group`, `Reset` |
| `ClientGroup` / `ClientInbound` | Grouping + client↔inbound join | many-to-many wiring, `FlowOverride` |
| `ClientExternalLink` | Extra links attached to a client | `Kind`, `Value`, `Remark`, `SortIndex` |
| `Host` | Subscription host overrides (per inbound) | `Address`, `Port`, `Sni`, `Path`, `Security`, `Fingerprint`, `SortOrder`, visibility/exclusion flags |
| `Node` | A managed child panel | `Guid`, `Address`, `Status`, `TlsVerifyMode`, `PinnedCertSha256`, `ConfigDirty`, version/heartbeat/metric fields |
| `NodeClientTraffic` | Per-node client traffic baseline | cross-node merge (anti-double-count) |
| `NodeClientIp` | Per-node client IP attribution | `NodeGuid`, `Email`, `Ips` |
| `ClientGlobalTraffic` | Cross-master usage totals | `MasterGuid`, `Email`, `Up`, `Down` |
| `xray.ClientTraffic` | Per-client counters (`client_traffics`) | `Email`, `Up`, `Down`, `Total`, `ExpiryTime`, `LastOnline` |
| `Policy` / `PolicyInbound` | A plan template and its inbound set (`policies`, `policy_inbounds`) | `Tiers` (jsonb ladder), nullable `QuotaBytes`/`DurationDays`/`LimitIP`, `Rev`, `OwnerUserID`, `Archived`, `ResetCycle`, `OnDepletion` (§5.9) |
| `ClientPolicy` | A client's plan assignment (`client_policies`, keyed by email) | nullable `PolicyId` (FK `SET NULL`), `override_*` bits, `AppliedRev`, `LastResetAt`, `DepletionNotifiedAt`, `ResolvedTiers` (node side) |
| `InboundClientIps` | IP set per client email | drives IP-limit enforcement |
| `ClientBan` | one banned (client, source) per host with ports and deadline | the re-ban memory and the source of the nft ban set (`internal/banset/`) |
| `OutboundTraffics` | Outbound counters | per outbound tag |
| `OutboundSubscription` | External provider subs | Warp/Nord style |
| `Setting` | Key/value panel settings | everything configurable |
| `ApiToken` | REST API tokens | SHA-256 hash (plaintext shown once) |
| `InboundFallback` | Fallback routing on a shared port | SNI/ALPN/path → dest |
| `HistoryOfSeeders` | Seeder bookkeeping | prevents re-running one-off migrations |
| `ResellerAccount` | One reseller's budget and rules (`reseller_accounts`, keyed by `users.id`) | `TrafficLimit`/`TrafficUsed`/`ClientCap`/`ExpiresAt`, `BudgetBlocked`, `OnDepletion`, `ResetCycle`, `PeriodStartedAt`, the `Allow*` flags, once-only notice stamps (§5.10) |
| `ResellerInboundGrant` / `ResellerPausedClient` / `ResellerToken` | Which inbounds a reseller may use; which clients a block paused; the reseller's Bearer tokens | all cascade with the account; the grant is also enforced by trigger |
| `ResellerAllocation` | Append-only ledger of quota handed to owned clients | written by trigger on create, quota increase and re-own; the dashboard's "sales" |
| `ResellerUsageSource` / `ResellerClientUsage` | Per-source counter cursors and per-client peak behind `traffic_used` | why resets never refund and transfers are prospective (`reseller_usage.sql`) |

---

## 7. Symptom → File index (start here when debugging)

| Symptom / task | Primary file(s) | Then check |
|---|---|---|
| A device over its IP limit is not dropped, or dropped on the wrong port | `internal/web/service/policy_bans.go` (`RecordBans`, `ConvergeBans`), `internal/banset/` | `nft list table inet p-ui-ipl`; `internal/web/job/core_policy.go` (`reportOverLimit`); `PUI_IP_LIMIT_BANS` |
| A ban for a client on a node never lands, or the cap ignores the node's devices | `internal/web/service/policy.go` (`EvaluateIPLimits`, `nodeObservations`, `enforcementFor`) | `policy_bans.go` (`BansForNode`, `AcceptBans`); `internal/web/job/node_traffic_sync_job.go` (`pushBans`) |
| An operator or client was not told about a cut or a ban | `internal/web/job/core_policy.go` (`publishDeviceLimit`) | the `client.device_limit` toggle; `tgbot/tgbot_event.go`, `email/subscriber.go` |
| Add/modify an **API endpoint** | `controller/<resource>.go` (route registration at top of each file) | corresponding `service/*.go`, `frontend/src/pages/api-docs/endpoints.ts` |
| **Inbound** create/update/delete behavior | `service/inbound.go`, `service/inbound_clients.go` | `runtime/*`, `service/xray.go` |
| **Client** CRUD / limits / expiry | `service/client_crud.go`, `service/client_inbound_apply.go` | model `ClientRecord`, `service/inbound_traffic.go` |
| A client op on a **non-Xray core** did not reach the daemon | `runtime/local.go` (`desiredState`, `userTarget`), `service/inbound_core.go` (`RenderForRuntime`) | `service/client_quota.go` (resets), `service/client_bulk.go` (the re-key) |
| A client **edit** is refused with a core's wording | `service/client_inbound_apply.go` (`validateClientForKind` on the edit path) | the core's `ValidateClient` under `cores/internal/<core>/` |
| The **inbound form** shows the wrong tabs or fields for a kind | `frontend/src/lib/cores/kinds/<kind>.ts` | `frontend/src/pages/inbounds/form/InboundFormModal.tsx` (the editor line) |
| **Bulk** client operations slow/wrong | `service/client_bulk.go` | `service/client_paging.go` |
| Xray **won't apply** a config change | `service/xray.go` (`RestartXray`, `tryHotApply`) | `xray/hot_diff.go`, `xray/process.go` |
| Xray **restarts when it shouldn't** (kills connections) | `xray/hot_diff.go` (diff not classified as hot) | `service/xray.go` |
| **Traffic** counts wrong / reset behavior | `service/inbound_traffic.go`, `job/xray_traffic_job.go` | `service/traffic_writer.go`, `job/periodic_traffic_reset_job.go` |
| **Node** operation not propagating | `runtime/remote.go`, `runtime/manager.go` | `service/inbound_node.go` |
| A **plan edit** did not change a member's quota, term or IP cap | `service/plan_assignment.go` (`ApplyPlan`, `ReapplyPlan`) — managed edits wait for Reapply; `applied_rev < rev` is the pending badge | `service/plan_catalogue.go` (`managedPlanEqual`), `client_policies.override_*` |
| A **scheduled plan reset** or **depletion notice** never fires | `service/plan_cycles.go` (`ConvergePlanCycles`) | `job/core_policy.go` (`convergePlans`), `policy/cycle.go` (`NextReset`) |
| A **speed plan** does nothing on a sub-node | `service/policy.go` (`AcceptAssignments`, `factsFor`) | `job/node_traffic_sync_job.go` (the push), `runtime/remote.go` |
| An **OpenConnect** client's limit does not change mid-session | `cores/internal/ocserv/core.go` (`ShapingTargets`) | `shaping/namespaces.go` (families), `service/policy.go` (`ShapingWants`) |
| The plan panel says **not enforced yet** for an SSH client | `service/policy.go` (`EnforcedFor`) | `cores/internal/ssh/server.go` (`enforcedRates`) |
| **Multi-hop / cross-node attribution** (traffic or online clients on wrong panel) | `service/inbound_node.go` (GUID merge, `synthNodeGuid`, `effectiveNodeGuid`) | `service/node.go`, model `OriginNodeGuid`/`Node.Guid` |
| Node stuck **offline / stale** | `job/node_heartbeat_job.go`, `service/node.go` (`Probe`, `UpdateHeartbeat`) | `runtime/tls_client.go` (TLS verify) |
| Node **TLS / mTLS** auth failures | `runtime/tls_client.go`, `service/node_mtls.go`, `service/setting_mtls.go` | `service/node.go` (`FetchCertFingerprint`) |
| Offline node edits **not reconciling** on reconnect | `service/inbound_node.go` (`ReconcileNode`, dirty flags) | `service/node.go` (`MarkNodeDirty`/`NodeSyncState`) |
| **Share link / QR** malformed (per protocol) | `service/client_link.go`, `util/link/outbound.go` | `frontend/src/lib/xray/`, `frontend/src/schemas/protocols/` |
| **Subscription** output wrong (raw/JSON/Clash) | `internal/sub/service.go` | `sub/json_service.go`, `sub/clash_service.go`, sub golden tests |
| Subscription **host overrides** not applied | `service/host.go`, `sub/host_sub.go` | model `Host`, `frontend/src/pages/hosts/` |
| **External subscription** import/aggregation | `sub/external_subscription.go`, `sub/external_config.go` | `sub/clash_external.go` |
| **Settings** not saving / defaults | `service/setting.go`, `controller/setting.go` | model `Setting` |
| **Login / 2FA / sessions / CSRF** | `controller/index.go`, `service/panel/user.go`, `middleware/` | `session/` |
| **API tokens** | `service/panel/api_token.go`, `controller/setting.go` | model `ApiToken` |
| **Port conflict** on inbound add | `service/port_conflict.go` | `controller/inbound.go` |
| **Fallbacks** (shared 443, SNI routing) | `service/fallback.go`, `controller/inbound.go` | model `InboundFallback` |
| **Telegram bot** commands | `service/tgbot/` | `job/stats_notify_job.go` |
| **Email notifications** | `service/email/` | `internal/eventbus/` (consumers) |
| **CPU / memory alerts** not firing | `job/check_cpu_usage.go`, `job/check_memory_usage.go` | `internal/eventbus/`, notifier settings in `service/setting.go` |
| Xray auto-restart on **dead tunnel** | `internal/tunnelmonitor/` | `PUI_TUNNEL_HEALTH_*` in `internal/config/` |
| **WARP / Nord** outbound integration | `service/integration/warp.go` / `nord.go` | `service/outbound_subscription.go` |
| **MTProto** proxy issues | `internal/mtproto/manager.go`, `mtproto/process*.go` | `job/core_supervise.go` |
| An MTProto client's speed or IP limit does not apply | `internal/mtproto/manager.go` (`withRatesLocked`, `renderConfig`, `secretsPayload`), `internal/cores/internal/mtproto/core.go` (`EnforceRates`, `Sessions`, `CutSessions`) | `internal/mtproto/process.go` (`CheckBinary`, `MinimumVersion`); the sidecar's `features` in `/stats` |
| The MTProto WEB relay does not start, or shows degraded | `internal/mtproto/web_relay.go` (`ensureRelayLocked`, `webRelayCheck`, `ValidateWebRelaySettings`) | the inbound's `Detail` on the Cores page; `web-relay --check <toml>` by hand |
| A core shows **failed / not answering** on the Cores page | `internal/web/service/core_status.go`, the core's `Health` in `internal/cores/internal/<core>/core.go`, `cores/internal/unithealth/` | `GET /panel/api/cores/:id/logs` → `internal/supervisor` `Logs` |
| A core's **version / install** is wrong | `internal/cores/internal/xray/versions.go`, `internal/cores/internal/mtproto/versions.go`, `internal/web/service/core_versions.go` | `cores.Deps.HTTPClient` (the egress proxy is hosted by xray) |
| **DB migration** / new column | `internal/database/db.go` (AutoMigrate list + hand-written migrations) | `model/model.go` |
| **Cron schedule** changes | `web.go` → `startTask()` | the specific `job/*.go` |
| **CORS / security headers / HTTPS** | `middleware/`, `web.go` (`initRouter`, TLS setup) | `config/` (env) |
| **Env vars / paths / DB DSN** | `internal/config/config.go` | `.env.example` |
| **Frontend route / screen** | `frontend/src/pages/<area>/`, `frontend/src/routes.tsx` | `frontend/src/api/queries/` |
| **Frontend ↔ backend type mismatch** | regenerate: `cd frontend && npm run gen` (`tools/openapigen`) | `frontend/src/generated/` |
| **System status / CPU / metrics** | `service/server.go`, `service/xray_metrics.go`, `service/metric_history.go` | `controller/server.go`, gopsutil |
| A **reseller's** client keeps its tunnel after the budget ran out, or comes back too early | `service/reseller_budget.go` (`convergeBudgetLocked`, `resellerNeedsPass`, `Suspend`), `service/client_cut.go` | the account's `onDepletion`; `restartXrayOnClientDisable`; `reseller_paused_clients` |
| Reseller **usage** looks wrong (refunded, double-counted, or a transfer moved old bytes) | `internal/database/reseller_usage.sql` (`pui_reseller_usage_observe`, `pui_reseller_owner_changed`) | `reseller_usage_sources` cursors; `reseller_stale_transfer_test.go` |
| A reseller **notice** never arrived, or arrived on every tick | `service/reseller_budget.go` (`settleWarning`, the `budget_blocked` flip, `publish`) | `traffic_warned_at` / `expiry_warned_at`; the `reseller.budget` toggle in notification settings |
| **Assigning or releasing** a reseller's client is refused, or a plan vanished with the move | `service/reseller_transfer.go` (`validateResellerIntake`, `transferClientsTx`) | `reseller_constraints.sql` triggers; `UnassignPlanTx` |

---

## 8. Layering rules (where new code belongs)

1. **Controllers are thin.** Only: bind/validate input, call one service, shape the HTTP
   response. No DB queries, no Xray calls, no business rules in `controller/`.
2. **Services own the logic and transactions.** All business rules, DB access, and decisions
   about applying changes live in `service/`. If you're tempted to query GORM from a
   controller, move it to a service.
3. **Never touch Xray's running state from a controller or job directly.** Go through
   `XrayService` / the `runtime.Runtime` interface so local vs node dispatch stays correct.
4. **Any state-changing inbound/client op must dispatch through `runtime.Runtime`**, not
   straight to `xray/api.go` — otherwise node deployments silently break.
5. **`internal/util/*` is leaf-only** (no imports of `service`/`controller`/`database`). Keep
   helpers pure.
6. **Don't hand-edit generated files:** `frontend/src/generated/*` and `internal/web/dist/*`.
   Regenerate instead.
7. **Models are the contract.** Changing a model field that crosses the API boundary means:
   update `model.go` → handle migration in `db.go` → regenerate frontend types.
8. **Two servers, two concerns.** Admin features go in `internal/web`; anything an *end user*
   fetches goes in `internal/sub`. Don't blur them.
9. **Cross-cutting notifications go through `internal/eventbus/`** — publish an event instead
   of importing the Telegram/email services into producers.

---

## 9. Build / Test / Lint (verify your changes)

The canonical gate is the **Makefile** (mirrors CI): `make verify`. Also: `make gen`
(regenerate Zod/OpenAPI), `make lint` (Go + frontend), `make test` (Go `-shuffle=on` +
frontend), `make race`, `make build`. Run `make help` for everything. Raw commands:

**Backend (Go):**
```bash
go build ./...                      # compile everything
go test ./...                       # run all Go tests (many *_test.go alongside sources)
go test ./internal/web/service/...  # focused: service-layer tests
go test ./internal/xray/...         # hot-diff / process / api tests
go test ./internal/sub/...          # subscription + golden link tests
go vet ./...                        # static checks
golangci-lint run                   # full lint (gofumpt + goimports formatting)
go run main.go                      # run the panel locally (serves embedded dist if built)
```

**Frontend (`cd frontend`, Node 24 — see `.nvmrc`):**
```bash
npm install
npm run dev          # Vite dev server on :5173; proxies API to Go backend on :2053 (run `go run main.go` too)
npm run typecheck    # tsc --noEmit
npm run lint         # eslint src
npm run test         # vitest (incl. golden config-generation snapshots)
npm run gen          # regenerate src/generated/* from Go (gen:zod + gen:api)
npm run build        # gen:api + vite build → outputs to internal/web/dist (then rebuild Go binary to embed)
```

**Full local loop:** `cd frontend && npm run build` (refresh embedded `dist/`) → back to repo
root → `go build ./...` / `go run main.go`.

**CI** (`.github/workflows/`): `ci.yml` (build/test/lint, incl. a live-PostgreSQL job),
`codeql.yml` (security scan), `smoke.yml` (smoke tests), `mutation.yml` (mutation testing),
`release.yml` (multi-arch Linux release builds, `CGO_ENABLED=0`), `cleanup_caches.yml`,
`claude-bot.yml` (issue bot).

---

## 10. Gotchas & conventions

- **Module path is `.../v3`.** Internal imports use `github.com/Arman2122/p-ui/internal/...`.
- **PostgreSQL only.** `PUI_DB_DSN` is required and the panel fails fast at startup without
  it — there is no file-backed fallback. Raw SQL is PostgreSQL syntax; shared fragments live
  in `database/dialect.go`. DB-backed tests skip unless `PUI_DB_DSN` names a reachable server.
- **`ClientTraffic` and `InboundConfig` live in `internal/core`, not `internal/xray`.**
  `internal/xray` keeps type aliases so existing call sites compile, but the data layer must
  not import a core — `internal/arch` fails the build if it does. `client_traffics` is
  email-keyed and `UNIQUE`, which is what makes one quota span every core a client uses.
- **`Inbound.Settings` / `StreamSettings` / `Sniffing` are raw JSON strings**, not structured
  columns. Parsing/validation happens in services and the `xray` package, not in GORM.
- **Hot-reload is the default; full restart is the fallback.** Changes that look config-only
  but cause a restart usually mean the diff in `xray/hot_diff.go` didn't recognize them as hot.
- **Node TLS:** remote calls honor `TlsVerifyMode` (`verify`/`skip`/`pin`/`mtls`). "Works on
  skip, fails on verify/pin/mtls" → cert/fingerprint handling in `service/node.go`
  (`FetchCertFingerprint`), `service/node_mtls.go`, and `runtime/tls_client.go`.
- **Restart is signal-driven.** `main.go` traps SIGHUP to restart panel+sub servers; the
  in-process restart hook (`global.SetRestartHook`) funnels into the same path.
- **i18n:** backend catalogs in `internal/web/translation/` (two locales — `en-US` and
  `fa-IR` — shared with the frontend); frontend wiring in `frontend/src/i18n/`. Persian
  (`fa_IR`) is a first-class locale (Jalali calendar via `persian-calendar-suite`).
- **Tests live next to code** (`foo.go` ↔ `foo_test.go`), plus golden snapshots in
  `frontend/src/test/golden/fixtures/` for config generation — update fixtures intentionally,
  not blindly, when output changes.
