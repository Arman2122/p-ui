# Contributing

Thanks for taking the time to contribute to Penhoon UI. This guide gets a development panel running locally and explains the conventions the project follows so changes land cleanly.

## Prerequisites

Penhoon UI is **Linux-only** — it targets Ubuntu 22.04 / 24.04 / 26.04 and Debian 12+ with systemd, `apt` and `iptables`, and releases ship Linux binaries only. Develop on one of those distributions (a VM or container is fine): the service management, firewall and Fail2ban paths have no equivalent elsewhere.

- **Go 1.26+** (the version pinned in `go.mod`)
- **Node.js 24 LTS** (the version pinned in `.nvmrc`) and npm 10+ (for the React frontend)
- **Git**
- **PostgreSQL** — the panel's only database backend. Install the distro package (`apt install postgresql postgresql-client`) and create a role and database for development.
- **`build-essential`** — *optional*. The panel itself is pure Go and builds with `CGO_ENABLED=0`; only `go test -race` needs a C toolchain.

`pg_dump` and `pg_restore` (from `postgresql-client`) must be on `PATH` for the panel's backup and restore features to work locally.

## First-time setup

```bash
git clone https://github.com/Arman2122/p-ui.git
cd p-ui

cp .env.example .env

mkdir p-ui

go mod download

cd frontend
npm install
npm run build
cd ..
```

`.env.example` ships with defaults that keep logs and the xray binary inside the local `p-ui/` folder so nothing escapes the project directory. The one value you must set is the database DSN — the panel refuses to start without it:

```
PUI_DEBUG=true
PUI_DB_DSN=postgres://pui:pui@127.0.0.1:5432/pui?sslmode=disable
PUI_LOG_FOLDER=p-ui
PUI_BIN_FOLDER=p-ui
PUI_INIT_WEB_BASE_PATH=/
# PUI_PORT=8080
```

Drop the xray binary (`xray-linux-amd64`, or the build matching your architecture) plus the matching `geoip.dat` and `geosite.dat` files into `p-ui/`. The easiest source is a [released Xray-core build](https://github.com/XTLS/Xray-core/releases).

## Running

```bash
go run .
```

Open [http://localhost:2053](http://localhost:2053) and log in with `admin` / `admin`. Credentials must be changed on first login.

### Inside VS Code

The repo checks in a **Run p-ui** launch profile in `.vscode/launch.json`. It runs the panel in debug mode with logs and binaries under the local `p-ui/` folder and points `PUI_DB_DSN` at a local PostgreSQL — adjust the DSN for your machine:

```jsonc
{
  "env": {
    "PUI_DEBUG": "true",
    "PUI_LOG_FOLDER": "p-ui",
    "PUI_BIN_FOLDER": "p-ui",
    "PUI_DB_DSN": "postgres://pui:puipass@127.0.0.1:5432/pui?sslmode=disable"
  }
}
```

Keep `pg_dump`/`pg_restore` on `PATH` for the panel's DB backup/restore to work from a debug session.

## Working on the frontend

The panel UI is a **React 19 + Ant Design 6 + TypeScript** app under `frontend/`, built with Vite 8. The sections below cover the architecture, the conventions, and the two dev workflows.

### Architecture

The frontend ships **three Vite bundles**, each emitted into `internal/web/dist/` and embedded into the Go binary at compile time via `embed.FS`:

- **`index.html`** — the admin panel, a **single-page app**. `src/main.tsx` mounts a `react-router` `createBrowserRouter` (see `src/routes.tsx`) under the `/panel` basename; every route (`/panel`, `/panel/inbounds`, `/panel/clients`, `/panel/groups`, `/panel/nodes`, `/panel/settings`, `/panel/xray`, `/panel/api-docs`) is lazy-loaded inside a shared `PanelLayout` (sidebar + header + `<Outlet>`).
- **`login.html`** — the login + 2FA screen (`src/entries/login.tsx`), a standalone bundle.
- **`subpage.html`** — the public subscription viewer (`src/entries/subpage.tsx`), a standalone bundle.

Panel navigation happens client-side through React Router, and per-route code is lazy-split so the initial panel load stays small. `login` and `subpage` stay separate documents because they are reached without an authenticated panel session.

### State and data flow

- **Server state via TanStack Query.** API reads go through `@tanstack/react-query` (`QueryProvider` in `src/main.tsx`, keys in `src/api/queryKeys.ts`); responses are cached and invalidated on mutation rather than blindly re-fetched, and WebSocket pushes feed back into the cache via `src/api/websocketBridge.ts`.
- **Local UI state stays in the page** (`useState`); shared concerns go through contexts and hooks in `src/hooks/` (`useTheme`, `useWebSocket`, `useClients`, `useDatepicker`, …). Prefer extending an existing hook over introducing a new global.
- **Zod is the single source of truth.** Schemas in `src/schemas/` define the xray config model; every API response is parsed through them, every form field validates against them, and TypeScript types are inferred with `z.infer` — never hand-written. Go-side types are mirrored into `src/generated/` by `npm run gen:zod` (do not hand-edit that folder).
- **xray domain logic** — link generation, protocol defaults, form ⇄ wire adapters — lives as pure functions in `src/lib/xray/`. `src/models/` keeps only thin legacy types still being migrated onto schemas.
- **HTTP** goes through `HttpUtil` in `src/utils/index.ts`, a thin `fetch` wrapper that handles CSRF, response toasts, and a `silent: true` opt-out for bulk operations that would otherwise spam toasts. The `fetch` setup itself (base path, CSRF, 401/403 handling) lives in `src/api/http-init.ts`.

### i18n

The panel ships two locales — `en-US.json` and `fa-IR.json` — and their strings live in `internal/web/translation/`, **not** under `frontend/`. The Go binary embeds the same JSON and serves it to both backend templates and `react-i18next` (initialized in `src/i18n/react.ts`). When a new English key is added it must also land in `fa-IR.json` — missing keys do not break the build, they just render the raw key in the UI.

### Dev workflows

| Goal | Command |
|------|---------|
| Iterate on UI changes with HMR | `cd frontend && npm run dev` (Vite on `:5173`, proxies `/panel/*` and the WebSocket to the Go panel on `:2053`). Start the Go panel first. |
| Verify what end users actually see | `cd frontend && npm run build`, then `go run .`. The Go binary serves the built bundle — embedded in release mode, off disk in debug mode. |
| Develop/preview a reusable component in isolation | `cd frontend && npm run storybook` (Storybook workbench + autodocs on `:6006`). |

The Vite dev proxy serves the admin SPA for any `/panel/*` URL — `bypassMigratedRoute` in `vite.config.js` rewrites those requests to `index.html` and lets React Router take over — while forwarding `/panel/api/*`, `/panel/api/setting/*`, `/panel/api/xray/*`, and the WebSocket to the Go panel. Because routing is now client-side, new panel routes need no proxy or allowlist changes.

> **`PUI_DEBUG=true` gotcha** — in debug mode the panel serves HTML from the embedded FS (frozen at the last `go build` / `go run`) but JS/CSS off disk. Re-running `npm run build` without restarting Go leaves the embedded HTML pointing at the *old* hashed asset names, producing a blank page with 404s in the console. Always restart `go run .` after a frontend rebuild.

### Adding a new page

Most new screens are **admin-panel routes** and need no new HTML or Vite entry:

1. Create the page component under `src/pages/<page>/<Page>.tsx` (kebab-case folder, PascalCase component).
2. Register it in `src/routes.tsx` under the `/panel` tree (lazy-import it like the others).
3. Add a sidebar link in `src/layouts/AppSidebar.tsx` if it should be reachable from the nav.

Only a genuinely **standalone bundle** (like `login` or `subpage`, reachable without the panel shell) needs the full entry treatment: add `frontend/<page>.html`, a `src/entries/<page>.tsx` bootstrap, register it in `rollupOptions.input` inside `vite.config.js`, and wire a Go controller route that calls `serveDistPage(c, "<page>.html")` to serve the embedded HTML in production.

### Conventions

- **TypeScript strict mode** — all new code in `.ts` / `.tsx`. Run `npm run typecheck` (`tsc --noEmit`) before pushing. The path alias `@/*` resolves to `src/*`.
- **Ant Design 6** is the only UI kit — no Tailwind, no shadcn. A previous attempt to migrate was rolled back. Small, targeted UX tweaks beat sweeping rewrites; raise broader visual changes for discussion before implementing.
- **Function components + hooks** everywhere. No class components.
- **No `//` line comments** in committed JS/TS/Vue/Go. HTML `<!-- ... -->` is fine for template structure. Names should carry the meaning; rename rather than annotate. Comments are reserved for the *why*, and only when the reason is surprising.
- **Persian (Farsi) users are first-class.** When writing Persian text in toasts or labels, isolate code identifiers on their own lines so RTL reading flows. (Full RTL layout is not currently wired through AntD `ConfigProvider direction` — only the Jalali date picker is RTL-aware — so treat RTL as an open area, not a solved one.)
- **Schemas over `any`.** New config shapes go in `src/schemas/`; `@typescript-eslint/no-explicit-any` is an error and production schemas use no `.loose()`. Validate form fields with `antdRule(Schema.shape.field, t)` rather than inline `z.string()` in rules.
- **Document new endpoints.** Every new `g.POST`/`g.GET` in `internal/web/controller/` needs a matching entry in `src/pages/api-docs/endpoints.ts` — it drives both the in-panel API docs and the generated OpenAPI/Zod (`npm run gen:api` / `gen:zod`).
- **Do not break link generation.** Share-link logic lives in `src/lib/xray/` (`inbound-link.ts`, `outbound-link-parser.ts`, …) and is round-tripped by the golden fixture suite — run `npm run test` after any change to URL generation, defaults, or TLS/Reality handling, and regenerate snapshots (`npx vitest run -u`) only for intentional changes. Two runtime paths consume it: the **inbounds page** and the **clients page** subscription links (`/panel/api/clients/subLinks/:subId` → backend `GetSubs`); exercise both.
- **Vite is pinned to an exact version** (no `^`) in `frontend/package.json` — read the live version there rather than trusting a number quoted here — so local, CI, and release builds resolve identically. Bump it deliberately and verify both `npm run dev` and `npm run build` afterward.
- **Reusable components are documented in Storybook.** When you add or change a component in `frontend/src/components/`, add or update its co-located `<Component>.stories.tsx` (`tags: ['autodocs']`), documenting props via `argTypes` / `parameters.docs` string metadata rather than JSDoc. CI compile-checks every story via `npm run build-storybook` and runs each story as a headless-browser test via `@storybook/addon-vitest` (`npm run test`, needs `npx playwright install chromium`); run `npm run storybook` to preview locally.

### Project layout

```
frontend/
├── index.html             — admin panel SPA entry
├── login.html             — login + 2FA entry
├── subpage.html           — public subscription viewer entry
├── tsconfig.json          — strict, jsx: "react-jsx", paths "@/*" → "src/*"
├── eslint.config.js       — ESLint flat config (@eslint/js + typescript-eslint + react-hooks)
├── vite.config.js
├── vitest.config.ts
├── scripts/               — build-openapi.mjs (endpoints.ts → openapi.json)
└── src/
    ├── main.tsx           — admin SPA bootstrap (router + providers)
    ├── routes.tsx         — react-router routes mounted under /panel
    ├── entries/           — bootstrap for the standalone bundles (login, subpage)
    ├── layouts/           — PanelLayout + AppSidebar
    ├── pages/             — one folder per route (index, inbounds, clients, groups, nodes, settings, xray, api-docs) plus login, sub
    ├── components/        — cross-page React components
    ├── hooks/             — reusable hooks (useTheme, useWebSocket, useClients, useDatepicker, …)
    ├── api/               — fetch client + CSRF handling, TanStack Query provider/keys, WebSocket client
    ├── i18n/              — react-i18next bootstrap (JSON lives in internal/web/translation/)
    ├── lib/xray/          — pure xray logic: link generation, defaults, form ⇄ wire adapters
    ├── schemas/           — Zod source of truth for the xray config model
    ├── generated/         — code-generated Zod + TS types from Go (do not hand-edit)
    ├── models/            — thin legacy types still being migrated
    ├── styles/            — shared CSS (page-cards, …)
    ├── test/              — Vitest specs + golden fixtures
    └── utils/             — HttpUtil, ClipboardManager, SizeFormatter, …
```

For deeper notes on the frontend toolchain see [`frontend/README.md`](frontend/README.md).

## Project layout

| Path | Contents |
|------|----------|
| `main.go` | Process entry point, CLI subcommands, signal handling |
| `internal/web/` | Gin HTTP server, controllers, services, embedded frontend assets |
| `frontend/` | React + Ant Design 6 + TypeScript source for the panel UI |
| `internal/database/` | GORM models, migrations, seeders (PostgreSQL) |
| `internal/xray/` | Xray-core process lifecycle and gRPC API client |
| `internal/sub/` | Subscription endpoints (raw, JSON, Clash) |
| `internal/config/` | Environment-variable helpers, paths, defaults |
| `p-ui/` | **Runtime data** — logs, xray binary, geo files (gitignored) |

## Testing

Tests live next to the code (`foo.go` ↔ `foo_test.go`); frontend specs and golden fixtures live in `frontend/src/test/`.

### Go conventions

- **Stdlib `testing` only** — no testify. Table-driven with `t.Run` subtests and `t.Helper()` on helpers.
- **Assert the contract, not internals.** Pin the exact value / typed error / emitted string — not `err != nil` or `len > 0`. A test that still passes when the behavior is broken is worse than no test.
- **Real dependencies over mocks.** Database-backed tests run against a real PostgreSQL server: they `t.Skip` unless `PUI_DB_DSN` points at a reachable instance, so a green `go test ./...` on a machine without PostgreSQL does not mean those paths ran. Use `httptest` servers for HTTP. The `internal/sub` suite's `initSubDB(t)` is the template for the setup/teardown pair.

### Running

| Goal | Command |
|------|---------|
| Standard run | `go test ./...` |
| Hygiene — data races + order-dependence | `go test -race -shuffle=on -count=1 ./...` (`-race` needs a C toolchain — `build-essential`) |
| Coverage gaps | `go test -coverprofile=cov.out ./<pkg>/... && go tool cover -func=cov.out` |
| Fuzz a parser briefly | `go test -run '^$' -fuzz 'FuzzName$' -fuzztime=30s ./<pkg>/...` |
| Include the database-backed tests | `PUI_DB_DSN="postgres://pui:puipass@127.0.0.1:5432/pui?sslmode=disable" go test ./...` |

Frontend: `cd frontend && npm run test` (vitest), or `npm run test -- --coverage`.

### Property and fuzz tests

Input-heavy or pure logic (link builders, parsers, decoders) is also covered by **property tests** (`pgregory.net/rapid`) and **native fuzz targets** (`go test -fuzz`). A fuzz target's **seed corpus** (its inline `f.Add` cases plus any `testdata/fuzz` entries) runs as ordinary subtests under a plain `go test` — no `-fuzz` flag needed — so CI's normal test job exercises the seeds; the time-boxed *fuzzing* exploration (`-fuzz=...`) runs separately as the `fuzz-smoke` job.

### Mutation testing (optional, manual)

[gremlins](https://github.com/go-gremlins/gremlins) checks whether tests actually fail when the code is mutated — a surviving (`LIVED`) mutant means a weak test. It is **slow**, so run it **scoped per package**, never repo-wide or per-commit:

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
gremlins unleash ./internal/sub/
gremlins unleash -E 'server\.go|xray\.go|inbound\.go|client_bulk\.go|inbound_traffic\.go|.*_postgres_test\.go' ./internal/web/service/
```

Treat each survivor as one of: a weak test (strengthen it), dead code (remove it), or an equivalent mutant (unkillable — leave it). Don't write a test purely to kill a mutant if it doesn't reflect real behavior.

CI runs this for you nightly (and on demand) via `.github/workflows/mutation.yml` — scoped per package, results uploaded as artifacts. It is **informational**, not a gate (no thresholds), so check the reports when hardening a suite rather than waiting for a red build.

### CI

`.github/workflows/ci.yml` runs per PR: `go-test` (with `-shuffle -count=1`), a `race` job (`-race -shuffle -count=1`), a `fuzz-smoke` job on the critical parsers, a job that runs the durability tests against a live PostgreSQL service container (a SKIP there counts as a failure), and the frontend `typecheck`/`lint`/`test`/`build`/`build-storybook`. Snapshots are regression guards — regenerate them (`npx vitest run -u`) only for intentional output changes, never to make a red test green.

## Sending a pull request

1. Branch off `main` (e.g. `feat/short-description`).
2. Keep the diff focused — separate refactors from feature work.
3. Run the relevant checks before pushing:
   - `go build ./...`
   - `go test ./...` (when Go code changed)
   - `cd frontend && npm run typecheck && npm run lint && npm run test && npm run build && npm run build-storybook` (when the frontend changed; CI runs this same set on every PR via `.github/workflows/ci.yml`)
4. Commit messages follow the existing pattern in `git log` — `<area>: short imperative summary`, then a body explaining the *why*. Conventional-commit prefixes (`feat`, `fix`, `refactor`, `chore`, `style`, `docs`) are encouraged.
5. Open the PR against `main` with a brief description of what changed and how to test it.

## Useful environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `PUI_DEBUG` | `false` | Verbose logs + Gin debug mode + serve `/assets` from disk |
| `PUI_LOG_LEVEL` | `info` | `debug` / `info` / `notice` / `warning` / `error` |
| `PUI_DB_DSN` | — | **Required.** PostgreSQL DSN; the panel exits at startup if it is missing or unparseable |
| `PUI_LOG_FOLDER` | `/var/log/p-ui` | Where `pui.log` lives |
| `PUI_BIN_FOLDER` | `bin` | Where the xray binary, geo files, and xray `config.json` live |
| `PUI_INIT_WEB_BASE_PATH` | `/` | The initial URI path for the web panel |
| `PUI_PORT` | persisted `webPort` | Runtime-only web panel listener port override (`1` through `65535`) |

A valid `PUI_PORT` takes precedence over the database-backed `webPort` for the
current process without changing the stored setting. Unset, empty, whitespace-only,
malformed, or out-of-range values fall back to `webPort`; invalid configured values
also produce a warning.

## Issues

- Bug reports and feature requests: [GitHub Issues](https://github.com/Arman2122/p-ui/issues)

Before filing a bug, include the OS, Go version, panel version (`/panel/api/server/status` or the dashboard footer), and the relevant excerpt from `p-ui/pui.log`.
