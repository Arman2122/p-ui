# Core review, 2026-09-13

Seven read-only review lanes (SSH, sing-box, ocserv + OpenVPN, routing/egress,
Xray protocols and transports, core management, extensibility) over the tree at
`d318cc5`, every finding traced to a line, then fixed in three waves with a test
that goes red without its fix. Every Go test ran on the Ubuntu 24.04 box with the
real daemons (Xray 26.7.28, sing-box 1.14.0, ocserv 1.2.4, OpenVPN 2.6.19, the
mtg-proxy fork). This file is the record of what was fixed, what is still open,
and what needs a decision.

## Fixed (commits f0b6e6e, 1a17718, 3356f95, cbb9238, 11d8f88, then d65a61e, 5c06677, f811d77)

Production-breaking, found by three lanes independently:

- The state directory of sing-box, OpenVPN and OpenConnect under `/var/lib/p-ui`
  was created readable by root alone, so under systemd the daemon user could not
  reach its config or control socket. Every OpenVPN unit and every exit died;
  ocserv ran while every login failed. Fixed in each core's directory setup; the
  systemd e2e now uses the core's own root the way an install does.
- OpenVPN wrote `disable-dco` unconditionally; 2.5 (Ubuntu 22.04) refuses it.
- A client's second OpenVPN/OpenConnect device was handed the first device's
  address; the RADIUS ledger dropped an idle session the daemon still reported.
- ocserv "redirect off" wrote no route, which is a default route to ocserv;
  the pool route it writes now passes `ocserv --test-config` (gated e2e).

Security:

- A routed SSH forward could name the server's own public address through the
  bridge, and `localhost` through a device exit (the marked freedom outbound
  resolved nothing before its private block). The bridge refuses own public
  addresses; the marked outbound carries the template direct's `finalRules`.

Correctness the operator would hit:

- The MTProto installer now asks the candidate binary to run every config
  (`run --check`, added to the fork in `8196ba8`, plus `web-relay --check`) the
  way the sing-box installer does; installs are serialised on every core. The
  panel's synthetic config and a live inbound's file pass the fork built from
  `8196ba8` (gated e2e, `TestRealBinaryAcceptsTheInstallDryRun`), so v1.9.0 has
  to be tagged from a commit that includes it.
- sing-box: a daemon that outlived the panel was never adopted (every panel
  restart SIGHUPed every daemon); a refused config was written to the live path;
  user order cost a second reload; the "connections survive" wording was false.
- SSH: a stop could wait forever on a login that completed after its snapshot,
  holding the core lock; an exit whose remote never speaks pinned the exit; a
  listener that could not bind vanished from Health; the limiter set its limit
  before its burst.
- Routing: a scope-all rule was refused whenever any inbound could not carry it;
  the UDP narrowed off a TCP-only exit was silent; protocol/attrs rules never
  switched the sniffer on; MTProto offered source criteria; Xray offered `user`
  on kinds with no users; re-enabling a routed kernel inbound came back dark.
- Core management: the versions dialog dropped the release list when the binary
  was missing; Xray's Restart was withheld beside a foreign xray; a core restart
  dropped that core's exits until the egress job; the ARM Xray name mismatched.
- Subscription: TUIC and AnyTLS links never reached it; the client pickers left
  ocserv out; REALITY offered the `unsafe` fingerprint xray-core refuses; an
  XHTTP-over-h3 inbound was counted as TCP by port-conflict detection.

## Decided and landed (2026-09-14: d65a61e, 5c06677, f811d77)

The four decisions the review left open were taken, each the fail-closed way,
and the ranked list below them landed; the anchors are the same as before.

1. **UDP toward a TCP-only exit is dropped, not left to fall through.** A
   companion `network: udp -> blackhole` rule follows the narrowed one; the
   warning says a `udp` rule placed above carries UDP elsewhere. The default
   template has no DNS rule, so client DNS (UDP) is what this catches first. Chosen over
   "warn only" because the fall-through left with the server's own address,
   which is the leak an exit rule exists to prevent.
2. **Routed SSH udpgw DNS goes through the route as DNS over TCP**, to the
   resolver the app named when it is public, else to `1.1.1.1`. TCP because the
   common routed exit is an SSH server, which carries nothing else; a direct
   inbound keeps the host resolver.
3. **A device exit the host does not route is contained.** `Exits()` asks the
   egress manager per device exit; a contained one compiles to the blackhole
   with a warning, and a change in what the host routes after a reconcile arms
   the Xray restart. Chosen over "drop the exit from the input", which would
   have compiled its rules to nothing and let the traffic through direct.
4. **The JSON subscription writes a Host row's TLS keys at the top level** of
   `tlsSettings` (pins comma-joined), verified against xray's own `TLSConfig`.
5. SSH: five refused logins from one address in five minutes shut it out for
   ten; refusals carry their reason as a banner (not a wrong password); the
   banner is hot-applied; the exit answers keyboard-interactive-only servers.
6. The Cores page counts a child of a managed pid as managed (`parentPid`).
7. The OpenConnect form takes an operator's own certificate pair, checked as a
   pair; the OpenConnect exit dials a camouflaged server with its secret.
8. TUIC and AnyTLS render in the Clash profile through the registry's share
   seam; the JSON format still omits them, because xray has no outbound.
9. Conformance runs in CI for every core: sing-box, OpenConnect and OpenVPN
   have daemon-less rigs, and the suite holds `InstanceMinter`,
   `RoutableEgress`, `ExitSupervisor` and `TagTrafficSource` to invariants.
10. sing-box keeps activity across a failed read and treats a restart it did
    not make as a new epoch; `RequiredBuildTags` is guarded against
    `release.yml`; the vmess XHTTP mode travels in `type` on both builders and
    the corpus pins vmess links. The release-list cache already had a
    15-minute TTL (`core_versions.go`); that item was wrong.

## What remains

- mKCP header type and seed are API-only: the form's kcp schema has neither,
  so the Go link renders them for API rows and the panel's link cannot. The
  corpus pins the form-reachable shape. Adding the fields is a form feature.
- The JSON subscription has no entry for TUIC or AnyTLS (xray has none).
- The SSH login throttle's numbers (5 failures, 5 minutes, 10 minutes) are
  fixed, not settings.
- The Xray v26.7.28 to v26.9.9 bump (go 1.27, new xray-core pin) was in flight
  in another session's working copy while this landed; this work was verified
  against the committed go.mod, and the pin test in `internal/arch` holds the
  two dates together once that bump commits.

## Adding a core today

Measured on the two newest cores: about 8 shared Go/script files, 12 TypeScript
files, two to four hand-kept test tables and three or four docs, roughly 27
touch points, of which the TypeScript column is the whole remaining gap against
§11.3's target. The Go side is registry-driven end to end (jobs, quota, routing,
exits, versions, credentials, transports); the TypeScript side has one module
per kind but no ratchet. The list below is what a new core touches, derived
from what the review verified, in the order that keeps each step compiling.

Go, the core itself:

1. `internal/cores/internal/<x>/core.go`: `ID`, the `Kind` constants,
   `Describe()` answering all five `Caps`, `Kinds()`, `Preflight()` (binary or
   module present; a failure disables the core, never the panel). Implement
   `Supervisor` and only the capabilities the daemon can honour. Every per-kind
   method (`ProvisionsUsers`, `ClientCredentials`, `Transports`,
   `IngressSelector`, `ShapingSelector`, `SharesFile`) answers for each kind;
   `Transports` sets at least one of TCP/UDP or says none. Credential names come
   from `internal/core/credentials.go` only. The control surface binds to
   loopback or a unix socket; `Deps.User/Group` reach `supervisor.Spec` and the
   state root under `/var/lib/p-ui/<x>` is group-traversable (the bug this
   review found in three cores). Minted secrets live in `settings` through
   `InstanceMinter` and are carried forward on update.
2. A rig for `coretest.Check` that runs without the daemon (fake
   `supervisor.Backend`, in-process sink; model `ssh/driver_test.go`), plus an
   env-gated real run for the VM. Hand tests for what coretest does not drive:
   tag-traffic deltas, mint carry-forward, quota reset reaching the daemon, an
   `Install` refusing an unlisted version.
3. `internal/cores/cores.go`: the import, a `<x>Deps(deps)` narrowing func when
   the core needs RADIUS, the supervisor, admission or an HTTP client, and one
   `reg.Register` line.
4. `internal/database/model/model.go`: one `Protocol` const
   (`TestProtocolSourcesAgree` fails until it matches).
5. `en-US.json` and `fa-IR.json`: `cores.<id>.title`, `protocols.names.<kind>`,
   a `protocols.groups.<g>` if the picker needs a group, every `BlockedKey` and
   validation key the core returns.
6. `internal/web/service/core_host_runtime.go`: the daemon's binary names.
7. Shipping: a distribution daemon gets `install_<x>_packages` in `install.sh`
   and `update.sh` (mask the distro unit; pin it in
   `internal/arch/installer_core_user_test.go`); a bundled binary gets a
   `release.yml` fetch or build named `<bin>-linux-<platform>` and an
   `internal/arch/<x>_pin_test.go` copied from `singbox_pin_test.go`.
8. Only if needed: a new credential name in `credentials.go`, a new API struct
   in openapigen's `StructAllow`, a new route in `endpoints.ts`.

TypeScript, until the §9 renderer exists:

9. `src/schemas/protocols/inbound/<x>.ts` (settings and client schema),
   registered in its `index.ts` (export and union member).
10. `src/lib/xray/inbound-defaults.ts`: the default settings and the
    `AnyInboundSettings` union.
11. `src/lib/cores/kinds/<x>.ts` with `multiUser` answered (the client pickers
    read it now), one line in `kinds/index.ts`, the name key and group in
    `kind-labels.ts`.
12. `src/pages/inbounds/form/protocols/<x>.tsx`, its export in
    `protocols/index.ts`, one editor line in `InboundFormModal.tsx`.
13. For a URI kind, `inbound-link.ts` (`getInboundClients`, `genLink`) for the
    panel's own link; the subscription renders from Go's `Share`.
14. The pinned tables: `src/test/inbound-tag.test.ts`,
    `src/test/kind-modules.test.ts`; `inbound-tone.ts`, `display.ts` and story
    fixtures when the core has a tone or a managed runtime.
15. `make gen` (Node 24), `make verify`, then `npm test` (the i18n dead-key
    test is frontend-side).

An exit, when the core also terminates routes:

16. `RoutableEgress`, plus `ExitSupervisor` when the daemon dials the exit; a
    supervised `ExitDevice` kind registers through the `coretun` driver. A
    panel-dialled exit is a driver in `internal/egress/drivers/`, an
    `egress.go` register and a `routing.go` `exitKindFor` arm. TypeScript:
    `schemas/api/egress.ts`, `exits/<x>-fields.tsx`, `exits/index.ts`
    `MODULES`, the `outbound-exit-modules.test.ts` list.

Docs, which nothing checks:

17. `frontend/public/openapi.json` copied to `docs/public/openapi.json` and
    `pnpm gen:api`; a section in `docs/content/docs/{en,fa}/config/inbounds.mdx`;
    `docs/lib/xray/links.ts` when the docs site builds the new URI; a §12.5
    note in `docs/multi-core-architecture.md`.

Then, on the VM as root with the daemon installed: the env-gated conformance
run, the raw subscription carrying the new kind's link, and the client form
listing its inbounds.
