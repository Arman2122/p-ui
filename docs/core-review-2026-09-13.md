# Core review, 2026-09-13

Seven read-only review lanes (SSH, sing-box, ocserv + OpenVPN, routing/egress,
Xray protocols and transports, core management, extensibility) over the tree at
`d318cc5`, every finding traced to a line, then fixed in three waves with a test
that goes red without its fix. Every Go test ran on the Ubuntu 24.04 box with the
real daemons (Xray 26.7.28, sing-box 1.14.0, ocserv 1.2.4, OpenVPN 2.6.19, the
mtg-proxy fork). This file is the record of what was fixed, what is still open,
and what needs a decision.

## Fixed (commits f0b6e6e, 1a17718, 3356f95, cbb9238)

Production-breaking, found by three lanes independently:

- The state directory of sing-box, OpenVPN and OpenConnect under `/var/lib/p-ui`
  was created readable by root alone, so under systemd the daemon user could not
  reach its config or control socket. Every OpenVPN unit and every exit died;
  ocserv ran while every login failed. Fixed in each core's directory setup; the
  systemd e2e now uses the core's own root the way an install does.
- OpenVPN wrote `disable-dco` unconditionally; 2.5 (Ubuntu 22.04) refuses it.
- A client's second OpenVPN/OpenConnect device was handed the first device's
  address; the RADIUS ledger dropped an idle session the daemon still reported.
- ocserv "redirect off" wrote no route, which is a default route to ocserv.

Security:

- A routed SSH forward could name the server's own public address through the
  bridge, and `localhost` through a device exit (the marked freedom outbound
  resolved nothing before its private block). The bridge refuses own public
  addresses; the marked outbound carries the template direct's `finalRules`.

Correctness the operator would hit:

- The MTProto installer now asks the candidate binary to run every config
  (`run --check`, added to the fork in `8196ba8`, plus `web-relay --check`) the
  way the sing-box installer does; installs are serialised on every core.
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

## Still open, ranked

Each with the lane's anchor and the smallest fix. Nothing here is fail-open.

1. **UDP toward a TCP-only exit stays uncovered by design** (`plan.go`
   `narrowToTCP`). The rule now warns visibly; the alternative is a companion
   `network: udp -> blackhole` rule at the same position, which would break a
   layered "all -> SSH exit, then udp -> WG exit" setup. Decision for the owner.
2. **Routed SSH udpgw DNS goes to the host resolver** (`ssh/udpgw.go:208-212`):
   DNS-flagged datagrams are answered from the server even on a routed inbound.
   Fix needs a resolver reachable through the router; a design choice.
3. **A failed `ip rule add fwmark` fails open** (`routing.go` `Exits`): the
   marked outbound is emitted from the row, never from the kernel. Two-line fix:
   ask `egressManager.MarkedExit` per device exit and drop `ErrNotRouted` ones
   from `Input.Exits`. Needs a check that the service tests' fake plane answers.
4. **Host-row TLS overrides are dropped from the JSON subscription**
   (`sub/service.go` `applyExternalProxyTLSToStream` writes
   `pinnedPeerCertSha256`/`echConfigList`/`verifyPeerCertByName` under
   `tlsSettings.settings`, a key xray has no member for). Verify the field types
   in the pinned `infra/conf/transport_security.go` before writing them top-level.
5. **No per-source throttle on failed SSH logins** (`ssh/server.go:247-255`):
   MaxAuthTries 3 per connection, no lockout, failures reach only the UI toast.
6. **mtproto's WEB relay and ocserv's workers show as "Other host process"**
   (`core_host_runtime.go`, `display.ts:22-27`) and the install dialog says they
   will not be restarted, while RestartCore restarts them. Fix: report the
   parent pid and count a child of a managed pid as managed.
7. **The Cisco Secure Client path needs a CA-signed pair the form cannot take**
   (`ocserv.tsx` has no `serverCert`/`serverKey` inputs; the API accepts them).
8. **The SSH exit cannot authenticate to keyboard-interactive-only sshd**
   (`ssh/exit.go:203-213`); an OpenConnect exit cannot dial a camouflaged
   server (`ocserv/exit.go:33-43`); SSH refusal reasons never reach the client
   (`BannerError`); a banner edit drops every session; `/clash` and `/json`
   omit TUIC and AnyTLS while mihomo supports both.
9. **Conformance runs in CI for five of eight cores**: ocserv, openvpn and
   sing-box call `coretest.Check` only under their e2e gates; a fake-backend
   rig for each would make CI hold the contract. `RoutableEgress`,
   `TagTrafficSource`, `InstanceMinter`, `ExitSupervisor` have no coretest case.
10. Lower: sing-box `Sessions()` forgets activity after one transient read
    failure; a systemd auto-restart changes no counter epoch (undercount only);
    the release-list cache has no age cap; `RequiredBuildTags` and
    `release.yml`'s `-tags` are unguarded copies; the kcp/vmess+xhttp share
    links disagree between the Go and TS builders with no golden fixture.

## Adding a core today

Measured on the two newest cores: about 8 shared Go/script files, 13 TypeScript
files, two to four hand-kept test tables and three or four docs, roughly 28
touch points, of which the TypeScript column is the whole remaining gap against
§11.3's target. The Go side is registry-driven end to end (jobs, quota, routing,
exits, versions, credentials, transports); the TypeScript side has one module
per kind but no ratchet, and the client pickers were the last hand-kept list.
