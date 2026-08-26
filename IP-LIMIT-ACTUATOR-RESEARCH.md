# IP Limit: keep fail2ban, or replace it?

**Research report — 2026-08-20.** Static code analysis + web research. Nothing was run on a
Linux box; every claim needing a runtime check is listed in §8.

---

## 0. Verdict

> **Keep fail2ban as the daemon. The problem is not the actuator — it is the *integration*
> and the *ban shape*.** Ten small, actuator-independent fixes remove far more real risk than
> any swap. Two of them are Critical. Do those first. Then, optionally, delete the synthetic
> log line by calling `fail2ban-client set p-ui-ipl banip` directly. Do **not** build a native
> nft applier yet, and do **not** adopt CrowdSec.

The premise needed correcting before the question could be answered — see §1.

Two adversarial challengers attacked this verdict. The operational challenger upheld it. The
technical challenger upheld the *direction* but falsified two arguments for doing the swap
first, which is why the swap is sequenced second rather than led with (§6, §7).

---

## 1. What the system actually is

fail2ban is **not** doing the detection. The IP limit has three layers and only the last
touches it:

| Layer | Where | What it does |
|---|---|---|
| **Observe** | `internal/web/service/policy.go:363` `ObserveSessions` | Asks every registered core for its live sessions via the `core.SessionReporter` capability (`internal/core/caps.go:238`). The Xray adapter (`internal/cores/internal/xray/core.go:347`) calls the gRPC `GetOnlineUsers()`; kernel-WG reports its own (`internal/cores/internal/wireguard/core.go:317`). **No access-log parsing.** Runs every 10s (`internal/web/web.go:303`). |
| **Decide** | `internal/policy/ipcap.go` `Decide()` | Pure function. Merges persisted + observed, applies a stale cutoff and a live window, sorts oldest-first with a deterministic tiebreak, keeps the newest N, bans the remainder. `limit <= 0` means unlimited. `AdvancedSince()` suppresses re-reporting a pair whose `lastSeen` has not advanced. |
| **Enforce** | `internal/web/job/core_policy.go:100` `reportOverLimit` | **(a)** appends `[LIMIT_IP] Email = … \|\| Disconnecting OLD IP = … \|\| Timestamp = …` (`core_policy.go:150`) to a log file that a fail2ban jail regex-matches into an iptables ban. **(b)** `BounceClient` (`policy.go:592`) = `RemoveUser` → 100ms → `AddUser` through `runtime.Runtime` — **which does not actually cut anything on Xray; see the correction below.** |

> ### ⚠ Correction — 2026-08-20, from the end-to-end audit
>
> This report originally treated the bounce as a second working enforcement prong. **It is
> not.** Two findings from `AUDIT-2026-08-20.md`, both CONFIRMED against pinned xray-core
> source:
>
> - **F335** — `RemoveUser` deletes an entry from the handler's user validator
>   (`proxy/vless/validator.go:46-58`, a plain `sync.Map` delete) and touches nothing else: no
>   connection registry, no session table, no `Close()`. The validator is consulted **once per
>   connection**, at handshake. An established session therefore keeps flowing after its quota
>   is spent — unbounded with mux, since new logical streams ride the already-authenticated
>   connection with no re-auth.
> - **F337** — so `BounceClient` is a **no-op on every Xray protocol**. The 100 ms sleep
>   ("the daemon needs a moment to drop the sockets") guards nothing, and the re-add restores a
>   user who never lost anything. The doc comment at `policy.go:529` asserts the opposite —
>   *"RemoveUser drops ALL of that client's sessions on it"* — and
>   `policy_enforcement_test.go:48` repeats it, so the false semantic is load-bearing in the
>   design of `bounceTarget`.
>
> **What this changes.** The firewall prong is not merely *the only enforcement for
> wgkernel/awgkernel* — it is the only thing that cuts an established session **on any
> protocol**. On an install without fail2ban, the IP limit detects, logs, and enforces nothing
> at all. It also **invalidates recommendation 14 below as written** (bounce first, escalate to
> a firewall ban only where `bounceable()` is false): bouncing first would enforce nothing for
> Xray clients, which is most of them. See the revised §7 note.

So the question is **"what is the right ban actuator?"**, not "is fail2ban a good IDS."

### Two constraints decide it

**The firewall prong cannot be dropped.** `internal/web/service/policy.go:570-583`
`bounceable()` refuses node-hosted inbounds and any core where `RemovalLosesCounters` is true —
removing a kernel-WireGuard peer zeroes the byte counters the panel bills from. For
`wgkernel`/`awgkernel` inbounds **the firewall ban is the only enforcement that exists**.

And the cores cannot absorb it either. Researched, with sources: Xray-core has no per-user
concurrent-connection or per-user source-IP limit in any current release; `mtg-proxy`'s
`[throttle]` is global overload protection, not a device cap; kernel WireGuard keeps exactly
one auto-roaming endpoint per peer with no concurrency limit. **"Bounce only" is dead on
arrival.**

**You already own an nft table.** `internal/egress/masquerade.go:30-58` — `table inet p-ui-nat`,
replaced wholesale with `nft -f` because that is one atomic transaction, explicitly modelled
on how fail2ban owns `f2b-table`. So a native nft applier is not a new dependency class here.

---

## 2. Where you stand against the field

Sourced from upstream repos and release notes.

| Axis | p-ui | 3x-ui | alireza0/x-ui | Marzban/Marzneshin | Hiddify |
|---|---|---|---|---|---|
| **Observe** | gRPC online-stats API, **plus kernel-WG** | gRPC since v3.3.1 (2026-06-12) | gRPC, Xray-only | no first-party feature | nothing shipped |
| **Decide** | extracted pure function + frozen-`lastSeen` suppression | inline, flat 120s window | inline | — | — |
| **Actuate** | fail2ban + bounce | fail2ban + bounce (identical) | **native nftables**, `(srcIP, dstPort)`, 60s | third-party log scrapers | — |

- **Observe: parity, and ahead on breadth.** 3x-ui only moved off access-log parsing in June
  2026. Your `SessionReporter` spans kernel-WireGuard where the peers are Xray-only.
- **Decide: ahead of everyone.** Nobody else has an extracted pure decision function.
- **Actuate: the ensemble is typical; the firewall prong is the crudest still shipping in this
  lineage.** All TCP+UDP ports minus a frozen exemption list, 30 minutes, versus alireza0's
  single destination port for 60 seconds.

**The decisive competitor datapoint:** alireza0/x-ui *already did the swap you are asking
about* — deleted fail2ban, shipped an in-process `github.com/google/nftables` applier (table
`xui`, sets with `HasTimeout: true`, `BlockDuration = 60 * time.Second`, rule
`saddr . dport @set drop`). And their issue **#1632** (Jul 2026, closed *not planned*) reports
that the `(srcIP, dstPort)` key **still** collaterally blocks unrelated clients behind shared
addresses, because there is no per-client component in the block key at all.

That is the ceiling of the whole approach: **no L3/L4 key can carry the client identity the
decision layer works in.** A swap does not fix CGNAT.

### One upstream gap worth knowing

Xray-core's `app/stats/online_map.go` has **no time-based expiry and no cleanup ticker** —
an entry dies only when `refCount` hits zero via `RemoveIP`. A leaked connection persists
forever with a frozen `lastSeen`. Your `AdvancedSince()` is compensating for exactly this, and
does it better than 3x-ui's flat 120s window. Also: `OnlineMap.AddIP` silently drops loopback,
so if clients reach Xray through a same-box fronting proxy without PROXY protocol, **online
stats read 0 forever** while traffic counters keep growing.

---

## 3. Two Critical defects — neither is an actuator question

### C1 — `resetIpLimitsWithoutFail2ban` permanently destroys customer configuration

`internal/database/db.go:1422-1491`. Verified line by line.

On boot, if `fail2banCanEnforce()` is false (`db.go:1493-1498` — fail2ban absent, **or**
`PUI_ENABLE_FAIL2BAN` set to anything other than `"true"`), the seeder:

1. walks every inbound and sets `obj["limitIp"] = 0` in the settings JSON (`db.go:1467`);
2. runs `UPDATE clients SET limit_ip = 0 WHERE limit_ip <> 0` (`db.go:1485-1486`);
3. records itself in `HistoryOfSeeders` (`db.go:1489`) so **it never runs again** — reinstalling
   fail2ban does not restore anything.

`log.Printf` appears only on the two *skip* paths (`db.go:1447`, `db.go:1477`). **The wipe
itself is silent.**

**And the install ordering arms it on every upgrade.** `update.sh:1076` starts the panel;
`update.sh:1083` calls `setup_fail2ban`. `install.sh:1623-1624` starts the panel;
`install.sh:1632` calls `setup_fail2ban`. The panel boots — and wipes — *before* fail2ban is
installed, on the same run that was about to install it.

This was deliberate: `ce8b1bed feat(iplimit): gate IP limit on fail2ban and reset stale limits`
(2026-06-22). It is still wrong. The UI already refuses to edit the field when the capability
is unusable, and `EvaluateIPLimits` already ignores limits when `enforce` is false, so zeroing
*stored* configuration to reflect a *host* capability was never necessary — and it is one-way.

**Fix:** make the seeder a no-op that only records itself. Do not "repoint" it at a better
probe; that preserves the destruction with a different trigger. Panels already wiped are
unrecoverable — say so in the release notes.

Fresh installs are unharmed — and deliberately so: `db.go:1237` pre-marks
`ResetIpLimitNoFail2ban` as already-run in the fresh-install fast path, and
`seeder_fastpath_test.go:30` fails with the message *"fresh-install fast path must mark
ResetIpLimitNoFail2ban done so it cannot wipe admin-set IP limits on the next boot."* So the
wipe risk is known and was guarded — **for the fresh-install case only.** The upgrade case,
which is the one that actually has clients to lose, has no such guard. The damage case is an
**existing panel with IP limits, on a box without fail2ban, upgrading**.

### C2 — the firewall actuator's availability gates the core bounce too

`internal/web/service/policy.go:485` — when `enforce` is false the loop `continue`s **before**
`policy.Decide()` at `:494`, so no verdict is appended and `Bounce` is never set. `enforce` is
`service.AnyClientHasAnIPLimit() && fail2BanIsInstalled()` (`core_policy.go:79`).

So **a missing `fail2ban-client` binary disables the core bounce** — and, worse, suppresses the
verdict entirely, so nothing is logged either. (Per the correction in §1, the bounce would not
have cut an established Xray session anyway; the defect here is that the *decision* is never
made, which also blanks the panel's own record of who was over-cap.) The two prongs are
complements, not a package.

This is also the design mistake that must not be carried into any replacement.

---

## 4. High-severity defects in the current path

| # | Where | What |
|---|---|---|
| H1 | `core_policy.go:70-72` | `PUI_ENABLE_FAIL2BAN=false` returns **before** `ObserveSessions`, so it also freezes the client IP Log, `/clientIps` and both info modals. Docs describe it as an *enforcement* toggle. The adjacent tier deliberately preserves observation (`policy.go:486`), so the freeze is unintended. |
| H2 | `core_policy.go:156` | Health probe is `fail2ban-client -h` — it proves a Python binary exists. The panel's **own** menu option (`p-ui.sh:2101-2103`) deletes all three jail files and leaves that probe green. The UI then shows the cap enabled, the job logs "reported N address(es) over its cap", and **not one packet is dropped.** |
| H3 | `p-ui.sh:2192-2198` | Exempt ports (SSH + panel) are scraped once at jail creation and frozen into the action file. The grep reads only `/etc/ssh/sshd_config`, missing the `Include /etc/ssh/sshd_config.d/*.conf` every cloud image ships — so SSH on 2222 yields `exemptports = 22`. |
| H4 | `p-ui.sh` (no `load_pui_env`) | The only `source` statements in 2900 lines are `/etc/os-release`. From a plain login shell, "Change Ban Duration" re-runs `create_iplimit_jails` with no `PUI_DB_DSN`: `p-ui setting -show true` dies before printing `port:`, so the action is rewritten **without the panel-port exemption**, and `log_folder` falls back to `/var/log/p-ui` while the panel may write elsewhere — a jail tailing a file nobody writes. `install.sh`/`update.sh` load the env themselves, which makes this worse: it appears to work until someone opens the menu. |
| H5 | `policy.go:597-604` | `BounceClient` does `RemoveUser` → `sleep 100ms` → `AddUser` with no retry. If `AddUser` fails the client is **gone from the running core** and the caller only logs a Warning. The supervisor explicitly skips the panel-converged core (`PanelConvergedCore = xray.ID`), so nothing repairs it until an unrelated event rebuilds the config. A paying customer whose only offence was a third device gets a total outage, invisible in the UI. |
| H6 | `p-ui.sh:2109` | `remove_iplimit` option 2 runs `rm -rf /etc/fail2ban` — destroying every custom jail, filter and `.local` override the operator owns — when option 1 immediately above already removes p-ui's three files surgically. |
| H7 | `p-ui.sh:2231-2245` | `sed -i "/\[p-ui-ipl\]/,/^$/d"` runs to end-of-file when no blank line follows, silently eating an operator's `[sshd]` or `[recidive]` jail during a routine update. |
| H8 | `p-ui.sh:2031-2032` | `apt-get install python3-pip` + `pip install pyasynchat --break-system-packages` — the **only** step in the entire fail2ban install that leaves the distro archive. Works around LP#2055114, fixed in Ubuntu `1.0.2-3ubuntu0.1` (2024-06-27), nonexistent on 26.04's 1.1.0-9. PyPI is repeatedly reported unreachable from Iranian IPs. |

**Collateral find, outside this question:** `internal/egress/masquerade.go:58` runs
`destroy table inet p-ui-nat`. `nft destroy` needs nft ≥ 1.0.8 **and kernel ≥ 6.3**. Ubuntu
22.04 ships nft 1.0.2 / kernel 5.15; Debian 12 ships nft 1.0.6 / kernel 6.1 — so on two of five
supported targets that teardown is a parser error and the masquerade table is never removed.
One-line fix; needs a runtime check to confirm.

---

## 5. Options scored

| | Keep as-is | **Keep daemon, drop the log line** | Native nft set | CrowdSec |
|---|---|---|---|---|
| **Dependency cost** | apt fail2ban + Python + a **PyPI** fetch | same, minus the PyPI step | `nft` — already in Ubuntu 22.04/24.04 cloud images and Debian priority `important` | third-party apt repo, 2 daemons, a DB, ~100MB RAM |
| **Iran reachability** | PyPI step can fail mid-install | good | **best** — zero apt downloads in the common case | **blocked**: packagecloud.io states it blocks Iran under US sanctions |
| **Config drift** | 4 text contracts across a Go constant + 3 root-owned files | failregex/datepattern/logpath all become inert | none — panel owns the ruleset | n/a |
| **Fleet migration** | — | **none** (`banip` bypasses the filter, so deployed files need no change either way) | needs teardown; the hook exists (`update.sh:1083` already re-runs `p-ui setup-fail2ban`) | large |
| **Ban persistence** | fail2ban sqlite, restored with remaining time | **same** | **lost** — must rebuild `ban_until` panel-side | its own DB |
| **CGNAT / granularity** | all TCP+UDP minus a frozen exempt list, 30 min | unchanged | **best available**: allowlist of your own inbound ports, recomputed every tick | **worse** — bouncer has no port scoping at all |
| **Observability** | none — every failure looks identical to success | partial: exit code catches a missing/unloaded jail; **not** a broken action | full — you own the state | moderate |
| **Admin lockout** | worst (H3, H4) | slight regression: `banip` bypasses `ignoreip` | best — SSH cannot be shadowed by construction | risk (no port scoping) |
| **Effort** | — | medium; 7 of 14 runtime tests exist only for it | **large — a new subsystem** | large |

### Why the nft applier is rejected *for now*

The architecture is right and the pattern is already in production. But:

1. **Banning destroys the evidence.** Once an IP is dropped the core stops reporting it,
   `ObserveSessions` no longer sees it, and a set rebuilt from current verdicts empties on the
   next 10s tick — a ten-second ban that flaps forever. The panel **must** hold `ban_until`
   state. That is new state, a new writer for the ban audit trail, and a new vocabulary floor
   (pin to nft 1.0.2 — Ubuntu 22.04).
2. It gives up fail2ban's sqlite persistence, nine free `fail2ban-client` operator commands
   (`p-ui.sh:1911-2005`), and `pui-ipl-banned.log` whose sole writer is the action file
   (`p-ui.sh:2217`).
3. **alireza0 #1632 proves it does not solve CGNAT anyway.**

Against the hard rule that fix size must match bug size, that is not a fix — it is a project.
Revisit only if **per-inbound port scoping becomes a binding requirement**; then make that
argument explicitly.

Also rejected, each with a reason: `ipset`+iptables (deprecated upstream, extra package, still
needs an iptables rule, offers nothing nft sets lack) · `github.com/google/nftables` (v0.3.0,
README says "in very early stages" — wrong place for that) · eBPF/XDP (BPF maps have no
per-entry TTL, so you hand-rebuild the expiry nft gives free, on driver-dependent XDP, for a
performance win that is noise at this scale) · sshguard (no external ban interface — keeps the
log-line hack and loses the action file) · blacklistd (BSD, no Linux port) · OpenSnitch
(interactive per-application *egress* firewall) · firewalld / ufw (owning the operator's whole
host firewall for a timeout feature).

**Never uninstall the fail2ban package**, under any future swap: p-ui apt-installed it and the
distro's default `sshd` jail rides along. Operators have had SSH brute-force protection since
install without asking for it. Remove only p-ui's own three files.

---

## 6. What the challengers falsified

Kept here because these were arguments *for* swapping first, and they do not hold:

- **"The swap makes failures detectable."** Only partly. In fail2ban's `actions.py`,
  `banManager.addBanTicket` succeeds and the count increments **before** the action loop, which
  wraps `action.ban()` in `try/except` and swallows the exception into fail2ban's own log. The
  client still exits 0. So a broken `actionban` stays invisible, and
  `fail2ban-client status p-ui-ipl` reports the address as "Currently banned" while nothing is
  dropped. It catches a *deleted or unloaded jail* — genuinely worth doing, strictly better
  than `-h` — but it does not close the silent-failure class.
- **"The swap is free."** `banip` bypasses the filter, so no capture groups exist; `<F-USER>`
  in the action's echo (`p-ui.sh:2217`) renders as an **empty string**. `pui-ipl-banned.log`
  keeps producing BAN lines with a blank Email — losing the one field that maps a ban to a
  paying customer, which is why `p-ui banlog` and the Telegram report exist. Either the panel
  writes that line itself, or you touch a fielded file (destroying the "no migration" property).
- **`ignoreip` is silently dropped.** `banip` goes `Server.setBanIP → addBannedIP → __checkBan`
  with no ignore-list check; `inIgnoreIPList` lives only in `filter.py`. Operators who
  whitelisted their management address in `jail.local` lose that protection with no signal.
- **The ban is REJECT, not DROP.** fail2ban's `iptables-common.conf` defaults `blocktype` to
  `REJECT --reject-with icmp-port-unreachable`, and p-ui's action sets only name/chain/
  exemptports, so it inherits REJECT. Softens the CGNAT argument somewhat — confirm at runtime.
- **Multi-IP `banip` landed in 0.10.5 (2020), not 1.0.1.** Ubuntu 22.04's 0.11.2-6 clears the
  floor, so there is no per-address fallback loop to write.
- **"Dead chain forever" for nft was overstated.** `update.sh:1083` already runs the new
  binary's `p-ui setup-fail2ban` fleet-wide on every update, and `p-ui.sh:2101-2104` is already
  the surgical teardown. If the swap may use that hook, so may nft.

---

## 7. Do this, in this order

Steps 1-10 are **independent of the actuator choice** and fleet-safe. If only four ship this
release, ship 1-4.

| # | Change | Size | Files |
|---|---|---|---|
| **1** | **Make `resetIpLimitsWithoutFail2ban` a no-op that only records itself.** Delete the mutation. | small | `internal/database/db.go:1422-1498`, call site `:1352`, `seeder_fastpath_test.go` |
| **2** | **Split the enforce gate** so the firewall actuator's availability gates only the firewall prong, never the bounce. | small | `core_policy.go:79-86`, `policy.go:485-513` |
| **3** | Delete the early return so `PUI_ENABLE_FAIL2BAN` stops freezing observation. | one-liner | `core_policy.go:70-72` |
| **4** | **Probe the enforcement path, not the binary**: `fail2ban-client status p-ui-ipl` instead of `-h`, keep the 30s cache, surface a third UI state ("installed but the jail is not running"). **Must be `context.WithTimeout`-bounded** — see the warning below. | small | `core_policy.go:152-157`, `service/server.go:205-239` |
| **5** | Delete the PyPI `pyasynchat` step. Biggest Iran-reachability win in the report, for two deleted lines. | small | `p-ui.sh:2028-2043` (+ mirrors in `install.sh`, `update.sh`) |
| **6** | Add `load_pui_env` to `p-ui.sh`, mirroring `update.sh:155-162`. Fixes H4 and half of H3 with one function. | small | `p-ui.sh` top level |
| **7** | Replace the sshd_config grep with `sshd -T \| awk '/^port /{print $2}'`, keeping the grep as fallback. | one-liner | `p-ui.sh:2192-2194` |
| **8** | Make `BounceClient` recoverable: bounded retry on `AddUser`, Error on final failure. `AddUser` is idempotent. **Note after the §1 correction:** this fixes the *outage* risk (a failed `AddUser` leaves the client gone from the core), not enforcement — the bounce cuts nothing on Xray either way. While you are in this function, correct the false doc comment at `policy.go:529` ("RemoveUser drops ALL of that client's sessions") and `policy_enforcement_test.go:48`, which pins the wrong semantic as if it were verified. | small | `policy.go:529`, `:592-604`, `core_policy.go:136-138` |
| **9** | Stop damaging config you do not own: drop `rm -rf /etc/fail2ban`, fix the `sed` range delete, drop the global `backend` rewrite (p-ui's own jail overrides it back to `auto` anyway, so it only affects *other* people's jails). | small | `p-ui.sh:2109`, `:2231-2245`, `:2167` |
| **10** | Fix the docs that are already false: `docs/.../config/inbounds.mdx` still says `limitIp` "is enforced by reading Xray's access log". `docs/architecture.md:384` still lists `check_client_ip_job`, which `core_policy.go:22` says it replaced. Both locales. `docs-ci.yml` fires only on `docs/**`, so `make verify` will not catch these. | small | en + fa mirrors |
| **11** | **Then, optionally: the swap.** Replace the log append with `fail2ban-client set p-ui-ipl banip <ip>…`, check the exit code, leave the three jail files untouched. **Decide the audit trail explicitly** (blank `<F-USER>` — §6) and **preserve an ignore list panel-side or document the loss.** Rewrite `TestIPLimitLogLineMatchesTheInstallerRegex` (`core_policy_test.go:36-110`) to pin the argv and jail name rather than deleting it — it is the repo's only machine-checked Go↔actuator contract. | medium | `core_policy.go:100-140,150` |

### ⚠ One warning that applies to steps 4 and 11

Today the actuator is `os.OpenFile(O_APPEND)` + `Printf` — a local write with **no daemon on
the far end**. Note that `fail2ban-client -h` is answered by the client's own argparse, which is
exactly why it stays green with the daemon stopped. Both step 4 and step 11 put the panel's
**first synchronous dependency on fail2band's socket** into the hot loop. The existing pattern
is `exec.CommandContext(context.Background(), …)` — **no timeout** (`core_policy.go:156`). Copy
that and one hung call blocks `enforceIPLimits`, which blocks `Run()` — and the scheduler is
built with `cron.WithChain(cron.SkipIfStillRunning(cron.DiscardLogger), …)`
(`internal/web/web.go:516-521`), so every subsequent tick is **discarded with no log**. Because
`Run()` is `convergeShaping` then `enforceIPLimits` (`core_policy.go:39-43`), a stuck socket
would also freeze speed-ladder convergence. Use `context.WithTimeout` on every such call.

Also budget for this: step 4 converts *silent non-enforcement into visible breakage on update
day.* Boxes whose jail was deleted or never created currently show a green, editable IP Limit
field. After step 4 they show a greyed-out control. That is correct — but to the operator it
reads as "p-ui broke my IP limit in this release." Needs release-note copy and an `fa-IR`
string.

### On CGNAT — the real product question

Under carrier-grade NAT one IPv4 can represent thousands of subscribers, so banning an address
punishes strangers. **No actuator fixes this** (alireza0 #1632). The levers are duration and
scope, and both are available today:

- **Shorten the default bantime** (`p-ui.sh:2160`, `:2178`) and persist the operator's choice
  so `update.sh:878` stops reverting it. **But measure first** — `core_policy.go:96-98` states
  that re-ban-after-lapse is designed behaviour, so a 5-minute bantime plausibly trades one
  30-minute outage for a repeating ban/reconnect/re-ban cycle at 10s granularity. Possibly
  worse for anything long-lived.
- ~~**Escalate instead of double-punishing** — bounce first, firewall ban only where
  `bounceable()` is false.~~ **WITHDRAWN.** This was the largest lever I proposed, and F337
  kills it: the bounce cuts nothing on Xray, so "bounce first" would enforce nothing for most
  clients. The escalation only becomes available **after** F335's prerequisite is fixed — i.e.
  after there is a real way to terminate an established Xray session. xray-core exposes no
  per-user kick, so today the only mechanism that does it is a full core restart
  (`restartXrayOnClientDisable`), whose blast radius is every client on the box. That trade —
  drop everyone's connections to enforce one client's device cap — is a genuine product
  decision, not an obvious win, and it is yours to make.

  **What remains available today** is the narrower half: keep both prongs firing, but shrink
  the firewall action's blast radius — ban only the inbound's own ports rather than all
  TCP+UDP minus a frozen exempt list. That is an edit to the existing `action.d` template and
  needs no new mechanism, though it reintroduces the frozen-file coupling (§6), which is
  precisely the argument for the nft applier if port scoping ever becomes binding.

---

## 8. Must be checked on a real box before any of this ships

None is resolvable by more reading.

1. `fail2ban-client set p-ui-ipl banip 203.0.113.7 203.0.113.8; echo exit=$?` on Ubuntu 22.04's
   0.11.2-6. Settles the multi-IP floor and the "no fleet migration" property in one command.
2. After a manual `banip`, does `pui-ipl-banned.log` show a **populated** Email field? (Assert
   the field, not the line — a line will appear either way.)
3. Does the jail load and stay loaded when its `logpath` is never written? If not, step 11
   needs `backend = polling` in a **fielded** file, and "no migration" is false.
4. Confirm `blocktype` resolves to REJECT, not DROP, on each target.
5. Does an IPv6 source actually get banned by the current custom action, or fail silently?
6. Does `destroy table inet p-ui-nat` (`masquerade.go:58`) fail on Ubuntu 22.04 (nft 1.0.2 /
   kernel 5.15) and Debian 12 (1.0.6 / 6.1)?
7. Measure the ban/reconnect/re-ban cycle under a short bantime before shipping the cut.
8. Check `/etc/fail2ban/jail.local` on the prod panel and the VM for operator-added `ignoreip`
   entries. If operators rely on it, step 11 needs a panel-side allowlist first.

## 9. Conditions that would flip the verdict

- **Per-inbound port scoping becomes a binding requirement** → build the nft applier as agreed
  work. Do not get there by regenerating fail2ban action files on inbound change; that
  reintroduces exactly the frozen-file coupling this removes.
- **Test 3 fails** (jail will not load against an unwritten logpath) → the swap needs a fielded
  file change, at which point nft's single vocabulary floor is no worse and its port scoping is
  free.
- **Test 2 fails** → both options lose the ban audit trail, and one of the three costs charged
  against nft disappears.
- **Operator reports show CGNAT collateral is the dominant support category** → port scoping
  becomes binding; see the first condition.
