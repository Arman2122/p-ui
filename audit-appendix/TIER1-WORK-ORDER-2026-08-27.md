# Tier-1 work order — 2026-08-27

`TRIAGE-2026-08-27.md` found 45 findings that can hurt a running panel. Eleven
are fixed. This is the queue for the rest, and the questions that gate it.

## How it was produced

Two passes, 148 agents. Every remaining finding was re-read against the tree and
a fix designed; an adversary then tried to break each plan. **It broke 35 of 38**
— missed sibling sites, tests that would have passed without the fix, behaviour
regressions, false premises. A second pass revised each against its critique and
split it into *what ships with no behaviour choice* versus *what needs a
decision*, and re-checked that split.

None of the 38 turned out to be already fixed.

**Nothing here is a plan you should trust without re-reading the code.** The
adversary's hit rate on the first pass was 92%; these are second drafts, not
verdicts. Every entry names its sibling sites and says what its test must fail
on — treat those as the parts worth checking first.

## Already landed

F100, F125, F152, F160, F225, F248, F315, F350, F371, F383 — plus the
`bulkResetTraffic` UI gap and the `Adjust` relabel.

Two were flagged as needing an owner decision and turned out not to:
**F248**'s docs contradicted the field's own tooltip, and **F315**'s tiebreaker
("does xray-core's WireGuard outbound honour finalmask?") was a fact about the
code, answered by `client.go:280` and confirmed with `xray -test`.

---

## A) EXECUTION QUEUE

Worst-first. `F216` folded into `F035` (same root, same loop). `F372` folded into `F369` (same loop, contradictory scope — see Q2). `F182` and `F316` are decision-blocked, not here.

| # | ID | Commit |
|---|---|---|
| 1 | **F304** | Three nil-map guards (`ensureStatsPolicy`, `ensureAPIServices`, `EnsureStatsRouting`) — stops a `{"policy":null}` template panicking the boot path into a systemd crash loop. Commit body must drop the false "must land together" interlock. |
| 2 | **F035** (+F216) | Port guard in `injectMtprotoEgress` using `listenOverlaps`+`inboundTransports`, plus the sibling-mtproto loop — a bridge port collision skips one bridge instead of killing the whole Xray core on EADDRINUSE after `Stop()`. Green-fence count is 3, not 2. |
| 3 | **F032** | `inboundPortClaimMu` around `checkPortConflict`→commit in AddInbound/UpdateInbound — stops two concurrent saves both claiming a port and taking the core down. Apply by anchor statement; line numbers are 13 stale. |
| 4 | **F238** | `Manager.applyMu` + `LockApply/UnlockApply` at four core sites — stops a concurrent apply gRPC-deleting the winner's live listener. Core-side only; service-side race stays open (Q5). |
| 5 | **F054** | Parameterize `GetXrayConfig(retireFronts bool)`; only `RestartXray` passes true — a settings-page GET stops deleting egress rows and tearing down `ip rule` state. |
| 6 | **F309** | Default `autoOutboundsInterface` to `''` + drop-when-empty + placeholder/info fixes — stops a new tun inbound binding every Xray outbound to one NIC. Stored rows unchanged (Q7). |
| 7 | **F007** | `else if xray.TestConfig(...)` on the cold branch of `RestartXray` — a config the core refuses stops reporting "started". Seed no template; use the shipped default. |
| 8 | **F140** | nil vs empty distinction honoured in `getSubs`/clash/json — an inbound whose every host excludes the format is withheld instead of falling back to the origin address the exclusion exists to hide. |
| 9 | **F135** | Emit `sniBlank` and honour it in all four TLS appliers (incl. hysteria) — `keepSniBlank` stops being a silent no-op. Correct the revert-protocol: reverting edit 6 leaves Case C green. |
| 10 | **F045** | `identityCollision` guard at AddInbound, addInboundClient, UpdateInboundClient + BulkCreate precheck — **one commit** — stops two clients sharing a config identity silently overwriting each other in the core. Use `mkInbound`, not `nodeInbound`. |
| 11 | **F067** | `wireguardHostAddrs` seam + foreign-prefix blocking in `wireguardPoolFor` + create-path refusal — WireGuard addresses stop colliding with real host devices. **Must** add the default-neutral stub in the package TestMain; expected allocations are `10.9.1.0/32` / `10.0.16.0/32`. |
| 12 | **F354** | One `SetToNeedRestart()` in `updateOutboundSub` — a disabled outbound subscription stops serving indefinitely. |
| 13 | **F338** | Count Xray-served depleted inbounds into the auto-disable restart signal — a depleted inbound stops serving its live sessions forever. Fix the named/unnamed result collision before committing. |
| 14 | **F235** | Add the missing `else` → `needRestart = true` when `rt.DelInbound` fails in UpdateInbound — a disabled inbound's listener no longer survives. |
| 15 | **F217** | `Ensure` loop instead of `Reconcile(desired)` before the quota reset — a traffic reset stops permanently killing an MTProto inbound whose settings won't parse. Log at Warning, not Debug. |
| 16 | **F076** | Teach `ownedUplinkID` the `paux` namespace — the egress manager stops treating its own AWG uplink devices as foreign squatters. |
| 17 | **F369** | Scope the LDAP **enable** direction to clients present on `ldapInboundTags` + surface the swallowed parse error — LDAP stops reverting an admin's manual disable panel-wide. Disable/delete scope pending Q2. |
| 18 | **F373** | `scheduleLdapSync` fallback to `@every 1m` + named cron parser in prod and in `cadence_test` — an unparseable spec stops silently disabling all LDAP sync. |
| 19 | **F292** | `currentAdminIds()` accessor at 5 read sites + `IsRunning()` at 2 — closes the torn-slice read on the Telegram auth gate. |
| 20 | **F396** | Reword `twoFactorModalSetSuccess` in both locales — stops claiming 2FA is established when nothing was sent. Scope the Confirm query to the dialog. |
| 21 | **F355** | `SSRFGuardedDialContext` on the traffic-inform and tgbot clients + `CheckRedirect` on the subscription client. **Use `DialTimeout`, not `Dial`**, or the 3s inform budget becomes 10s and traffic polls skip. |
| 22 | **F376** | Third `auth_posture` branch for `ldapUseTLS=false`, with bind-DN and admin-filter clauses gated on their settings. |
| 23 | **F236** | `storeRoutingSync` fallback + `applyWarn` on rename — routing-tag propagation stops failing permanently on an already-invalid stored template. |
| 24 | **F189** | Cross-store guard on moveUp/moveDown + `data-merged-index` for drag (both files) — reordering stops targeting the wrong rule and intent drag starts working. |
| 25 | **F188** | Move `attrs` out of the L3 mask, regex-validate it, fix both TS bridge legs, gate `attrs`/`vlessRoute`, render it in both stores. `Compiled.Fronts` does have a consumer — fix the deferred narrative. |
| 26 | **F050** | mtproto core reports a forced-outbound subject blocked + `groupRules` skips blocked subjects — the picker stops lying and scope=all saves stop being refused. Use the literal key string in the test. |
| 27 | **F069** | Latch the migration walk on every kept non-api rule — stops the one-time conversion hoisting rules above the private block. |
| 28 | **F362** | Restore the operator's WARP+ licence into the blob before `UpdateWarpXraySetting` — rotation stops permanently erasing it. Needs `warpAPIBase` const→var. |
| 29 | **F352** | `!= "tls"` in TestConnection — the Test SMTP button stops green-lighting a dead channel and red-lighting a working one. |
| 30 | **F366** | Wrap auth-over-cleartext in `sendPlain` + new `smtpErrorUnencrypted` key + strip the double `pages.settings.` prefix. After F352. |
| 31 | **F273** | Chat notice on both DB-backup failure paths + one new i18n key — the admin stops being told a backup happened and receiving nothing. |

---

## B) DECISION BATCH

**Q1 — New save-time refusals vs rows that already violate them.** For `routeXrayPort` collisions (F035/F216), WireGuard host-address overlap on edit (F067), duplicate client identity in `UpdateInbound` (F045), a bad `ldapSyncCron` blocking *every* settings save (F373), template-section validation on the internal repair paths (F007), and a portMap that can never fire (F316): refuse **(A) always**, **(B) on create only**, **(C) only when the edit worsens it**, or **(D) never — warn at generation time**? *Unblocks 6 findings, incl. all of F316.*

**Q2 — How far does LDAP's authority reach?** Scope only enable to `ldapInboundTags` (shipped), **or also disable** (F369 — fail-open on off-boarding), **and does the delete sweep need a provenance column + migration** so hand-made clients survive, plus adoption of pre-existing identities onto LDAP inbounds (F372)? *Unblocks F372 entirely and F369's other half; needs a column + migration.*

**Q3 — Encryption posture for the panel's own credentialed outbound connections.** SMTP `"none"` + STARTTLS and LDAP without TLS: **(A)** keep opportunistic/cleartext and only report honestly, **(B)** enforce/refuse when credentials are configured (F352, F366's save-time rejection), or **(C)** add the missing capability (LDAP StartTLS) and flip shipped defaults to 636/TLS (F376)? *Unblocks F352's enforcement half, F366's save gate, F376's capability.*

**Q4 — Panel-injected rules vs operator intent rules.** Does the mtproto force-outbound rule keep winning first match (F050) and do intent rules keep being spliced *above* the `geoip:private` block (F069), or should intent become overridable / constrained below it? *Sets product semantics for both; F069's harm narrative outlives its fix either way.*

**Q5 — How much serialization on the apply path.** Extend the apply lock to `RestartXray`/`StopXray` with a staleness recheck (F238) and move `AddInbound`'s tx onto the serialized writer (F032) — accepting stalled traffic polls and dropped deltas on large imports — or keep the narrow core-only lock and leave the service-vs-core race open? *Unblocks the headline half of F238 and F032's 40P01 exposure.*

**Q6 — Reach of host-level hiding controls.** Do reality hosts honour keep-SNI-blank (ships an unconnectable config, F135), do the panel's own admin link renderers withhold excluded formats (F140), and does "all hosts disabled" mean withhold or fall back to origin (F140)? *Closes the residual origin-leak in both.*

**Q7 — Healing legacy stored values the panel itself wrote.** Synthesize an absent/null `policy`+`stats` block so per-user accounting is guaranteed (F304, reverses a pinned punt) and strip `autoOutboundsInterface:"auto"` from stored tun rows at render (F309, indistinguishable from an operator-typed value)? *Both are "we cannot tell our own writes from theirs".*

**Q8 — Widen the core/driver contracts, or keep service-layer approximations?** A per-kind credential-normalization answer (F045: SS-2022 uPSK, MTProto secret, vless byte-6/7 and hex-case variants), per-protocol criterion granularity + L3 sniffing (F188: `vlessRoute`, `attrs` on L3), and driver-declared dialled-device namespaces (F076: SSH/OpenVPN/IKEv2/PPP each bring another blind prefix)? *One architectural call, three extensions.*

**Q9 — Partial failure: report to the caller, or log only?** A WARP rotation whose licence re-apply failed currently returns success (F362); the config.json arm of the Telegram backup is silent and a notice there would false-alarm every panel that never started Xray (F273). *Sets the house rule for both.*

**Q10 — Node tag collision on push (F182).** When a node renames a pushed tag: **(A)** refuse + roll back (silently clobbers a serving node-local inbound on the next sync), **(B)** converge — central adopts the node's tag, needs runtime→service plumbing + a UNIQUE(tag) guard, or **(C)** leave the false fingerprint? *F182 has no sound shippable subset today; this is all of it.*

**Q11 — Should a config read still upsert egress fronts (F054)?** Removing the upsert opens the `KeyNoFront` hole; keeping it means a page view still creates a panel front row the reconcile job converges before the core has the matching tun inbound.

**Q12 — Should a manual/admin disable bounce Xray the way auto-disable does (F338)?** The existing setting says "Auto Disable"; extending it to hand toggles is new semantics but the live-session hole is identical.

**Q13 — Closing the tgbot `bot` global race (F292).** Don't clobber `bot` on a failed reload / nil-guard the two edit helpers / full locked accessor — different reload semantics, a worker panic takes the panel down today.

**Q14 — Where does the core quota reset belong relative to re-enable (F217)?** Today every reset path zeroes the sidecar counter while the client is still filtered out, so mtg's retained `quotaUsed` blocks the renewed client indefinitely.

**Q15 — Admin-typed private subscription targets (F355).** Block `ClientExternalLink.Value` resolving to a private IP — requiring an `AllowPrivate` column + migration to match `OutboundSubscription` — or keep loopback/LAN subscriptions working, given the failure would be entirely silent?

**Q16 — Immediacy of operator actions.** Should enabling 2FA persist on Confirm rather than at Save (F396 — posts the whole draft and fires `BumpLoginEpoch`/logout early), and should outbound-subscription mutations apply synchronously rather than at the next 30s tick (F354, family-wide)?

---

## Per-finding detail

Every entry below carries the revised root cause, the exact edit, the sites
including siblings, the test that must be red without it, and the adversary's
verdict. Generated from the workflow journal.

### F007 — ship-it (small)
**Root:** Two defects, only one of which is undisputed.

UNDISPUTED (the outage): internal/web/service/xray.go:1240-1260. `xray.TestConfig(xrayConfig)` — the panel's real config gate, the `-test` subprocess that runs the core's own atomic loader — sits INSIDE `if process != nil && process.IsRunning()`. The launch at :1262-1265 is reached by BOTH branches. So every cold start (boot via web.go:350, the Restart button after a manual stop via server.go:903, the CLI at main.go:121/163, CheckXrayRunningJob's crash-recovery restart) skips the gate entirely. `process.Start()` then returns nil as soon as `cmd.Start()` succeeds (process.go:602-609, startCommand) — the core's refusal happens milliseconds later in the child. The core's own message DOES reach the log (process.go:686-695 logs at Error, calls OnCrash, publishes eventbus.EventXrayCrash) — the original plan's "no diagnostic anywhere" was wrong. The real harm is that `RestartXray` RETURNS NIL: ServerService.RestartXrayService (server.go:903) hands that nil to the operator's Restart button as success, and main.go's CLI exits 0, while xray is down.

DISPUTED (deferred): CheckXrayConfig (xray_setting.go:62-88) validates only the outbounds array; `routing`, `dns`, `policy`, `log`, `stats`, `api` are `json_util.RawMessage` on xray.Config, so any syntactically valid JSON persists. But CheckXrayConfig is NOT an operator-only boundary — verified four internal callers — so tightening it in place is a behaviour choice, not a repair. See `deferred`.
**Ships now:** Hoist the pre-flight onto the cold branch of RestartXray. One `else if` at internal/web/service/xray.go:1260, three lines, no reordering:

    	_ = process.Stop()
    } else if err := xray.TestConfig(xrayConfig); err != nil {
    	return common.NewError("refusing to start xray, the generated config is rejected by the core:", err)
    }

Why an `else` and not a hoist above the `if`: the running branch's early return at :1243-1246 (configUnchanged && !isNeedXrayRestart) must keep returning without an exec — ApplyPendingRestart hits it every 30s. The `else` fires only when there is no running process, so it adds zero execs to the no-op path and preserves the deliberate ordering at :1247-1254 (TestConfig ahead of tryHotApply and ahead of process.Stop()).

Why this is an undisputed repair, not a choice:
- The identical call already guards the running path at :1252. It introduces no new class of refusal — if `-test` were prone to false negatives it would already be breaking every restart on every running box.
- TestConfig fails open by construction (preflight.go:63-67: no binary, timeout, or non-ExitError → returns nil, "this check must never become the reason a restart cannot happen"). It cannot lock an operator out.
- Exec cost is neutral. CheckXrayRunningJob retries every ~3s while xray is down; today each retry spawns a doomed full process, after the fix each runs `-test` and skips the doomed spawn.
- End state on a bad config is identical (xray down). Only the report changes: from a false "started" to the core's own refusal line, parsed by coreRefusal().
- No caller rolls back on error. controller/xray_setting.go saves at :142 BEFORE restarting at :156, so a refusal never loses the operator's edit. inbound.go:1225/1704 already treat restart-adjacent failures as non-fatal. web.go:350 logs a Warning and the panel still boots.

No new columns, jobs, config knobs or abstractions. No i18n key (the message is the core's own, surfaced through the existing jsonMsg path).
**Deferred:** DEFERRED: all save-boundary section validation (routing / dns / policy / log / stats / api).

Why, verified rather than assumed. `CheckXrayConfig` is not an operator-only boundary. Four internal paths re-save a template the operator did not just type, and I read each caller's error handling:
- UpdateWarpXraySetting (xray_setting.go:220) returns SaveXraySetting's error verbatim; integration/warp.go:202 propagates it. Validators here HARD-FAIL WARP configuration on any box whose stored template is already bad — which is exactly the F007 population.
- PropagateInboundTagRename (:285) and RemoveInboundTagReferences (:322): callers at inbound.go:1704 and :1225 only `logger.Warning` and continue, and the inbound edit is already committed. Validators here do not block the rename or delete — they SILENTLY stop propagating, leaving routing rules naming a renamed or deleted inbound tag. That is worse than the adversary's "refuses ALL of them": it is invisible.
- The unwrap-heal (controller/xray_setting.go:84) already assigns `xraySetting = unwrapped` in both branches, so it is unaffected either way.

So gating them all is a regression, and gating only the operator POST needs a new entry point — a behaviour choice about whether a legacy-bad template stays repairable. That is the question below, not a repair.

Also deferred, with reasons:
- Duplicate outbound tags. Cannot be a save-boundary vendored check at all: preflight_test.go:79-81 states it is "The one refusal no config builder catches ... it comes from registering the handlers". It only exists in the `-test` subprocess, so it belongs to the restart boundary and the shipped hoist already covers it.
- Any guard test for the fs.ErrNotExist carve-out in ValidateRoutingCriteria (api.go:219-224). The adversary is right that "geoip:private must pass with no .dat files" is environment-dependent: ensureXrayAssetLocation points XRAY_LOCATION_ASSET at the bin folder, and on any box that HAS geoip.dat the build succeeds outright, so the test stays green with the carve-out deleted. If that guard is ever wanted it must stage an empty asset dir, not rely on the host.
- db.go's FreedomFinalRules / FreedomFinalRulesReverseFix direct template writes. One-shot migrations bypassing SaveXraySetting; the shipped hoist catches their output at the next start.
- The finding's literal suggestion of calling xray.TestConfig inside CheckXrayConfig. Rejected on its own merits regardless of the above: it collapses the split preflight_test.go:110-113 documents deliberately (the save boundary must let a not-yet-issued certificate through, and validateInboundForXray's fs.ErrNotExist carve-out at xray.go:435-442 enforces it), and it puts a subprocess exec on every internal repair path including the unwrap-heal that runs on every settings page GET.
- conf.Config.Build() over the whole template. Rejected: xray-core infra/conf/xray.go:533-537 calls os.Setenv for every key in the template's `env` block, letting an operator-authored JSON field mutate the panel process's environment.
**Test:** New test in internal/web/service/xray_restart_test.go (the file already holds RestartXray tests and the setupSettingTestDB pattern):

`TestRestartXrayRefusesAConfigTheCoreRejectsWhenNothingIsRunning`
1. Skip if runtime.GOOS == "windows" (mirrors requirePosixHost in internal/xray/posix_only_test.go — the stand-in core is a shell script). Repo is Linux-only; CI is Linux.
2. `setupSettingTestDB(t)` — skips without PUI_DB_DSN, and CI's live-Postgres job counts a SKIP as a failure, so it does run.
3. Seed a VALID `xrayTemplateConfig` (unlike the two existing tests in this file, which seed `{ not valid json` so GetXrayConfig fails early). It must parse, or the cold branch is never reached.
4. `dir := t.TempDir(); t.Setenv("PUI_BIN_FOLDER", dir)`. Write a stand-in core at `filepath.Join(dir, xray.GetBinaryName())`, mode 0755, contents:
       #!/bin/sh
       echo "Failed to start: main: failed to load config"
       exit 23
   (Same technique as internal/xray/preflight_staged_test.go's writeStandInCore; that helper is in package xray, so this test writes its own ~6 lines.) PUI_BIN_FOLDER also relocates GetConfigPath(), so nothing touches a real install.
5. Assert `currentXrayProcess()` is nil; register a t.Cleanup that stops any process left registered in xray.GetManager() and restores it, or -shuffle=on leaks the stand-in child into sibling tests.
6. `err := (&XrayService{}).RestartXray(true)`
7. Assert: `err != nil && strings.Contains(err.Error(), "main: failed to load config")`.

WHY IT IS RED TODAY, precisely. process is nil, so the entire :1241-1260 block is skipped and TestConfig never runs. Execution reaches :1262: NewProcess, Replace, NoteCoreRestart, then Start(). Inside Start, `checkAPIPortFree` returns nil (process.go:538-540 — a failed dial means the port is free, and nothing is listening in a test), the config marshals and is written to the temp dir, and `exec.CommandContext(stand-in, "-c", path)` is a real executable so `cmd.Start()` succeeds. startCommand returns nil, Start returns nil, RestartXray returns nil. The test fails with:
   RestartXray returned <nil> after launching a core that cannot load this config; want the core's refusal containing "main: failed to load config"

WHY IT IS GREEN WITH THE FIX. The `else if` runs TestConfig → TestConfigWith(GetBinaryPath(), cfg) → writes the preflight temp file into filepath.Dir(GetConfigPath()) (the same temp dir) → execs the stand-in with `-test -c <path>` → exit 23 is an *exec.ExitError, so preflight.go:69 returns common.NewError(coreRefusal(out)); coreRefusal cuts the "Failed to start: " prefix and yields `main: failed to load config`. RestartXray wraps it as "refusing to start xray, the generated config is rejected by the core: main: failed to load config". Substring matches.

THE GUARD AGAINST PASSING EITHER WAY. The assertion is the substring, never `err != nil`. That is the exact defect earlier plans were rejected for. If the error arrived for any other reason — GetXrayConfig failing on a bad seed, or checkAPIPortFree finding the port busy on a loaded CI box — it would be non-nil but would NOT carry the stand-in's line, and the test would still fail. The substring ties the pass to the pre-flight actually having run.

Optional second subtest, same file, cheap and also red today: with a stand-in that exits 0, RestartXray(true) must return nil — proving the new branch fails open and does not become a new reason a start cannot happen.
**Sites:**
- internal/web/service/xray.go:1241 — `if process != nil && process.IsRunning() {` — the scope bug: the gate lives inside this branch
- internal/web/service/xray.go:1252-1254 — the existing `xray.TestConfig(xrayConfig)` call, running-path only; the model the cold branch must mirror
- internal/web/service/xray.go:1260 — the closing brace; THE EDIT SITE, where the `else if` attaches
- internal/web/service/xray.go:1262-1268 — `process = xray.NewProcess(xrayConfig); xrayState().Replace(process); s.xrayAPI.NoteCoreRestart(); err = process.Start()` — reached by both branches, ungated today
- internal/xray/process.go:602-609 — `err = p.startCommand(cmd); if err != nil { return err }; ...; return nil` — Start() returns nil once cmd.Start() succeeds, regardless of whether the core accepts the config
- internal/xray/process.go:686-695 — waitForCommand logs Error + OnCrash; this is why the finding's 'silent' framing was wrong and the fix is sized to the lying return value, not to missing logs
- internal/xray/preflight.go:35-70 — TestConfig / TestConfigWith and their documented fail-open contract; unchanged by this fix, but it is what makes the hoist safe
- internal/web/service/server.go:903-908 — ServerService.RestartXrayService returns RestartXray's error to the operator's Restart button; the caller that today receives a false nil
- internal/web/web.go:348-353 — boot always takes the cold branch; logs a Warning, panel still boots
- internal/web/job/check_xray_running_job.go:33 — crash-recovery restart, cold branch, retries roughly every 3s; the exec-cost site
- main.go:121 and main.go:163 — the `p-ui` CLI restart paths, cold branch, exit code driven by this error
- SIBLING, DEFERRED: internal/web/service/xray_setting.go:62-88 CheckXrayConfig — outbounds-only validation, the save-boundary hole
- SIBLING, DEFERRED: internal/web/service/xray_setting.go:220 — UpdateWarpXraySetting returns SaveXraySetting's error directly; propagated by internal/web/service/integration/warp.go:202. HARD FAIL if validators are added here
- SIBLING, DEFERRED: internal/web/service/xray_setting_routing_sync.go:285 (PropagateInboundTagRename) and :322 (RemoveInboundTagReferences) — callers at internal/web/service/inbound.go:1704 and :1225 only logger.Warning and continue, so validators here cause SILENT routing divergence, not a blocked edit
- SIBLING, ALREADY TOLERANT: internal/web/controller/xray_setting.go:84 — the unwrap-heal assigns `xraySetting = unwrapped` in both the success and failure branches, so a refusal here changes nothing
- SIBLING, OUT OF SCOPE: internal/database/db.go FreedomFinalRulesReverseFix / FreedomFinalRules — one-shot migrations that write the template directly, bypassing SaveXraySetting; whatever they produce is now caught at the next start by the shipped hoist
- MODEL TO CITE, ALREADY CORRECT: internal/web/service/outbound_subscription.go:30-52 filterOutboundsRejectedByCore, and internal/xray/api.go:204-224 ValidateRoutingCriteria with its existing errors.Is(err, fs.ErrNotExist) carve-out

**Re-check notes:**
- NON-BLOCKING, rationale slightly overstated: 'Exec cost is neutral' holds only when the config IS the crash cause. internal/web/job/check_xray_running_job.go:26-38 restarts after 2 consecutive down-checks (~3s), and when the crash cause is something else -- a port conflict, an OOM kill -- TestConfig passes and the doomed spawn still happens, so that loop gains one exec per retry rather than swapping one. Bounded by xrayTestTimeout (preflight.go:19) and serialized by the package `lock` at xray.go:1227, so it is a cost note, not a defect. The fix ships as written.
- NON-BLOCKING, omitted from the sites list: internal/cores/internal/xray/core.go:103-126 Core.Reconcile has the identical shape -- NewProcess -> mgr.Replace -> noteRestart -> `return process.Start()` -- with no TestConfig on EITHER branch, running or cold. It is dormant today, not a live sibling: internal/web/job/core_supervise.go:44 skips the panel-converged core, and internal/cores/cores.go:162 sets `PanelConvergedCore = xray.ID`. Worth naming in the commit body as where this defect returns when that seam goes live; not a reason to hold the fix.
- NON-BLOCKING, test-authoring risk in step 3: the plan says to seed a VALID xrayTemplateConfig by hand. If that hand-written seed does not survive GetXrayBaseConfig's json.Unmarshal (xray.go:113-117), the test is red both before AND after the fix, for the wrong reason. The substring assertion does catch it -- that is the guard working -- but the simpler route is to seed NOTHING: SettingService.GetXrayConfigTemplate is getString("xrayTemplateConfig") (setting.go:410-412), which falls back to the shipped default template, guaranteed to parse. The stand-in exits 23 regardless of config content, so the test does not need a bespoke template.
- CONTEXT, narrows the operator-visible surface without undercutting the claim: internal/web/controller/xray_setting.go:155 calls RestartXray only `if a.XrayService.IsXrayRunning()`, so the operator's settings POST never reaches the cold branch. The paths the fix actually repairs are boot (web.go:349), crash recovery (check_xray_running_job.go:33), the CLI/SIGUSR1 restarts in main.go, and the Restart button while xray is down (server.go:903). All of those are exactly the F007 population -- a box whose stored template the core will not load.


### F032 — ship-it (small)
**Root:** The invariant "no two inbounds on the same node may claim overlapping (listen, port, transport)" is enforced by an application read that is not mutually exclusive with the write it guards. `checkPortConflict` scans through the ambient handle (port_conflict.go:215 `db := database.GetDB()`, :218 `Where("port = ?")`) and BOTH callers run it before their write opens — AddInbound at inbound.go:953 (write is a plain `db.Transaction` at :1036) and UpdateInbound at inbound.go:1416 (write is `runSerializedTx` at :1450). Nothing spans check→commit. Postgres READ COMMITTED gives no phantom protection over an empty candidate set, so two racers both see nothing and both commit.

The only DB uniqueness on the table is `Tag` (`gorm:"unique"`, model/model.go:77) and it is NOT a backstop: `generateInboundTag`'s "-2".."-99" loop (port_conflict.go:318-327) renames the loser past it. For UpdateInbound the escape is deterministic, not lucky — tag resolution happens INSIDE the tx at :1578, on the single serialized writer, therefore always after the winner's commit, so `tagExists` sees the winner's tag taken and hands the loser "in-<port>-tcp-2". The row lands, Xray then binds the port twice and the process fails to start.

The ADVERSARY IS RIGHT ON ALL FOUR POINTS; I verified each independently:
1. The proposed second subtest is a duplicate — port_conflict_test.go:180-206 already asserts both directions of the shadowsocks dual-listen case, and `shared := existingBits & newBits` (port_conflict.go:234) is commutative. Its stated justification ("pins semantics across the tag boundary") is false: calling checkPortConflict directly never reaches resolveInboundTag. Dropped.
2. The old test pinned only Update-vs-Update, so reverting the AddInbound half left it green. Replaced with a test that goes red if EITHER half is reverted.
3. The mutex alternative was strawmanned. A single package-level mutex taken by both callers IS sufficient — mutual exclusion across check→commit is exactly the missing property, and the panel is one process per database (nodes carry their own panel+DB), so no cross-process primitive is needed. Verified no lock-order cycle: `lockInbound` (client_locks.go:29) is taken only by client-level ops (client_bulk.go:563/922/1541, client_inbound_apply.go:57/285/501/871, pending_client_disable.go:66) and none of them call AddInbound/UpdateInbound; no traffic-writer job calls them either, so holding this mutex across UpdateInbound's runSerializedTx cannot deadlock.
4. Moving AddInbound onto runSerializedTx is unrequested architecture with a real operator cost. Verified: importInbound (controller/inbound.go:442) hands AddInbound an arbitrary number of ClientStats, and AddInbound's tx (inbound.go:1036-1125) holds the per-stat OnConflict loop, the per-client AddClientStat loop, clientService.SyncInbound and CreateHostsFromExternalProxy. On the single writer that stalls the @every-5s traffic poll and every node heartbeat's SetRemoteTraffic (inbound_node.go:266-268 submits through the same writer).

ONE CAVEAT THE ADVERSARY DID NOT STATE: a `defer`-based unlock holds the mutex across the post-commit runtime push (inbound.go:1129 and :1694). That is acceptable because it is already the house idiom — `defer lockInbound(inboundId).Unlock()` (client_inbound_apply.go:57) likewise spans post-commit pushes — and it is precisely why AddInbound must NOT also move onto the writer.

Correction to the finding's evidence, unchanged from the first plan and re-verified: the cited "in-443-tcp vs in-443-ws" example is wrong for this tree. `transportTagSuffix` (port_conflict.go:264-274) emits only tcp/udp/tcpudp/any, and ws is TCP (pinned by port_conflict_test.go:59). The real smoking gun is a "-2"-suffixed duplicate, e.g. two rows on port 3000 tagged `in-3000-tcp` and `in-3000-tcp-2`.
**Ships now:** One package-level mutex, taken by both mutation entry points before their first committed-state read and released after their write commits. Two files, ~8 lines, no signature changes, no relocated blocks, no migration, no i18n key, no new endpoint, no layering change (runtime.Runtime dispatch untouched).

(1) internal/web/service/client_locks.go — next to the existing `inboundMutationLocks` block:

    // One inbound create/edit at a time. checkPortConflict and its siblings read
    // committed rows, so a second mutation must not land between check and commit.
    var inboundPortClaimMu sync.Mutex

(2) internal/web/service/inbound.go — in AddInbound, immediately before line 953:

    inboundPortClaimMu.Lock()
    defer inboundPortClaimMu.Unlock()

    conflict, err := s.checkPortConflict(inbound, 0)

(3) internal/web/service/inbound.go — in UpdateInbound, immediately before line 1416:

    inboundPortClaimMu.Lock()
    defer inboundPortClaimMu.Unlock()

    conflict, err := s.checkPortConflict(inbound, inbound.Id)

WHY THIS IS THE UNDISPUTED REPAIR: every read whose result the write depends on now sits inside one critical section with that write's commit, so the losing racer's `checkPortConflict` sees the winner's committed row and is refused with the existing message — "port 3000 (tcp) already used by inbound 'X' (#1) on *" — instead of being silently renamed to `in-3000-tcp-2` and taking Xray down. No operator can prefer two inbounds on one port and a dead core.

SIBLINGS COVERED FOR FREE, because they already sit between the lock point and the write:
- checkWireguardAddressConflict (client_wireguard.go:65, reads `database.GetDB()` at :74, NO DB backstop at all) — its only two call sites are inbound.go:960 and :1423, both inside the new critical section.
- checkFallbacksSurviveEdit (inbound_protocol.go:51) — only call site inbound.go:1429, inside.
- resolveInboundTag / tagExists (port_conflict.go:331/407, ambient handle at :408) — both call sites, inbound.go:964 (Add, pre-tx) and :1578 (Update, in-tx), inside. The "-2" rename can no longer fire on a racer.
- checkEmailsExistForClients at inbound.go:980 — inside.

The lock covers the sub-node ingress path too: a master pushing an inbound to a node arrives at the node through the same controller endpoint (controller/inbound.go:187) and therefore the same AddInbound.

NOT CHANGED, deliberately: `checkPortConflict`/`tagExists` signatures stay as they are. Threading a `*gorm.DB` through them would be a bigger diff that buys nothing here — the mutex, not transaction scoping, is what supplies the missing exclusion, and READ COMMITTED inside a tx would still see no phantom rows.
**Deferred:** 1. AddInbound's transaction (inbound.go:1036) stays a plain `db.Transaction` rather than `runSerializedTx`. That leaves the pre-existing lock-order deadlock exposure (SQLSTATE 40P01) that runSerializedTx exists to remove: an AddInbound tx can still run concurrently with the @every-5s traffic poll. Fixing it is a behaviour choice with an operator-visible cost (importInbound, controller/inbound.go:442, would put an arbitrary number of ClientStats plus SyncInbound plus CreateHostsFromExternalProxy on the single writer, stalling the poll and every node heartbeat's SetRemoteTraffic). Not needed for F032. → the decision question.

2. Node adoption, inbound_node.go:579 `tx.Create(&newIb)`, creates central inbound rows with a tag check (`pickFreeTag`) and NO port check at all. It runs on the writer goroutine, outside the new mutex, so an admin AddInbound for node N can still race an adoption for node N. Left out because adoption is mirroring what the node already runs — refusing there is a different behaviour question and a different finding, not a race repair.

3. Releasing the mutex immediately after commit instead of at function end (so it is not held across the post-commit runtime push). Rejected on fix-size grounds: ~8 explicit unlock sites across the early returns in AddInbound alone, against a cost the house idiom already accepts.

4. Threading a `*gorm.DB` through checkPortConflict / tagExists / generateInboundTag / resolveInboundTag, as the first plan proposed. Dropped: it changes four signatures and every caller to buy transaction scoping that does not supply the missing property — READ COMMITTED inside a tx still sees no phantom rows.

5. The other two checkEmailsExistForClients call sites (client_inbound_apply.go:313/:557) stay outside the mutex. Verified backstopped by `gorm:"unique"` on ClientTraffic.Email (internal/core/traffic.go:9), so the loser gets a DB refusal rather than a duplicate.
**Test:** New file internal/web/service/inbound_port_race_test.go, stdlib testing, real Postgres via `setupConflictDB(t)` (skips without PUI_DB_DSN). Harness template: inbound_update_tag_test.go:15, which already drives UpdateInbound end-to-end with nothing but setupConflictDB + seedInboundConflict and no runtime manager.

ONE test, `TestInboundPortClaimIsExclusiveAcrossAddAndUpdate`, red if EITHER half of the fix is reverted. The barrier is a test-only GORM query callback — no production hook, no sleep as a correctness signal.

Setup:
- seedInboundConflict(t, "in-1000-tcp", "0.0.0.0", 1000, model.VLESS, `{"network":"tcp"}`, `{"clients":[],"decryption":"none"}`)
- Register on database.GetDB(): `Callback().Query().After("gorm:query").Register("f032_barrier", fn)`, removed in t.Cleanup. fn ignores everything except `tx.Statement.Table == "inbounds" && strings.Contains(tx.Statement.SQL.String(), "port = ")` — which, verified by grep, only port_conflict.go:218 emits. On the 1st such query: `close(bChecked); <-releaseB`. On the 2nd: `close(aChecked)`.

Sequence:
1. Goroutine B: UpdateInbound moving the seeded row to port 3000. Its scan fires the barrier and parks INSIDE checkPortConflict — with the fix, holding the mutex.
2. Main: `<-bChecked` (5s failsafe that closes releaseB and t.Fatal). B is now provably past its read and before its commit.
3. Goroutine A: signals `aStarted`, then AddInbound of a fresh disabled VLESS/tcp inbound on port 3000. Main waits on `<-aStarted` so scheduling slack cannot fake a green.
4. THE RED ASSERTION — a positive event, not a timeout:

    select {
    case <-aChecked:
        close(releaseB)
        t.Fatal("AddInbound ran its port-conflict scan while an UpdateInbound was between its own scan and its commit on the same port: the check and the write it guards are not mutually exclusive")
    case <-time.After(500 * time.Millisecond):
    }
    close(releaseB)

5. Join both. Assert exactly one call returned nil; assert A's error contains "port 3000 (tcp) already used by"; assert `SELECT count(*) FROM inbounds WHERE port = 3000` is 1, with the message "port 3000 ended up with %d inbounds; the loser was renamed past UNIQUE(inbounds.tag) instead of refused".

WHAT FAILS WITHOUT THE FIX, EXACTLY: A reaches port_conflict.go:222 within microseconds, the callback's 2nd hit closes aChecked, and the test fails with "AddInbound ran its port-conflict scan while an UpdateInbound was between its own scan and its commit on the same port: the check and the write it guards are not mutually exclusive". Let the timeout branch run instead and the count assertion fails with "port 3000 ended up with 2 inbounds; the loser was renamed past UNIQUE(inbounds.tag) instead of refused" — two independent reds.

BOTH HALVES PINNED (the specific failure the adversary caught):
- Revert only AddInbound's 2 lines → A is unblocked → aChecked fires → RED.
- Revert only UpdateInbound's 2 lines → B holds nothing → A acquires freely → aChecked fires → RED.
- Both present → A blocks on the mutex, emits no second query, the 500ms elapses, releaseB lets B commit, A's scan then sees B's row and A is refused → GREEN.

The 500ms is a liveness grace window only; the red signal is A's query actually happening, so the test cannot go green by being slow.

DROPPED from the first plan: the "second, cheaper subtest". Verified duplicate of TestCheckPortConflict_ShadowsocksDualListenBlocksBoth (port_conflict_test.go:180-206) and its stated justification was false.

Verification loop per CLAUDE.md: write it, revert each half in turn, watch it go red both times, restore. Run with PUI_DB_DSN set, plus `make lint`.
**Sites:**
- internal/web/service/port_conflict.go:215 — `db := database.GetDB()` in checkPortConflict; the ambient handle, so the read never joins the caller's write
- internal/web/service/port_conflict.go:218 — `q := db.Model(model.Inbound{}).Where("port = ?", inbound.Port)`; the candidate scan that is stale by commit time (the ONLY `port = ?` predicate against inbounds in production Go — verified by grep)
- internal/web/service/port_conflict.go:234 — `shared := existingBits & newBits`; commutative, which is why the adversary's duplicate-subtest objection holds
- internal/web/service/port_conflict.go:318-327 — generateInboundTag's "-2".."-99" loop; the rename that walks the loser past UNIQUE(inbounds.tag)
- internal/web/service/port_conflict.go:408 — `db := database.GetDB()` in tagExists; same ambient handle
- internal/database/model/model.go:77 — `Tag string ... gorm:"unique"`; the only uniqueness on inbounds, and Port has none
- internal/web/service/inbound.go:953 — `conflict, err := s.checkPortConflict(inbound, 0)` in AddInbound; LOCK POINT 1
- internal/web/service/inbound.go:960 — `s.checkWireguardAddressConflict(inbound, 0)`; sibling, covered
- internal/web/service/inbound.go:964 — `inbound.Tag, err = s.resolveInboundTag(inbound, 0)`; sibling, covered
- internal/web/service/inbound.go:980 — `s.clientService.checkEmailsExistForClients(...)`; sibling, covered (and separately backstopped)
- internal/web/service/inbound.go:1036 — `err = db.Transaction(func(tx *gorm.DB) error {`; AddInbound's write, NOT on the serialized writer (left as-is — see deferred)
- internal/web/service/inbound.go:1129 — `postCommitApply()`; the runtime push the mutex is held across under a defer-based unlock
- internal/web/service/inbound.go:1416 — `conflict, err := s.checkPortConflict(inbound, inbound.Id)` in UpdateInbound; LOCK POINT 2
- internal/web/service/inbound.go:1423 — `s.checkWireguardAddressConflict(inbound, inbound.Id)`; sibling, covered
- internal/web/service/inbound.go:1429 — `s.checkFallbacksSurviveEdit(inbound)`; sibling, covered
- internal/web/service/inbound.go:1450 — `txErr := runSerializedTx(func(tx *gorm.DB) error {`; the write, 34 lines after the check
- internal/web/service/inbound.go:1578 — `resolvedTag, err := s.resolveInboundTag(inbound, inbound.Id)`; INSIDE the tx, which makes the loser's escape deterministic rather than lucky
- internal/web/service/inbound.go:1670 — `tx.Save(oldInbound)`; the duplicate row actually landing
- internal/web/service/client_wireguard.go:74 — `q := database.GetDB().Model(model.Inbound{}).Where("protocol IN ?", ...)`; same shape, NO DB backstop, covered by the same mutex
- internal/web/service/client_locks.go:23-38 — the existing `inboundMutationLocks` / `lockInbound` block; where the new mutex goes, and the proof there is no lock-order cycle
- internal/web/service/traffic_writer.go:126 — runSerializedTx; the writer the mutex is held across in UpdateInbound
- internal/web/controller/inbound.go:442 — importInbound → AddInbound with arbitrary ClientStats; the cost that keeps AddInbound off the writer
- internal/core/traffic.go:9 — `Email string ... gorm:"unique"`; the verified DB backstop for the checkEmailsExistForClients call sites left outside the mutex
- internal/web/service/inbound_node.go:579 — `tx.Create(&newIb)`; node adoption creates central inbounds with a tag check but no port check, outside the mutex (deferred, different finding)

**Re-check notes:**
- Line numbers in the claim are uniformly 13 stale against the working tree: checkPortConflict is at inbound.go:940 (Add) and :1403 (Update), the AddInbound db.Transaction at :1023, the UpdateInbound runSerializedTx at :1437, the in-tx resolveInboundTag at :1565, tx.Save(oldInbound) at :1657. Apply by the anchor statements, not the line numbers. A naive line-number apply fails at compile (duplicate `conflict, err :=`), so this is noise, not a silent wrong fix.
- Wording erratum, not a defect: 'before their first committed-state read' is loose for UpdateInbound. s.GetInbound(inbound.Id) at ~inbound.go:1385 stays outside the critical section. It feeds the NodeID restore and the oldTagWasAuto heuristic, not the port claim, so the exclusion property the fix needs is still intact.
- Test-authoring caution: A's payload must actually survive AddInbound's pre-checks (validateInstanceForKind, refuseUnenforcedAccounts, normalizeInboundShareAddressStrict) to reach the port scan. If it errors earlier the test goes green-when-broken. Failure direction is otherwise safe, UpdateInbound's identical gauntlet already passes with the same `{"clients":[],"decryption":"none"}` shape (inbound_update_tag_test.go:15), and the mandated revert-each-half loop is the proof. Verified separately that validateInboundForXray (xray.go:424) no-ops on a disabled inbound, so the count==1 assertion needs no xray binary.
- One sibling stays live, correctly fenced: node adoption at inbound_node.go:556-579 creates central inbound rows with pickFreeTag (tag only) and no port check, outside the mutex. Verified as described. It is node-scoped mirroring of what the node already runs, so it is a different finding, not this race.


### F035 — ship-with-the-noted-correction (small)
**Root:** The mtproto loopback SOCKS bridge port (`routeXrayPort`) is the panel's only persisted, fixed, generated listener, and it participates in none of the port-uniqueness mechanisms the repo owns. It is born unchecked (`normalizeMtprotoXrayPort`, inbound.go:863-910, takes stored → client-supplied → `mtproto.FreeLocalPort()` with no scan), never re-checked (`checkPortConflict`, port_conflict.go:194-248, reads `reservedAPIPort`, the panel/sub listeners and `inbounds.port` — `routeXrayPort` appears nowhere in that file), and emitted unchecked (`injectMtprotoEgress`, xray.go:712-717, guards only on tag collision before appending at :750-756). Because `xray -test` binds nothing (internal/xray/preflight.go:23-33) the pre-flight passes, and `RestartXray` does `_ = process.Stop()` (xray.go:1258) BEFORE `Start()` (:1263) — so the duplicate bind takes the entire Xray core down, every inbound of every protocol, and each retry repeats it. Corrections to the first plan: (i) the collision class is wider than cfg.InboundConfigs — no mtproto row is ever in the generated config, because `kinds` in internal/cores/internal/xray/core.go:29-32 omits "mtproto" and `RenderInbound` returns nil at xray.go:246, so an mtg sidecar's own bind (`0.0.0.0:P`, manager.go:76-82, TCP per cores/internal/mtproto/core.go:306) is invisible to any cfg-only guard, including the inbound's OWN port; (ii) a port-equality guard must be transport-aware, because internal/cores/internal/xray/transports.go:25-28 and :39-44 produce genuinely UDP-only inbounds (hysteria, xray-wireguard, kcp/quic network) that share a port with a loopback TCP bridge today with no OS conflict — refusing there would be a regression, and not a benign one, since manager.go:631-632 writes `proxies = ["socks5://127.0.0.1:P"]` from the DB whether or not the bridge was injected, so an over-skip leaves mtg dialling a dead port and that whole mtproto inbound dark.
**Ships now:** Containment only, entirely inside config generation: no DB write, no migration, no new column/job/knob/route, no i18n key (the existing skips in this function already log English via `logger.Warning`). Two edits.

(1) `internal/web/service/xray.go:699` — give `injectMtprotoEgress` a third parameter `siblings []*model.Inbound`, and at the single call site `internal/web/service/xray.go:173` pass the `inbounds` slice the loop already holds: `injectMtprotoEgress(xrayConfig, inbound, inbounds)`. The slice includes the inbound itself, so the self-collision case falls out of the sibling check rather than needing its own branch.

(2) `internal/web/service/xray.go:712-717` — extend the ONE existing loop, and add one more, both placed exactly where the tag guard already sits (before the routing-rule block at :719, so a skipped bridge also leaves `cfg.RouterConfig` untouched — the current code adds the rule before the append):

  a. In the existing `for i := range cfg.InboundConfigs` loop, after the tag check, add a port check that is BOTH listen-aware and transport-aware, mirroring `checkPortConflict`'s own three conjuncts (port_conflict.go:226-237):
       c := &cfg.InboundConfigs[i]
       if c.Port != parsed.RouteXrayPort { continue }
       var listen string; _ = json.Unmarshal(c.Listen, &listen)   // Listen is a quoted RawMessage; absent → "" → isAnyListen
       if !listenOverlaps(listen, "127.0.0.1") { continue }
       if inboundTransports(model.Protocol(c.Protocol), string(c.StreamSettings), string(c.Settings))&transportTCP == 0 { continue }
       logger.Warning("mtproto egress: port [", parsed.RouteXrayPort, "] is already bound by inbound [", c.Tag, "], skipping bridge")
       return
     This covers a real Xray inbound on the port, the template's `api` inbound (62789, tunnel/TCP/127.0.0.1 — it IS in cfg.InboundConfigs), and a second mtproto bridge already appended by an earlier loop iteration (protocol "socks" is in no core's `kinds`, so `inboundTransports` falls back to `transportTCP`). It does NOT fire on a UDP-only hysteria/xray-wireguard/kcp/quic inbound, nor on an inbound pinned to a specific public IP, nor on a disabled inbound (never rendered).

  b. A second, small loop over `siblings`, covering the mtg sidecar binds no generated config can show:
       for _, other := range siblings {
         if other.Protocol != model.MTProto || !other.Enable || other.NodeID != nil { continue }
         if other.Port != parsed.RouteXrayPort || !listenOverlaps(other.Listen, "127.0.0.1") { continue }
         logger.Warning("mtproto egress: port [", parsed.RouteXrayPort, "] is the listen port of mtproto inbound [", other.Tag, "], skipping bridge")
         return
       }
     No transport check: mtg is TCP-only (cores/internal/mtproto/core.go:306-307). This catches both the inbound's OWN port and any other local enabled mtproto row's port.

Comments: one 2-line block above (a) saying why a UDP-only neighbour is not a conflict, and one 2-line block above (b) saying mtproto rows are never in the generated config. Nothing else.

Effect, and why no reasonable operator prefers the status quo: today a collision kills the whole Xray core after Stop() has already run — every inbound of every protocol dark, on every retry. After this, one mtproto inbound's routed egress goes dark with a named Warning and everything else keeps serving. Same posture as the existing tag-collision skip in this function and as commit 40f5fbfd.
**Deferred:** 1. THE ROOT CAUSE — the save-time check. Blocked on the decision below, in both directions: (b1) `checkPortConflict` learning that a routed mtproto row's `routeXrayPort` occupies loopback TCP (so a later user inbound on it is refused), and (b2) the new/edited mtproto inbound's own `routeXrayPort` being checked against `inbounds.port`, the panel/sub listeners and `reservedAPIPort()`. Two corrections the first plan lacked and that must land with it whichever way the decision goes: b1's query must mirror the injector's own condition (`routeThroughXray && enable && node_id IS NULL`), because `settingsRouteXrayPort` (inbound.go:822-834) reads the number without consulting `routeThroughXray` and would otherwise reserve a port nothing binds; and `s.normalizeMtprotoXrayPort` must move in `UpdateInbound` from :1438 to above `checkPortConflict` at :1416 (it only needs `oldInbound.Settings`, already loaded at :1396), matching AddInbound's order (:934 before :953) — otherwise the update path checks a port the save has not yet decided.

2. THE PANEL/SUB LISTENER CLASS (`routeXrayPort` == the panel's web or subscription port). Cannot be reached from `injectMtprotoEgress`: those listeners have no row and no entry in `cfg.InboundConfigs`, and reading them needs `SettingService` and the DB, which would turn a pure, DB-free function with eight existing tests into a DB-dependent one. It belongs to b2 (which already calls `panelListenerConflict`), so it ships with the decision.

3. THE STRANDED-SIDECAR COUPLING. When the bridge is skipped, `renderConfig` still writes `[network] proxies = ["socks5://127.0.0.1:P"]` (manager.go:631-632) from the stored settings, so mtg dials a port with no SOCKS server and that inbound is dark rather than direct. Naming it, not fixing it: making the two agree means the mtproto manager learning what the Xray generator decided this pass — a cross-process seam, and new architecture the finding does not earn. The Warning is the operator's signal, exactly as it is for the four existing skips in the same function.

4. HEALING DEPLOYMENTS THAT ALREADY COLLIDE. Once b1 lands, an unrelated edit to the innocent inbound sitting on the contested port starts failing. Which side yields is the decision below; containment (shippableNow) is deliberately chosen to need no answer, because it changes no stored value and refuses no save.
**Test:** All DB-free, stdlib `testing`, added to internal/web/service/xray_config_inject_test.go next to `TestInjectMtprotoEgress_TagCollisionSkips` (:507), reusing `egressTestConfig()` (:130) and `mtprotoInbound()` (:424). Three subtests are RED today; one is a green fence that must stay green. Explicitly stated because earlier plans were rejected for tests that pass either way.

RED #1 — `TestInjectMtprotoEgress_PortCollisionSkips/tcp_neighbour`. Seed `cfg.InboundConfigs` with `xray.InboundConfig{Port: 50007, Protocol: "vless", Tag: "in-50007-tcp", Listen: json_util.RawMessage(`"0.0.0.0"`), StreamSettings: json_util.RawMessage(`{"network":"tcp"}`)}` (a DIFFERENT tag, so the existing tag guard cannot fire), record `before := string(cfg.RouterConfig)`, then `injectMtprotoEgress(cfg, mtprotoInbound("inbound-443", `{"routeThroughXray":true,"routeXrayPort":50007,"outboundTag":"warp"}`), nil)`. Assert `len(cfg.InboundConfigs) == 2` and `string(cfg.RouterConfig) == before`. TODAY IT FAILS TWICE, on the first assert: `len` is 3, message "a TCP inbound already binding 50007 must make the bridge a no-op, got 3 inbounds"; and the routing rule for `warp` has also been prepended, so the second assert would fail too. Both are the current unguarded append at xray.go:750-756 and the rule block at :719-748.

RED #2 — `TestInjectMtprotoEgress_PortCollisionSkips/second_bridge`. One `egressTestConfig()`, two calls with DIFFERENT tags ("inbound-443", "inbound-8443") and the SAME `routeXrayPort` 50009. Assert `len(cfg.InboundConfigs) == 2` (api + the first bridge only). TODAY: 3, message "two mtproto bridges must not both claim 50009, got 3 inbounds". Proves the guard sees an entry appended by an earlier iteration and that `Protocol: "socks"` (in no core's `kinds`) falls back to `transportTCP`.

RED #3 — `TestInjectMtprotoEgress_SelfPortCollisionSkips`. Built inline rather than via the helper, because `mtprotoInbound` sets no Port/Listen: `ib := &model.Inbound{Tag: "inbound-8443", Protocol: model.MTProto, Enable: true, Port: 8443, Listen: "", Settings: `{"routeThroughXray":true,"routeXrayPort":8443,"outboundTag":"warp"}`}`, then `injectMtprotoEgress(cfg, ib, []*model.Inbound{ib})`. Assert `len(cfg.InboundConfigs) == 1` (api only) and routing unchanged. TODAY: 2, message "an mtproto inbound must not bridge onto its own listen port 8443, got 2 inbounds". This is the class the first plan missed entirely; it fails today AND would still fail under the first plan's cfg-only guard, which is the point of the subtest.

GREEN FENCE (passes before and after — the regression the adversary caught, not a proof of the fix) — `TestInjectMtprotoEgress_UdpNeighbourStillBridges`. Seed `xray.InboundConfig{Port: 50008, Protocol: "hysteria", Tag: "in-50008-udp", Listen: json_util.RawMessage(`"0.0.0.0"`)}` and inject with `routeXrayPort: 50008`. Assert `len(cfg.InboundConfigs) == 2` and `cfg.InboundConfigs[1].Port == 50008`, message "a UDP-only neighbour is not an OS conflict; the bridge must still be injected". A port-only guard turns this red, which is exactly the over-skip that would strand mtg on a dead SOCKS port (manager.go:631-632). `inboundTransports` is safe to call with no DB — `TestInboundTransports` (internal/web/service/port_conflict_test.go:49-89) already does.

Revert check: with edit (2) reverted, #1/#2/#3 go red with the messages above and the fence stays green; with only the transport conjunct reverted, the fence goes red. Run `make test-go` (or `go test ./internal/web/service/ -run TestInjectMtprotoEgress`); no frontend test and no `make gen` are involved, since nothing touches a route, a struct in `StructAllow`, or a translation file.
**Sites:**
- internal/web/service/xray.go:699 — `func injectMtprotoEgress(cfg *xray.Config, inbound *model.Inbound)`; gains the `siblings []*model.Inbound` parameter
- internal/web/service/xray.go:712-717 — the tag-only guard loop; gains the listen- and transport-aware port check (case a) and is followed by the sibling-bind loop (case b)
- internal/web/service/xray.go:750-756 — the unconditional `cfg.InboundConfigs = append(..., Listen: `"127.0.0.1"`, Port: parsed.RouteXrayPort, Protocol: "socks", Tag: tag)` this guard protects
- internal/web/service/xray.go:167-173 — the only call site; `injectMtprotoEgress(xrayConfig, inbound)` becomes `injectMtprotoEgress(xrayConfig, inbound, inbounds)`
- internal/web/service/xray.go:246 — `RenderInbound` returns nil for a kind Xray does not serve; with internal/cores/internal/xray/core.go:29-32 (`kinds` omits "mtproto") this is WHY no mtproto row is ever in cfg.InboundConfigs, and why case (b) is needed at all
- internal/mtproto/manager.go:76-82 — `bindTo` defaults listen to 0.0.0.0, so an mtproto row's own port overlaps the bridge's 127.0.0.1 (SELF-COLLISION, the class the first plan missed)
- internal/cores/internal/xray/transports.go:25-28 and :39-44 — hysteria/xray-wireguard forced UDP, and kcp/quic network → UDP; the neighbours a port-only guard would wrongly refuse
- internal/cores/internal/mtproto/core.go:304-307 — mtg is TCP-only, so case (b) needs no transport intersection
- internal/mtproto/manager.go:88-102 — `StructuralFingerprint` includes `XrayRoutePort`: a re-roll restarts the sidecar (drives the decision, option A)
- internal/mtproto/manager.go:631-632 — `[network] proxies = ["socks5://127.0.0.1:%d"]` written from the DB regardless of injection; the reason the guard must not over-skip (deferred item 3)
- internal/web/service/port_conflict.go:194-248 — `checkPortConflict`; `routeXrayPort` appears nowhere in this file (DEFERRED, b1/b2)
- internal/web/service/port_conflict.go:32-52 and :54-59 — `inboundTransports` and `listenOverlaps`, reused verbatim by the guard
- internal/web/service/inbound.go:822-834 — `settingsRouteXrayPort` reads the port without consulting `routeThroughXray` (DEFERRED, b1 must mirror the injector's condition)
- internal/web/service/inbound.go:886-894 — the three unchecked branches: stored port, client-supplied port, `mtproto.FreeLocalPort()` (DEFERRED, the decision)
- internal/web/service/inbound.go:1438 vs :1416 — UpdateInbound normalizes AFTER checkPortConflict; AddInbound does it before (:934 before :953) (DEFERRED, must move with b2)
- internal/xray/preflight.go:23-33 — `xray -test` binds no ports, so the collision survives the pre-flight
- internal/web/service/xray.go:1258-1263 — `_ = process.Stop()` runs before `process.Start()`, which is why the bind failure means the whole core is down and stays down
- internal/web/service/xray.go:539-557 — EXAMINED, SAFE: `injectPanelEgress` builds a used-port set and bumps from 62790; runs after the mtproto bridges, so it moves around them
- internal/web/service/xray.go:600-651 — EXAMINED, SAFE: `injectNodeEgresses` checks both usedTags and used ports, bumping from 62800
- internal/egress/drivers/xraytun/xraytun.go:107-113 — EXAMINED, SAFE: the L3 fronts emit `Port: 0`, and `applyRoutingPlan` runs after the bridges anyway
- internal/mtproto/manager.go:374 — SAME SHAPE, NAME-DON'T-FIX: `apiPort, err := FreeLocalPort()` in `ensureLocked` is an identical unscanned allocation, but transient and re-rolled on every restart
- internal/web/service/node.go:1024-1044 — SAME SHAPE, FAILS SOFT: `withOutboundBridge` binds 127.0.0.1:0 and hot-adds a SOCKS inbound on that port; transient and per-request

**Correction from the re-check:** GREEN FENCE arithmetic is off by one and would fail both before and after the fix. `egressTestConfig()` (internal/web/service/xray_config_inject_test.go:130) already holds the `api` entry (Port 62789, Protocol "tunnel", Listen "127.0.0.1"), so in `TestInjectMtprotoEgress_UdpNeighbourStillBridges` seeding the hysteria entry gives 2 inbounds and a successful injection gives 3, with the bridge at index 2 — not `len == 2` with the bridge at `[1]` as specced. Fix the fence to assert `len(cfg.InboundConfigs) == 3` and `cfg.InboundConfigs[2].Port == 50008` (or build its config without the api entry and keep len==2 / index 1). Nothing else in the plan changes: the three RED subtests already count the api entry correctly (RED#2's "api + the first bridge only" proves it), and the repair itself is untouched.
**Re-check notes:**
- GREEN FENCE `TestInjectMtprotoEgress_UdpNeighbourStillBridges` is specced with the wrong count: `egressTestConfig()` already contains the `api` inbound, so after seeding the hysteria neighbour a successful injection yields 3 inbounds with the bridge at index 2, making the asserted `len == 2` red both before and after the fix — a broken test rather than a fence. Corrected in the `correction` field; it does not affect the repair or the three RED subtests.


### F045 — ship-with-the-noted-correction (medium)
**Root:** VERIFIED — the adversary is right on all four counts; I confirmed each in the tree and in xray-core.

The panel enforces sibling uniqueness on email, on subId, and on WireGuard's publicKey, and on nothing else — while every core keys its user table on the client's credential. The one repair that is protocol-agnostic and true for every kind is narrower and more honest than the original plan's framing: **two clients on one inbound that share the field the core declares as this kind's config identity (`ClientIdentity`) are the same user to that core.** For vless/vmess/trojan/hysteria that field IS the authenticating credential; the core's map is keyed on it, so the second `Store` silently replaces the first (verified: vless `validator.go:34-42` returns an error only for a duplicate EMAIL, then does an unconditional `v.users.Store(ProcessUUID(...))`; trojan `validator.go:19-27` the same over `hexString(Key)`). The victim then cannot connect, its traffic is billed to the other client, and `Del(email)` on either one removes the single shared map entry and disconnects both.

The original plan's `field == "email" -> return ""` was presented as a principle and is not one — it is a scope boundary. Renaming the helper `identityCollision` (not `credentialCollision`) makes the invariant stated rather than emergent, and makes the shadowsocks/mtproto gap an explicit exclusion instead of a silent no-op.

Corrections to the original plan, each verified:
1. THREE doors, not two. `validateClientForKind` has exactly two non-test call sites (`grep`): `client_inbound_apply.go:379` and `inbound.go:1024`. The plan did the expensive one and deferred `AddInbound` — which is the cheapest (whole sibling set already in `clients`, `inbound.Id=0`, no stored state) and the one that closes `/inbounds/import` (`controller/inbound.go:441` calls `AddInbound` directly; `inbound_account_auth.go:64-65` already names import as the door that "bypasses every validate: tag").
2. BULK BATCH POISONING is real. `client_bulk.go:1345-1358` batches every client destined for one inbound into ONE `addInboundClient` call and fans the single error onto every row (`for _, idx := range idxByInbound[ibId] { failed[idx]=true }`), against an explicit per-row-skip contract at `client_bulk.go:1212-1219`. `fillProtocolDefaults` mints only when the field is EMPTY (`client_crud.go:191`), so an import file carrying explicit duplicate uuids arrives intact and would fail the whole inbound's restore.
3. NORMALIZATION IS PER-KIND PROTOCOL KNOWLEDGE — this is the decisive finding, and it is why the exact-string compare is the right shippable scope rather than a shortcut. I read four DIFFERENT effective keying rules: vless keys on `ProcessUUID` with bytes 6 and 7 ZEROED (`proxy/vless/validator.go:21-24,42`); vmess keys on `account.ID.CmdKey()` over the FULL uuid, so bytes 6-7 are significant and it does NOT collide where vless does (`proxy/vmess/validator.go:53-54`); trojan keys on sha224 of the raw password, so it is case- and byte-exact; hysteria's effective lookup is a `uuid.Parse` with bytes 6,7 zeroed in a second `ids` map (`proxy/hysteria/account/config.go:52-62`), and `DelByEmail` deletes from both maps. vless and vmess differing from each other is the proof: there is no correct "uuid kinds" shortcut in the service layer. Any normalization belongs behind the `core` seam, which is a contract change and therefore a decision.
4. The `existingClients` set is already fetched at `client_inbound_apply.go:326` and `defaultWireguardClients` (`client_wireguard.go:372-406`) already builds exactly the owners map this needs — so the guard costs no new query and copies a shipped pattern verbatim.
**Ships now:** One guard on the field each core already declares, mirroring the shipped WireGuard pair exactly. No new core method, no column, no migration, no route, no i18n key, no config knob. Four edits + one shared 20-line helper pair, all in one commit.

**0. Helpers — internal/web/service/client_cred_fields.go, beside clientIdentityValue.** Named `identityCollision`, not `credentialCollision`: the invariant is "two clients sharing the core-declared config identity are one user in a rendered config", which is true for every kind and makes the email case a stated boundary.
```go
func identityFieldFor(p model.Protocol) string {
    if a, ok := cores.ClientCredentialAuthority(core.Kind(p)); ok { return a.ClientIdentity(core.Kind(p)) }
    return core.CredUUID
}
// identityCollision names the other client already carrying this identity. The
// core keys its user map on it, so a second claimant silently replaces the first.
func identityCollision(p model.Protocol, value string, others []model.Client, skip int) (string, string) {
    field := identityFieldFor(p)
    // email is already globally unique; the kinds that answer it are out of scope.
    if field == "email" { return "", "" }
    value = strings.TrimSpace(value)
    if value == "" { return "", "" }
    for i := range others {
        if i == skip { continue }
        if strings.TrimSpace(clientIdentityValue(&others[i], field)) == value { return others[i].Email, field }
    }
    return "", ""
}
```
Comparison is byte-exact and case-SENSITIVE. That is deliberate, not laziness: trojan keys on sha224 of the raw password, so case-folding it would over-refuse. Anything beyond exact-string is per-kind protocol knowledge (see decisionQuestion).
Refusal string, mirroring `wireguardKeyCollision`'s caller verbatim in shape:
`common.NewError(string(protocol)+": "+field+" already used by client:", other)` -> e.g. `vless: uuid already used by client: alice@x` (+ trailing "\n" from Sprintln).

**1. AddInbound — internal/web/service/inbound.go:1021-1027.** Convert the existing `for _, client := range clients` to index form and check each client against the earlier members of the same slice:
```go
if other, field := identityCollision(inbound.Protocol, clientIdentityValue(&clients[i], identityFieldFor(inbound.Protocol)), clients[:i], -1); other != "" {
    return inbound, false, "", common.NewError(string(inbound.Protocol)+": "+field+" already used by client:", other)
}
```
Brand-new inbound, zero stored state, no carve-out. This is the door `/inbounds/import` and REST inbound-create come through.

**2. addInboundClient — internal/web/service/client_inbound_apply.go:374-382.** Seed an `owners map[string]string` from `existingClients` (already in scope from :326) and extend it per client inside the existing loop — the exact shape of `defaultWireguardClients`'s owners map at client_wireguard.go:374-406, so it is O(n+m) and catches new-vs-stored AND new-vs-new. The loop is already positioned AFTER the email-dedupe filter at :336-361, which is required: a re-add of a client already on this inbound is dropped there, so the guard never sees the client colliding with itself.

**3. UpdateInboundClient — internal/web/service/client_inbound_apply.go, inserted after the existing check at :545-547.** `identityField` (:530-532), `newClientId` (:532), `oldClients` (:525) and `clientIndex` (:536-543) are all already resolved:
```go
old := clientIdentityValue(&oldClients[clientIndex], identityField)
if strings.TrimSpace(newClientId) != strings.TrimSpace(old) {
    if other, field := identityCollision(oldInbound.Protocol, newClientId, oldClients, clientIndex); other != "" {
        return false, common.NewError(string(oldInbound.Protocol)+": "+field+" already used by client:", other)
    }
}
```
The changed-only carve-out is copied from the WireGuard block at :578-586 for its reason, not its style: it is what stops a metadata-only edit from stranding a legacy duplicate an older build let in, and it is why the tgbot/quota/reset mutators at :1087 and :1217 — which re-pass the stored identity untouched — stay silent.

**4. BulkCreate precheck — internal/web/service/client_bulk.go, in the `for idx := range prep` loop at :1325-1338.** Cache `identityOwners map[int]map[string]string` beside `inboundCache`, seeded lazily from `inboundSvc.GetClients(ib)` and extended per accepted row; on a hit set `failed[idx]=true; reason[idx]=<message>; ok=false; break`, matching the per-row `skip()` contract at :1212-1219. ~14 lines. This is NOT optional polish — without it edit 2 turns one bad row into a whole-inbound restore failure, because :1345-1358 fans a single error onto every row for that inbound.

BulkAttach (client_bulk.go:114) needs no precheck: it reports per-inbound into `result.Errors`, which is already its contract. Create/Attach (client_crud.go:166, :714) send single-client payloads.
**Deferred:** **A. Shadowsocks-2022 uPSK and MTProto FakeTLS secret.** Both `ClientIdentity` answers are "email" (xray credentials.go:84, mtproto/core.go:99), so the guard is a deliberate, stated no-op for them — even though both collide in their core (sing-shadowsocks' `uPSKHash[hash]=user`; mtg attributing one secret to one name). Reaching them requires the core to answer a question it is not asked today. That is decisionQuestion option A/B.

**B. Normalized-key comparison.** The exact-string guard is bypassable on its own four kinds: two vless IDs differing only in bytes 6-7, or only in hex case, or hyphenated vs 32-char raw, all collide inside xray but pass a `strings.TrimSpace` equality test. It is deferred because the normalization is genuinely per-kind — vless zeroes bytes 6-7, vmess does not (CmdKey over the full uuid), trojan is byte-exact sha224, hysteria zeroes 6-7 in its `ids` map. That vless and vmess DIFFER is the proof there is no correct service-layer shortcut; it belongs behind the core seam. Same decision as A.

**C. UpdateInbound (internal/web/service/inbound.go:1384).** A fourth door, found on the record: it writes `inbound.Settings` including the clients array and calls `validateClientForKind` zero times — it does not even check for an empty client ID today. It is deliberately NOT in shippableNow because the whole settings blob round-trips on every inbound edit, so a naive guard would block *every* edit of an inbound already carrying a legacy duplicate — worse stranding than the client-edit path, and the same trap the WireGuard carve-out exists to avoid. Doing it correctly means a "refuse only collisions absent from the old settings" diff: more code, on the door with the lowest exposure of the four (the UI form round-trips stored clients unchanged). Worth its own finding.

**D. A declared invariant.** Nothing stops the fifth door from being added un-guarded, because the rule lives in three call sites rather than one contract. `internal/arch/` is where a ratchet for that would go. Out of scope for a fix this size.
**Test:** Go, stdlib testing, real Postgres (skips without `PUI_DB_DSN`), package `internal/web/service`, using the package's own `setupBulkDB(t)` (bulk_clients_test.go:13) and `nodeInbound(t, nodeID, port, clients)` (node_bulk_dispatch_test.go:117), following del_shared_email_runtime_test.go. Every assertion is on the exact emitted string; `common.NewError` uses `fmt.Sprintln`, so all comparisons are against `strings.TrimSpace(err.Error())`.

Two setup rules that the plan-before-last's tests would have failed on, and that make these red:
- client B must carry a DIFFERENT email, or the :336-361 email-dedupe filter drops it and the call returns `(false, nil)` regardless of the guard.
- B's credential must be set EXPLICITLY, or `fillProtocolDefaults`/`MintClientCredentials` (client_crud.go:191) mints a fresh unique one and no collision ever reaches the guard.

**T1 — add path (guards edit 2).** `nodeInbound` a vless inbound holding A = `{ID: "11111111-1111-4111-8111-111111111111", Email: "a@x"}`. Then `ClientService.CreateOne(inboundSvc, ib.Id, model.Client{ID: "11111111-1111-4111-8111-111111111111", Email: "b@x", SubID: "..."})`.
- BASELINE (red): returns `nil`, and re-reading `inboundSvc.GetClients(ib)` shows len 2 with both entries carrying the same ID. Assert `err != nil` AND `strings.TrimSpace(err.Error()) == "vless: uuid already used by client: a@x"`, so the test fails today with `got <nil>, want "vless: uuid already used by client: a@x"`.
- Table-driven over the four in-scope kinds and their fields: vless/vmess -> `uuid`, trojan -> `password`, hysteria -> `auth`. The shadowsocks row asserts the guard stays SILENT (`err == nil`), pinning the stated email boundary so a later change to `ClientIdentity` cannot widen it unnoticed.

**T2 — import door (guards edit 1).** Build a fresh `*model.Inbound` (Id 0, vless) whose Settings JSON carries BOTH A and B with the same `id`, and call `inboundService.AddInbound(inbound)` — the exact call `controller/inbound.go:441` makes for `/inbounds/import`. Baseline returns a saved inbound and `err == nil`; assert the same exact refusal string. Red today with `AddInbound succeeded, want refusal naming a@x`.

**T3 — bulk (the discriminating one; pins edit 4 and only edit 4).** `BulkCreate` with two payload rows targeting the SAME inbound: row1 `{Email:"c1@x", ID:U}`, row2 `{Email:"c2@x", ID:U}`. Assert `result.Created == 1` AND `len(result.Skipped) == 1` AND `result.Skipped[0].Email == "c2@x"` AND its Reason == `"vless: uuid already used by client: c1@x"`.
This is the one test that is red BOTH ways, which is why it is the load-bearing one: against baseline it fails with `Created=2, Skipped=0` (both rows land, silently broken); against a guard-only implementation that omits the BulkCreate precheck it fails with `Created=0, Skipped=2` because client_bulk.go:1345-1358 fans the single `addInboundClient` error onto every row. No other assertion in this suite separates those two.

**T4 — over-refusal guard (NOT the red proof; it passes either way by design).** Seed an inbound whose stored settings already carry A and B with the SAME id (a legacy duplicate written directly, bypassing the guard), then run a metadata-only edit on B — `ClientService.Update` with the identity unchanged, and separately `SetClientTelegramUserID` (client_inbound_apply.go:1087). Assert `err == nil`. Stated explicitly as a regression fence for the changed-only carve-out at :545-547, not as evidence the fix works.
**Sites:**
- internal/web/service/client_cred_fields.go:56-62 — clientIdentityValue; the two new helpers go here beside it
- internal/web/service/client_cred_fields.go:70-80 — validateClientForKind; the per-client presence test the guard sits next to
- internal/cores/internal/xray/credentials.go:45-77 — ValidateClient: every arm is a presence test, no sibling context in the signature
- internal/cores/internal/xray/credentials.go:79-90 — ClientIdentity: trojan->password, hysteria->auth, shadowsocks/wireguard->email, default->uuid
- internal/web/service/inbound.go:1021-1027 — AddInbound per-client loop; DOOR 1, closes /inbounds/import and REST inbound-create
- internal/web/controller/inbound.go:409-441 — importInbound unmarshals a form field and calls AddInbound; the restore path
- internal/web/service/inbound_account_auth.go:64-65 — the repo's own note that /inbounds/import bypasses every validate: tag
- internal/web/service/client_inbound_apply.go:326 — existingClients already fetched; the sibling set the add guard needs, no new query
- internal/web/service/client_inbound_apply.go:336-361 — the email-dedupe filter; the guard MUST run after it or a re-add of an existing client self-collides
- internal/web/service/client_inbound_apply.go:374-382 — addInboundClient validate loop; DOOR 2
- internal/web/service/client_inbound_apply.go:528-547 — identityField/newClientId already resolved, then used only for an emptiness test; DOOR 3
- internal/web/service/client_inbound_apply.go:578-586 — the WireGuard 'only an edit that CHANGES the key is checked' carve-out to copy verbatim
- internal/web/service/client_inbound_apply.go:1073-1088 and :1192-1218 — tgbot/quota/reset mutators; they re-pass the STORED identity, so the changed-only carve-out keeps them silent
- internal/web/service/client_bulk.go:1325-1338 — the per-row loop where the BulkCreate precheck goes
- internal/web/service/client_bulk.go:1212-1219 — the existing per-row skip() contract the precheck must match
- internal/web/service/client_bulk.go:1345-1358 — the one-error-fans-onto-every-row fanout that makes the precheck mandatory
- internal/web/service/client_crud.go:185-199 — fillProtocolDefaults; mints only when EMPTY, so an import's explicit duplicate survives
- internal/web/service/client_bulk.go:114 — BulkAttach's multi-client addInboundClient call (per-inbound Errors, not per-row; no precheck needed)
- internal/web/service/client_crud.go:166 and :714 — Create and Attach; single-client payloads, no poison risk
- internal/web/service/client_wireguard.go:222-233 — wireguardKeyCollision, the edit-path helper shape to mirror
- internal/web/service/client_wireguard.go:372-406 — defaultWireguardClients owners map, the add-path shape to mirror
- internal/util/common/err.go:19-22 — NewError uses fmt.Sprintln: the message carries a TRAILING NEWLINE, which every exact-string assertion must account for
- internal/web/service/inbound.go:1384-1420 — UpdateInbound; DEFERRED door, writes clients with zero validateClientForKind
- C:/Users/Arman/go/pkg/mod/github.com/xtls/xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc/proxy/vless/validator.go:21-24,34-42,55-57 — ProcessUUID zeroes bytes 6,7; Add errors on duplicate email only then Stores unconditionally; Del removes the shared entry
- C:/Users/Arman/go/pkg/mod/github.com/xtls/xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc/proxy/vmess/validator.go:53-54 — keys on CmdKey() over the FULL uuid; differs from vless
- C:/Users/Arman/go/pkg/mod/github.com/xtls/xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc/proxy/trojan/validator.go:19-27,40 — keys on hexString(sha224 key); byte-exact, no folding
- C:/Users/Arman/go/pkg/mod/github.com/xtls/xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc/proxy/hysteria/account/config.go:52-62,68-76 — the ids map keyed on uuid with bytes 6,7 zeroed; DelByEmail deletes from both maps
- internal/cores/internal/mtproto/core.go:84-99 — ValidateClient checks presence/adTag hex; ClientIdentity returns "email"; the secret is unguarded (deferred)

**Correction from the re-check:** T1's protocol table cannot use nodeInbound. internal/web/service/node_bulk_dispatch_test.go:117-133 hardcodes Protocol: model.VLESS and takes no protocol argument, so the vmess/trojan/hysteria/shadowsocks rows would all silently run as VLESS and the trojan/hysteria/shadowsocks rows would assert the wrong field name (or, for shadowsocks, the wrong nil/non-nil expectation). Use mkInbound(t, port, proto, clientsSettings(t, clients)) from internal/web/service/bulk_clients_test.go:49 for the table, and keep nodeInbound only if a row genuinely needs the node-runtime seeding. Everything else in the plan ships as written.
**Re-check notes:**
- Test-helper mismatch (the correction above): nodeInbound is VLESS-hardcoded at node_bulk_dispatch_test.go:124, so the four-kind table in T1 needs mkInbound (bulk_clients_test.go:49) instead. Mechanical, but the plan names a helper that cannot do what T1 asks of it.
- Cosmetic redundancy at door 1: the snippet calls identityFieldFor(inbound.Protocol) in the caller to compute the value and identityCollision calls it again internally. Resolve the field once before the loop.
- Worth one line in the commit body, because it is the first thing a reviewer will ask: the guard is per-inbound and add/edit-time only, so the panel's deliberate cross-inbound identity sharing (the subId exemption at client_inbound_apply.go:266-270) is untouched and legacy stored duplicates stay editable. That scope is correct, but it is currently only implicit in the plan.
- Disclosed and correctly deferred, but still live after this commit: UpdateInbound (internal/web/service/inbound.go:1370-1420) writes inbound.Settings including the clients array and calls validateClientForKind zero times -- verified, it does not even reject an empty client ID. The plan's reason for deferring (a naive guard would block every edit of an inbound already carrying a legacy duplicate) is sound, but this leaves a fourth door open and it should be filed before the commit lands, not after.
- Deferred item B understates nothing but is worth restating as a known under-refusal: the byte-exact comparison misses vless/hysteria uuids differing only in bytes 6-7 (both zeroed by ProcessUUID), hex case (xray parses case-insensitively), and hyphenated vs 32-char raw. This is the safe direction (misses, never over-refuses) and it does catch the realistic case of a copy-pasted or re-imported identical string, so it does not block shipping.


### F050 — ship-with-the-noted-correction (small)
**Root:** The adversary is RIGHT; I verified every claim. The original plan's premise is wrong. F050 is a DIAGNOSTIC lie, not an ordering bug.

VERIFIED: `internal/web/translation/en-US.json:462` = "Optional. Force Telegram traffic out through this outbound (or balancer). Leave empty to let your routing rules decide."; `:463` placeholder = "Use routing rules"; `fa-IR.json:462-463` mirrors both ("وادار کنید" = force). The prepend at `xray.go:741` implements that shipped contract exactly. Reordering it is a behaviour redefinition, not a repair.

The one writer that is actually wrong is the CORE. `internal/cores/internal/mtproto/core.go:286-297` returns `core.IngressHandle{Tag: inst.Tag}` — a fully routable subject — whenever `routeThroughXray && routeXrayPort > 0`, and it never reads `settings.outboundTag`. But when `outboundTag` is non-empty, `injectMtprotoEgress` (xray.go:718-748) prepends a criteria-free rule for that same tag at index 0, above the pinned api rule and therefore above everything `applyRoutingPlan` splices in after `splitPinnedAPIRule` (routing.go:352-361, 373-383). Result: both subject pickers offer the tag as routable (`RoutingService.Subjects` routing.go:53-75 → `SubjectViews` :486-506; `routingSubjectReason` inbound.go:1943-1975), `routing.Plan` stamps `SeverityOK` (plan.go:233), and the operator's rule provably cannot win first match. The panel's answer contradicts the panel's own config.

The core already owns exactly this channel: `IngressHandle.BlockedKey` (mtproto/core.go:292,295 for the bridge-off case) flows core-blind into both pickers AND into `plan.go:118-127`, which turns it into a `SeverityBlocked` diag and emits no Xray rule. `routing.Plan` never learns which core it is serving (routing.go:26-32) — the adversary's stated architecture cost of a core-aware compile is avoided entirely by answering in the core.

NEW BLOCKER I found while verifying, not in either prior plan: `groupRules` (plan.go:194-217) expands `ScopeAll` to EVERY subject including blocked ones, contradicting its own comment ("expanded to every ROUTABLE subject") and `audit-appendix/verified-correct.md:570`, which recorded the comment rather than the code. Because `refuseInertRule` (routing.go:922-943) returns on the first refusal with a non-empty MessageKey, this is a LIVE pre-existing bug today: any bridge-off mtproto inbound plus any scope=all rule refuses every routing-rule save with `reasonBridgeOff`. Without fixing it, adding a second BlockedKey trigger widens that wedge onto forced-outbound panels — so it must land in the same commit.
**Ships now:** Make the core tell the truth. No packet on any live panel changes route; `xray.go` is not touched at all, and `TestInjectMtprotoEgress_WithOutbound` (xray_config_inject_test.go:428-455), which asserts the force rule sits at index 0, stays green untouched — that is the proof of zero packet-path change.

1. `internal/cores/internal/mtproto/core.go` — add `OutboundTag string \`json:"outboundTag"\`` to the anonymous struct in `IngressHandle`, and after the two existing bridge-off returns:
```go
// The panel pins this tag to that outbound above every compiled rule, so a
// rule naming it could never win first match. F050.
if parsed.OutboundTag != "" {
    return core.IngressHandle{BlockedKey: blockedForcedOutbound}, nil
}
```
plus `const blockedForcedOutbound = "pages.xray.subjects.reasonForcedOutbound"` beside `blockedBridgeOff` at :302. The dead-keys scan (`i18n-dead-keys.test.ts`, GO_KEY_DECLARATION) counts a Go const as a use only when the identifier is used elsewhere — it is, at the return site — so NO frontend source change is required to keep the key alive.

2. `internal/routing/plan.go` `groupRules` ScopeAll arm — skip blocked subjects (2 lines):
```go
for _, subject := range in.Subjects {
    // "Every routable subject", as above: expanding onto a blocked one
    // refuses a save for a subject the rule never named.
    if subject.Handle.BlockedKey != "" { continue }
    ids = append(ids, subject.InboundID)
}
```
Emitted rules are identical either way — `plan.go:118-127` already emits nothing for a blocked subject — so this only removes bogus refusals. Required companion, not scope creep: without it my change 1 makes every scope=all rule save refuse on a forced-outbound panel.

3. i18n, both files, same commit. `internal/web/translation/en-US.json` `pages.xray.subjects` block (:1995 area): `"reasonForcedOutbound": "This inbound's Telegram traffic is forced out through the Outbound set on its own form, so routing rules can never match it. Clear that field to let your routing rules decide."` — deliberately echoing the shipped hint at :462. `fa-IR.json` (:1875 area): `"reasonForcedOutbound": "ترافیک تلگرام این ورودی از خروجیِ تعیین‌شده در فرم خودش خارج می‌شود، پس قوانین مسیریابی هرگز نمی‌توانند با آن مطابقت کنند. برای اینکه قوانین مسیریابی تصمیم بگیرند، آن فیلد را خالی کنید."` (reuses the existing fa phrasing from :462).

4. `frontend/src/test/routing-subject-picker.test.ts:59-61` — add `'reasonForcedOutbound'` to the REASONS list. Its own comment calls that list "the frontend's half of that contract"; leaving it stale is the rejection the adversary raised about stale invariants.

5. `internal/web/service/routing.go:272-278` — the doc comment on `applyRoutingPlan` ends "Both are safe because every injected and intent rule is inboundTag-scoped", which is the exact false reasoning that produced F050. Replace that one sentence (2 lines, within the comment budget): "An injected tag is not nameable by intent: the panel and node bridges have no inbound row, and the mtproto core reports a forced bridge tag blocked. F050." `splitPinnedAPIRule`'s comment at :370-372 needs NO edit — nothing moves, so it stays true.

Total: ~10 lines of Go, 2 locale strings, 1 test-list line, 1 comment sentence. `make gen` is not needed — no new endpoint, no new struct, no changed `example:` tag.
**Deferred:** 1. THE ORDERING FLIP ITSELF (the entire original plan). Nothing moves at `xray.go:741`; the force rule stays at index 0. Deferred because it is a behaviour redefinition the shipped UI contradicts in two locales, not a repair — see the decision question.

2. Everything the fallback semantic would drag in: rewording `mtgRouteOutboundHint` and `mtgRouteOutboundPlaceholder` in both locale files, and the upgrade-time exposure of Telegram bridge traffic to existing scope=all rules.

3. `refuseInertRule`'s whole-set refusal (routing.go:936-942). It returns on the first refusal across the entire candidate set, not the rule being saved, so ONE stored rule naming a blocked subject wedges every subsequent routing save. That is live today (turn off `routeThroughXray` on an inbound a rule names and the Routing page can no longer save anything) and my change adds a second way to reach it. It is a deliberate F049 design — "refuse a save the planner would drop" — and narrowing it to `diag.RuleID == rule.Id` is a separate finding with its own argument. NOT bundled: bundling it would make this three behaviour changes in one commit. FLAGGED so it is not discovered as a surprise.

4. `ruleIngressIDs` (routing.go:214-227) has the same unfiltered ScopeAll expansion as groupRules, but `ensureFronts` gates on `Handle.Device != ""` and no core emits BlockedKey with a Device set (verified by grep over internal/cores/internal/), so it is provably inert today. Left alone rather than "fixed" pre-emptively; it becomes real only when the first device core learns to block.

5. Surfacing non-refusing diags. `recordRoutingRefusals` (routing_health.go:35) is fed only `Compiled.Refusals()` (inert+blocked), so a `SeverityWarn` would be invisible with no plumbing. That is why warn was rejected as the mechanism here, and why any future advisory diag needs its own design.
**Test:** Three tests, each RED without its half. Run BOTH `make test-go` AND `cd frontend && npm test` — the locale-parity check (`i18n-dead-keys.test.ts`, every locale must carry the exact en-US key set) and the REASONS list live only in the frontend suite, and "make test-go only" was an explicit rejection last round.

TEST 1 (primary, pure, no DB, no env gate — new, `internal/web/service/xray_config_inject_test.go`, reusing the existing `egressTestConfig()` and `mtprotoInbound()` helpers). This is the one that drives the actual two-writer collision:
```go
const tag, settings = "inbound-443", `{"routeThroughXray":true,"routeXrayPort":50000,"outboundTag":"warp"}`
cfg := egressTestConfig()
injectMtprotoEgress(cfg, mtprotoInbound(tag, settings))
// half 1: the force rule really is criteria-free and at index 0 (guards the premise)
// half 2: the same settings must therefore report the tag NOT routable
handle, _ := cores.IngressHandleFor(context.Background(),
    core.Instance{ID: 1, Kind: core.Kind(model.MTProto), Tag: tag, Settings: settings})
if handle.BlockedKey == "" { t.Fatalf(...) }
```
TODAY IT FAILS WITH EXACTLY: `injectMtprotoEgress prepends a criteria-free rule for inbound-443 at index 0, but IngressHandleFor offers it routable (Tag="inbound-443" BlockedKey=""): the Routing page reports SeverityOK for a rule that can never win first match`. After the fix, `handle.Tag == ""` and `handle.BlockedKey == "pages.xray.subjects.reasonForcedOutbound"`. No Postgres, no `XRAY_E2E_BINARY`, runs on every box (`cores.IngressHandleFor` falls back to `fallbackRegistry()`, cores.go:145-151, so no wiring is needed).

TEST 2 (the red test for the groupRules companion — `internal/routing/plan_test.go`, pure, uses the existing `baseInput` helper):
```go
got := Plan(baseInput(
  []Subject{
    {InboundID: 3, Tag: "mt-1", Handle: core.IngressHandle{BlockedKey: "pages.xray.subjects.reasonBridgeOff"}},
    {InboundID: 4, Tag: "vless-in", Handle: core.IngressHandle{Tag: "vless-in"}},
  },
  []Rule{{ID: 1, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestBlock}}},
  map[int]int{}))
```
Assert `len(got.Refusals()) == 0` and `len(got.XrayRules) == 1`. TODAY IT FAILS WITH: `a scope=all rule must not be refused by a subject it never named: [{RuleID:1 SubjectTag:mt-1 Pattern:inert Severity:blocked MessageKey:pages.xray.subjects.reasonBridgeOff Args:map[]}]`. The existing `TestBlockedIngressIsReportedNotFronted` (plan_test.go:399-418) uses `ScopeSelected` and must stay GREEN — that is the assertion that explicitly naming a blocked subject still refuses.

TEST 3 (core unit, table row in `internal/cores/internal/mtproto/ingress_test.go:23-25`): `{"a forced outbound is not routable", ` + "`" + `{"routeThroughXray":true,"routeXrayPort":50000,"outboundTag":"warp"}` + "`" + `, "", blockedForcedOutbound}`. Today the existing table assertion fails with `IngressHandle = {Tag:"mt-tag" BlockedKey:""}, want tag "" blocked "pages.xray.subjects.reasonForcedOutbound"`.

TEST 4 (DB-gated, `internal/web/service/inbound_routing_subjects_test.go`): seed `mt-forced` with `{"routeThroughXray":true,"routeXrayPort":2399,"outboundTag":"warp"}` and add the row `{"mt-forced", false, "pages.xray.subjects.reasonForcedOutbound"}`. Red today with `Routable = true, want false`. Skips without `PUI_DB_DSN`, so CI's live-Postgres job is the one that proves it — do not rely on it as the primary red test.

REVERT-AND-WATCH: revert edit 1 → tests 1, 3, 4 go red; revert edit 2 → test 2 goes red. Do both before committing.
**Sites:**
- internal/cores/internal/mtproto/core.go:286-297 — `IngressHandle` returns `core.IngressHandle{Tag: inst.Tag}` under exactly the `routeThroughXray && routeXrayPort > 0` condition that also triggers the force injection; it never parses `outboundTag`. THE FIX SITE.
- internal/cores/internal/mtproto/core.go:302 — `const blockedBridgeOff = "pages.xray.subjects.reasonBridgeOff"`; the new const goes beside it. Only BlockedKey emitter in the whole tree (verified: grep over internal/cores/internal/).
- internal/routing/plan.go:194-217 — `groupRules`; the ScopeAll arm appends every `in.Subjects` id with no routability filter, contradicting its own comment 'expanded to every routable subject'. COMPANION FIX SITE.
- internal/routing/plan.go:118-127 — the blocked-subject arm of `Plan`: emits one `SeverityBlocked` diag per rule and `continue`s, emitting no XrayRule and no Kernel/Front entry. This is the machinery the fix reuses.
- internal/routing/plan.go:232-233 — `compileRule` seeds `diag := Diag{... Severity: SeverityOK}`; the OK that F050 calls a lie. Unchanged by the fix — the pair never reaches compileRule once the subject is blocked.
- internal/routing/diag.go:29-31,48-58 — `SeverityBlocked` is in `Refusals()`, so a blocked pair both refuses a save and reaches the Routing page snapshot. No new key needed in this file; the reason travels with the core.
- internal/web/service/xray.go:732-741 — `rule := map[string]any{"type":"field","inboundTag":[]any{tag}}` then `routing["rules"] = append([]any{rule}, rules...)`; criteria-free, index 0. NOT EDITED — this is the shipped force contract.
- internal/web/service/xray.go:167-174 and :205 — injection loop runs before `applyRoutingPlan`, so intent can only be spliced below what was already prepended. NOT EDITED.
- internal/web/service/routing.go:272-278 — `applyRoutingPlan`'s doc comment; last sentence ('Both are safe because every injected and intent rule is inboundTag-scoped') is the false invariant and must be corrected in this commit.
- internal/web/service/routing.go:352-361 and :373-383 — `splitPinnedAPIRule` split; comment at :370-372 stays TRUE under this plan because nothing moves. No edit.
- internal/web/service/routing.go:53-75 — `RoutingService.Subjects`; includes a handle whose only non-empty field is BlockedKey, so the new block reaches Plan and SubjectViews. No edit needed.
- internal/web/service/routing.go:486-506 — `SubjectViews` sets `Routable: subject.Handle.BlockedKey == ""` and passes BlockedKey through. Picks up the fix for free.
- internal/web/service/routing.go:922-943 — `refuseInertRule` returns on the FIRST refusal over the whole candidate set, with no rule-id filter. This is why the groupRules companion is mandatory, and it is the design decision I deliberately do NOT touch.
- internal/web/service/routing.go:134-169 — `ensureFronts` gates on `subject.Handle.Device != ""`; mtproto never sets Device, so a newly-blocked subject provably cannot upsert an egresses row. Verified, no edit.
- internal/web/service/inbound.go:1889-1893 and :1943-1975 — `routingSubjectReason` returns `handle.BlockedKey` verbatim, so the inbound-side picker picks up the new reason with no edit.
- internal/web/translation/en-US.json:462-463 / fa-IR.json:462-463 — `mtgRouteOutboundHint` ('Force ... Leave empty to let your routing rules decide') and `mtgRouteOutboundPlaceholder` ('Use routing rules'). These are the primary source evidence that force is the shipped contract; UNCHANGED by this plan, and the new reason string is worded to echo them.
- internal/web/translation/en-US.json:1995 block / fa-IR.json:1875 block — `pages.xray.subjects.*`; where `reasonForcedOutbound` goes in BOTH files.
- frontend/src/test/routing-subject-picker.test.ts:59-61 — hand-maintained REASONS list; add the new key.
- internal/web/service/inbound_routing_subjects_test.go:59-68 — the routable/reason table; `mt-bridged` carries no outboundTag, so NO existing expectation flips; a new `mt-forced` row is added.
- SIBLINGS RULED OUT WITH EVIDENCE — internal/web/service/xray.go:531 (injectPanelEgress, `panel-egress`), xray.go:656 (injectNodeEgresses, `node-egress-N`), node.go:1062 (withOutboundBridge, `node-test-%d-%d`): none can collide, because `Subjects()` (routing.go:54) reads the INBOUNDS TABLE — a synthetic tag with no row can never be a subject, so no intent rule can ever name it.
- OTHER IngressHandleFor CALLERS RULED OUT — internal/web/service/egress.go:84 (`egressIngressDevice`) and internal/web/service/routing.go:757 (`EnsureHostForwarding`) both key solely on `handle.Device`, which mtproto never sets. The new BlockedKey is provably inert for both.

**Correction from the re-check:** In TEST 3 (internal/cores/internal/mtproto/ingress_test.go), write the expected key as the LITERAL "pages.xray.subjects.reasonForcedOutbound" rather than the identifier blockedForcedOutbound. As written the row references a constant that does not exist before edit 1, so without the fix the package fails to COMPILE instead of failing the assertion — the plan's quoted red output (`IngressHandle = {Tag:"mt-tag" BlockedKey:""}`) can never be printed, and the fixture tag is "mt-1", not "mt-tag". With the literal, reverting edit 1 turns that row genuinely red on `Tag = "mt-1", want ""` / `BlockedKey = "", want "pages.xray.subjects.reasonForcedOutbound"`, which is what the revert-and-watch step in the plan claims to prove.
**Re-check notes:**
- TEST 3's red-test claim is wrong as written: `blockedForcedOutbound` is undefined before edit 1, so the mtproto package fails to compile rather than producing the quoted assertion failure, and the table's fixture tag is "mt-1" not "mt-tag". Fix per the correction. TEST 1 and TEST 2 are unaffected — both compile today and are genuinely red.
- Operator-visible consequence the plan flags but the commit message must carry: on a panel that already has a stored routing rule naming an mtproto inbound with `outboundTag` set, `refuseInertRule` (routing.go:922-943) returns on the FIRST refusal across the whole candidate set — confirmed, `candidateRules(rule)` passes the merged full set — so every subsequent Add/Update on the Routing page is refused until that rule is repointed. The rule was already dead, and the picker now shows the reason, but this goes from silent to blocking. Deferring the narrowing to `diag.RuleID == rule.Id` is the right call; shipping it unannounced would not be.
- The 'force rule always wins first match' premise depends on the api rule being present in routing.rules — if it were absent, `splitPinnedAPIRule` returns head=nil and intent rules would land ABOVE the force rule and actually win. Verified unreachable in practice: config.json ships the api rule at index 0, EnsureStatsRouting re-pins or inserts it on every SaveXraySetting, and it deletes the `enabled` key so stripDisabledRules cannot remove it. Non-blocking, but it is the load-bearing assumption and deserves a line in the commit body rather than being left implicit.
- Cited line numbers have drifted: the blocked-subject arm of Plan is plan.go:125-133 (not 118-127), groupRules is plan.go:199-219 (not 194-217), and injectMtprotoEgress's force block is xray.go:718-741. Immaterial to the change; worth correcting so the commit body does not point at the wrong lines.
- Minor wording edge, not a defect: when `outboundTag` names a tag that does not exist, injectMtprotoEgress returns early and appends NO bridge inbound at all, so the inbound is unroutable for a different reason than the new string states. The advice the string gives ('clear that field') is still exactly the correct remedy in that case, so no change is needed.


### F054 — ship-it (small)
**Root:** Two lifecycles are fused inside one builder, and only one of them is safe to run on a read.

`ensureFronts` (routing.go:134) does two unrelated things: it UPSERTS a panel-owned `egresses` row per device ingress an enabled rule names (routing.go:154, benign — a row the reconcile job converges), and it then unconditionally RETIRES every panel-owned front no *currently enabled* rule names (routing.go:175 -> `reapFronts` routing.go:192 -> `EgressService.Del` -> `egressManager.Remove`, egress.go:224, synchronous kernel teardown). Because `ensureFronts` sits under `compile` -> `applyRoutingPlan` -> `GetXrayConfig`, the retire fires for the two callers that only READ the config: `ServerService.GetConfigJson` (server.go:1352, the index page's "view config" button) and the xray-update pre-flight (server.go:1130).

The window is the NORMAL state, not an exotic one — this is the part the original plan understated. `RoutingService.Del` (routing.go:606) and controller/routing.go:105,129,143,161 call `SetToNeedRestart()`; they never restart synchronously. So after every rule add/edit/delete/toggle, and after any inbound disable (`Subjects` filters on `enable = true`, routing.go:54), stored intent leads the running config by design. During that window a plain GET deletes the front row and tears down the `ip rule`/table the RUNNING core is still forwarding through, with no restart following to make the config agree. Ids are `autoIncrement` and never reused (model.go:122) while `egress.ValidID` caps at 999 (alloc.go:20-21), so the next restart also re-provisions under a NEW id and a new `peg<n>` device name.

The original plan's error: it tried to fix this by suppressing the upsert too. The adversary is RIGHT, verified at plan.go:139-149 — with no row, `FrontIDFor` answers false, the planner emits `KeyNoFront` and `continue`s, dropping the subject's rules AND its `out.Kernel` front entirely. A preview built that way is a config with no tun inbound and no intent rules, which is exactly what the pre-flight at server.go:1130 would then hand the new binary. The plan's cited precedent is also mis-read: `refuseInertRule` (routing.go:926-936) answers `FrontIDFor` unconditionally TRUE, the opposite of what the plan proposed, and its own comment says why.
**Ships now:** Gate ONLY the retire on the applying caller. Keep the upsert exactly as it is.

1. `internal/web/service/xray.go:130` — `func (s *XrayService) GetXrayConfig(retireFronts bool) (*xray.Config, error)`. Parameterize the EXPORTED function; do not add a wrapper (config_integrity_test.go:117 anchors on that literal receiver+name and would then read the wrapper's body and t.Fatal).
2. `xray.go:205` — `applyRoutingPlan(context.Background(), xrayConfig, retireFronts)`.
3. `routing.go:279/290` — `applyRoutingPlan(ctx, cfg, retireFronts)` -> `(&RoutingService{}).compile(ctx, outbounds, retireFronts)`.
4. `routing.go:95/107` — `compile(ctx, cfgOutbounds, retireFronts)` -> `s.ensureFronts(subjects, rules, retireFronts)`.
5. `routing.go:175` — wrap the existing block, preserving the literal call text and the warning:
   `if retireFronts {` / `  if err := s.reapFronts(out); err != nil {` / `    logger.Warning("routing: a front that no rule names could not be retired:", err)` / `  }` / `}`
   Extend the existing comment by 2 lines max: a read must not retire a front the running core still forwards through (F054). The existing 5-line block comment above it is pre-existing; leave it, do not grow it.
6. Call sites: `xray.go:1235` (RestartXray) passes `true` — the only one. `server.go:1352` (GetConfigJson) and `server.go:1130` (update pre-flight) pass `false`. The 10 test callers and the 2 `applyRoutingPlan` test calls take the mechanical literal.

Why this is undisputed and needs no decision: the upsert still runs on the read path, so `out` is unchanged, so `FrontIDFor` still answers true, so `KeyNoFront` never fires, so the generated config — for BOTH readers, including the pre-flight — is byte-identical to today's. `recordRoutingRefusals` sees the same diag set, so no phantom badge. Nothing an operator can observe changes except that a GET no longer deletes rows or tears down kernel state. No new column, job, abstraction or knob; no migration; no i18n key; no route, so no endpoints.ts / openapigen / docs step.
**Deferred:** 1. The upsert on the read path (routing.go:154) stays. A read still CREATES a panel front row for an enabled rule whose subject has none, which the egress reconcile job then converges — installing an `ip rule` into a table holding only its blackhole before the core has the matching tun inbound. That is the pre-existing hazard xray.go already names in its own comment ("a refused config can leave a front the reconcile job then converges"). Removing it is NOT a free repair: it opens the `KeyNoFront` hole the adversary correctly identified, which is a behaviour choice about what the two readers should show. Hence the decision below.

2. The pre-flight's artifact (server.go:1130). The adversary's suggested "test the RUNNING process's config via currentXrayProcess().GetConfig()" is a genuinely better answer to that comment's own intent, but it is a different scope with its own fallback question (what to test when nothing is running) and it is not needed to remove today's harm. Left out on purpose.

3. Whether `getConfigJson` should mark staged-but-unprovisioned rules. Today it shows the would-be-generated config with no marker distinguishing "not provisioned yet" from "the compiler dropped your rule". Untouched.

4. Separate finding, not F054: `db.Clauses(clause.OnConflict{...}).Create(row)` on Postgres advances the `egresses` sequence even when the insert conflicts, so EVERY config generation burns an id against `egress.MaxID = 999` (alloc.go:21). Past 999, `ValidID` fails and every L3 rule goes permanently inert. Worth its own finding; do not bundle it here.

5. `RestartXray`'s early `return nil` path (xray.go:1231) still reaps before deciding not to restart. Self-consistent today (a reap changes the generated config, so `configUnchanged` is false and the restart proceeds); noted, not fixed.
**Test:** One new Postgres-gated test in `internal/web/service` (new file `routing_front_retire_test.go`), stdlib testing, `initTestDB(t)` + `t.Cleanup` per the package norm.

Drive the READ through `(&ServerService{}).GetConfigJson()`, NOT through `GetXrayConfig`/`applyRoutingPlan`. That is deliberate: `GetConfigJson()` has a stable signature, so reverting the fix leaves the test COMPILING and red. A test written against `GetXrayConfig(false)` would fail to compile on revert, which certifies nothing. (`ServerService.xrayService` is a value field, so `&ServerService{}` works — same as core_control_test.go:25.)

Body:
- `initTestDB(t)`; `withFakeEgressKernel(t)` (egress_db_test.go:21) so no `ip` shells out.
- `installCores(t, newPeerCore(&declaredCore{id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg8"}))` then `inbound := seedPolicyInbound(t, "wgkernel", "wg-l3", 45711, "peer")` — the exact fixture pair `TestARefusedSaveProvisionsNothing` (routing_refuse_at_save_test.go:69) already relies on to produce a Device subject, so no kernel and no real wgkernel core is needed.
- `front := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel, Remark: "wg-l3", IngressInboundId: &inbound.Id})` — the front the last restart provisioned and the running core still forwards through.
- Seed the rule that named it, already disabled: `&model.RoutingRule{Enable: false, IngressScope: model.RoutingScopeSelected, IngressIds: ingressIdsJSON(t, inbound.Id), DestKind: model.RoutingDestDirect}` — exactly what `SetToNeedRestart` leaves behind after the operator toggles a rule off.
- `if _, err := (&ServerService{}).GetConfigJson(); err != nil { t.Fatalf("GetConfigJson: %v", err) }`.
- Assert: `database.GetDB().Model(&model.Egress{}).Where("id = ?", front.Id).Count(&alive)`; then
  `if alive != 1 { t.Fatalf("viewing the config retired panel front %d for inbound %d — the running core still forwards through %s, and rule edits only SetToNeedRestart, so this is the normal window after every toggle", front.Id, inbound.Id, egress.Device(front.Id)) }`

Red today, precisely: with the rule disabled, `needed` is empty, `out` is empty, `reapFronts` deletes the seeded row and calls `egressManager.Remove`, so `alive == 0` and the test fails with the message above. Green with the gate, because `retireFronts` is false on the `GetConfigJson` path.

Do NOT stake red/green on `kernel.Ops()`/`Rules()` — the fake kernel was never converged for that id, so the del ops may be no-ops. Add the row-survives assertion only; optionally assert `kernel.Ops()` contains no `DelRule` as a non-fatal extra.

Ritual: write it, revert step 5 (`if retireFronts {`) alone, confirm the Fatalf fires, restore. Run as `PUI_DB_DSN=... go test ./internal/web/service/ -run 'Front|ConfigBuild|Retiring'` so both source guards in config_integrity_test.go run alongside it.
**Sites:**
- internal/web/service/routing.go:175 — `if err := s.reapFronts(out); err != nil {` — the unconditional retire inside ensureFronts; THE line to gate
- internal/web/service/routing.go:192 — `func (s *RoutingService) reapFronts(keep map[int]int) error` -> `(&EgressService{}).Del(row.Id)`
- internal/web/service/egress.go:224 — `egressManager.Remove(context.Background(), id)` — synchronous kernel teardown, failure only logged
- internal/web/service/routing.go:134 — `func (s *RoutingService) ensureFronts(subjects []routing.Subject, rules []*model.RoutingRule)` — gains the flag
- internal/web/service/routing.go:107 — `fronts, err := s.ensureFronts(subjects, rules)` inside compile
- internal/web/service/routing.go:95 — `func (s *RoutingService) compile(ctx context.Context, cfgOutbounds []map[string]any)` — gains the flag
- internal/web/service/routing.go:279 + :290 — `func applyRoutingPlan(ctx context.Context, cfg *xray.Config) error` / `(&RoutingService{}).compile(ctx, outbounds)` — gains the flag
- internal/web/service/xray.go:130 — `func (s *XrayService) GetXrayConfig() (*xray.Config, error)` — becomes `GetXrayConfig(retireFronts bool)`
- internal/web/service/xray.go:205 — `applyRoutingPlan(context.Background(), xrayConfig)` — passes the flag through
- internal/web/service/xray.go:1235 — `xrayConfig, err := s.GetXrayConfig()` in RestartXray — the ONLY site that passes true
- internal/web/service/server.go:1352 — `config, err := s.xrayService.GetXrayConfig()` in GetConfigJson — passes false (sibling #1)
- internal/web/service/server.go:1130 — `if cfg, cfgErr := s.xrayService.GetXrayConfig(); cfgErr == nil {` — the xray-update pre-flight; passes false (sibling #2). Safe ONLY because the upsert is kept: the config stays byte-identical to today's, so the new binary is still asked to load the fronts
- internal/web/controller/server.go:55 — `g.GET("/getConfigJson", a.getConfigJson)` — the reachable trigger; no change
- internal/web/service/config_integrity_test.go:151-166 — TRAP: source guard requires the literal `s.reapFronts(out)` inside routing.go with a `logger.Warning` within ±160 chars, else t.Fatal("reapFronts is no longer called from ensureFronts; this guard is now vacuous")
- internal/web/service/config_integrity_test.go:117-141 — TRAP: source guard indexes the literal `func (s *XrayService) GetXrayConfig(` and requires `applyRoutingPlan(` + `return nil,` inside THAT function's body. A thin exported wrapper delegating to an unexported `getXrayConfig` makes the guard match the wrapper, stop at its `\n}\n`, and t.Fatal("GetXrayConfig no longer applies the routing plan at all"). Parameterize the exported func; do NOT wrap
- internal/web/service/routing_outbound_preserve_test.go:55 and :88 — `applyRoutingPlan(context.Background(), cfg)` — mechanical `, false`
- internal/web/service/{inbound_finalmask_reality_test.go:192, reset_lifecycle_e2e_test.go:283,304,319, xray_config_clients_test.go:43, xray_config_golden_test.go:174, xray_config_order_test.go:66, xray_config_scale_test.go:32, xray_render_unified_test.go:51, xray_wireguard_config_test.go:19} — 10 `GetXrayConfig()` test callers, mechanical `(false)`
- internal/routing/plan.go:139-149 — `if !ok || !egress.ValidID(frontID) { ... MessageKey: KeyNoFront ... continue }` — the drop that kills the ORIGINAL plan; untouched by this one
- internal/web/service/routing.go:154 — the OnConflict upsert — deliberately LEFT ALONE (see deferred)
- internal/web/service/routing.go:606 and internal/web/controller/routing.go:105,129,143,161 — `SetToNeedRestart()` — evidence the divergence window is the normal post-edit state
- internal/web/service/routing.go:54 — `Where("node_id IS NULL AND enable = ?", true)` — disabling an inbound drops its subject, so a read reaps its live front

**Re-check notes:**
- Non-blocking, style only: step 5 says to extend the existing 5-line block comment above the reap by 2 lines. CLAUDE.md caps a comment block at 2 lines; growing a grandfathered 5-line block to 7 moves the wrong way. Put the new 2-line note as its own block directly above `if retireFronts {` instead. Neither placement affects the config_integrity_test.go:151 guard, whose +-160 char window is centred on `s.reapFronts(out)` and only needs the `logger.Warning` that follows it.
- Non-blocking, cosmetic: `compile` is at routing.go:98, not :95 (the doc comment starts at :96). Every other line number in the plan is exact -- 107, 134, 154, 175, 192, 279, 290, xray.go 130/205/1235, server.go 1130/1352, egress.go 224.
- Non-blocking, reasoning only: deferred item 5's justification is stated backwards. A reap does not change the generated config -- the reaped fronts were already absent from it. What is true is that the state making a reap necessary (a disabled rule or a disabled inbound) is the same state that already removed the front inbound from the generated config, so `process.GetConfig().Equals(xrayConfig)` is false and the restart proceeds. The deferral is still correct, and it is a `true` path unaffected by this change. Also, the early `return nil` referenced as xray.go:1231 is the `isManuallyStopped` return, which sits BEFORE the `GetXrayConfig` call and reaps nothing; the path meant is the `configUnchanged` return further down.


### F067 — ship-with-the-noted-correction (medium)
**Root:** The panel treats `SELECT * FROM inbounds` as the authority on which tunnel subnets are claimed, but the resource being allocated is the host's ONE routing table, which no Postgres query can enumerate. Verified: `grep -rn "AddrList|RouteList" --include=*.go internal/` returns only internal/wireguard/plane_linux.go:112 (one named link) and internal/egress/plane_linux.go:55-61 (a full host dump, consumed only by egress preflight). Nothing on the WireGuard inbound/client save path reads the host, so a wgkernel device prefix or a per-client /32 that collides with eth0/docker0/a private-network NIC saves cleanly, and `desiredRoutes` (internal/wireguard/device.go:519-539) then installs a connected route or a per-peer /32 that steals those addresses from the real interface.

THE ADVERSARY IS RIGHT ON ALL FOUR REJECTIONS — verified, not conceded:
(1) Self-exclusion: client_wireguard.go:74-76 `q.Where("id != ?", ignoreId)`, called with `inbound.Id` from inbound.go:1423 and with `inbound.Id` in wireguardPoolFor (:189-191). Defining "foreign" as "not declared by a candidate row" makes the inbound's OWN live device address foreign on every edit.
(2) Neither patch to that definition works: including `want` refuses a widening while the old /24 is still on pwg7 (contradicts client_wireguard.go:106 "Widening always passes"); "overlaps any declared prefix" is vacuous because the reported host 10.0.0.5/20 overlaps the declared 10.0.0.1/24.
(3) The stated reason for rejecting a device check is false: internal/web/service/egress.go:80 `egressIngressDevice` -> internal/cores/cores.go:207 `IngressHandleFor` -> internal/cores/internal/wireguard/core.go:352 `core.IngressHandle{Device: c.mgr.Name(inst.ID)}`, and manager.go:209 `Name(id) = prefix + id` is pure string arithmetic. No prefix table is needed and no host is touched.
(4) The fix shipped to 2 of 3 branches: `pool.blocked` is appended at :201 and consumed ONLY at :408 inside `allocateWireguardAddress`. The two hand-entered-allowedIPs branches — client_wireguard.go:421 `wireguardAllowedIPsCollision(normalized, used)` and client_inbound_apply.go:599-609 (`peers := pool.taken + sibling clients`) — compare against client ADDRESSES only, by exact string equality, and never consult `pool.blocked` at all. So even today, before any host read, a hand-entered `10.90.0.5/32` on inbound A is accepted while inbound B's device is `10.90.0.1/24`.

The corrected ownership rule is DEVICE IDENTITY, the `ownGateway` precedent (internal/egress/preflight.go:135-146): a host address is panel-owned iff its `Device` equals the name a WG-owning inbound row derives. Deliberately device-ONLY, not `ownGateway`'s device-AND-value: value matching would re-introduce exactly the converge-lag false refusal, because a widening edit 10.90.0.1/24 -> /16 leaves the old /24 on pwg7 and the stored value no longer equals it. `ownGateway` can afford the value half because a front's address is derived from its id and cannot lag; a WG device address is operator-typed and lags by design.
**Ships now:** Three undisputed repairs, one seam, ~50 lines across three files. No new column, job, package, config knob or i18n key.

1. SEAM — internal/web/service/client_wireguard.go, after :18. Imports `context` and `github.com/Arman2122/p-ui/internal/egress` (already imported by this package at service/egress.go:19).
   `var wireguardHostAddrs = func(ctx context.Context) ([]egress.AddrSpec, error) { snap, err := egress.HostPlane().Snapshot(ctx); return snap.Addrs, err }`
   A var for the reason service/egress.go:58-59 already states for `egressManager` ("a var so a test can converge against a fake plane without root or a Linux kernel"). FAIL-OPEN, stated as a decision, not left implicit: on error, `logger.Warning` and contribute nothing — that is byte-for-byte today's behaviour, whereas failing closed would refuse every WireGuard save on a netlink hiccup and unconditionally on every non-Linux dev box (internal/egress/plane_other.go:15-17 returns ErrPlatformUnsupported).

2. ONE COMPUTATION, IN THE ONE PLACE THAT ALREADY LOADS THE ROWS — `wireguardPoolFor` (:180-206). Add one field `foreign []egress.AddrSpec` to `wireguardPool` (:172-178). After the existing candidate loop, when `inbound.NodeID == nil` (the api-port gating precedent, port_conflict.go:199-201 "Nodes run their own Xray, so this only applies to the local panel"): build `mine := set of egressIngressDevice(ctx, r) for every r in candidates, PLUS egressIngressDevice(ctx, inbound) for the candidate itself` — that last term is what repairs the self-exclusion the adversary killed, and it costs no extra query because `candidates` is already in hand. Then `pool.foreign` = every AddrSpec whose `Device` is not in `mine`, and `pool.blocked = append(pool.blocked, masked foreign prefixes...)`. Everything downstream of `blocked` is now correct for free.

3. CREATE-PATH REFUSAL — new `func (s *InboundService) checkWireguardHostAddressConflict(inbound *model.Inbound) error` in client_wireguard.go: gates on `ownsHostWireguardDevice` + `NodeID == nil`, calls `wireguardPoolFor(inbound)`, and for each `pool.foreign` whose `Prefix.Masked().Overlaps(p)` for a declared device prefix `p` returns `common.NewError("wireguard: address", p, "overlaps", addr.Prefix, "already carried by this host's", addr.Device)`. Called from ONE site: internal/web/service/inbound.go:960, immediately after the existing `checkWireguardAddressConflict(inbound, 0)`. A separate function rather than overloading `ignoreId == 0` to mean "create", so that adding the edit path later (see `deferred`) is a one-line call-site addition at inbound.go:1423 whatever the owner chooses.

4. ALLOCATOR — no code change. `allocateWireguardAddress(used, base, pool.blocked)` at :408 already steps over `blocked` (:307), so step 2 fixes it. A client is handed a different address, never refused; the widened /16 scope (:296-301) absorbs a blocked /24.

5. HAND-ENTERED allowedIPs, BOTH branches — the third branch the last plan missed. New 5-line helper `wireguardBlockedEntry(entries []string, blocked []netip.Prefix) string` returning the first entry whose `wireguardHostAddr(e)` satisfies the existing `insideAny` (:316-323). Wired in twice:
   (a) internal/web/service/client_wireguard.go:421, after the existing `wireguardAllowedIPsCollision` check — a brand-new client, nothing installed, no lockout possible.
   (b) internal/web/service/client_inbound_apply.go:~608, after the existing `wireguardAllowedIPsCollision(normalized, peers)` — but ONLY when the edit CHANGES the address, mirroring the rule this exact block already states 30 lines above at :577-579 ("Only an edit that CHANGES the key is checked... the edit introduces no collision, it just keeps one"). Compare `normalized` against `normalizeWireguardAllowedIPs(old.AllowedIPs)` with `slices.Equal` — both sides normalized, or a legacy-formatted stored entry like `10.0.0.9` reads as changed and locks a metadata edit out.

Note (b) also closes the pre-existing, host-independent hole: today a hand-entered address inside ANOTHER inbound's device subnet is accepted, because `blocked` never reached these two branches.
**Deferred:** 1. THE EDIT PATH of the device-prefix refusal — internal/web/service/inbound.go:1423. Not shipped: refusing an inbound that has run for months on a colliding subnet locks the operator out of every unrelated edit, and the .conf files are already on users' devices — the exact hazard `checkWireguardAddressCoverage` (client_wireguard.go:96-131) was written to respect. This is the one real behaviour choice; see `decisionQuestion`. Step 2 above already makes the pool/allocator half correct on the edit path, so the deferred piece is one call, not a redesign.

2. `tun` — internal/cores/internal/xray/core.go:31 lists "tun" as an Xray-served kind, so `ownsHostWireguardDevice` (:28-30) returns false via `!cores.ServedByXray` and a tun inbound enters NO address check at all, host or DB, while it puts a real L3 interface on the same routing table. Out of scope because the right repair is a capability-table answer in internal/core/capability.go ("does this kind put a device in the host routing table"), not widening a WireGuard-shaped predicate — a separate finding, not a clause in this one.

3. internal/egress/drivers/wgclient/wgclient.go:104 — an uplink's provider-assigned `Address` is checked for address FAMILY only (`familiesOf`) and never for a host collision, so a `pux<id>` uplink can carry a prefix that collides with eth0 or with a WG inbound. Different subsystem (egress driver validate), different save path (`EgressService.validate`, service/egress.go:420). It benefits from step 1's seam existing but must not be bolted into this commit.

4. internal/web/service/port_conflict.go:194-243 `checkPortConflict` — the same root (one host resource enumerated from Postgres) and the same half-finished shape: the repo threaded in the two non-DB owners it knew about (the api port at :199-208, `panelListenerConflict` at :211) and never read the host's actual listeners. Separate finding; the same fix-size argument applies to it alone.

5. `frontend/src/lib/xray/wireguard-subnets.ts:125-133 suggestFreeSubnet` still pre-fills from panel rows only, so the form can still SUGGEST a subnet the server now refuses. Deferred deliberately: making it host-aware needs a new API surface (endpoints.ts + openapigen + `make gen` + the docs copy step), which is architecture the bug has not earned. The server refusal names the device, which is the actionable half.

6. `wireguardAllowedIPsCollision` (:353-364) compares by exact string equality, so a hand-entered `10.0.0.0/24` does not collide with a stored `10.0.0.9/32`. Real, but an independent defect of a different shape (equality vs containment) — folding it in would make this commit two fixes.
**Test:** New file internal/web/service/client_wireguard_host_test.go, stdlib testing, table-free (each case drives a different service call). Helper `stubHostAddrs(t, addrs ...egress.AddrSpec)` swaps `wireguardHostAddrs` and restores it in `t.Cleanup`. Host layout in every case: `{Prefix: netip.MustParsePrefix("10.0.0.5/20"), Device: "eth0"}` — host bits on purpose, so the fix is forced to mask.

THREE RED TESTS (DB-gated, `setupBulkDB(t)`; template internal/web/service/client_wgkernel_crud_test.go:25-40):

R1 `TestWireguardInboundCannotOverlapAHostAddress` — stub on, then `inboundSvc.AddInbound` a wgkernel inbound with `"address":["10.0.0.1/24"]`. TODAY: `AddInbound` returns a nil error (nothing on that path reads the host), so the test fails with `t.Fatal("a wgkernel inbound on 10.0.0.1/24 was accepted while the host already carries 10.0.0.5/20 on eth0; its connected route steals that /24 from the real interface")`. With the fix: error, and `strings.Contains(err.Error(), "eth0")` must hold, so a refusal that does not name the device also fails.

R2 `TestWireguardClientIsNotAllocatedInsideAHostSubnet` — create the same inbound with the stub OFF, then turn it on and `clientSvc.AddInboundClient` with an empty `allowedIPs`. TODAY: `allocateWireguardAddress` hands out `10.0.0.2/32`, and the assertion `!netip.MustParsePrefix("10.0.0.0/20").Contains(got.Addr())` fails with `t.Fatalf("allocated %s, inside the host's own 10.0.0.0/20 on eth0; that /32 becomes a host route stealing a LAN address", alice.AllowedIPs[0])`. With the fix the /24 base is fully blocked, the widened /16 scope steps past the /20, and it allocates `10.0.16.2/32`.

R3 `TestHandEnteredAllowedIPsCannotLandInAHostSubnet` — same inbound, `AddInboundClient` with `allowedIPs:["10.0.0.9/32"]`. TODAY: accepted (client_wireguard.go:421 checks only exact-string equality against other clients), failing with `t.Fatal("a hand-entered 10.0.0.9/32 was accepted while the host holds 10.0.0.5/20 on eth0; it installs a /32 host route that steals that address")`.

TWO GREEN TESTS THAT GO RED IF THE CORRECTIONS ARE DROPPED (these are the adversary's blocking objections, pinned):

G1 `TestAPanelOwnedWireguardDeviceIsNotForeign` — create a wgkernel inbound at `10.90.0.1/24`, then stub the host as `{10.90.0.1/24 on "pwg"+strconv.Itoa(created.Id)}` (the device-name form pinned by internal/web/service/egress_db_test.go:330) and `AddInboundClient`. Must still allocate `10.90.0.2/32`. Goes RED with "the inbound's own device address was treated as foreign and its whole subnet blocked; got %s" under the original plan's row-membership definition, which excludes self at client_wireguard.go:189-191.

G2 `TestAMetadataEditOfACollidedClientStillSaves` — stub off: create the inbound and a client whose stored `allowedIPs` is `10.0.0.9/32`. Stub on. `clientSvc.UpdateInboundClient` resending the SAME `allowedIPs` with only `comment` changed. Must succeed. Goes RED with "a metadata-only edit of a client that already sits in a host subnet was refused; the operator is locked out of an address they cannot change from here" if the changed-only guard (or the normalize-both-sides comparison) is dropped.

NOT THE RED TEST, labelled as such: one added row in the existing pure table at client_wireguard_test.go:13-30 — `{base:"10.9.0.0/30", blocked:[10.9.0.0/24], want:"10.9.1.2/32"}` — pins that the widened /16 scope steps over a blocked net. It passes today; it exists so the primitive cannot silently regress.

Procedure per CLAUDE.md: write, revert each fix hunk, watch R1-R3 and G1-G2 go red, restore. Run with `PUI_DB_DSN` set (a SKIP is a failure in the live-Postgres CI job).
**Sites:**
- internal/web/service/client_wireguard.go:18 — insertion point for the `wireguardHostAddrs` seam (next to `const defaultWireguardBase = "10.0.0.0/24"`)
- internal/web/service/client_wireguard.go:172-178 — `wireguardPool`; add the `foreign []egress.AddrSpec` field beside `taken` and `blocked`
- internal/web/service/client_wireguard.go:188-204 — `wireguardPoolFor`: the DB-only candidate query (`q.Where("id != ?", inbound.Id)` at :189-191) and the loop that fills `blocked` (:201) / `taken` (:203); the host read and the device-identity `mine` set go here
- internal/web/service/client_wireguard.go:65-94 — `checkWireguardAddressConflict`, the DB-only sibling the new host check sits next to; its `q.Where("id != ?", ignoreId)` at :74-76 is the self-exclusion the adversary named
- internal/web/service/client_wireguard.go:307-308 + :316-323 — `allocateWireguardAddress`'s `!insideAny(addr, blocked)` and `insideAny`; no change, they become correct once `blocked` carries the host prefixes
- internal/web/service/client_wireguard.go:421 — SIBLING BRANCH 2: `wireguardAllowedIPsCollision(normalized, used)` on the add-client path, which never consults `pool.blocked`
- internal/web/service/client_inbound_apply.go:599-609 — SIBLING BRANCH 3: `peers := append(slices.Clone(pool.taken), ...)` on the edit-client path, same omission; the changed-only guard goes here
- internal/web/service/client_inbound_apply.go:577-579 — the precedent comment the changed-only guard must mirror ("Only an edit that CHANGES the key is checked")
- internal/web/service/client_inbound_apply.go:365 and :595 — the two `inboundSvc.wireguardPoolFor(oldInbound)` call sites that inherit the fix with no edit
- internal/web/service/inbound.go:960 — ADD path, `checkWireguardAddressConflict(inbound, 0)`; the ONE new call site in shippableNow
- internal/web/service/inbound.go:1423 — UPDATE path, `checkWireguardAddressConflict(inbound, inbound.Id)`; DEFERRED, pending the decision
- internal/web/service/egress.go:80-91 — `egressIngressDevice`, the existing in-package seam that names a row's device with no host touch
- internal/cores/cores.go:207-213 — `IngressHandleFor`; internal/cores/internal/wireguard/core.go:352 `core.IngressHandle{Device: c.mgr.Name(inst.ID)}`; internal/wireguard/manager.go:209 `Name(id) = prefix + id` — proof the device name is pure, refuting the original plan's "prefix table" excuse
- internal/egress/plane.go:109-113 (`AddrSpec{Prefix, Device}`), :131-133 (`Snapshot.Addrs` = "every address the host holds"), :172 (`HostPlane()`) — the seam being consumed
- internal/egress/plane_linux.go:55-61 — the netlink `AddrList(nil, FAMILY_ALL)` that fills `Addrs`; internal/egress/plane_other.go:15-17 — the ErrPlatformUnsupported stub that makes fail-open mandatory
- internal/egress/preflight.go:135-146 `ownGateway` — the ownership precedent, adopted device-only and for a stated reason
- internal/web/service/port_conflict.go:199-208 — the `inbound.NodeID == nil` gating precedent for a non-DB host owner (#5304)
- internal/wireguard/device.go:519-539 `desiredRoutes` — the mechanism: every peer prefix `!coveredByAddrs(prefix, addrs)` becomes a host route
- SIBLING (deferred) internal/cores/internal/xray/core.go:31 — `"tun"` is Xray-served, so `ownsHostWireguardDevice` is false and tun enters no address check at all
- SIBLING (deferred) internal/egress/drivers/wgclient/wgclient.go:104 — `familiesOf(settings.Address)`: an uplink address is family-checked, never host-collision-checked
- SIBLING (deferred) internal/web/service/port_conflict.go:194-243 — same root, same half-finished shape, different resource
- SIBLING (deferred) frontend/src/lib/xray/wireguard-subnets.ts:125-133 `suggestFreeSubnet` — pre-fills from panel rows only

**Correction from the re-check:** Two test-story corrections; the repair itself needs no change.

(1) MANDATORY — default-neutral stub. The new `wireguardHostAddrs` var makes `wireguardPoolFor` and the create-path check read the REAL host in every existing DB test on Linux. Add one line to the package's existing TestMain (internal/web/service/xray_config_inject_test.go:16), beside the puilogger.InitLogger call:
    wireguardHostAddrs = func(context.Context) ([]egress.AddrSpec, error) { return nil, nil }
with `stubHostAddrs` overriding it per-test in the new file. Without it these existing tests become machine-dependent and go red on any host with an overlapping 10.x address (Hetzner cloud private nets default to 10.0.0.0/16; the live-Postgres CI job treats a SKIP as a failure, so it cannot be skipped past):
    - internal/web/service/client_wgkernel_crud_test.go:32 and :106 (AddInbound at 10.0.0.1/24, which the new refusal would now reject), :118 (10.9.0.1/24), :240-266 (asserts the exact allocation "10.20.0.2/32" and "10.21.0.2/32")
    - internal/web/service/client_wgkernel_multi_test.go:36 (10.0.0.1/24)
    - internal/web/service/awg_address_conflict_test.go:31 (10.77.0.1/24)

(2) Two expected values are off by two, because allocateWireguardAddress walks linearly from base+2 and merely STEPS OVER blocked addresses (pinned by the existing row client_wireguard_test.go:26, base 10.9.0.0/30 with .2/.3 used -> 10.9.0.4/32):
    - The added pure-table row must be want:"10.9.1.0/32", not "10.9.1.2/32". This one IS the assertion, so as written the row fails WITH the fix in place. (It also needs a new `blocked` field on the table struct and a non-nil third argument at the call site, which today passes nil.)
    - R2 allocates 10.0.16.0/32, not 10.0.16.2/32 (10.0.0.0/20 ends at 10.0.15.255). Prose only — the stated assertion is containment-based and is correct — but do not turn it into an equality check on 10.0.16.2/32.
**Re-check notes:**
- TEST INFRASTRUCTURE (blocking): the seam has no default-neutral stub, so on Linux every existing WireGuard DB test in internal/web/service reads the real host through egress.HostPlane().Snapshot(). client_wgkernel_crud_test.go:106 creates 10.0.0.1/24 and t.Fatalf's on any error (the new refusal), :266 asserts the exact allocation "10.20.0.2/32", client_wgkernel_multi_test.go:36 and awg_address_conflict_test.go:31 do the same. A CI runner or Hetzner box with a 10.x private address flips them red for reasons unrelated to any change, and CLAUDE.md's live-Postgres job counts a SKIP as a failure. Fix is one line in the package's existing TestMain (xray_config_inject_test.go:16).
- EXPECTED VALUES off by two: allocateWireguardAddress walks linearly from base+2 and steps over blocked entries, pinned by client_wireguard_test.go:26 (base 10.9.0.0/30, used .2/.3 -> 10.9.0.4/32). So the added table row's want must be 10.9.1.0/32 not 10.9.1.2/32 -- as written that row FAILS with the fix in place, since want is the assertion -- and R2 allocates 10.0.16.0/32 not 10.0.16.2/32 (10.0.0.0/20 ends at 10.0.15.255). R2's stated assertion is containment, so R2 itself survives; only the prose is wrong.
- SIBLING LEFT LIVE (Q4), honestly labeled but worth naming: the device-address refusal ships on the create path only (inbound.go:960), so inbound.go:1423 remains a real bypass -- create at a free subnet, then rebase into a host subnet. checkWireguardAddressCoverage (client_wireguard.go:112-131) only refuses when existing clients are stranded, so an inbound with no clients rebases freely. The plan's OWN changed-only pattern -- the one it builds for client_inbound_apply.go:608, mirroring the key precedent at :577-579 -- would close this with zero lockout risk by refusing only an edit that MOVES the address into a host subnet, so the deferral is a scoping choice rather than forced by the lockout hazard cited. Shipping without it is still monotone-safe: step 2 already fixes the pool/allocator half on the edit path, and nothing that works today starts failing.


### F069 — ship-it (small)
**Root:** importTemplateRoutingRules documents itself as MAXIMAL CONTIGUOUS PREFIX ONLY (:157-164: "importing a rule that sat BELOW a rule left behind would silently move it above that rule"), but two of its three keep-paths leave a rule behind WITHOUT latching `stopped`. Only the inexpressible path (:197-198) latches. The keep-without-latch paths are (a) `rule == nil` at :188 — any non-object element in routing.rules — and (b) `isTransparentRule(rule)` at :192. Every rule below such a rule is still imported, and applyRoutingPlan (routing.go:352-361) splices compiled intent immediately after `head`, so those imports are hoisted above the rule that was kept. The api rule is the ONLY safe walk-past, because splitPinnedAPIRule's head is `rules[:i+1]` — it always ends at the api rule, so imported intent is always strictly below it. Correcting the original plan's reachability claim: the transparent branch has TWO production-reachable no-latch rules, not one. The geoip:private block is index 1 of the shipped default template (internal/web/service/config.json routing.rules), and the balancer loopback rule `{inboundTag:["_bl_X"],balancerTag:"X"}` is spliced at index 1 by firstGeneralRoutingRuleIndex (frontend/src/pages/xray/balancers/balancer-loopback.ts:60-65, 99-103) on any panel with a balancer. The adversary's claim that `_bl_` already latches via expressibleRule is FALSE: isTransparentRule at :192 runs before expressibleRule at :196, so a loopback rule never reaches it. Conversely the adversary is right that the `-dns-allow` arm (:235) is dead — a real pui-dns-allow rule is `{type,ruleTag,ip,port,outboundTag:"direct"}` with no inboundTag (xray_setting_dns_routing.go:389-402).
**Ships now:** Latch `stopped` on EVERY rule the walk leaves behind except the api rule, covering both no-latch sites in one edit. In internal/database/routing_migration.go, replace lines 188-195 with:

		if stopped {
			kept = append(kept, raw)
			continue
		}
		if rule == nil {
			stopped = true
			kept = append(kept, raw)
			continue
		}
		if isTransparentRule(rule) {
			// Walking past a kept rule can lift what follows above it; only the api
			// rule is safe, since splitPinnedAPIRule pins intent strictly below it.
			if out, _ := rule["outboundTag"].(string); out != "api" {
				stopped = true
			}
			kept = append(kept, raw)
			continue
		}

Net +8 lines and a 2-line comment inside one existing function. The comment is deliberately hedged ("can lift") and states only what is true in every layout — head is `rules[:i+1]` so the api rule is above intent unconditionally; it does NOT repeat the first plan's false claim that api is the only rule above intent (a transparent rule sitting above api is in head too, and there the guard merely over-stops). The nil arm is split out rather than folded into the transparent arm so the fix does not lean on reading `outboundTag` from a nil map. No new column, migration, helper, job, config knob, i18n key or route; no schema or model change; no frontend change. The guard can only ever stop the walk EARLIER — it cannot import more — so the worst case is the migration's own documented fallback: the rule stays in the template exactly where the operator put it and Xray enforces it unchanged. All five existing tests in internal/database/routing_migration_test.go still pass: TestMigrationKeepsThePrivateBlockAnchor now latches at the dns-allow rule instead of at bittorrent but keeps all four rules either way (len(kept)==len(rawRules), early return, template untouched), and the other four seed no transparent and no nil rules.
**Deferred:** 1. THE BEHAVIOUR CHOICE, LEFT OUT DELIBERATELY (this is the decisionQuestion). The adversary is right that the harm narrative outlives the fix, and the commit message must not claim otherwise. plan.go:135 emits guardRule only when `subject.Handle.Tag == ""` (an L3 ingress needing a front), and routing.go:352-361 splices every compiled rule between the api rule and the tail. So on the SHIPPED DEFAULT template, with no migration involved, every rule an operator creates on the Routing page for a plain Xray inbound ALREADY sits above `{ip:[geoip:private], outboundTag:"blocked"}`. That is the product's steady state, not a consequence of F069, and closing it is a behaviour change that would start blocking private/LAN destinations for rules that reach them today. Mitigating context for whoever answers: the `direct` outbound is freedom with finalRules `[{action:block, ip:[geoip:private]},{action:allow}]` (config.json:34-38, enforced by hardenFreedomFinalRules in db.go:1800-1826), so dest=direct is already blocked at the outbound unless the operator customised finalRules; the exposure is a rule pointing at a proxy outbound, where private ranges leave via the proxy rather than reaching the panel host's LAN.

2. NOT DEFERRED, REJECTED. Making splitPinnedAPIRule split at the private block instead of the api rule: that is option B of the decision, not a smaller version of this fix, and it changes what every existing panel routes on its next save.

3. NOT DEFERRED, REJECTED. Repairing the dead `-dns-allow` arm of isTransparentRule (:235 tests inboundTag; the real rule carries ruleTag). It is dead in the safe direction — such a rule falls through to expressibleRule, which returns ok=false for a missing inboundTag and latches today. Zero behaviour delta, so touching it is scope the bug did not earn. Do not let it into this commit.

4. NOT DEFERRED, NOTED. The first plan's own residual — adoptEgressAttachments (:85-138) does not re-check egressIngressSelectable and converts any inbound carrying egress_id. Different function, different defect, no shared root with the walk; it needs its own finding.
**Test:** One table-driven test in internal/database/routing_migration_test.go, Go stdlib testing, real Postgres via the package's own initTestDB(t) gate (skips without PUI_DB_DSN; CI's live-Postgres job counts a SKIP as a failure, so it really runs). Reuses the existing seedTemplate / seedRoutedInbound / rulesInOrder / templateRules helpers — no new helper.

TestMigrationStopsAtEveryRuleItLeavesBehind, two cases, each calling initTestDB(t) then seedRoutedInbound(t, "vless-in", 10443, model.VLESS, nil) then seedTemplate with three rules:
  case "kept private block": [ {type:field, inboundTag:["api"], outboundTag:"api"}, {type:field, ip:["geoip:private"], outboundTag:"blocked"}, {type:field, inboundTag:["vless-in"], outboundTag:"warp"} ]
  case "non-object rule":    [ {type:field, inboundTag:["api"], outboundTag:"api"}, nil, {type:field, inboundTag:["vless-in"], outboundTag:"warp"} ]
Then migrateRoutingIntent(), then:
  if got := len(rulesInOrder(t)); got != 0 { t.Fatalf("a rule below a kept rule was imported and now compiles above it: %d intent rows, want 0", got) }
  if got := len(templateRules(t)); got != len(tc.rules) { t.Fatalf("the template lost rules the walk should have stopped at: %d, want %d", got, len(tc.rules)) }

EXACTLY WHAT FAILS TODAY, AND WITH WHAT MESSAGE. Case 1: api is transparent-kept, the private block is transparent-kept with no latch, so the vless-in rule is judged expressible (single known non-node tag + outboundTag) and createImportedRule writes one model.RoutingRule row; len(kept)==2 != 3 so the template is rewritten to [api, private]. The first assertion fires: "a rule below a kept rule was imported and now compiles above it: 1 intent rows, want 0". Case 2: `raw.(map[string]any)` yields a nil map for the JSON null, line 188 keeps it without latching, the vless-in rule is imported the same way, and the same Fatalf fires with the same 1-vs-0 message.

WHY IT IS GREEN AFTER. Case 1: the private block is transparent with outboundTag "blocked" != "api", so `stopped` latches; the vless-in rule is kept; len(kept)==3==len(rawRules) hits the `return nil` at :206-208 and the template is never rewritten. Case 2: the nil arm latches identically. Both cases: zero RoutingRule rows, three template rules.

The two cases pin the two sites INDEPENDENTLY — applying only the transparent-branch half of the fix leaves case 2 red — which is exactly the coverage the first plan lacked. Neither case can pass without the fix: the import happens inside the same transaction that writes the seeder marker, so there is no path where migrateRoutingIntent returns nil having imported nothing.

Also worth running once by hand for the sibling the fix now also covers: seedTemplate [api, {inboundTag:["_bl_bal1"], balancerTag:"bal1"}, {inboundTag:["vless-in"], outboundTag:"warp"}] — red today for the same reason, green after. I left it out of the table only because it exercises the identical latch as case 1; add it if you want the balancer shape pinned by name.
**Sites:**
- internal/database/routing_migration.go:188 — `if stopped || rule == nil { kept = append(kept, raw); continue }` : the sibling the first plan's grep structurally could not match; a non-object element is kept and the walk carries on
- internal/database/routing_migration.go:192-195 — `if isTransparentRule(rule) { kept = append(kept, raw); continue }` : keeps without latching; the primary site
- internal/database/routing_migration.go:197-198 — `if !ok { stopped = true; ... }` : the one branch that already does the right thing, and the shape the two above must match
- internal/database/routing_migration.go:157-164 — the doc comment stating the invariant both branches break
- internal/database/routing_migration.go:227-245 — isTransparentRule; the api arm (:228) is the only safe walk-past, the geoip:private arm (:239-243) and the `_bl_` inboundTag arm (:235) are both production-reachable, the `-dns-allow` arm (:235) is dead
- internal/web/service/routing.go:373-384 — splitPinnedAPIRule returns `rules[:i+1], rules[i+1:]`; head ENDS at the api rule, which is why api is the only exemption
- internal/web/service/routing.go:352-361 — `head, tail := splitPinnedAPIRule(existing)` then `routingSection["rules"] = append(next, tail...)`: compiled intent is spliced strictly after head
- internal/web/service/config.json (routing.rules) — the shipped default: [api, {ip:[geoip:private],outboundTag:blocked}, bittorrent]; the private block is a walked-past transparent rule at index 1
- frontend/src/pages/xray/balancers/balancer-loopback.ts:60-65,99-103 — firstGeneralRoutingRuleIndex splices `{inboundTag:["_bl_X"],balancerTag:"X"}` at the first rule with no inboundTag, i.e. index 1, above the private block
- internal/routing/plan.go:135,169-170,405-410 — guardRule is emitted only when `subject.Handle.Tag == ""`; evidence for the DEFERRED item, not touched by this fix


### F076 — ship-it (small)
**Root:** `internal/egress` decides "is this dialled device mine?" from ONE hard-coded namespace literal instead of from the set of namespaces the registered uplink drivers actually create in. `alloc.go:33` declares `uplinkPrefix = "pux"`, and `alloc.go:68-78 ownedUplinkID` round-trips only through `Uplink(id)`. But the device name comes from the engine the driver holds: `wgclient.Fill` names it `d.mgr.Name(e.ID)`, and the SECOND registered uplink driver (`internal/web/service/egress.go:53`, `wgclient.NewTyped(awg.UplinkDriverType, awg.UplinkManager())`) is built over a manager whose prefix is `internal/awg/plane_linux.go:131 UplinkPrefix = "paux"`. So every ownership predicate in the package is blind to `paux<N>`.

Corrected scope after reading the code: `ownsDevice(want, got)` short-circuits on `got == want` (manager.go:290-292), so an ENABLED, typed awg row DOES classify its own front correctly. The blindness only bites where `want` is empty — which is exactly (a) `foreignBandObjects`, which calls `ownsDevice("", route.Device)` because preflight holds no rows, and (b) `plan` returning `want{}` for a disabled/deleted row or a row whose `Fill` failed. That is why the original plan's one-pass tests were green: they never reached either state with a route on the host.

Three live consequences, all from that one predicate:
1. PERMANENT false refusal. Any host with a converged awg-client uplink has `default dev paux<N>` in table 3000<N>; `preflight.go:179` cannot claim it, so `Preflight` refuses forever with `ErrForeignResource: ... sits in a reserved table` — the panel accusing its own healthy route of squatting. `EgressService.Preflight` (service/egress.go:280) is what the operator's preflight endpoint (controller/egress.go:63) renders.
2. SPURIOUS HARD ERROR on every disable/delete of an awg row. `ownedView(snap, id, "")` files the still-in-snapshot front route into `h.foreign` AND `h.shadowing`, and `manager.go:437` turns it into `ErrForeignResource: ... defeats egress N's own containment`. That error is returned by `Manager.Reconcile` → `EgressService.Reconcile` → `RoutingService.converge` (routing.go:613), so a routing-rule save can fail for the operator, and the drift job logs "egress reconcile failed" on the tick. The `pux` path does not do this: `settledDel` absorbs the already-purged route benignly.
3. LEAKED UPLINK. `manager.go:117-123`'s orphan sweep is the only thing that finds a dialled device whose rule and table were already reaped; it misses every `paux<N>`, so that device keeps dialling the provider forever with no row, no route and nothing that will ever notice.
**Ships now:** Teach the one predicate that the panel dials in more than one namespace. Two files, ~12 lines, no new types, no config, no migration, no i18n.

1. `internal/egress/alloc.go` — keep `uplinkPrefix = "pux"` unchanged (it is what `Uplink(id)` must keep returning for the plain wg engine) and add one sibling constant beside it:

    // awgUplinkPrefix is internal/awg's dialled-device namespace (plane_linux.go
    // UplinkPrefix). Re-declared, not imported: this package may name a device.
    awgUplinkPrefix = "paux"

2. `internal/egress/alloc.go:67-78` — loop `ownedUplinkID` over both prefixes, keeping the exact round-trip rather than a bare HasPrefix, and `continue` (never early-return) on a near miss:

    // ownedUplinkID reads the id back out of a device this panel dials, in either
    // namespace it dials in. It round-trips, so pux007 and pux0 are not ours.
    func ownedUplinkID(name string) (int, bool) {
        for _, prefix := range [...]string{uplinkPrefix, awgUplinkPrefix} {
            rest, ok := strings.CutPrefix(name, prefix)
            if !ok {
                continue
            }
            id, err := strconv.Atoi(rest)
            if err != nil || !ValidID(id) || prefix+strconv.Itoa(id) != name {
                continue
            }
            return id, true
        }
        return 0, false
    }

   `pux` and `paux` are letters-only and neither is a prefix of the other, so a name splits at exactly one place and the two can never both claim a device.

3. `internal/egress/manager.go:284` — the `ownsDevice` doc comment's last line becomes: "Three namespaces: a front the panel carved (peg) and the two it dials through (pux, paux)."

That is the whole change. `ownsDevice` (manager.go:296), the Reconcile orphan sweep (manager.go:118) and `foreignBandObjects` (preflight.go:179) are the only three callers, and all three symptoms clear together. Do NOT collapse awg onto `pux`: a retype of one row would then name two engines' devices identically and the two managers would fight over one device.
**Deferred:** 1. THE STRUCTURAL FIX — deriving the namespace from the driver registry instead of from literals. `Manager` already holds `m.drivers`, so `ownedUplinkID` could ask every registered Provisioner "is this device yours?" via a new capability method. That is a new seam on the `Driver` interface and cannot be shipped unasked next to a repair (hard rule: fix size must match bug size). It matters because SSH, OpenVPN, IKEv2 and PPP are on the core roadmap and each brings a fifth, sixth, seventh dialled namespace into the same blind predicate. This is the decision question below.

2. AN ARCH GUARD pinning every dialled-device prefix declared in the tree into `internal/egress/alloc.go` (the `internal/arch` package is exactly the place for it — see `internal/arch/imports_test.go`, `policy_naming_test.go`). Cheaper than option B and would have caught this miss and the next one. Still a new guard, so it waits on the same answer.

3. THE RETYPE LEAK — `internal/web/service/egress.go:178 stored.Type = row.Type`. Retyping a row from `wg-client` to `awg-client` provisions `paux<N>` and leaves `pux<N>` dialling forever: `provision` only looks up the NEW type's driver, and the orphan sweep skips an id that still has a row, so THIS FIX DOES NOT COVER IT in either direction. Distinct root cause, and it carries its own behaviour choice (refuse a retype outright, versus deprovision-old-then-provision-new inside one converge). Should be filed as its own finding.

4. `preflight.go:143-150 ownGateway` exempts only `peg<N>`. `DefaultGatewayBase` is 100.127.0.0/16, which sits inside CGNAT 100.64.0.0/10, so a provider that assigns an uplink a CGNAT address makes Preflight refuse permanently with "move the egress gateway base off it". The collision is real and the refusal is defensible; only the message misattributes it. Separate call, separate finding.

5. No corresponding positive `pux` case exists in `preflight_test.go` today either. Adding one alongside Test A would be free and is worth doing in the same commit, but it is not what makes the commit red.
**Test:** Three tests, all in plain `make test-go` — no root, no Postgres, no XRAY_E2E_BINARY. Test A and B are the load-bearing ones; each is provably red today for a reason that does not depend on converge's snapshot timing.

TEST A (primary — the permanent false refusal). One case appended to the existing table in `internal/egress/preflight_test.go:TestPreflight`, which runs `egress.New(k, nil).Preflight(...)` and needs no driver at all:

    {
        name: "an AmneziaWG uplink this panel dialled is not foreign",
        host: func(t *testing.T, k *egtest.Kernel) {
            t.Helper()
            k.AddLink("paux9")
            k.SeedRoute(egress.RouteSpec{
                Family: egress.FamilyV4, Table: 30009, Type: egress.RouteUnicast,
                Dst: egress.FamilyV4.DefaultRoute(), Device: "paux9", Metric: egress.FrontMetric,
            })
        },
        base: egress.DefaultGatewayBase,   // no refusal expected
    }

  RED TODAY, exact failure: `preflight_test.go` takes the `tc.refusal == nil` branch and calls `t.Fatalf("Preflight refused a usable host: %v", report.Err())`, printing `egress: an object in the reserved band belongs to somebody else: v4 table 30009 default dev paux9 metric 100 sits in a reserved table — move it out of 30001..30999`. Green with the fix, because `ownsDevice("", "paux9")` then returns true at `preflight.go:179` and the route is skipped. Nothing else in the case can mask it: `cleanHost` sets all=2/forward=1, `probeHost` passes on the fake, and `darkFronts` sees no rows. (SeedRoute bypasses the `ErrNoDevice` check at egtest/kernel.go:113 — the `AddLink` is there only to match the file's style, not because it is required.)

TEST B (the leaked uplink). New test in `internal/egress/provision_test.go`, exactly mirroring `TestOrphanedUplinkDeviceIsReaped` but in the other namespace. Needs a second fake whose Type is distinct from `fake-uplink` (Registry.Register refuses a duplicate) and whose Fill/Provision/Deprovision name `"paux"+strconv.Itoa(id)` — with a 2-line comment pointing at internal/awg/plane_linux.go:131 as the source of that literal:

    kernel := egtest.New()
    driver := &fakeAWGUplink{kernel: kernel}          // Type() == "fake-awg-uplink"
    manager := newManager(t, kernel, driver)
    kernel.AddLink("paux7")                            // the crash state: device left, band reaped
    if err := manager.Reconcile(context.Background(), nil); err != nil { t.Fatalf(...) }
    if len(driver.deprovisioned) == 0 { t.Fatalf("paux7 is in a namespace this panel dials and no row wants it, so it must be taken down") }

  RED TODAY: with no rows, no rules and no routes, `byID` is empty — `manager.go:118` cannot see `paux7`, converge never runs for id 7, `deprovisionAll` is never reached, and the test fails on `len(driver.deprovisioned) == 0` with that message. Green with the fix: the sweep synthesises `Egress{ID: 7}`, `provision` takes the `e.Type == ""` arm and asks every registered Provisioner (xray-tun is an Injector, not a Provisioner, so only the fake is called).

TEST C (the spurious hard error on disable — covers manager.go:265/437). Same fake. The device MUST exist in the snapshot before the enabling pass, or the front route is never installed at all — this is the exact trap the first plan fell into:

    kernel := egtest.New()
    kernel.AddLink("paux4")                            // NOT optional: plan() reads the pre-provision snapshot
    driver := &fakeAWGUplink{kernel: kernel}
    manager := newManager(t, kernel, driver)
    row := egress.Egress{ID: 4, Type: "fake-awg-uplink", Enable: true}
    mustReconcile(row)                                 // installs default dev paux4 table 30004 metric 100
    off := row; off.Enable = false
    err := manager.Reconcile(ctx, []egress.Egress{off})
    if err != nil { t.Fatalf("disabling a dialled egress reported its own front as a squatter: %v", err) }

  RED TODAY, exact failure: `egress 4: egress: an object in the reserved band belongs to somebody else: v4 table 30004 default dev paux4 metric 100 defeats egress 4's own containment` (and the v6 twin), joined. Green with the fix, where `delFront` issues the DelRoute, the kernel answers `ErrNotInstalled` (DelLink already purged it) and `settledDel` absorbs it.
  Do NOT assert "table 30004 holds only its blackhole" — that post-condition is wrong and would fail with the fix too: `plan` returns `want{}` for a disabled row, `live` reaches 0 and every blackhole is deleted. If a state assertion is wanted, assert `kernel.Routes()` and `kernel.Rules()` are both EMPTY. Note honestly that on this path the fix changes the returned error, not the kernel state — the harm is the error reaching `RoutingService.converge` and the operator's save.

Regression check: nothing existing seeds a `paux` device, so no current test flips. Gate is `make lint` + `make test-go`.
**Sites:**
- internal/egress/alloc.go:33 — `uplinkPrefix = "pux"`, the only dialled-device namespace this package knows
- internal/egress/alloc.go:67-78 — `ownedUplinkID`, the ONE predicate all three symptoms flow through; its comment ("round-trips through Uplink, so pux007 and pux0 are not ours") also needs updating
- internal/egress/manager.go:284 — the doc comment "Both namespaces: a front the panel carved (peg) and an uplink it dials (pux)" is now wrong; there are three
- internal/egress/manager.go:286-298 — `ownsDevice`; note it short-circuits on `got == want` at :290, which is why an enabled typed row is NOT affected
- internal/egress/manager.go:265 — `ownedView`'s front arm, reached with device=="" for a disabled/deleted row or a failed Fill; misclassifies the paux front as foreign+shadowing
- internal/egress/manager.go:437 — the `%w: %s defeats egress %d's own containment` sentence that reaches RoutingService.converge
- internal/egress/manager.go:117-123 — Reconcile's orphan sweep; misses every `paux<N>` device whose band objects are gone
- internal/egress/preflight.go:179 — `ownsDevice("", route.Device)` in `foreignBandObjects`; the permanent false refusal
- internal/awg/plane_linux.go:131 — `UplinkPrefix = "paux"`, the source of the second namespace (mirrored at internal/awg/plane_other.go:31)
- internal/web/service/egress.go:53 — `registry.Register(wgclient.NewTyped(awg.UplinkDriverType, awg.UplinkManager())...)`, where the second uplink namespace enters the build
- internal/shaping/alloc.go:21-26 — the parallel prefix list that DID get `amneziawgPrefix = "pawg"`; the precedent for the literal and the proof this is a one-of-a-pair miss
- internal/web/service/egress.go:178 — SIBLING, DISTINCT ROOT, DEFERRED: `stored.Type = row.Type` lets a row be retyped wg-client→awg-client, and `provision` only knows the NEW type, so `pux<N>` is left dialling. Not fixed by this change: the orphan sweep skips an id that still has a row.
- internal/egress/preflight.go:143-150 — SIBLING, DEFERRED: `ownGateway` exempts only `ownedEgressID` (peg). A provider-assigned uplink address inside 100.127.0.0/16 (which is inside CGNAT 100.64.0.0/10) is refused as a squatter. Arguably a correct refusal with misleading advice; separate call.

**Re-check notes:**
- Severity is overstated in one place, though not in a way that changes the verdict. The claim calls the preflight symptom a "permanent false refusal" and leans on preflight.go's own doc comment ("A refusal blocks an attach"). It does not block an attach: `Manager.Preflight` has exactly one production caller chain — `EgressService.Preflight` (internal/web/service/egress.go:289) reached only from `EgressController.preflight` (internal/web/controller/egress.go:63), which serializes `report.OK()` into a JSON health payload. Nothing on the attach path consumes it. The real harm there is a health page that names the operator's own working `pauxN` route a foreign squatter and tells them to "move it out of 30001..30999" — advice that would break the egress if followed. The finding's severity text should say "misleading health report with harmful remedy", not "blocked operation". Symptoms 2 and 3 carry the finding regardless.
- Only three of the four listed symptom sites are load-bearing repairs; the manager.go:437 shadowing sentence is a consequence of manager.go:265, not a separate site. Not a defect in the change (the fix is at the one predicate, which is correct), just a note that the "sites" list double-counts one symptom as two.


### F135 — ship-with-the-noted-correction (small)
**Root:** The endpoint map has vocabulary for "override this" but none for "clear this". The emitter expresses KeepSniBlank by declining to write ep["sni"] (host_sub.go:68-70), so absent-key and "no override" become the same signal — and every SNI applier is written as set-or-skip, never clear. Because each generator writes the inbound's own SNI into the target FIRST, skipping means inheriting. The switch is therefore a guaranteed no-op on every TLS path.

Corrected base-write map (the original plan got this wrong, and the error is what hid the missed site):
- service.go:1100 is inside genHysteriaLink (1074-1180), and feeds applyExternalProxyHysteriaParams via the call at :1152 — NOT the vless path.
- The vless/trojan/ss base write is service.go:1463, inside applyShareTLSParams (1449-1485), feeding applyExternalProxyTLSParams at :1701.
- The vmess base write is service.go:1498 (applyVmessTLSParams), feeding applyExternalProxyTLSObj at :1677.
- The JSON/Clash stream keeps tlsSettings.serverName untouched, feeding applyExternalProxyTLSToStream at :1802.

There are FOUR appliers, not three. applyExternalProxyHysteriaParams (:1723) reads ep["sni"] directly at :1730 and never calls externalProxySNI, so it bypasses the shared helper and would bypass any marker added to it.

Verified NOT part of the root cause: the Host mappers are clean. KeepSniBlank is carried by both writers (service/host.go:49 model->entity, :138 entity->model), is persisted (model.go:1144), and is in entity.go:307 — the three-mapper trap this repo repeats does not apply. The column already ships, so no migration.
**Ships now:** Give the endpoint an explicit blank marker and teach all FOUR TLS appliers to honour it. Six small edits. No new column (keep_sni_blank already ships), no migration, no i18n key, no route, no model/entity change, no new abstraction.

1. internal/sub/host_sub.go:68-70 — emit the instruction instead of swallowing it:
   `if h.KeepSniBlank { ep["sniBlank"] = true } else if sni != "" { ep["sni"] = sni }`
   Rides the same convention as the existing `isHost` marker in the same function; verified it cannot leak (json_service.go:176 and the clash path both drop `externalProxy` before emit).

2. internal/sub/service.go beside externalProxySNI (~:1850) — a 3-line predicate, deliberately NOT folded into externalProxySNI (site 4 does not call it, so folding would create a false sense of coverage):
   `func externalProxySNIBlank(ep map[string]any) bool { b, _ := ep["sniBlank"].(bool); return b }`

3. service.go:1701 (raw vless/trojan/ss) — `if externalProxySNIBlank(ep) { delete(params, "sni") } else if sni, ok := externalProxySNI(ep); ok { params["sni"] = sni }`  (DELETE: q.Set at :1994 is unconditional.)

4. service.go:1677 (vmess obj) — same shape, `delete(obj, "sni")`.

5. service.go:1802 (JSON + Clash stream) — same shape but ASSIGN `tlsSettings["serverName"] = ""`, because json_service.go:328 / clash_service.go:737 copy the key unconditionally and a delete renders `null`. Clash's own `serverName != ""` guards (clash_service.go:302, :657) then correctly omit `sni`.

6. service.go:1730 (hysteria params, THE MISSED SITE) — `if externalProxySNIBlank(ep) { delete(params, "sni") } else if sni, ok := ep["sni"].(string); ok && sni != "" { params["sni"] = sni }`

Why edit 6 is in shippableNow and not deferred: hysteria is TLS-only, so blanking is exactly what the label promises and needs no behaviour choice. Shipping 1-5 without 6 would make Clash omit `sni` while the raw hysteria2:// link still carries it — a divergence that does NOT exist today. Splitting here would create breakage rather than remove it.

This is an undisputed repair: a persisted, mapped, user-facing switch labelled "keep SNI blank" provably cannot do what its label says on any TLS path. No operator can prefer a silent no-op.
**Deferred:** REALITY hosts are deliberately excluded. All six edits scope to TLS for free: appliers 1-3 early-return on `security != "tls"`, and applier 4 is hysteria-only. Reality's two SNI sites (host_sub.go:302-307 raw, host_sub.go:163-173 stream) both read `externalProxySNI(ep)`, which returns false under KeepSniBlank — so a reality host keeps inheriting the inbound's serverName both before and after this fix.

This is deferred rather than fixed because it requires a behaviour choice I should not make alone: a reality handshake needs a serverName the client can present, and blanking it ships a config that cannot connect. Fixing it "for completeness" would trade a silent no-op for a broken tunnel.

The deferral is clean in a way the TLS case is not: reality is a UNIFORM silent no-op in both raw and stream renderings today, and stays uniform after this fix. No new divergence is created by leaving it, which is exactly the test the hysteria site failed.

Also deferred: the confirmed same-shape dead-seam siblings found while tracing this (ShuffleHost at model.go:1179, MihomoX25519, IsHidden at model.go:1131 — each persisted, mapped, surfaced in the form, and consumed nowhere in internal/sub). They are separate findings with separate root causes; folding them in would violate fix-size-matches-bug-size.
**Test:** One new file internal/sub/host_sni_blank_test.go, package sub. No Postgres: every function is pure and linkSettings (service.go:170) is DB-free (verified — it only unmarshals inbound.Settings).

CRITICAL DESIGN POINT: the ep is built by calling the REAL emitter, never by hand-writing `sniBlank`. Hand-writing the marker would test the appliers against a key the emitter might not produce and would pass even if edit 1 were wrong. Building it via hostToExternalProxyMap is what pins emitter-key and predicate-key agreement.

  ep := hostToExternalProxyMap(&model.Host{Sni: "host.example", KeepSniBlank: true}, "cdn.example", 443)

Case A — table over all four appliers. Each is RED today with an exact message:
- applyExternalProxyTLSParams(ep, map[string]string{"security":"tls","sni":"inbound.example"}, "tls") -> assert key absent. TODAY FAILS: `raw vless/trojan/ss: sni = "inbound.example", want the key absent`. (Today the emitter writes no ep["sni"], externalProxySNI returns false, the applier skips, and the :1463 base write survives.)
- applyExternalProxyTLSObj(ep, map[string]any{"sni":"inbound.example"}, "tls") -> assert key absent. TODAY FAILS: `vmess: sni = "inbound.example", want the key absent`.
- applyExternalProxyTLSToStream(ep, {"security":"tls","tlsSettings":{"serverName":"inbound.example"}}, "tls") -> assert serverName == "". TODAY FAILS: `JSON/Clash stream: serverName = "inbound.example", want ""`.
- applyExternalProxyHysteriaParams(ep, map[string]string{"sni":"inbound.example"}) -> assert key absent. TODAY FAILS: `hysteria: sni = "inbound.example", want the key absent`. THIS IS THE CASE THE REJECTED PLAN HAD NO COVERAGE FOR.

Case B — raw hysteria e2e, reusing the existing harness hysteriaHostInbound (host_link_gaps_test.go:120), whose inbound TLS names "origin.sni" precisely so inherited is distinguishable from overridden. Build the endpoint JSON from the real emitter output, then genHysteriaLink -> url.Parse -> assert q.Get("sni") == "" AND that the raw query contains no "sni=" substring (the second assertion is what catches a bare `sni=` if someone assigns "" instead of deleting). TODAY FAILS: `hysteria2:// link carries sni=origin.sni, want no sni param`.

Case C — the raw/Clash agreement proof the adversary demanded. Same Host row through SubClashService.getProxies (template: clash_hysteria_host_test.go:47) -> assert proxy["sni"] is absent. Today Clash and the raw link are uniformly wrong; after the fix both must be uniformly blank. Asserting them in one test is what stops a future partial fix from re-splitting them.

REGRESSION GUARD THAT MUST STAY GREEN: TestHysteriaLinkInheritsWhenTheHostSaysNothing (host_link_gaps_test.go:156) asserts a host that sets nothing still inherits origin.sni. With KeepSniBlank=false the emitter writes no marker, so inheritance is preserved. If this test goes red, the marker is leaking into the default path.

Verification protocol per CLAUDE.md: write the tests, revert each of the six edits in turn, confirm the matching assertion goes red, restore. Edit 6 specifically must be reverted on its own to confirm Case B/C go red while Case A's first three stay green — that is the proof the missed site is genuinely covered rather than masked by the other edits.
**Sites:**
- internal/sub/host_sub.go:68-70 — THE EMITTER, suppression-only: `if !h.KeepSniBlank && sni != "" { ep["sni"] = sni }`. Absent-key cannot be distinguished from "no override".
- internal/sub/service.go:1845-1850 — THE COLLAPSE: `externalProxySNI` returns ("", false) for both the absent key and an empty string, i.e. one signal meaning "no override".
- internal/sub/service.go:1701-1703 — APPLIER 1, raw vless/trojan/ss: `if sni, ok := externalProxySNI(ep); ok { params["sni"] = sni }`. On false the base write at :1463 (applyShareTLSParams) survives.
- internal/sub/service.go:1677-1679 — APPLIER 2, vmess base64-JSON: on false the base write at :1498 (applyVmessTLSParams) survives.
- internal/sub/service.go:1802-1804 — APPLIER 3, the JSON + Clash stream: on false tlsSettings.serverName is never touched, so the inbound's value is inherited.
- internal/sub/service.go:1730-1732 — APPLIER 4, THE SITE THE FIRST PLAN MISSED (adversary correct, verified): `if sni, ok := ep["sni"].(string); ok && sni != ""` reads ep DIRECTLY, bypassing externalProxySNI. Reached by genHysteriaLink (:1074) -> :1152. Omitting it leaves raw hysteria2:// links carrying the inbound SNI while the plan's stream edit already fixes Clash hysteria — a NEW divergence that does not exist today.
- internal/sub/service.go:1993-1994 — CONSTRAINT: appendQueryAndFragment's `q.Set(k, v)` is unconditional, and its omitTLSFields skip is not used by the hysteria call site. Forces DELETE (not assign "") on all three params/obj appliers, or the link emits a bare `sni=`.
- internal/sub/json_service.go:328 + internal/sub/clash_service.go:737 — CONSTRAINT: both `tlsData` copies do an UNCONDITIONAL `tlsData["serverName"] = tData["serverName"]`. Forces assign "" (not delete) on the stream applier, since delete would render `"serverName": null`.
- internal/sub/json_service.go:192 — VERIFIED ROUTING (advisor-flagged, not inherited): json_service DOES call applyEndpointToStream per endpoint, so the one stream edit repairs JSON as well as Clash. :176 does `delete(stream, "externalProxy")` before emit, so the new marker cannot leak into output.
- internal/sub/host_sub.go:302-307 — DEFERRED SITE, reality raw params: applyEndpointRealityParams gated on security=="reality".
- internal/sub/host_sub.go:163-173 — DEFERRED SITE, reality stream override in applyHostStreamOverrides: also reads externalProxySNI(ep), so reality no-ops CONSISTENTLY in both raw and stream form, before and after this fix.
- frontend/src/pages/hosts/HostFormModal.tsx:242 — the switch is gated on `showTls`, which is `security === 'tls' || security === 'reality'` (:87). This is what makes reality a real question rather than a theoretical one.
- NOTE FOR THE NEXT READER: `frontend/src/lib/hosts/host-link.ts` honours keepSniBlank, but it exists ONLY under .claude/worktrees/ — it is NOT on mainline. Grepping without excluding worktrees produces a false 'the frontend already does this' argument.

**Correction from the re-check:** Fix one sentence in the verification protocol, not the code. The plan states: "Edit 6 specifically must be reverted on its own to confirm Case B/C go red while Case A's first three stay green." Case C will NOT go red. Verified: clash_service.go:161-163 sets hasExternalProxy=true for a host row, and :189/:191 fold the endpoint into BOTH workingStream and workingRaw via applyEndpointToStream -> applyExternalProxyTLSToStream; Clash hysteria then reads serverName from workingRaw at clash_service.go:302, whose `serverName != ""` guard omits `sni`. So edit 5 alone repairs Clash hysteria; edit 6 repairs only the raw hysteria2:// link. Correct the protocol to: "revert edit 6 alone -> Case B (raw hysteria2:// link) and Case A's fourth row (applyExternalProxyHysteriaParams) go red, while Case C stays GREEN. That combination -- Clash blank, raw link still carrying origin.sni -- IS the divergence proof edit 6 exists to prevent." As written the engineer would hunt a phantom failure in a correct test. No change to any of the six edits or to the test assertions themselves.
**Re-check notes:**
- VERIFICATION-PROTOCOL SLIP (the only defect, corrected above): the plan predicts Case C goes red when edit 6 is reverted in isolation. It stays green, because edit 5 already repairs Clash hysteria through workingRaw (clash_service.go:191 -> :302). The prediction is wrong; the test is not.
- NOT A DEFECT, recorded for the reader: edit 5 emits `"serverName": ""` into the Xray JSON subscription rather than omitting the key, because json_service.go:328 copies it unconditionally. Semantically identical to omission in Xray (the client falls back to the dial address), and it is the only form expressible without touching tlsData -- which would be a larger fix than the bug. Not a behaviour choice.


### F140 — ship-it (small)
**Root:** `hostEndpoints` (internal/sub/host_sub.go:19-40) returns THREE outcomes in one value, and every caller collapses two of them with `len(...) > 0`:

- `nil` on DB error (host_sub.go:24-27)
- `nil` when the inbound owns zero enabled Host rows (host_sub.go:28-30) — the WHERE at :22 also filters `is_disabled = true`, so "all hosts disabled" lands here too
- a NON-NIL, EMPTY slice when at least one enabled row exists but every one of them lists the requested format in `ExcludeFromSubTypes` — because `eps := make([]map[string]any, 0, len(hosts))` (host_sub.go:32) is non-nil and the loop `continue`s past all of them (host_sub.go:33-38)

Only the caller can distinguish "no hosts, use the legacy origin" from "the operator closed every entry point to this format, emit nothing". All five callers use `len(...) > 0`, so case 3 falls into the identical branch as cases 1-2 and the subscriber is handed `resolveInboundAddress(inbound)` — the inbound's own origin. That is the precise inverse of what `ExcludeFromSubTypes` is sold as, and the repo's own audit-appendix/feature-gaps.md:77 calls it "the exact opposite of withholding an entry point", i.e. an admitted gap rather than a decision.

Two mechanisms make it a leak rather than a no-op:
- raw: `s.GetLink(inbound, client.Email)` in the `else` arm (service.go:323).
- clash/json: even injecting the empty array would not help — `externalProxies, ok := stream["externalProxy"].([]any); hasExternalProxy := ok && len(externalProxies) > 0` (clash_service.go:162, json_service.go:164) is false for an empty array, and the block immediately below SYNTHESISES an origin entry from `resolveInboundAddress`. So clash/json need a real skip, not a token swap.

ADVERSARY VERDICT: correct on all four counts, verified by reading:
1. CONFIRMED — the `hostEps != nil` swap does NOT close service.go:372-386. `linkForEmail` enters the block, but only `return`s inside `for _, client := range clients` on an email match; with no match it falls out and hits `return s.GetLink(inbound, email)` at :385. Reachable when `s.inboundService.GetClients(inbound)` errors (`clients` is nil, and the code deliberately continues — `if err == nil` only guards `primeLinkClients`), and via `InboundService.GetInboundLinks(host, id, email)` (internal/web/service/inbound_sublink.go:83), which passes a caller-supplied `c.Query("email")` (internal/web/controller/inbound.go:163) that need not belong to the inbound. Correct edit is an early `return ""`, not a comparison swap.
2. CONFIRMED — `TestSub_ExcludeFromSubTypes` (host_sub_test.go:392) seeds subId "s1" with ONE inbound whose only host excludes "clash" but NOT "raw" (host_sub_test.go:395), and the test's own raw assertion at :402 requires that host to render. A raw subtest on that subId can never reach an empty result. A separate top-level test is mandatory, and is safe: `seedSubDB` -> `initSubDB` (testdb_test.go:33) mints a fresh schema `pui_sub_test_<pid>_<seq>` per call.
3. CONFIRMED — with the withheld inbound as the sub's only inbound, `GetClash` returns early at clash_service.go:70 (`len(proxies) == 0`) and `GetJson` at json_service.go:118, so a post-fix `!strings.Contains("", needle)` is vacuously true and would also pass if the renderer were broken outright. And `seedSubInbound` hardcodes `Listen: "203.0.113.5"` for EVERY inbound (host_sub_test.go:27), so a co-tenant poisons that needle. Both must be designed around.
4. CONFIRMED — a fully-withheld single-inbound subscription becomes HTTP 404, not an empty profile: `serveClashBody`/`serveJsonBody` return false on `len(...) == 0` (controller.go:701, :737) and the caller runs `writeSubError(c, nil)` = `c.Status(http.StatusNotFound)` (controller.go:30-36); raw does the same at controller.go:343 and :425. `ApplyCommonHeaders` is never reached, so no `Subscription-Userinfo` header ships — which does partially undercut the plan's care to preserve `seenEmails`. That preservation still matters for the multi-inbound case (the common one), but the 404 must be named, not discovered in production.
**Ships now:** Withhold on the three SUBSCRIBER-FACING renderers only. No new columns, jobs, config knobs, helper types or i18n keys; ~7 changed lines of production code.

(1) internal/sub/host_sub.go:13-18 — the doc comment currently contracts only "Returns nil when the inbound has no applicable host", which is now actively false. Replace that ONE sentence in place (net zero new lines, so the pre-existing 6-line block does not grow past CLAUDE.md's 2-line budget for new blocks) with the three-way contract: nil = no hosts or DB error -> caller takes the legacy origin path; empty non-nil = every host closed this format -> caller emits NOTHING.

(2) internal/sub/service.go:320 (getSubs, raw) — one-token swap:
    `if len(hostEps) > 0 {`  ->  `if hostEps != nil {`
Correct here with zero extra code, and the chain is already documented as a deliberate seam at host_sub.go:216-219: `linkFromHosts` returns "" for empty eps (host_sub.go:221-222); getSubs already drops empty links via `if link != ""` (service.go:328) while `seenEmails[client.Email]` stays unconditional at :332 (the F311 traffic-header invariant); `hasEnabledClient` at :316-318 is untouched so `traffic.Enable` is unchanged; `splitLinkLines("")` returns nil (links.go:51-54); a DB error still returns nil so today's fail-open fallback is preserved.

(3) internal/sub/clash_service.go:49-55 — a token swap is WRONG here (injecting an empty array re-synthesises the origin at :162-168). Add a local flag, keeping the email counted:

    hostEps := subReq.hostEndpoints(inbound, "clash")
    // Every enabled host closed this format: withhold the inbound rather than
    // fall back to the origin address the exclusion exists to hide. F140.
    withheld := hostEps != nil && len(hostEps) == 0
    if len(hostEps) > 0 {
        injectExternalProxy(inbound, hostEps)
    }
    for _, client := range clients {
        seenEmails[client.Email] = struct{}{}
        if !withheld {
            proxies = append(proxies, s.getProxies(subReq, inbound, client, host)...)
        }
    }

The `len(hostEps) > 0` guard on the inject must stay: `injectExternalProxy(inbound, nil)` would overwrite a legacy `externalProxy` array with an empty one. A bare `continue` on the inbound is the wrong shape — it skips `seenEmails` and under-reports the Subscription-Userinfo totals for a multi-inbound subscription whose other inbounds carry different clients.

(4) internal/sub/json_service.go:87-94 — identical shape with format "json" and `configArray = append(configArray, s.getConfig(subReq, inbound, client, host)...)` inside the `if !withheld`.

NAMED, NOT CHANGED (a consequence of shipping, not a separate defect): a subscription whose ONLY inbound is fully withheld now returns 404 with no Subscription-Userinfo header (controller.go:343, :425, :701, :737 -> :30-36) instead of an origin-bearing 200. That is the same path a subscription with zero matching clients already takes today, and no operator who closed every host to a format prefers "the origin, silently" over "nothing". Multi-inbound subscriptions — the common case — simply drop the withheld inbound and keep a full, correct profile with an intact traffic header.

Note the SCOPE limit this fix has by construction: it covers "every host excluded from this format". It does NOT cover "every host disabled", which the SQL WHERE at host_sub.go:22 turns into the nil path — see `deferred`.
**Deferred:** 1. THE TWO PANEL-SIDE RAW RENDERERS — internal/sub/service.go:378 (`linkForEmail`, behind GET /panel/api/clients/links/:email and GET /panel/api/inbounds/:id/links?email=, via links.go:39 -> inbound_sublink.go:83 and :109) and internal/sub/service.go:395/405 (`inboundLinks`, behind GET /panel/api/inbounds/allLinks and /:id/links, via links.go:46 -> inbound_sublink.go:68 and :85). These are admin surfaces, not subscriber output, and a reasonable operator can prefer either answer — hence the decision question. Two things must be recorded for whoever implements option A: (a) the correct edit at linkForEmail is an EARLY `return ""` before the client loop, NOT `hostEps != nil`, because the no-match path falls through to `return s.GetLink(inbound, email)` at :385 and still leaks; (b) landing A makes the doc comment at service.go:362-370 ("the two must not disagree about the same client") true again, and leaving it as-is makes that comment false — so B should also rewrite that comment.
   Note the getSubs fix in `shippableNow` already changes ONE panel surface: GET /panel/api/clients/subs/:subId (controller/client.go:584 -> GetSubLinks -> SubLinksForSubId -> getSubs). That is intentional and not part of the decision — that endpoint's whole contract is "show me what the subscriber gets".

2. THE ALL-HOSTS-DISABLED VARIANT. `is_disabled = ?, false` in the WHERE at host_sub.go:22 means an inbound whose every host is disabled produces `len(hosts) == 0` -> nil -> legacy origin, indistinguishable from an inbound that never had a host. feature-gaps.md:77 lumps it in with the excluded case, but it is a different root cause (the SQL filter, not the nil/empty collapse) and a different question: disabling a host plausibly means "turn this alternate entrance off for now", not "shut this inbound out of every subscription". Closing it needs a second query or a COUNT — strictly more code than this fix — and answering it wrong silently blanks every subscription on a panel where an operator disabled hosts as a temporary measure. Explicitly out.

3. THE EMPTY-BUT-VALID-PROFILE ALTERNATIVE. Making a fully-withheld subscription return a 200 with an empty `proxies` list and a live Subscription-Userinfo header, instead of 404, would touch controller.go:30-36/:343/:425/:701/:737 — four call sites and the error contract for every empty subscription, not just withheld ones. Far larger than the bug. Out.

4. FileShare / the .conf download (feature-gaps.md:258) is a FOURTH representation of the same inbound that `ExcludeFromSubTypes`' raw/json/clash vocabulary cannot name, and it resolves its endpoint with `resolveInboundAddress` while never consulting Host rows at all. A separate finding; this fix cannot and does not close it.

5. Same-shape-different-function siblings, deliberately untouched: service.go:729 and :779 (`if endpoints := externalProxyEndpoints(...); len(endpoints) > 0 { ... } return <origin link>` in genWireguardLink/genMtprotoLink). Those read the legacy `externalProxy` array, are unreachable from the withheld path (linkFromHosts returns "" before GetLink), and are covered by F129/F143.

6. Adjacent, NOT fixed here: model.Host.IsHidden (model.go) and model.Host.NodeGuids — persisted and exposed through entity.HostGroup but with no consumer in the renderers. Separate findings.

NO CROSS-IMPLEMENTATION SYNC IS NEEDED: `git grep -n "excludeFromSubTypes" -- frontend/src docs/lib` shows the third link implementation (docs/lib/xray/) and the frontend renderer know nothing about Host rows — only the form (HostFormModal.tsx:296) and the generated schemas mention the field. No `make gen`, no openapi copy, no i18n key, no migration.
**Test:** See the detailed protocol above — summarised: new file internal/sub/host_withheld_format_test.go, `TestSub_EveryHostExcluded_WithholdsInsteadOfServingTheOrigin`, seeding TWO inbounds on subId "s1" (a co-tenant "keep" at 203.0.113.5:24901 with no hosts, and "shut" whose listen is UPDATEd to 198.51.100.9 and whose only host carries ExcludeFromSubTypes ["raw","json","clash"]). The three red assertions are `strings.Contains(<raw joined | clash yaml | json cfg>, "198.51.100.9")` must be false — all three are TRUE today (raw via GetLink at service.go:323; clash/json via the synthetic origin entry at clash_service.go:162-168 and json_service.go:164-174), failing with "raw subscription served 198.51.100.9, the origin of an inbound whose every host excludes \"raw\"..." and its clash/json equivalents. Anti-vacuity: the co-tenant's 203.0.113.5 must be present in all three and `strings.Count(yaml, "server:") == 1`, so the red assertions cannot pass by the renderer returning "". Wrong-fix guards: traffic.Up == 5_550_123 (raw) and `upload=5550123` in the clash/json headers, which go red against a naive `continue` that drops the email from seenEmails. Run with PUI_DB_DSN set or initSubDB skips and the run proves nothing.
**Sites:**
- internal/sub/host_sub.go:13-18 — doc comment that contracts only the nil case and is now false; rewrite in place, do not grow the block
- internal/sub/host_sub.go:22 — `Where("inbound_id = ? AND is_disabled = ?", inbound.Id, false)` — routes all-disabled into the nil path (deferred item 2)
- internal/sub/host_sub.go:28-30 — `if len(hosts) == 0 { return nil }` — outcome 2
- internal/sub/host_sub.go:32-38 — `eps := make([]map[string]any, 0, len(hosts))` + the `slices.Contains(h.ExcludeFromSubTypes, format) { continue }` loop — the non-nil EMPTY slice, outcome 3
- internal/sub/host_sub.go:216-222 — `linkFromHosts` doc + `if len(eps) == 0 { return "" }`, the documented seam that makes the getSubs one-token swap correct
- internal/sub/clash_service.go:49-55 — SHIP: withheld flag; token swap is insufficient here
- internal/sub/clash_service.go:161-168 — `hasExternalProxy := ok && len(externalProxies) > 0` and the synthetic origin entry below it; the reason an empty inject does not work
- internal/sub/clash_service.go:70 — `if len(proxies) == 0 { return "", "", nil }` — the vacuity trap the test must design around
- internal/sub/json_service.go:87-94 — SHIP: same withheld flag for format "json"
- internal/sub/json_service.go:163-174 — the json synthetic-origin block
- internal/sub/json_service.go:118 — `if len(configArray) == 0 { return "", "", nil }` — same vacuity trap
- internal/sub/service.go:314-333 (getSubs) — SHIP: `if len(hostEps) > 0` -> `if hostEps != nil` at :320; :328 `if link != ""` and :332 unconditional seenEmails are what make it safe
- internal/sub/service.go:372-386 (linkForEmail) — DEFERRED; the adversary's leak: `return s.GetLink(inbound, email)` at :385 on GetClients error or email no-match
- internal/sub/service.go:388-413 (inboundLinks) — DEFERRED; `if len(hostEps) > 0` at :405
- internal/sub/service.go:362-370 — the "the two must not disagree about the same client" doc comment the decision makes true or false
- internal/sub/links.go:38-49 — LinksForClient / LinksForInbounds, the panel entry points into the two deferred sites
- internal/web/service/inbound_sublink.go:74-86 and :88-111 — GetInboundLinks / GetAllClientLinks; :83 passes a caller-supplied email straight through
- internal/web/controller/inbound.go:113, :163 and internal/web/controller/client.go:584, :616 — the four panel endpoints affected by the decision
- internal/sub/controller.go:30-36, :343, :425, :701, :737 — the 404 shape a fully-withheld single-inbound subscription now takes
- internal/sub/host_sub_test.go:392-414 — the existing half-asserting TestSub_ExcludeFromSubTypes; leave it alone, it cannot host the new case
- internal/sub/host_sub_test.go:27 — `Listen: "203.0.113.5"` hardcoded for every seeded inbound; the reason the needle must be re-pointed
- internal/sub/testdb_test.go:33 — initSubDB mints a fresh per-call schema, which is why a separate top-level test is safe
- internal/sub/empty_render_test.go:34 — TestASubscriptionSkipsAnInboundThatRendersNothing, the F311 template the new test mirrors

**Re-check notes:**
- Non-blocking, already flagged by the claim: after the fix the existing TestSub_ExcludeFromSubTypes (host_sub_test.go:392-414) keeps passing but its clash assertion goes vacuous — GetClash returns "" (clash_service.go:70) so `strings.Contains(yaml, "clashless.cdn.com")` is false for the wrong reason. The new test carries the real coverage; leaving the old one alone is correct.
- Non-blocking observation not spelled out in the claim: for an inbound that has BOTH hosts and a legacy `externalProxy` array where every host excludes the format, the fix withholds the inbound rather than falling back to the legacy array (today's raw path would emit the legacy proxies via GetLink, not the origin). This is consistent, not a new decision — injectExternalProxy (host_sub.go:201-214) already wipes the legacy array whenever any host renders, so 'hosts supersede legacy externalProxy' is the model the code already commits to.


### F182 — still-needs-a-decision (small)
**Root:** Not `resolveInboundTag`'s fall-through. That fall-through is harmless for every caller that reads the response back — the UI, the advanced JSON editor and the REST API all receive the resolved tag and the central row is committed with it. The one caller that does not read it back is `Remote.AddInbound` (remote.go:403-420): it decodes `created.Tag`, spends it on an in-memory `cacheSet`, never compares it to the tag it actually sent, never errors, and then unconditionally runs `recordPushedInbound(ib)` keyed on the CENTRAL tag. So the panel records "deployed as T" while the node holds T_n, and every downstream harm follows from that one discarded comparison. Three verified end states: (1) `all` sync mode — `existsOnNode` is true because the colliding node-local inbound carries the bare tag (inbound_node.go:106-118) and the fingerprint matches, so `ReconcileInbound` reports "unchanged" forever while the sweep (inbound_node.go:150-165) deletes the just-created inbound on the next sync; the panel shows an inbound that does not exist and clients on it die silently. (2) `selected` mode — it survives as a permanent orphan the panel can never update or delete. (3) Either mode, once `refreshRemoteIDs` (remote.go:378-400) rebuilds the map from the node's list, `cacheGetTag`'s prefix-stripping fallback (remote.go:322-331) resolves the central's `n1-in-443-tcp` to the node's UNRELATED `in-443-tcp`, so `DelInbound` deletes the wrong inbound and `UpdateInbound` overwrites it. The triage's closing evidence ("the central picks the resolved tag up from the AddInbound response") is false in the tree, and `TestResolveInboundTag_RegeneratesOnCollision`'s comment states the same false rationale.
**Ships now:** Refuse to record a push the node did not perform. In `Remote.AddInbound` (internal/web/runtime/remote.go:403-420), inside the existing `created.Id > 0 && created.Tag != ""` guard, compare the node's tag against the tag actually sent; on a mismatch roll back the inbound we just created, skip the cache write and the fingerprint stamp, and return a named error:

    if sent := payload.Get("tag"); created.Tag != sent {
        // The node renamed the tag on a collision; recording it would stamp a
        // push under a tag the node does not have.
        if _, delErr := r.do(ctx, http.MethodPost,
            "panel/api/inbounds/del/"+strconv.Itoa(created.Id), nil); delErr != nil {
            logger.Warning("remote AddInbound: could not roll back", created.Tag, "on", r.node.Name, delErr)
        }
        return fmt.Errorf("node %s stored inbound %q as %q: rename one side so the tag is free",
            r.node.Name, sent, created.Tag)
    }
    r.cacheSet(created.Tag, created.Id)

~10 lines in one function. No new field, no DB/model change, no migration, no config knob, no i18n key (ReconcileNode's error path is plain `fmt.Errorf`, not translated), no controller route, no frontend change. All identifiers used are already imported (`strconv`, `fmt`, `logger`, `http`). Comment is 2 lines.

In the SAME commit, correct the false rationale in `TestResolveInboundTag_RegeneratesOnCollision`'s comment (port_conflict_test.go:433-437): the central does not pick the resolved tag up from the AddInbound response. The test's assertion is left alone — this change touches no tag policy.

Why this is an undisputed repair and not a behaviour choice: it does not touch who may pick a tag, so none of the four regressions the adversary found can occur — the UI's auto-composed tag path, the port-change edit path, and every local create are untouched. It only stops the panel from asserting a deployment that does not exist. The status quo has exactly three end states and no reasonable operator prefers any of them: the inbound is created and then silently deleted by the next sweep while the panel reports it fine (`all` mode); or it is a permanent orphan the panel can never update or delete (`selected` mode); or the central's tag resolves through cacheGetTag to a DIFFERENT node inbound so a later delete removes the wrong one and a later update overwrites it. After the guard the operator gets a named error naming both tags on every node sync until they rename one side.
**Deferred:** 1. Every change to `resolveInboundTag` — the blanket refusal AND the adversary's shape-gated refusal. The blanket refusal is dead: verified that the form always sends a tag and has no tag field, so two inbounds on one port with different listen IPs become uncreatable, and a port-change edit is refused because inbound.go:1575 compares against the old stored tag. The shape gate is also rejected, on new evidence the adversary did not have: wireInbound strips the `n<id>-` prefix and the node stores the inbound with NodeID nil, so `isAutoGeneratedTag("in-443-tcp", 443, nil, TCP)` is TRUE on the node and the gate falls straight through to `generateInboundTag`. It would be UI-safe and fix nothing.

2. Convergence — the central adopting the node's chosen tag into its own row so the inbound keeps serving. This is the option that preserves today's "the inbound still lands" semantics, but it needs plumbing the bug has not earned: the runtime layer must return the corrected tag across the runtime→service seam at two push sites (ReconcileNode's `runtimeIb`, which is a built copy, and AddInbound's postCommitApply), plus a UNIQUE(inbounds.tag) guard on the write-back. That is new architecture and per CLAUDE.md needs agreement first.

3. The wrong-target resolution in cacheGetTag (remote.go:322-331) is not independently repaired. It cannot be: the prefix-stripping fallback cannot distinguish the pushed inbound from a node-local one holding the bare tag, and no per-inbound stable identity exists (`OriginNodeGuid`, model.go:86-92, names the hosting PANEL, not the inbound). An in-memory alias map was considered and rejected — refreshRemoteIDs replaces the map wholesale and a panel restart drops it, so it stops working exactly when it matters. shippableNow prevents the divergent state that makes the fallback dangerous through the add path, which is the reachable route; it does not harden the lookup itself.

4. `DelInbound` returning nil on a resolve miss (remote.go:426-429) — a separate fail-open, left alone.
**Test:** Go, stdlib testing, `httptest` — no Postgres, no env gate. New test in `internal/web/runtime/`, reusing the existing harness `NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)` (remote_mtls_test.go:16-31, node Id 1 so the prefix is `n1-`), following the shape of reconcile_skip_test.go:17-40.

Stub node: POST `/panel/api/inbounds/add` replies `{"success":true,"obj":{"id":9,"tag":"in-443-tcp-2"}}` (the node already owns `in-443-tcp` and renamed); POST `/panel/api/inbounds/del/9` counts a call and replies `{"success":true}`; everything else replies `{"success":true}`. Push `&model.Inbound{Tag:"n1-in-443-tcp", Port:443, Protocol: model.VLESS, Settings:`{"clients":[]}`}` so wireInbound sends the bare `in-443-tcp`.

Two independent assertions, both RED today:

(a) `if err := r.AddInbound(ctx, ib); err == nil { t.Fatal("AddInbound accepted a node-renamed tag: want an error naming both in-443-tcp and in-443-tcp-2") }`
Today AddInbound has no error path after `r.do` succeeds — it caches and returns nil — so this fails on the current tree with exactly that message. Post-fix, additionally assert `strings.Contains(err.Error(), "in-443-tcp-2")` and that the rollback delete fired once.

(b) The lie the fingerprint tells:
`if pushed, err2 := r.ReconcileInbound(ctx, ib, true); err2 == nil && !pushed { t.Fatal("reconcile reported 'unchanged' for an inbound the node stores under a different tag") }`
Today `recordPushedInbound(ib)` stamped `pushedFP["n1-in-443-tcp"]` with the fingerprint of this exact payload, so ReconcileInbound with existsOnNode=true hits `prev == fp` and returns `(false, nil)` — the assertion fires with that message. Post-fix the fingerprint is never stamped, ReconcileInbound falls through to UpdateInbound → resolveRemoteID miss → AddInbound → the new error, so `err2 != nil` and the assertion is skipped. Written as `err2 == nil && !pushed` deliberately: asserting `pushed` alone would go red post-fix too, which is the trap several rejected plans fell into.

Both assertions go green only with the guard; neither can pass either way.
**Sites:**
- internal/web/runtime/remote.go:403-420 — THE root. `AddInbound` decodes `created.Tag`, does only `r.cacheSet(created.Tag, created.Id)`, never compares it to the tag it sent, then `r.recordPushedInbound(ib)` stamps the fingerprint on the central tag regardless.
- internal/web/runtime/remote.go:756-760 — `wireInbound` strips the `n<id>-` prefix before pushing, so the node receives the bare `in-443-tcp` with NodeID nil. This is why the adversary's shape gate cannot work: on the node the pushed tag IS auto-shaped.
- internal/web/runtime/remote.go:322-331 — `cacheGetTag`'s CutPrefix fallback resolves `n1-in-443-tcp` to the node's unrelated `in-443-tcp`. Wrong-target resolution.
- internal/web/runtime/remote.go:378-400 — `refreshRemoteIDs` replaces `remoteIDByTag` wholesale from the node's list; this is why an in-memory central→node alias is not a durable repair.
- internal/web/runtime/remote.go:422-433 — `DelInbound` resolves by the central tag (so it deletes the wrong node inbound) and returns nil on a miss.
- internal/web/runtime/remote.go:435-448 — `UpdateInbound` resolves by `oldIb.Tag`, so a payload change overwrites the wrong node inbound.
- internal/web/runtime/remote.go:457-471 — `ReconcileInbound`: existsOnNode true + fingerprint match = permanent "unchanged" for an inbound that is not there.
- internal/web/service/inbound_node.go:106-118 — existsOnNode computed from the stripped/prefixed tag forms; the colliding node-local tag makes the collision read as "present".
- internal/web/service/inbound_node.go:150-165 — the sweep. The node's renamed tag is absent from desiredTags, so in `all` mode the just-created inbound is deleted on the next sync.
- internal/web/service/port_conflict.go:337-345 — `resolveInboundTag`'s fall-through. Real, but NOT the fix site.
- internal/web/service/port_conflict.go:290-305 — `isAutoGeneratedTag`. Returns TRUE for the pushed bare tag on the node; the disproof of the adversary's suggested correction.
- internal/web/service/port_conflict_test.go:433-457 — the pin whose comment states the false rationale the triage cited to close F182.
- internal/web/service/inbound.go:1575-1577 — the auto-tag clear compares against the OLD stored tag, so a port change submits a recomposed tag that is treated as explicit (adversary's update-path regression, confirmed).
- frontend/src/pages/inbounds/form/InboundFormModal.tsx:443-451 — auto-writes composeInboundTag into `tag` on open and on every port/nodeId/protocol/network change.
- frontend/src/lib/xray/inbound-form-adapter.ts:372 — `tag: values.tag` goes on the wire; with frontend/src/schemas/forms/inbound-form.ts:39 (`tag: z.string().default('')`) this disproves the original plan's "the local UI sends no tag".

**Re-check notes:**
- The claim's promised end state is false, and it is the load-bearing part of the 'no behaviour decision' argument. It says: 'After the guard the operator gets a named error naming both tags on every node sync until they rename one side.' Verified trace of the first ReconcileNode after the guard fires: (1) inbound_node.go:99 calls rt.ListRemoteTags, which calls refreshRemoteIDs, so remoteIDByTag is populated with the node's real tags including the colliding bare 'in-443-tcp' -> its id; (2) inbound_node.go:114-121 sets existsOnNode=true via the stripped form; (3) remote.go:459-466 finds no pushedFP entry (the fix's whole point) so the skip does not fire and it falls to UpdateInbound(ctx, ib, ib); (4) remote.go:436 resolveRemoteID('n1-in-443-tcp') -> cacheGetTag's CutPrefix fallback (remote.go:327-329) HITS the node-local id; (5) POST update/<id> with the central payload, and on the node resolveInboundTag(tag='in-443-tcp', ignoreId=<that same row>) finds the tag untaken and returns it verbatim, so the update SUCCEEDS. The error cannot recur, because resolve cannot miss: the colliding row is by definition in the node's inbound list. In production the named error appears exactly once, at create time, at logger.Debug level (inbound.go:1091-1096).
- Shipping the guard alone silently destroys a real node-local inbound. Because the collision only clears checkPortConflict when the listen IPs do not overlap (port_conflict.go:230 `if !listenOverlaps(...) { continue }`), the colliding row is always a separate, serving inbound with its own clients. Step 5 above overwrites its Listen, Protocol, StreamSettings and Settings (i.e. its client list) with the central's payload, then recordPushedInbound + cacheSet mark it converged so no later sync ever reports it. Today the false fingerprint is what SHIELDS that inbound: existsOnNode true + prev==fp returns (false, nil) and the reconcile path never touches it. The wrong-target clobber is reachable today only through an operator's own edit; the guard makes it automatic and unattended on the next sync cycle. Trading a silent non-deployment for silent data loss on the node is a behaviour choice an operator could reasonably dispute, and it needs sign-off.
- The claim's own rationale for deferring item 3 inverts under this trace. It states 'shippableNow prevents the divergent state that makes the fallback dangerous through the add path.' In fact it removes the only thing keeping the cacheGetTag strip-fallback dormant on the reconcile path. There is no small correction that restores the promised semantics: making the error recur requires refusing strip-fallback resolution, which is the per-inbound identity problem the claim itself argues cannot be repaired cheaply (OriginNodeGuid names the hosting panel, not the inbound).
- Test structuring defect: assertion (a) uses t.Fatal, so in one test function assertion (b) is dead code on the current tree and is never observed red. Split them into two test funcs or t.Run subtests so each is independently red. Separately verified that (b) IS red in isolation today (recordPushedInbound stamps pushedFP['n1-in-443-tcp'] with this exact payload's fingerprint, so ReconcileInbound with existsOnNode=true returns (false, nil)). But note (b)'s post-fix green depends on the stub returning no inbound list: with a production-shaped stub (list containing in-443-tcp) it goes green for the opposite reason -- the clobbering update succeeds and pushed=true. So the test pins the fingerprint lie and nothing else; it cannot detect the silent overwrite the fix introduces.
- Live sibling with the same defect shape, not named in the deferred list: Remote.UpdateInbound (remote.go:435-448) discards the response entirely (`if _, err := r.do(...)`), then unconditionally cacheSet(newIb.Tag, id) + recordPushedInbound(newIb). The node's update endpoint returns the full inbound object including its resolved tag (controller/inbound.go:272-282 jsonMsgObj), and the node re-runs resolveInboundTag on update (service/inbound.go:1565), so a tag-changing edit that collides with a different node-local row produces the identical 'record a push the node did not perform' lie. The claim's sites list mentions remote.go:435-448 only for wrong-target resolution, not for this. It is genuinely harder to repair (there is no clean rollback after an update has already overwritten something), which is a legitimate reason to defer -- but it should be named as deferred rather than left unmentioned.
- Not a blocker, recorded as accurate: the guard code itself compiles and is correct as written -- wireInbound returns url.Values (remote.go:744) so payload.Get('tag') is valid, the node's add response does carry the stored tag (controller/inbound.go:187-194 jsonMsgObj on the inbound whose Tag was set by resolveInboundTag at service/inbound.go:951), the rollback route POST panel/api/inbounds/del/<id> matches DelInbound's existing call, and all identifiers are already imported. The correction to TestResolveInboundTag_RegeneratesOnCollision's comment (port_conflict_test.go:433-437) is right: AddInbound only does cacheSet(created.Tag, created.Id) and never propagates the tag back across the runtime->service seam, so 'the central will pick up via the AddInbound response' is false. The fix is also not bigger than the bug -- ~10 lines, no new field, migration, route or i18n key.


### F188 — ship-it (medium)
**Root:** The adversary is RIGHT on all four counts; I verified each against the source and adopt its correction.

Confirmed: (1) `criteriaMaskFor` (internal/web/service/routing.go:87) puts "attrs" in `base`, and `base` is exactly what the device branch returns (:90-92) — but attrs can never match on a fronted L3 ingress: the front inbound is built by xraytun.Inject from the `tunInbound`/`tunSettings` structs (internal/egress/drivers/xraytun/xraytun.go:69-108), which have NO `sniffing` key and by their own doc-comment "cannot grow a key the driver never meant"; attributes exist only via `content.SetAttribute` in common/protocol/http/sniff.go and proxy/http/server.go; `AttributeMatcher.Apply` (app/router/condition.go:283-289) returns false when `ctx.GetAttributes()` is nil; and `Subject.Inspect` is never assigned (routing.go:64-70 sets only InboundID/Tag/Handle/CriteriaMask), so `FrontSpec{Sniff:}` is always false — and `Compiled.Fronts` has no production consumer at all. (2) `ruleCriteriaChips` (helpers.ts:91-103) has no attrs branch, so the plan's "one line covers desktop and mobile" was false. (3) The vlessRoute input (RuleFormModal.tsx:246-255) carries no `gate()` and `subjects` is passed for BOTH stores (RoutingTab.tsx:563-573, only `exits` is gated on editingSource). (4) `ValidateRoutingCriteria` (internal/xray/api.go:204-224) calls `(&conf.RouterConfig{}).Build()`, which merely assigns `rule.Attributes = rawFieldRule.Attributes` (infra/conf/router.go:256-257); the regex is compiled by `regexp.MustCompile` in app/router/config.go:68-72, reached only from `Router.Init` at core start — so `attrs: {":path":"("}` saves clean and then panics `xray -test`.

So the root cause is one level deeper than the original plan said. F188's real defect is a THREE-WAY disagreement about one vocabulary: the modal authors `attrs` and `vlessRoute`; `ruleToIntent`'s LIST_KEYS + three-scalar whitelist (routing.ts:173-175, 243-250) silently discards both; and the Go mask (routing.go:87) advertises `attrs` as matchable on subjects where it provably cannot match. Fixing only the frontend carry — the original plan — converts today's silent OVER-match (attrs dropped → a block rule blocks the whole inbound) into a silent NON-match on every L3 subject (attrs carried → emitted verbatim by plan.go:243-246 → AttributeMatcher always false → the block rule stops blocking). That is a fail-open, and it is why the plan was rightly rejected. The carry is only safe once the mask tells the truth.
**Ships now:** Make `attrs` mean the same thing in all three places, in ONE commit. Every item below is a repair with no behaviour choice: it either removes a silent drop, a silent no-match, or a silent crash. Ten edits, none longer than four lines.

GO (must land with the carry — the carry is unsafe without it):
1. internal/web/service/routing.go:87 — move `"attrs"` out of `base` and into the non-device append: `base := []string{"ip","port","sourcePort","network","source"}` … `return append(base, "user", "domain", "protocol", "attrs")`. attrs joins the group that needs sniffing/identity, which is where it always belonged: the front has no `sniffing` block, so `AttributeMatcher.Apply` returns false for every packet. Effect: `forbiddenCriterion` now catches an attrs rule on an L3 subject, `refuseInertRule` turns that into a save refusal, and the existing `KeyCriterionUnsupported` message ("{{.subject}} cannot match on {{.criterion}}…") is emitted — no new i18n key; the key exists in BOTH en-US.json:1692 and fa-IR.json:1572.
2. internal/xray/api.go, inside `ValidateRoutingCriteria` (~:216, after the body copy) — unmarshal the `attrs` key and `regexp.Compile` each value, returning the error. Five lines. Same RE2 package `MustCompile` uses, so the check is exact. Its own comment at routing.go:672-673 already claims this contract ("one unreadable character here is a config the core refuses whole"); today the claim is false.

TS BRIDGE (frontend/src/schemas/api/routing.ts) — both legs, same commit; one leg alone leaves the value mangled:
3. `XrayRuleShape` (:156-171) — add `attrs?: Record<string, string>;`.
4. READ, in `intentToRule` (:186-191) — `if (key === 'attrs') continue;` in the flatten loop, and take attrs from the parsed criteria object instead: parse `record.criteria` via the existing `safeParse`, and assign only when it is a non-null non-array object. ~4 lines. An object criterion never becomes "[object Object]", and a malformed stored value is ignored rather than expanded into 15 junk rows at RuleFormModal.tsx:128.
5. WRITE, in `criteriaFromForm` (:126-136) — widen the parameter to `Record<string, unknown>` and pass a plain object through untouched (2 lines); then in `ruleToIntent` (:242-250) carry `rule.attrs` into `criteria` when it is a non-empty object (2 lines). No other caller passes a non-string, so the widening is inert everywhere else.
6. `CRITERIA_FIELDS` (:100-109) — add `'attrs'`. Vocabulary alignment with the Go mask and with `gate('attrs')`; behaviourally dead (`intersectMask([])` is unreachable from the modal, which returns null at picked.length===0). Closes the "third key list, drifting" sibling.

EDITOR (frontend/src/pages/xray/routing/RuleFormModal.tsx + RoutingTab.tsx):
7. RoutingTab.tsx:570 — `subjects={editingSource === 'intent' ? subjects : undefined}`, mirroring the `exits` line directly below it. PREREQUISITE for 8 and 9: without it, gating a criterion would disable it on TEMPLATE rules too, where it is written verbatim and works. It also independently repairs the pre-existing mis-gating of domain/user/protocol on template rules that name a tag matching an L3 subject.
8. RuleFormModal.tsx:269-306 — apply `gate('attrs')` to the Attributes Form.Item and `disabled={!!gate('attrs').disabled}` to the add button and the row inputs (~3 lines). Pairs with edit 1: without the mask move the gate never fires; without the gate the operator meets the save refusal with no forewarning.
9. RuleFormModal.tsx:246-255 — apply `gate('vlessRoute')` to the vlessRoute FormField (1 line). vlessRoute is in NO mask, so for an intent rule with picked subjects it is now disabled with the existing `criterionUnavailable` string instead of silently eaten. This does NOT decide whether vlessRoute should work — see decisionQuestion — it only stops the editor lying about a field it discards.

DISPLAY (one line each, both stores):
10. RoutingTab.tsx `intentRows` (:164-187) — `attrs: shape.attrs ? JSON.stringify(shape.attrs, null, 2) : undefined`, mirroring the template path at :95-97, feeding the renderer that already exists at useRoutingColumns.tsx:172. AND helpers.ts `ruleCriteriaChips` (:91-103) — `if (rule.attrs) chips.push({ label: 'Attrs', value: rule.attrs });`. The chip line is itself an independent repair: attrs is invisible on mobile TODAY for template rules too. (`chipPreview` splits the JSON on commas, so a multi-key attrs previews as `{ "a": "b" +1` with the full text in the tooltip — same convention as every other chip.)

Net behaviour: on an Xray inbound, attrs now round-trips and shows in both lists. On an L3 inbound the field is greyed with its reason, and a rule that reaches the API anyway is refused by name instead of being stored as a blanket rule (today) or as a rule that never fires (naive fix). No migration, no new column, no new i18n key, no new endpoint, no `make gen` output change (criteria is `unknown` in the generated types).
**Deferred:** 1. WHETHER vlessRoute SHOULD WORK for operator rules. Shipped gated, not carried — see decisionQuestion. Deferred because carrying it is not free: vlessRoute is bytes 7-8 of the client's VLESS UUID, set only by proxy/vless/inbound and proxy/hysteria/server, and the mask has no per-protocol granularity — adding it to the non-L3 branch would offer it on Trojan/Shadowsocks/SOCKS inbounds where xray never sets it, manufacturing the exact "saves and never matches" class the mask exists to prevent.

2. ATTRS ON L3 VIA SNIFFING, and the dead seam behind it. `model.RoutingRule.Inspect` (model.go:170) is stored and updated (routing.go:563); `routing.Subject.Inspect` (plan.go:45) is never assigned; `FrontSpec.Sniff` (plan.go:78, 155) is derived from it; and `Compiled.Fronts` has NO production consumer — nothing turns a front's sniffing on. Wiring inspect → Subject.Inspect → a `sniffing` block on the tun inbound (xraytun.go's `tunSettings`, which its own comment forbids growing keys) would make attrs, domain and protocol matchable on L3 and would let the mask widen honestly. That is a feature with a kernel-facing blast radius, not part of a criterion-drop fix.

3. THE `inspect` BLANKING BUG — the remark bug's exact twin, and the reason it is NOT in shippableNow. `intentToRule` never emits `inspect` and `ruleToIntent` hardcodes `inspect: false` (routing.ts:267), so every UI edit of a rule stored with inspect=true silently clears it. It belongs to F188's family and shares the root, but by this exercise's own test it removes no real breakage today: the column has no consumer (item 2), so blanking it changes nothing on the wire. It must land WITH item 2, in the same commit, or the seam gets wired to a column the editor keeps zeroing.

4. THE GENERALIZATION. Any criterion outside the eight whitelisted keys is still dropped on any UI edit of an intent rule: `localIP`, `localPort`, `process`, `domains`, `ruleTag` and `webhook` all exist in xray's RawFieldRule (infra/conf/router.go:140-152) and none can be authored in the panel UI, so today the loss only bites an API-authored rule. `webhook` is object-shaped like attrs, so it also hits the "[object Object]" mangle on read — my attrs-specific read guard does not cover it. Each of these needs the same mask answer attrs just got ("can this subject honour it?"), which is per-criterion research, not one edit. Kept out so the shippable commit stays a repair rather than a redesign of the bridge.

5. THE GATE'S REMAINING HOLE. `allowed` is null when no inbound tag is picked yet (RuleFormModal.tsx:143-148), so on a fresh rule or an `all`-scope rule attrs and vlessRoute are still enabled and still discarded/refused at save. Closing it means gating against the intersection of ALL subjects when the scope is `all` — a real change to what `allowed` means, and the save refusal already catches the case loudly.
**Test:** Four tests, each verified red-first against the code as it stands. The adversary rejected earlier plans for tests that pass either way, so each entry states the exact failing assertion and the message it prints TODAY.

A. GO, the mask move — internal/web/service (pure; `criteriaMaskFor` and `routing.Plan` touch no DB, and the file must not inherit the package's `PUI_DB_DSN` skip). Plan-level, NOT `slices.Contains(mask, "attrs")` — that version restates the code, which CLAUDE.md forbids:
   subject := routing.Subject{InboundID: 7, Tag: "wg-home", Handle: core.IngressHandle{Device: "pwg7"}, CriteriaMask: criteriaMaskFor(core.IngressHandle{Device: "pwg7"})}
   rule := routing.Rule{ID: 1, Enable: true, Scope: routing.ScopeSelected, IngressIDs: []int{7}, Dest: routing.Dest{Kind: routing.DestBlock}, Criteria: map[string]json.RawMessage{"attrs": json.RawMessage(`{":method":"GET"}`)}}
   got := routing.Plan(routing.Input{Rules: []routing.Rule{rule}, Subjects: []routing.Subject{subject}, Blackhole: "blocked", FrontIDFor: func(int) (int, bool) { return 1, true }})
   want exactly one `got.Refusals()` entry with MessageKey == routing.KeyCriterionUnsupported and Args["criterion"] == "attrs".
   TODAY: "attrs" is in `base`, `forbiddenCriterion` allows it, `compileRule` emits the rule and returns a SeverityOK diag. FAILS with: `got 0 refusals and 2 xray rules; an attrs rule on an L3 subject compiled instead of being refused — the front carries no sniffing, so AttributeMatcher can never return true and this block silently stops blocking`. Assert the refusal COUNT and the key, not just non-empty, so a future mask that refuses everything cannot pass this.

B. GO, the regex guard — internal/xray, plain unit test, no binary and no DB:
   err := ValidateRoutingCriteria(map[string]json.RawMessage{"attrs": json.RawMessage(`{":path":"("}`)})
   want err != nil; and a companion case with `{":path":"^/api"}` wanting err == nil, so the guard cannot be satisfied by rejecting all attrs.
   TODAY: `RouterConfig.Build()` assigns Attributes without compiling them. FAILS with: `ValidateRoutingCriteria accepted an attrs value the core cannot compile: got <nil>, want an error naming ":path"`. `regexp.Compile` is the same RE2 package `app/router/config.go:70` calls `MustCompile` on, so the check is exact, not an approximation.

C. TS, both bridge legs + the round trip — new frontend/src/test/routing-attrs-roundtrip.test.ts beside the existing routing-remark-roundtrip.test.ts, which guards this same class for `remark`. Run with `npm test` (a frontend test; `make test-go` will not see it):
   C1 WRITE: `JSON.parse(expectResolved(ruleToIntent({ type:'field', attrs:{':method':'GET'}, outboundTag:'blocked' }, () => undefined)).criteria).attrs` deep-equals `{':method':'GET'}`. TODAY criteria is the literal string `'{}'`, so the value is `undefined`. FAILS with: `expected undefined to deeply equal { ':method': 'GET' }`.
   C2 READ: for a record whose criteria is `JSON.stringify({attrs:{':method':'GET'}})`, assert `typeof intentToRule(record, () => undefined).attrs === 'object'` FIRST, then the deep equal. TODAY `criteriaToForm` stringifies it. FAILS with: `expected 'string' to be 'object'` — and the explicit typeof assertion is what stops the "[object Object]" regression from ever passing a deep-equal on a coerced value.
   C3 ROUND TRIP: record → `intentToRule` → `ruleToIntent` → parse criteria → deep-equals the original attrs object. Red today on both legs, and it is the case a one-leg fix cannot pass: fixing only the write leg leaves C3 comparing against "[object Object]".

D. TS, the mobile chip — same file or routing-rules-panel.test.ts: `ruleCriteriaChips({ key: 0, attrs: '{"a":"b"}' })` contains a chip with label 'Attrs'. TODAY the function has no attrs branch. FAILS with: `expected [] to include a chip labelled Attrs` (the array is empty for an attrs-only row). This is the assertion that would have caught the original plan's false "one line covers desktop and mobile" claim.

Skipped deliberately: a .tsx render test for `gate('attrs')`/`gate('vlessRoute')`. A and C carry the red-first burden for the behaviour that matters; a render assertion on a disabled prop is weight without evidence.

Procedure per CLAUDE.md: write each, revert its edit, watch it go red with the message above, restore. Gate: `make verify` (npm test included), plus `make test-go` for A and B.
**Sites:**
- internal/web/service/routing.go:87 — `base := []string{"ip", "port", "sourcePort", "network", "source", "attrs"}`; :90-92 `if handle.Device != "" { return base }` — attrs advertised as matchable on a fronted L3 ingress
- internal/web/service/routing.go:64-70 — `routing.Subject{InboundID:..., Tag:..., Handle:..., CriteriaMask:...}`: Inspect is never assigned, so no front is ever sniffed
- internal/egress/drivers/xraytun/xraytun.go:69-108 — `tunInbound`/`tunSettings`/`Inject`: the generated front carries no `sniffing` block, which is what makes attrs unmatchable there
- internal/routing/plan.go:243-246 — `for key, value := range rule.Criteria { body[key] = value }`: every criterion is emitted verbatim, so an unmatchable one is silently inert
- internal/routing/plan.go:155 — `out.Fronts = append(out.Fronts, FrontSpec{ID: frontID, Sniff: subject.Inspect})`: Compiled.Fronts has no production consumer (dead seam)
- internal/routing/plan.go:320-342 — `forbiddenCriterion`: the ONLY thing standing between a carried criterion and a rule that saves and never matches; it consults CriteriaMask alone
- internal/web/service/routing.go:922-943 — `refuseInertRule`: turns a SeverityInert diag into a save refusal, so a mask that tells the truth becomes a loud refusal rather than a silent no-match
- internal/xray/api.go:204-224 — `ValidateRoutingCriteria`: `RouterConfig.Build()` only builds the proto; it never reaches `BuildCondition`'s `regexp.MustCompile`, so `attrs: {":path":"("}` passes save and panics `xray -test`
- frontend/src/schemas/api/routing.ts:117-124 — `criteriaToForm`: `out[key] = Array.isArray(value) ? value.join(',') : String(value ?? '')` turns an attrs object into "[object Object]"
- frontend/src/schemas/api/routing.ts:186-191 — `intentToRule`'s flatten loop `else out[key] = value;` re-emits that 15-character string as `rule.attrs`
- frontend/src/schemas/api/routing.ts:173-175, 243-250 — `LIST_KEYS` plus the `['port','sourcePort','network']` loop: the whitelist that drops attrs and vlessRoute on every save
- frontend/src/schemas/api/routing.ts:126-136 — `criteriaFromForm(form: Record<string, string | undefined>)`: string-only, so an object criterion cannot be written even if carried
- frontend/src/schemas/api/routing.ts:100-109 — `CRITERIA_FIELDS` omits 'attrs' while the Go mask includes it: the third, drifting copy of the vocabulary
- frontend/src/schemas/api/routing.ts:156-171 — `XrayRuleShape` has no `attrs` member, so `shape.attrs` is `unknown` at every read site
- frontend/src/schemas/api/routing.ts:267 — `inspect: false` hardcoded in ruleToIntent (paired with intentToRule never emitting it): the remark bug's exact twin, deferred
- frontend/src/pages/xray/routing/RuleFormModal.tsx:128 — `attrs: rule.attrs ? Object.entries(rule.attrs) : []`: no shape check, so "[object Object]" expands to 15 index/char rows that RuleFormSchema (schemas/xray.ts:119, array of [string,string]) accepts
- frontend/src/pages/xray/routing/RuleFormModal.tsx:164-199 — `submit()` builds `attrs` (:181) and `vlessRoute` (:173) into `built` and copies them into `out`
- frontend/src/pages/xray/routing/RuleFormModal.tsx:246-255 — the vlessRoute FormField: no `gate()`, so the intent editor shows a live-looking input whose value is discarded on save
- frontend/src/pages/xray/routing/RuleFormModal.tsx:269-306 — the Attributes editor (add button + rows): no `gate()` either
- frontend/src/pages/xray/routing/RuleFormModal.tsx:140-154 — the `gate()` helper and the doctrine comment it serves: "disabled with the reason rather than hidden"
- frontend/src/pages/xray/routing/RoutingTab.tsx:563-573 — `subjects={subjects}` passed unconditionally while `exits` is gated on `editingSource`: template rules are gated by an intent-store mask
- frontend/src/pages/xray/routing/RoutingTab.tsx:164-187 — `intentRows` omits attrs (and vlessRoute) while the template path at :95-97 sets `r.attrs = JSON.stringify(rule.attrs, null, 2)`
- frontend/src/pages/xray/routing/helpers.ts:91-103 — `ruleCriteriaChips` lists domain/ip/port/sourceIP/sourcePort/network/protocol/user/vlessRoute and has NO attrs branch: attrs is invisible on mobile for both stores
- frontend/src/pages/xray/routing/useRoutingColumns.tsx:172 — `{record.attrs && <CriterionRow label="Attrs" .../>}`: the desktop renderer already exists and is fed nothing on intent rows
- frontend/src/pages/xray/routing/types.ts:34 — `attrs?: string` on RuleRow: the row field both list renderers share

**Re-check notes:**
- NON-BLOCKING, and it is in the deferred narrative rather than in anything that ships: the claim 'Compiled.Fronts has NO production consumer' (deferred item 2, and repeated in the sites entry for plan.go:155) is false. internal/web/service/routing.go:308 and :312 iterate compiled.Fronts and inject each front as an inbound. What is actually dead is FrontSpec.Sniff -- written at plan.go:155, never read by the injector -- and routing.Subject.Inspect, declared at plan.go:45 and never assigned anywhere. The conclusion ('nothing turns a front's sniffing on', so blanking the inspect column is inert today) is correct and the deferral still holds, but the wording would send whoever picks up that item hunting for a consumer that already exists; the real work there is threading Sniff into the existing injection, not building a consumer.
- KNOWN-LIVE SIBLINGS, all disclosed in deferred, none blocking: the `inspect` blanking bug (routing.ts:267 hardcodes inspect:false while intentToRule never emits it) is the exact same round-trip-drop as remark and attrs and stays live after this commit. Verified that it is inert on the wire -- Subject.Inspect is never assigned, so no front is ever sniffed -- but it IS persisted and served back (routing.go:563), so an API-authored inspect=true still silently reverts to false on any UI edit. Correctly deferred because carrying it through only matters once item 2 wires the seam. Likewise webhook, localIP, localPort, process, domains and ruleTag remain droppable, and webhook is object-shaped so it also hits the '[object Object]' mangle that the attrs-specific `if (key === 'attrs') continue` guard does not cover -- named in deferred item 4.
- CONSEQUENCE TO EXPECT, not a smuggled choice: after edit 1, a scope=all attrs rule on an install that has any L3 inbound becomes a whole-save refusal rather than a rule that saves and is inert on the L3 subject. That is refuseInertRule's already-shipped semantics -- forbiddenCriterion is per-subject and groupRules expands scope=all across every subject, so `domain` and `user` behave identically today -- so it introduces no new policy, but it is the one operator-visible edge worth mentioning in the commit body.


### F189 — ship-it (small)
**Root:** A merged-list POSITION and a per-store INDEX are both bare numbers in RoutingTab.tsx, and three readers mix them. `mergedRows = [...intentRows, ...templateRows]` (:189-195) is one table over two independently-ordered stores; an intent row's `storeIndex` indexes `intentRecords`, a template row's indexes `templateSettings.routing.rules`. (1) `moveUp` (:359) takes the neighbour as a bare `rowAt(idx-1)?.storeIndex` and uses it as an index into `tt.routing.rules`; on the first template row that number is an intentRecords index. The guard at :363 is existence-only, so the swap proceeds. With 2 operator rules + 2 template rules, "Move up" on the first template row swaps template rules 0 and 1 — reordering two first-match rules the operator never touched. (2) `moveDown` (:372) is the same shape, unreachable today. (3) VERIFIED ADVERSARY CLAIM — the drag path is NOT clear. `onRow` returns `'data-row-key': i` (:549, the merged position), but rc-table overwrites it: `_extends({}, rowProps, {"data-row-key": rowKey, ...})` in node_modules/@rc-component/table/es/Body/BodyRow.js:124-125, where `rowProps = onRow?.(record, recordIndex)` (es/hooks/useRowInfo.js:30) and `rowKey` comes from `rowKey={(r) => r.key}`. Object.assign order means the library wins, so the DOM carries `r.key` — 10000+i for intent rows, the ORIGINAL rules index for template rows. `onMove` (:416) reads that as a merged position. Consequences with N intent rows present: intent→intent drag resolves `mergedRows[10000+q]` → undefined → dead (the feature never works); intent→template drag reorders operator rules to an arbitrary position; template→template drag lands at position k-N instead of k, a silent misorder. The boundary refusal at :438 is real but mis-fires because `to` is in the wrong index space. Root is shared: nothing distinguishes the two number kinds, and :549 being dead code is what hid it.
**Ships now:** One commit, 2 files, 7 line-level edits. No behaviour choice, no new helper/prop/state/file, no Go, no migration, no i18n key.

A. Cross-store guard on the button path (RoutingTab.tsx). In `moveUp`, replace `const prev = rowAt(idx - 1)?.storeIndex;` (:359) with a row-level refusal placed ABOVE the `row?.source === 'intent'` branch (i.e. right after `const row = rowAt(idx);` at :356):
  `const prevRow = rowAt(idx - 1);`
  `if (!row || !prevRow || row.source !== prevRow.source) return;`
then `const prev = prevRow.storeIndex;` with the existing `if (prev == null) return;` kept. Mirror in `moveDown` against `rowAt(idx + 1)` (:372). This is the guard the drag path already declares at :438; it simply never reached the button path.

B. Bring the drag target back into the merged index space (RoutingTab.tsx + RuleCardList.tsx). Stop reading an attribute the table library owns:
  - :549 — replace the dead `'data-row-key': i` with `'data-merged-index': i`. rc-table overwrites `data-row-key` and cannot overwrite this one.
  - :414 — `const target = el.closest('[data-merged-index]');`
  - :416 — `const newIdx = Number(target.getAttribute('data-merged-index'));` (the existing `Number.isFinite(newIdx)` check at :417 still holds; a missing attribute yields NaN, and `closest` returning null already returns early).
  - RuleCardList.tsx:58 — `data-row-key={index}` becomes `data-merged-index={index}`. Mobile already emits the positional index, so this is a rename, not a semantic change.

DELIBERATE AMENDMENT TO THE ADVERSARY'S CORRECTION: it proposed `mergedRowsRef.current.findIndex((r) => r.key === Number(el.getAttribute('data-row-key')))`. That is correct for desktop and WRONG for mobile — RuleCardList.tsx:58 emits the positional index, and matching it against `r.key` (10000+i for intent, original-rules index for template) resolves card 0 to merged position 2 whenever operator rules exist. Both approaches need the RuleCardList edit; a dedicated attribute is preferred because only this component writes it, it does not depend on rc-table's rowKey identity or on `r.key` uniqueness, and its failure mode is fail-safe (no match → no drop target). Grep confirms `data-row-key` has no other reader in frontend/src — no CSS, no test, no story.

The moveDown guard is untested symmetry: the input is unreachable today (the last intent row's moveDown already dead-ends in `moveIntent`'s bounds check at :211), and CLAUDE.md forbids a test for an input the function cannot receive.
**Deferred:** 1. OutboundCardList exit-row misdirection (LIVE, SEPARATE, needs the decision below). frontend/src/pages/xray/outbounds/OutboundCardList.tsx:139/141/143 calls `setFirst(index)`, `openEdit(index)`, `confirmDelete(index)` over the MERGED `tableRows` (OutboundsTab.tsx:187, 621). Unlike the desktop columns, which branch on `record.egress` and route exit rows to editExit/toggleExit/deleteExit (useOutboundColumns.tsx:86-105), the card list has no such branch and is not even given those props (OutboundCardList.tsx:35-44). `originalOutboundIndex` (outbounds-tab-helpers.ts:19-22) falls back to the positional index when `rows[idx]` is undefined, which every exit row is. Concretely: outbounds `[loopback, a, b]` + 1 exit → the exit card is index 2 → deleting it deletes outbound `b`. Excluded because the correct repair adds three props across two files and turns on a behaviour question.
2. `row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx)` at RoutingTab.tsx :296, :340, :358, :371, :389, :446, :447. The fallback resolves a MERGED index against the template-only `rows` list. Unreachable while `mergedRows` sets `storeIndex` on every row, so it is a dormant landmine, not a bug. Do NOT touch it here — removing it is a separate refactor.
3. Greying out the two boundary menu items (threading a boundary index into useRoutingColumns + RuleCardList). Cosmetic, two more files, and a silent refusal is exactly what the drag path at :438 already does.
CLEARED, with evidence: useOutboundColumns.tsx:86-105 (desktop egress branch is correct); HostsPage.tsx:70-78 `onMove` (finds its index by identity over a single store).
**Test:** One new file, frontend/src/test/routing-move-boundary.test.tsx (components project, jsdom). Both cases drive the real code path and are red without the fix.

SHARED SETUP. `templateSettings.routing.rules = [{type:'field',outboundTag:'t0',enabled:true},{type:'field',outboundTag:'t1',enabled:true}]` — no balancer-loopback rule, so template row keys are 0 and 1. `vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(url.includes('/routing/rules') ? new Msg(true,'',[REC(1),REC(2)]) : new Msg(true,'',[])))` where `REC = (id) => ({id, sortIndex:id, enable:true, remark:`r${id}`, ingressScope:'all', ingressIds:[], destKind:'outbound', destTag:'direct', destExitId:null, criteria:{}, inspect:false})` — schema-valid against RoutingRuleListSchema (frontend/src/schemas/api/routing.ts:18-35), which matters because `parseMsg` is non-strict and `intentToRule` reads the fields. TWO records are load-bearing: with one, `prev` and `target` both resolve to 0 and the buggy swap is a self-swap that hides the defect. Render `<RoutingTab isMobile={false} setTemplateSettings={spy} …/>` via `renderWithProviders`, then flush react-query as outbound-exits-section.test.tsx does: `for (let i=0;i<6;i+=1) await act(async () => { await new Promise(r => setTimeout(r,0)); });`. Without the flush `intentRecords` is still `[]` and there is no third row. Then `fireEvent.click(screen.getByRole('tab', { name: /Routing Rules/ }))` and flush once more. Assert `document.querySelectorAll('.routing-table .ant-table-tbody tr.ant-table-row').length === 4` before proceeding — mergedRows is [I1, I2, T0, T1]. `HttpUtil.post` is already spied globally in setup.components.ts and resolves `{success:true}`; do not call `vi.restoreAllMocks()`, which would strip it.

TEST 1 — moveUp guard (the primary proof). Snapshot `const before = setTemplateSettings.mock.calls.length;`. Open the third row's dropdown: `fireEvent.click(rows[2].querySelectorAll('.action-buttons button')[1])` (the `aria-label="more"` button; `.action-buttons` exists because isMobile=false), flush one act tick — the antd menu renders into a portal — then `fireEvent.click(screen.getByText('Move up'))` (en-US `pages.inbounds.form.moveUp` === "Move up"). Assert `expect(setTemplateSettings.mock.calls.length).toBe(before)`. WITHOUT the fix: row 2 is template (target=0), `prev = mergedRows[1].storeIndex = 1` (an intentRecords index), both `list[0]` and `list[1]` exist, the swap runs and `mutate` calls the setter once — the assertion fails with "expected 1 to be 0" (or before+1 vs before). WITH the fix, `prevRow.source === 'intent' !== 'template'` returns before `mutate`. Strengthen it by also running the updater when it IS called and asserting `rules[0].outboundTag === 't0'`, so the failure names the actual harm. NOTE: the repo has no precedent for clicking an antd Dropdown item (grep for `ant-dropdown-menu-item` in frontend/src/test returns nothing) — budget the act tick after opening the menu.

TEST 2 — drag index space. jsdom has no layout, so assign `document.elementFromPoint = () => tr` where `tr = rows[1]` (the second INTENT row's rendered `<tr>`; the handler calls `el.closest(...)` on it). The repo has no pointer-event test either, and jsdom's PointerEvent support is not guaranteed, so dispatch manually rather than through `fireEvent.pointerDown`: `handle.dispatchEvent(new MouseEvent('pointerdown', {bubbles:true, clientX:0, clientY:0}))` on `rows[0].querySelector('.drag-handle')` (React 19 dispatches onPointerDown off the root listener by event type; `pointerId` is undefined so `setPointerCapture` throws into the handler's existing try/catch), then `document.dispatchEvent(new MouseEvent('pointermove', {bubbles:true, clientX:0, clientY:40}))` — 40 clears the 5px threshold at :410 — then `document.dispatchEvent(new MouseEvent('pointerup', {bubbles:true}))`. Assert `expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/routing/rules/order', {ids:[2,1]}, expect.anything())`. WITHOUT the fix the `<tr>` carries rc-table's `data-row-key="10001"`, `mergedRows[10001]` is undefined, `toRow` is falsy and :438 returns — the assertion fails with "expected spy to be called with arguments … Number of calls: 0". WITH the fix `data-merged-index="1"` resolves merged position 1 and the intent branch posts the swapped order. Do NOT fall back to merely asserting the attribute is present: that is green for the wrong reason and never drives the drag path.

NO moveDown test — its cross-boundary input is unreachable today.
**Sites:**
- frontend/src/pages/xray/routing/RoutingTab.tsx:356-366 — moveUp; :359 `const prev = rowAt(idx - 1)?.storeIndex;` takes the neighbour's per-store index with no check of which store it indexes
- frontend/src/pages/xray/routing/RoutingTab.tsx:363-364 — `if (!list || !list[target] || !list[prev]) return; [list[prev], list[target]] = [list[target], list[prev]];` — existence-only guard, so a cross-store index passes
- frontend/src/pages/xray/routing/RoutingTab.tsx:367-379 — moveDown's mirror; :372 `const next = rowAt(idx + 1)?.storeIndex;`. Unreachable today (template rows are last), fixed for symmetry
- frontend/src/pages/xray/routing/RoutingTab.tsx:549 — `return { className: classes.join(' '), 'data-row-key': i }` — dead: rc-table overwrites it with the row KEY (BodyRow.js:124-125). This is what hid the drag bug
- frontend/src/pages/xray/routing/RoutingTab.tsx:414-417 — `el.closest('[data-row-key]')` + `Number(target.getAttribute('data-row-key'))` — reads a row key as a merged position
- frontend/src/pages/xray/routing/RoutingTab.tsx:438 — `if (!fromRow || !toRow || fromRow.source !== toRow.source) return;` — the intended semantics, mis-firing because `to` is in the wrong index space
- frontend/src/pages/xray/routing/RoutingTab.tsx:189-195 — `mergedRows = [...intentRows, ...rows.map(r => ({...r, source: 'template', storeIndex: r.key}))]` — the two-store merge that makes the two number kinds collide
- frontend/src/pages/xray/routing/RuleCardList.tsx:58 — `data-row-key={index}` — mobile emits the POSITIONAL index under the same attribute name, which is why matching by row key would break mobile
- frontend/src/pages/xray/routing/RoutingTab.tsx:296, :340, :358, :371, :389, :446, :447 — `row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx)` — dormant landmine, deferred
- frontend/src/pages/xray/outbounds/OutboundCardList.tsx:139, :141, :143 — `setFirst(index)` / `openEdit(index)` / `confirmDelete(index)` on merged tableRows with no `record.egress` branch — deferred sibling
- frontend/src/pages/xray/outbounds/OutboundsTab.tsx:187, :231-232, :268-269, :282-283, :621 — `tableRows = [...rows, ...exitRows]` handed to the card list while the handlers address `rows` by position
- frontend/src/pages/xray/outbounds/outbounds-tab-helpers.ts:19-22 — `originalOutboundIndex` returns the positional index when the row is absent, which turns an exit row into an outbound index
- frontend/src/pages/xray/outbounds/useOutboundColumns.tsx:86-105 — CLEARED: desktop branches on `record.egress` into editExit/toggleExit/deleteExit
- frontend/src/pages/hosts/HostsPage.tsx:70-78 — CLEARED: `onMove` finds its index by identity over a single store

**Re-check notes:**
- DISCLOSED SIBLING, STILL LIVE: OutboundCardList.tsx:139/141/143 has the same merged-index-vs-store-index defect and is destructive on mobile. Verified concretely: OutboundsTab.tsx:187 hands `tableRows = [...rows, ...exitRows]` to the card list, while confirmDelete (OutboundsTab.tsx:268-269) resolves through originalOutboundIndex (outbounds-tab-helpers.ts:19-22), which returns the POSITIONAL index when rows[idx] is undefined - true of every exit row. With outbounds [loopback, a, b] + 1 exit, deleting the exit card deletes outbound `b`. The deferral is legitimate (three new props across two files plus a decision about what an exit card's mobile menu offers), but it is the same defect class and arguably more severe than what ships here, so it must be tracked, not forgotten. Desktop is genuinely clear: useOutboundColumns.tsx:86-105 branches on record.egress.
- TEST-AUTHORING RISK (not a soundness risk): the repo has zero precedent for clicking an antd Dropdown item or dispatching pointer events - greps for `ant-dropdown`, `menuitem` and `pointerDown/pointerdown` in frontend/src/test all return nothing. The synthetic `new MouseEvent('pointerdown')` reaching React 19's onPointerDown, and getByText('Move up') resolving to exactly one node, may need iteration. This cannot produce a false green: if the gesture fails to drive the UI, both tests fail in BOTH states (fixed and unfixed), so the worst case is rework, never false certification. Note getByText uses direct-child-text-node matching, so the `.ant-dropdown-menu-title-content` span is the single match; getByRole('menuitem', {name: /Move up/}) is the safer form if it misbehaves.
- COVERAGE CAVEAT: the existing frontend/src/test/routing-drag-reorder.test.ts is a MIRROR test - it re-declares `resolveDrag` locally (lines 25-30) rather than driving RoutingTab, so it certifies the :438 semantics while the real drag path was feeding that guard a garbage `to`. It stays green with or without this fix and must not be cited as coverage for the drag path. It does corroborate answer 2: the boundary semantics were already decided and already tested, so part A smuggles no new choice.


### F216 — ship-it (small)
**Root:** The adversary is RIGHT on all three counts; I verified each against the tree.

(1) REGRESSION — CONFIRMED. The bridge is loopback + TCP-only: `Listen: "127.0.0.1"`, `Settings: {"auth":"noauth","udp":false}` (xray.go:750-756, mtprotoEgressSocksSettings :691). A bare `entry.Port == parsed.RouteXrayPort` predicate therefore skips configurations that bind cleanly today. The concrete case: `mtproto.FreeLocalPort` (manager.go:577-585) binds `tcp 127.0.0.1:0`, so it hands out an ephemeral port from Linux's 32768-60999 range — which contains 51820. A WireGuard inbound on 0.0.0.0:51820 is UDP-only (`transports.go:26-28` forces `{UDP:true}` for hysteria/wireguard), `checkPortConflict` accepts that row (`shared := existingBits & newBits` is 0, port_conflict.go:233-237), and the OS accepts both binds. Bare equality kills that working bridge. Same for a disjoint listen: `GenXrayInboundConfig` (model.go:457-462) writes the operator's own `Listen`, so an inbound on "203.0.113.5":N and a bridge on 127.0.0.1:N coexist today.

(2) ONE OF A PAIR — CONFIRMED. `checkPortConflict` (port_conflict.go:194-248) reads `inbounds.port`, `reservedAPIPort()`, the web port and the sub port. It never reads `routeXrayPort` in either direction, and `normalizeMtprotoXrayPort` (inbound.go:888-897) will take a client-supplied port verbatim when no port is stored. I also verified the swallow first-hand: the failed hot-add is `logger.Debug("Unable to add inbound on", ...)` + `needRestart = true` (inbound.go:1095-1096).

(3) ASYMMETRIC VICTIM — CONFIRMED, and the consequence is worse than the original plan claimed. `GetXrayConfig` appends every rendered inbound (xray.go:142-151) before the mtproto injection loop (:168-174), so a bridge that has served for months always loses to any inbound rendered today. And a skipped bridge does NOT degrade to the default route: `renderConfig` emits `[network] proxies = ["socks5://127.0.0.1:N"]` from the persisted `XrayRoutePort` whenever routing is on (manager.go:630-633), independent of whether the panel injected anything. mtg then speaks SOCKS5 at a foreign listener and every Telegram connection through that inbound fails.

The actual root cause is unchanged and still real: `routeXrayPort` is a real loopback TCP listener port that no layer checks. At generation time `injectMtprotoEgress` appends it verbatim, alone among the three injectors in the file (`injectPanelEgress` :539-549 and `injectNodeEgresses` :599-642 both build a `used` set and bump). Today's outcome on a true collision is not a misroute — it is a hard outage: `xray -test` binds nothing (port_conflict.go:141-143), so the pre-flight passes, then the real start hits EADDRINUSE on the duplicate bind and the WHOLE core dies into a restart loop. What the original plan got wrong was the predicate (too coarse) and the framing of the consequence (a skipped bridge is a dead inbound, not a degraded one).
**Ships now:** Give injectMtprotoEgress a port guard that uses the PACKAGE'S OWN conflict semantics, not bare port equality, folded into the existing tag loop at internal/web/service/xray.go:711-717 so it fires before the routing mutation at :719-748 and leaves no orphan rule:

	tag := inbound.Tag
	for i := range cfg.InboundConfigs {
		entry := &cfg.InboundConfigs[i]
		if entry.Tag == tag {
			logger.Warning("mtproto egress: inbound tag [", tag, "] already present in generated config, skipping bridge")
			return
		}
		if entry.Port != parsed.RouteXrayPort {
			continue
		}
		var listen string
		_ = json.Unmarshal(entry.Listen, &listen)
		if !listenOverlaps(listen, "127.0.0.1") {
			continue
		}
		if inboundTransports(model.Protocol(entry.Protocol), string(entry.StreamSettings), string(entry.Settings))&transportTCP == 0 {
			continue
		}
		// The sidecar dials this exact port, so bumping it the way the panel and
		// node bridges do would only make it undialable. F216.
		logger.Warning("mtproto egress: bridge port", parsed.RouteXrayPort, "for inbound [", tag,
			"] is already bound by [", entry.Tag, "]; skipping the bridge - this inbound's Telegram traffic will fail until the port conflict is resolved")
		return
	}

All three conjuncts are the ones checkPortConflict already applies, with the bridge's side fixed as (127.0.0.1, TCP): port equality, listen overlap, shared TCP bit. An unparsable/absent Listen unmarshals to "" -> isAnyListen -> overlaps, which is the conservative default. Both helpers (listenOverlaps, inboundTransports) already live in the same package and are DB-free. `json`, `model` and `logger` are already imported in xray.go.

Why this is an undisputed repair: on a true collision today, xray -test passes (it binds nothing) and the real start hits EADDRINUSE on the duplicate bind, taking the ENTIRE core down into a restart loop. After the guard the core stays up and exactly one inbound loses its egress. No operator prefers losing every inbound to losing one. And the tightened predicate means the WireGuard-on-the-same-port and disjoint-listen configurations that work today keep working, which the rejected bare predicate would have broken.

~14 lines, one 2-line comment. No new state, no schema change, no route, no i18n key, no migration, no new helper.
**Deferred:** TWO things, deliberately out.

1. THE SAVE-TIME HALF (the pair's other side). checkPortConflict (port_conflict.go:194-248) still never reads routeXrayPort in either direction, so both paths that create the collision stay open: (a) an operator creates a normal inbound on a port an existing bridge holds — accepted, hot-add fails, swallowed to Debug + needRestart (inbound.go:1095-1096); (b) an API caller sets routeXrayPort to an existing inbound's port — normalizeMtprotoXrayPort (inbound.go:888-897) takes it verbatim. The generation guard DOWNGRADES both from "core down" to "one inbound dark"; it does not close them. Closing them means teaching checkPortConflict to scan mtproto rows' settings for routeXrayPort and to compare against the bridge's fixed (127.0.0.1, TCP) side in both directions — a new query, a JSON parse per mtproto row, and a new refusal that starts erroring on saves the panel accepts today. That is a behaviour choice, and it is Decision 1. It needs no i18n key (portConflictDetail.String is plain Go, surfaced via common.NewError at inbound.go:958/1420).

2. THE LISTENERS THE INJECTOR STRUCTURALLY CANNOT SEE. The guard scans cfg.InboundConfigs, so it is blind to every real listener that is not in there: another mtproto row's own mtg bind port (RenderInbound returns nil for MTProto, so no mtproto row is ever in InboundConfigs), the panel web port and the subscription port (panelListenerConflict knows them; the injector does not). A bridge landing on any of those still produces a live bind conflict that this fix does not see. Pulling that in would drag SettingService and a DB read into what is today a pure function over a config struct — it belongs with item 1, in checkPortConflict, where those ports are already known.

NOT deferred, decided: skip-and-warn rather than refusing the whole config. applyRoutingPlan (xray.go:205-207, F055) refuses generation outright, but refusing here converts a one-inbound failure back into a whole-box outage on a cold boot, where the running core is not there to keep serving. Skip is the only defensible generation-time behaviour and needs no ruling.
**Test:** Pure unit tests, no DB, no env gate — injectMtprotoEgress is a free function over (*xray.Config, *model.Inbound) and inboundTransports is DB-free (proven by TestInboundTransports, port_conflict_test.go:50, which uses no fixture). Add to internal/web/service/xray_config_inject_test.go beside TestInjectMtprotoEgress_TagCollisionSkips (:507-518), which pairs port 443 with bridge port 50003 and so never exercises this. All four cases reuse egressTestConfig() (:505-513: one entry, the api inbound Port 62789 / Protocol "tunnel" / Listen "127.0.0.1") and mtprotoInbound() (:423-425).

RED WITHOUT THE FIX — case A, TestInjectMtprotoEgress_LoopbackTCPCollisionSkips:
  cfg.InboundConfigs = append(cfg.InboundConfigs, xray.InboundConfig{Port: 50007, Protocol: "vless", Tag: "in-50007-tcp", Listen: json_util.RawMessage(`"0.0.0.0"`), StreamSettings: json_util.RawMessage(`{"network":"tcp"}`)})
  before := string(cfg.RouterConfig)
  injectMtprotoEgress(cfg, mtprotoInbound("mt-1", `{"routeThroughXray":true,"routeXrayPort":50007,"outboundTag":"warp"}`))
  assert len(cfg.InboundConfigs) == 2 && string(cfg.RouterConfig) == before
The mtproto tag "mt-1" deliberately differs from "in-50007-tcp" so the existing tag guard cannot mask the port guard. Today the tag loop finds no match, "warp" resolves in OutboundConfigs, the rule is prepended and the bridge appended: the test fails at the first assertion with `t.Fatalf("a loopback TCP inbound already holding the bridge port must make the bridge a no-op, got %d inbounds", len(cfg.InboundConfigs))` printing "got 3 inbounds" (two of them on 50007: vless 0.0.0.0 and socks 127.0.0.1), and the RouterConfig assertion also fails because a rule was prepended.

RED WITHOUT THE FIX — case B, the reserved api port, same test, second subcase:
  injectMtprotoEgress(egressTestConfig(), mtprotoInbound("mt-2", `{"routeThroughXray":true,"routeXrayPort":62789,"outboundTag":"warp"}`))
  assert len(cfg.InboundConfigs) == 1
Today yields 2 (api tunnel 127.0.0.1:62789 + socks bridge 127.0.0.1:62789) and fails with "got 2 inbounds". This is the case where the collision is guaranteed and unconditional, since 62789 is in every template.

REGRESSION GUARDS — green today, green after the fix, RED under the REJECTED bare-equality predicate. These are what distinguish this plan from the rejected one, so they must be in the same commit:
  C) UDP-only neighbour: append xray.InboundConfig{Port: 51820, Protocol: "wireguard", Tag: "in-51820-udp", Listen: `"0.0.0.0"`}; inject routeXrayPort 51820 -> the bridge MUST still be appended (len == 3), because transports.go:26-28 answers {UDP:true} and the bridge is TCP. Fatal message: "a UDP-only inbound does not conflict with the loopback TCP bridge; the bridge must still be injected".
  D) Disjoint listen: append xray.InboundConfig{Port: 50009, Protocol: "vless", Tag: "in-50009-tcp", Listen: `"203.0.113.5"`}; inject routeXrayPort 50009 -> the bridge MUST still be appended.

Verify by reverting the guard and watching A and B go red, then by substituting the bare `entry.Port == parsed.RouteXrayPort` predicate and watching C and D go red. Neither pair passes both ways.
**Sites:**
- internal/web/service/xray.go:711-717 — the only guard in injectMtprotoEgress, tag-only; where the port conjunct belongs (before the routing mutation at :719-748)
- internal/web/service/xray.go:750-756 — the unguarded append: Listen "127.0.0.1", Port: parsed.RouteXrayPort, Protocol socks, Settings mtprotoEgressSocksSettings
- internal/web/service/xray.go:142-151 and :168-174 — GetXrayConfig appends every rendered inbound BEFORE the mtproto injection loop; this ordering is why the pre-existing bridge always loses
- internal/web/service/xray.go:539-549, :551-557 — panel-egress sibling that builds a `used` set and bumps
- internal/web/service/xray.go:599-604, :635-650 — node-egress sibling that builds a `used` set and bumps
- internal/web/service/port_conflict.go:32-52 — inboundTransports, the existing DB-free transport authority to reuse (TestInboundTransports at port_conflict_test.go:50 runs with no DB)
- internal/web/service/port_conflict.go:54-63 — listenOverlaps / isAnyListen, the existing listen semantics to reuse
- internal/web/service/port_conflict.go:194-248 — checkPortConflict, the save-time half; queries only inbounds.port + reservedAPIPort + web/sub ports, never routeXrayPort (DEFERRED)
- internal/web/service/inbound.go:888-897 — normalizeMtprotoXrayPort takes a client-supplied routeXrayPort when none is stored (API ingress for an arbitrary value)
- internal/web/service/inbound.go:1095-1096 — the hot-add EADDRINUSE swallow: logger.Debug + needRestart = true
- internal/mtproto/manager.go:577-585 — FreeLocalPort binds TCP only on 127.0.0.1:0, so it can legally return a port a UDP inbound already holds
- internal/mtproto/manager.go:630-633 — renderConfig emits socks5://127.0.0.1:N from the persisted port regardless of injection; this is why a bumped OR skipped bridge breaks mtg outright
- internal/database/model/model.go:457-473 — GenXrayInboundConfig writes the operator's Listen (default "0.0.0.0") quoted into InboundConfig.Listen
- internal/cores/internal/xray/transports.go:22-78 — hysteria/wireguard forced UDP; the regression case the bare predicate breaks
- internal/web/service/routing.go:312-331 + internal/egress/drivers/xraytun/xraytun.go:107-113 — NOT a sibling: the xray-tun front is injected with Port: 0, so it can never collide
- internal/cores/internal/xray/core.go — Reconcile/withInbound splice by tag only, never by port; NOT siblings

**Re-check notes:**
- NON-BLOCKING prose slip in the plan's test section: egressTestConfig() is at internal/web/service/xray_config_inject_test.go:130-138, not ':505-513' (the :505-518 range is TestInjectMtprotoEgress_TagCollisionSkips, which the plan cites correctly by name). No effect on the repair.
- NON-BLOCKING snippet slip: the case-B line `injectMtprotoEgress(egressTestConfig(), ...)` passes the config inline and then asserts on `cfg`; the test author must bind it to a variable first. Mechanical, not a behaviour question.
- NOTE, not an objection: the guard only changes behaviour in configs that cannot start today. Its three conjuncts are exactly the Linux bind-conflict condition (verified: '::' dual-stack is caught via isAnyListen, and xray inbounds set no SO_REUSEPORT), so no currently-working deployment can be turned dark by it.


### F217 — ship-with-the-noted-correction (small)
**Root:** VERIFIED: all four adversary objections are correct, and the finding is worse than either plan said.

`resetAllMtprotoQuotas` (internal/web/service/inbound_mtproto.go:132-151) is the ONLY non-core caller of an engine manager's destructive `Reconcile` in the tree (re-grepped `\.Reconcile(` across non-test Go under internal/ — every other one is `bound.Supervise.Reconcile` or a core adapter's own). `Manager.Reconcile(desired, keep...)` treats absence from `want` as "stop the process and delete its config" (manager.go:432-437) and this call site passes no `keep`. Its desired state comes from a second, weaker builder, `DesiredMtprotoInstances`, which collapses `mtproto.InstanceFromInbound`'s two very different falses into one `continue` (:41-44): "nothing left to serve" (correctly a stop) and "stored settings will not typed-parse" (which the core adapter spends a `keep` list and a comment to NOT treat as a stop, cores/internal/mtproto/core.go:108-113).

Reachable: `InstanceFromInbound`/`ApplySettings` unmarshal into the typed `instanceSettings` (manager.go:187-190, 255-264), while every save-path healer is `map[string]any` and tolerant (model.go:804-812 `HealMtprotoClientSecrets`), and no strict validator guards mtproto settings on add/update. A REST payload carrying `"throttleMaxConnections":"100"` persists intact and then fails the typed parse.

NEW, and the reason this outranks the original write-up: the stop is PERMANENT, not a 10s blip. The supervise path (job/core_supervise.go:54 → core.go:101-118) puts an unparseable inbound in `keep` — which prevents stopping but never starts — and `want` never contains it. So once a routine traffic reset stops that sidecar, nothing restarts it, not the next `@every 10s` tick (web.go:322), not a panel restart, until someone hand-fixes the settings. An entire MTProto inbound and every client on it stays dark.

The call site also violates the repo's own stated invariant one file over: `DesiredInstances` fails the whole set rather than omitting one inbound, "omitted reads as 'stop it', and a transient error must not do that" (inbound_core.go:40-44).

Adversary claim 2 also stands and changes the fix: mtglib never prunes `s.users` (proxy_stats.go only ever `getOrCreate`s; usage even survives restarts via LoadUsage/FlushUsage), and `handleResetQuota` 404s while the secret is absent (proxy_api.go:329-341), which the panel swallows as success (manager.go:569). So converge-then-reset is load-bearing: re-adding a drifted secret is what makes the following ResetQuota land instead of silently 404ing. The original plan's "just delete it" therefore traded away something real — and did not have to.
**Ships now:** Replace the destructive half of the call with the non-destructive half of the same manager API, in `resetAllMtprotoQuotas`. Delete inbound_mtproto.go:141-143 and put in its place, above the untouched reset loop:

	for _, inst := range desired {
		// Ensure, never Reconcile: absence from this set can mean settings that
		// merely will not parse, and a reset must not stop a live sidecar. F217.
		if err := mgr.Ensure(inst); err != nil {
			logger.Debug("mtproto: converge before quota reset failed for inbound", inst.Id, ":", err)
		}
	}

Why this and not the deletion the first plan proposed: `Reconcile(desired)` is exactly `ensureLocked` per desired instance PLUS the stop-the-absent loop. An `Ensure` loop is `ensureLocked` per desired instance and nothing else — a strict behavioural SUBSET of the call it replaces. It removes only the destructive branch and keeps the converge-then-reset the adversary proved load-bearing. Converge-all-then-reset-all preserves the current ordering exactly. No operator prefers "a traffic reset permanently kills an MTProto inbound whose settings will not parse", so there is no behaviour choice here.

Owning the flip the first plan's own argument would raise: the objection to a second convergence path applies to DESTRUCTIVE authority. Under `Ensure` the second builder's failure mode is fail-safe — an inbound it cannot parse is simply absent and nothing happens to it; under `Reconcile` absence killed the process and deleted its config. This demotes the builder from stop-authority to add/refresh-authority; adversary finding 2 is why the authority cannot go to zero. The direct `mgr.Ensure` follows the file's existing direct-manager convention (:92, :133) rather than adding a new layering violation. `applyLocalMtproto`-per-inbound (the runtime-dispatched sibling used by `resetMtprotoClientQuota`) was deliberately rejected for now: its core path has a `!serve → mgr.Remove` branch (core.go:180-182), so it would reintroduce a stop path on a racing DB write — exactly the class of bug being removed.

Same commit, second edit: delete the trailing sentence of `DesiredMtprotoInstances`' doc comment, inbound_mtproto.go:18-20 — "Inbounds whose every secret is filtered away are omitted, so a caller reconciling this set stops their sidecar." After this change no caller reconciles that set, and that sentence is a standing invitation to add one back. Replace it with nothing, or at most: "Inbounds with nothing to serve are omitted; so are inbounds whose settings will not parse, so absence here must never be read as a stop."

Net: 3 lines deleted, 6 added (2 of them one comment block), 1 doc sentence removed. No new import (`logger` still used at :147, `context` at :114), no i18n key, no route, no openapigen struct, no column, no migration.
**Deferred:** 1) The `InstanceFromInbound` contract change. It still returns one `false` for both "will not parse" and "nothing to serve", so `DesiredMtprotoInstances` (:41-44) and `localMtprotoInboundIdForEmail` (:161-165) still silently skip an unparseable inbound. After the fix that is no longer a process kill — it is only a quota reset that quietly does not happen, and a warning nobody sees. Fixing it properly means a third return value on a function with four call sites; that is a contract change, not a one-line guard, and it is not what removes today's outage.

2) The reset-before-re-enable ordering family — a genuinely separate finding, verified, deliberately NOT bundled. Every reset path zeroes the sidecar counter before the client is re-enabled: `resetAllClientTrafficsLocked` updates only `up`/`down` (client_traffic.go:283-286) and `ResetAllTraffics` likewise, then `resetAllMtprotoQuotas()` runs, then `reEnableRecovered` → `BulkSetEnable` (client_traffic.go:344-346, 79-108). At reset time `depletedEmails` still names the client, so it is filtered out of `desired` and its email never reaches `ResetQuota` at all; when `BulkSetEnable` re-adds the secret, mtglib's retained `quotaUsed` — never pruned, and persisted across restarts — blocks the renewed client indefinitely. `ClientService.ResetClientTraffics` has the same shape from the other direction (client_traffic.go:204,226: `enable=true` inside the transaction, then `resetCoreQuotas` before the sidecar has the secret back → 404 swallowed as success). This needs a choice about where the reset belongs, which is why it is not in shippableNow.

3) Consolidating this path onto the core seam (`applyLocalMtproto` / `bound.Quota`) so the service layer stops grabbing `mtproto.GetManager()` at :92 and :133. Correct direction, but it is architecture next to a fix, and its `!serve → Remove` branch needs its own reasoning about racing DB reads.
**Test:** One Postgres-gated Go test in `internal/web/service` (new file `inbound_mtproto_reset_test.go`); `initTestDB` skips without `PUI_DB_DSN` (testdb_test.go:26-27). No `t.Parallel` (installFakeMtg uses `t.Setenv`). No `runtime.SetManager` needed — the path is DB plus the direct manager. `mtproto.GetManager()` is a process-wide singleton whose `procs` map outlives a test, so call `mtproto.GetManager().StopAll()` at the top AND in `t.Cleanup`.

`TestResetAllMtprotoQuotasKeepsUnreadableSidecar` — THE RED ONE:
1. `setupConflictDB(t)`; `pidFile := installFakeMtg(t)`.
2. `seedInboundConflict(t, "mt-broken", "", 46131, model.MTProto, "", `{"throttleMaxConnections":"100","clients":[{"email":"mtga","secret":"`+mtprotoTestSecretA+`","enable":true}]}`)` — valid JSON, type-broken scalar. `seeded := loadInboundByTag(t, "mt-broken")`; `seedClientTraffic(t, seeded.Id, "mtga", true)`.
3. Pin the premise: `if _, ok := mtproto.InstanceFromInbound(seeded); ok { t.Fatal("fixture must not typed-parse; the divergence it tests no longer exists") }`.
4. Prime the sidecar by hand — the builder cannot make it, which is the point: `mtproto.GetManager().Ensure(mtproto.Instance{Id: seeded.Id, Tag: seeded.Tag, Port: 46131, Secrets: []mtproto.SecretEntry{{Name: "mtga", Secret: mtprotoTestSecretA}}})`, then `waitForSpawns(t, pidFile, 1)`. This is the real-world state: started while the settings were good, broken afterwards.
5. `(&InboundService{}).resetAllMtprotoQuotas()`.
6. Assert, both deterministic and synchronous — `Manager.Reconcile` takes `m.mu`, calls `proc.Stop()` (blocking, process.go:228-241), `delete(m.procs, id)` and `os.Remove(configPathForID(id))` before returning:
   a. `if _, err := os.Stat(mtgConfigPath(t, seeded.Id)); err != nil { t.Fatalf("quota reset deleted the sidecar config for inbound %d (%v); an inbound whose settings will not parse must not be stopped by a reset", seeded.Id, err) }`
   b. `if !mtproto.GetManager().HasRunning() { t.Fatal("quota reset stopped the only running mtg sidecar; its inbound's settings merely will not parse, and nothing ever restarts it") }`

WITHOUT the fix both fail, every run: `DesiredMtprotoInstances` skips the broken inbound, `desired` is empty, `mgr.Reconcile(nil)` stops the primed proc, drops it from `procs` and unlinks `$PUI_BIN_FOLDER/mtproto/mtg-<id>.toml` — so `os.Stat` returns ErrNotExist and `HasRunning()` returns false. WITH the fix the empty `desired` drives an empty `Ensure` loop, the process and config are untouched, both pass. Explicitly NOT the harness the rejected plan cited: `assertNoNewSpawns` counts cumulative pid-file lines, and a stop with no respawn leaves that count identical either way.

Second subtest `convergesDriftBeforeReset` — declared honestly as a GREEN-UNDER-STATUS-QUO regression guard, not the proof of the fix: seed a healthy inbound with clients A and B, prime its process via `Ensure` with only secret A (drift), call `resetAllMtprotoQuotas()`, expect `waitForSpawns(t, pidFile, 2)` — the fake mtg serves no HTTP, so `applySecrets` fails and `ensureLocked` falls through to a restart. It passes today and passes after the fix; it fails under the rejected "just delete the reconcile" variant. Its job is to pin the convergence this plan deliberately preserves.
**Sites:**
- internal/web/service/inbound_mtproto.go:141-143 — `if err := mgr.Reconcile(desired); err != nil { logger.Debug("mtproto: reconcile before quota reset failed:", err) }` — the fix site; the only non-core caller of a destructive engine Reconcile in the tree
- internal/web/service/inbound_mtproto.go:18-20 — the doc sentence 'so a caller reconciling this set stops their sidecar', deleted in the same commit; it is the artifact that authorized the bug
- internal/web/service/inbound_mtproto.go:41-44 — `inst, ok := mtproto.InstanceFromInbound(ib); if !ok { continue }` — the second builder merging 'unparseable' into 'no longer desired'; after the fix it loses lifecycle authority but still silently skips those inbounds' quota resets (deferred)
- internal/web/service/inbound_mtproto.go:161-165 (`localMtprotoInboundIdForEmail`) — SAME `!ok → continue` shape, benign-but-silent: a single-client reset on an unparseable inbound finds no inbound, so neither applyLocalMtproto nor ResetQuota runs and the client stays blocked by the sidecar's own counter (deferred, same root)
- internal/web/service/inbound_mtproto.go:91-104 (`resetMtprotoClientQuota`) — the single-client sibling and the correct template: `applyLocalMtproto` (runtime-dispatched converge) THEN `mgr.ResetQuota`; the only other `mtproto.GetManager()` grab outside internal/cores/
- internal/mtproto/manager.go:421-447 — `Reconcile(desired []Instance, keep ...int)`; :432-437 is the destructive branch (`Stop`, `delete(m.procs, id)`, `os.Remove(configPathForID(id))`); :440-445 is the per-instance ensure the Ensure loop keeps
- internal/mtproto/manager.go:300-305 — `Ensure(inst Instance)`, the non-destructive half the fix calls; identical `ensureLocked` + `sweepOrphansLocked` semantics, minus the stop-the-absent loop
- internal/mtproto/manager.go:183-212, 255-265 — `InstanceFromInbound` and `ApplySettings`, both strict typed unmarshal into `instanceSettings`; the shared failure mode
- internal/mtproto/manager.go:556-573 — `resetQuota` treats HTTP 404 as success, so a reset against a secret the running proc lost is silently a no-op
- internal/cores/internal/mtproto/core.go:101-118 — the core's `keep` list and its comment ('Omitting it would read as "no longer desired" and stop the sidecar with every client on it'); also proves the stop is PERMANENT, since keep prevents stopping but never starts
- internal/cores/internal/mtproto/core.go:174-185 — `apply`: unparseable returns an error and touches nothing; `!serve` calls `mgr.Remove` — the stop branch that made `applyLocalMtproto`-per-inbound the wrong shippable fix
- internal/web/service/inbound_core.go:40-44 — `DesiredInstances`, the ONE convergence path: 'A build failure fails the whole set rather than omitting one inbound: omitted reads as "stop it"' — the invariant the fix site violates
- internal/web/job/core_supervise.go:48-56 + internal/web/web.go:322 (`cadenceCoreSupervise = "@every 10s"`) — the supervise tick that owns the sidecar lifecycle and that will never restart a keep-listed inbound
- internal/database/model/model.go:804-812 (`HealMtprotoClientSecrets`) — map-based, tolerant save path; with no strict mtproto settings validator on add/update, a type-broken scalar persists and reaches the strict readers
- SEARCHED AND CLEAN — `\.Reconcile(` across non-test Go under internal/: internal/web/service/inbound_mtproto.go:141 is the only non-core caller; the rest are `bound.Supervise.Reconcile` (job/core_supervise.go:54, service/core_control.go:62, coretest/suite.go) or a core adapter's own (mtproto core.go:117, wireguard core.go:121, amneziawg core.go:92) or the unrelated EgressService
- internal/cores/internal/wireguard/core.go:105-122 — the IDENTICAL keep guard with the same comment; no second wg desired-state builder exists. Clean.
- internal/cores/internal/amneziawg/core.go:84-97 — delegates to the wireguard core's Reconcile and inherits the keep guard. Clean.
- internal/web/service/client_traffic.go:344-346 and :226, internal/web/service/inbound_traffic.go:629-630 and :744 — the four reset call sites; each resets the sidecar quota BEFORE re-enabling (deferred ordering family, see deferred)
- mtg-proxy fork: mtglib/proxy_stats.go:84-104 (getOrCreate only, `s.users` never pruned), :168-172 (ResetQuota), :183-215 (LoadUsage/FlushUsage — usage survives a restart), mtglib/proxy_api.go:327-341 (`hasSecret` gate, 404 for an absent secret)

**Correction from the re-check:** In the new loop, log at Warning, not Debug: `logger.Warning("mtproto: converge before quota reset failed for inbound", inst.Id, ":", err)`. Reason: the call being replaced logs every per-instance converge failure at Warning inside the manager (`logger.Warningf("mtproto: reconcile failed for inbound %d: %v", ...)`, internal/mtproto/manager.go:442) — that Warningf fires only from `Reconcile`, never from `Ensure`, which returns its error unlogged. As written the patch drops that line to Debug, which is suppressed at the default log level, so a sidecar that fails to converge before a quota reset becomes silent. Still no new import; `logger` is already used at :147. Everything else in shippableNow ships as written.
**Re-check notes:**
- The plan's load-bearing claim that the Ensure loop is 'a strict behavioural SUBSET' of `Reconcile` is inexact. `Reconcile` logs each per-instance converge failure at Warning (internal/mtproto/manager.go:442); `Ensure` (manager.go:300-305 -> ensureLocked) returns its error unlogged. With the proposed `logger.Debug` the patch silently downgrades converge-before-reset failures from Warning to Debug, invisible at the default log level — a small observability regression, and directionally wrong for a finding whose theme is silent mtproto reset failures. Fix: use logger.Warning. See `correction`.
- Second inexactness, benign: the lock granularity changes. `Reconcile` holds `m.mu` across the whole batch; an `Ensure` loop takes and releases it per instance, so the 10s supervise tick can interleave between instances. Harmless here — the interleaving reconcile is the core's, which carries the `keep` guard (internal/cores/internal/mtproto/core.go:101-118) — but it is not literally 'preserves the current ordering exactly'. Not a reason to change the shape; the direct-Ensure loop is still the right patch.


### F235 — ship-it (small)
**Root:** The adversary is CONFIRMED on all three counts; I re-read every line. Also: the original plan's line numbers are stale by +13 throughout — every anchor below is re-pinned against the current tree (git clean at 07c2dca4).

The defect. In `UpdateInbound`'s both-sides-Xray branch (internal/web/service/inbound.go:1611-1648, reached when `oldInbound.NodeID == nil`, `push` is true, and BOTH old and new protocol are Xray-served), the `postCommitApply` closure at :1635 handles the remove case as:

    if runtimeInbound == nil {
        if err2 := rt.DelInbound(context.Background(), &oldSnapshot); err2 == nil {
            logger.Debug("Disabled inbound removed on", rt.Name(), ":", tag)
        }
        return
    }

There is no `else`. A `DelInbound` failure falls out of the `if` and the closure returns, having recorded nothing. The listener stays up serving every existing client while the panel row reads disabled, and the operator is told "successfully updated". The contrast is three lines below at :1644-1647, in the SAME closure, where an `UpdateInbound` failure does `logger.Debug(...); needRestart = true`.

Correction 1 (adversary claim 2 — the enumeration was incomplete and load-bearing). `UpdateInbound` begins `needRestart := false` at :1433 and assigns it at SIX sites, not the five the original plan listed: :1578 (`!push`), :1603 (non-Xray-new-protocol update failure), :1620 (build failure — guarded by `if inbound.Enable`, so unreachable on a disable), :1646 (the enabled in-place update failure), :1676 (`mtprotoRoutesThroughXray(inbound) || oldRoutedMtproto`), and :1694 (`PropagateInboundTagRename` → `routingChanged`, inside `if tag != oldInbound.Tag` at :1690). The plan missed :1694 entirely. The conclusion survives — none of the six fires for a plain Enable→false edit with no rename — but the claim "these are the only assignments" was false as written, and the red test's polarity rests on exactly that enumeration, so it has to be right.

Correction 2 (adversary claim 3 — the log names a state that is not always true). `buildInboundForLocalRuntime` (inbound.go:1739-1783) returns `nil, err` on all four of its error paths. So `runtimeInbound == nil` is TWO states, not one: (a) the disable — `inbound.Enable` false, so the build at :1615-1621 never runs; and (b) `inbound.Enable` TRUE with a failed build, where :1620 already set `needRestart = true` and the same closure then calls `rt.DelInbound(&oldSnapshot)`, dropping the listener of an inbound the operator just asked to keep enabled. Behaviour on (b) is unaffected by this fix (needRestart is already true, and the restart repairs it because `RenderInbound` at xray.go:246 rebuilds an enabled inbound from the row via a path that tolerates the settings JSON this one choked on). But the word "disabled" is wrong on (b) — in BOTH the existing success log at :1638 and the message the original plan proposed for the new failure branch — and (b) is precisely the path an operator debugging a vanished listener is reading.

Correction 3 (adversary claim 1 — the test already exists and the sibling sweep missed the file). `TestDisablingAnXrayInboundRemovesItFromTheCore` (internal/web/service/inbound_update_restart_polarity_test.go:112-138) already performs the exact scenario the original plan's step 3 proposed: `setupConflictDB`, `runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})`, a clean `fakeNodeRuntime`, a seeded VLESS row (`vless-off`, port 46172), `update.Enable = false`, `UpdateInbound`, then `del == 1` and `add == 0`. The one thing it does not assert is `needRestart` — line 128 discards it as `_`. Re-seeding a fourth near-identical VLESS row to re-prove a covered path is what CLAUDE.md forbids.

Correction 4 (mine — the adversary did not have this, and it dissolves the original plan's hand-back). `DelDepletedClients` is DEAD CODE. `git grep -n "DelDepletedClients" -- '*.go'` returns exactly one line, the definition at internal/web/service/inbound_traffic.go:831. Zero callers, including tests. So `_, _ = s.DelInbound(inbound.Id)` at inbound_traffic.go:904 does not drop a live restart signal — it drops nothing, because nothing reaches it. The original plan's "genuine hole to hand back" is not a hole; it is unreachable code, and deleting it is a separate chore, not an F235 question.

Provenance of the shape: f431e9cc introduced an unconditional `DelInbound` whose swallowed error was masked by the `AddInbound` that followed it (that one did report). When the delete+add was replaced by converge-in-place, the add went away and the swallow was left behind, alone, on the one branch that still needs a delete.
**Ships now:** The whole fix ships now. Nothing in it requires a behaviour choice, and no operator prefers "the listener stays up while the panel says it is off".

EDIT — internal/web/service/inbound.go:1636-1641, add the missing `else`, mirroring :1644-1647 three lines below, and drop the word "disabled" from both log lines because the branch is also reached for an ENABLED inbound whose runtime config failed to build (:1615-1621):

    if runtimeInbound == nil {
        if err2 := rt.DelInbound(context.Background(), &oldSnapshot); err2 == nil {
            logger.Debug("Inbound removed on", rt.Name(), ":", tag)
        } else {
            logger.Debug("Unable to remove inbound on", rt.Name(), ":", err2)
            needRestart = true
        }
        return
    }

Three added lines plus one word deleted from the existing success log at :1638. No helper, no config knob, no schema change, no new i18n key, no controller or route change — so no migration, no `endpoints.ts` entry, no `make gen`, no docs copy.

Why the assignment is observed: `needRestart` and `applyWarn` are closure captures of the enclosing function's locals; `postCommitApply()` is invoked at :1683-1685, before `return inbound, needRestart, applyWarn, nil` at :1697.

Why `needRestart` and not `applyWarn`: pinned by `TestAnXrayApplyFailureStillJustSchedulesTheRestart` (inbound_apply_warning_test.go:57-81), which asserts an Xray-served apply failure must schedule the restart and warn about nothing — "a warning nobody needs to act on is how the ones that matter get ignored". And the restart is a complete repair: `RenderInbound` (internal/web/service/xray.go:246-247) returns `nil, nil` for `!inbound.Enable`, so the regenerated config omits the disabled inbound and the listener closes.

Why no `strings.Contains(err.Error(), "not found")` guard: `SetInboundEnable` carries one at :1350-1351 for historical reasons, but the local path here bottoms out in `Local.DelInbound` (runtime/local.go:113-120) → `Core.DropInstance` (cores/internal/xray/core.go:192-205), which returns only "xray: core is not running" or "xray: inbound %q cannot be removed in place" — never "not found", and nil for a tag it does not hold. A string match here would be a second opinion on a question the core already answers, which the repo's own comment at :1623-1634 rejects by name.
**Deferred:** Nothing needed for the repair is held back. Four siblings are deliberately NOT touched, each for a stated reason:

1. internal/web/service/inbound_traffic.go:904 (`_, _ = s.DelInbound(inbound.Id)` in `DelDepletedClients`). The original plan flagged this as a genuine hole to hand back. It is not: `git grep -n "DelDepletedClients" -- '*.go'` returns only the definition at :831 — zero callers, tests included. It is dead code. Deleting it is a `chore(service)` of its own, unrelated to F235, and folding a dead-code deletion into a bug fix violates "diff is focused".

2. internal/web/service/inbound.go:1146-1150 (`DelInbound`, the NODE branch): logs `logger.Warning("DelInbound on", rt.Name(), "failed after commit:", err1)` with no `needRestart`, while the local branch 13 lines down at :1159-1163 does raise it. Correct as written — the inbound is served by a remote node, so restarting the local Xray cannot apply anything; the node reconciles via `MarkNodeDirtyTx`. Not a defect.

3. internal/web/runtime/local.go:132 (`if l.strandsOldInbound(oldIb, newIb) { _ = l.DelInbound(ctx, oldIb) }`): a swallowed error, but the comment above it states the intent — "The drop is not fatal here — the old core may legitimately no longer be serving it." Deliberate, and the caller's own error handling covers the apply that follows.

4. The enabled-inbound/build-failure path (:1615-1621 → the same nil branch): this fix corrects its LOG WORDING only. Its behaviour — drop the old listener, rely on the already-set `needRestart` at :1620 to rebuild it — is left exactly as it is. Changing that behaviour would be a real design question and is not what F235 is about.
**Test:** Three parts, labeled honestly. Only part 2 is the red test. Go stdlib `testing` in `internal/web/service`; DB-gated via `setupConflictDB(t)` (skips without `PUI_DB_DSN`, the repo norm), so it must be run as `make test-go` with `PUI_DB_DSN` set — a green run without it proves nothing.

PART 1 — ENABLE THE FAILURE (harness, not a test). In `node_bulk_dispatch_test.go`, add one field `delInboundErr error` beside `updateInboundErr` (:31-34) and return it from `DelInbound` (:45-48): `f.delInbound.Add(1); return f.delInboundErr`. The zero value keeps all four existing `delInbound` assertions (inbound_durable_postgres_test.go:109,147; polarity:102,132; node_bulk_dispatch_test.go:311) on the success path, exactly as `updateInboundErr` already does.

PART 2 — THE RED TEST. New test in `inbound_update_restart_polarity_test.go`, fresh tag/port to avoid colliding with `vless-off`/46172 and `vless-warn`/46321:

    func TestADisableTheCoreRefusedSchedulesTheRestart(t *testing.T) {
        setupConflictDB(t)
        mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
        fake := &fakeNodeRuntime{delInboundErr: errors.New("xray: core is not running")}
        mgr.SetLocalRuntimeOverride(fake)
        runtime.SetManager(mgr)
        t.Cleanup(func() { runtime.SetManager(nil) })
        seedInboundConflict(t, "vless-off-refused", "", 46173, model.VLESS, "",
            `{"clients":[{"email":"offr","id":"...","enable":true}],"decryption":"none"}`)
        update := *loadInboundByTag(t, "vless-off-refused")
        update.Enable = false
        _, needRestart, _, err := (&InboundService{}).UpdateInbound(&update)
        ...
    }

TWO assertions, both required:
 (a) `if n := fake.delInbound.Load(); n != 1 { t.Fatalf("remove ran %d time(s), want 1 — the failure path this test is about never ran", n) }`. Not decoration: if a future change makes `push` false, `needRestart` becomes true at :1578 for an unrelated reason and (b) goes falsely green. The existing tests carry this same guard at :35 and :100 for exactly this reason.
 (b) `if !needRestart { t.Fatal("the core refused to remove the disabled inbound and nothing scheduled the restart; the listener keeps serving every client while the panel shows the inbound off") }`.

EXACTLY WHAT FAILS WITHOUT THE FIX: assertion (b). `UpdateInbound` returns `needRestart == false`, and the run prints `--- FAIL: TestADisableTheCoreRefusedSchedulesTheRestart` with that message. Assertion (a) passes on both sides.

POLARITY PROOF that `false` is what the unfixed code returns — walking all six assignment sites for this input: :1578 needs `!push`, but push is true (the existing `TestDisablingAnXrayInboundRemovesItFromTheCore` asserting `del == 1` on the identical arrangement proves `postCommitApply` is installed and invoked); :1603 is the other arm of the `cores.ServedByXray` branch, and VLESS→VLESS takes the `else` at :1611; :1620 is inside `if inbound.Enable`, false here; :1646 is after the `return` at :1640, unreachable on the nil path; :1676 needs `mtprotoRoutesThroughXray`, false for VLESS; :1694 needs `tag != oldInbound.Tag`, and nothing renames — the update is loaded by tag and only `Enable` is changed. With the fix, the new `else` sets it true.

PART 3 — THE GREEN PIN (passes both sides; NOT the red test, and not offered as one). In the existing `TestDisablingAnXrayInboundRemovesItFromTheCore`, capture the second return at :128 instead of discarding it and add `if needRestart { t.Fatal("a disable the core accepted still asked for an Xray restart, dropping every connection on every other inbound to change nothing") }`. Its only job is to stop a future `needRestart = true` being hoisted out of the `else` and made unconditional — the failure mode a bare red test would not catch. One added assertion to a test that already exists; no fourth VLESS row is seeded.
**Sites:**
- internal/web/service/inbound.go:1636-1641 — THE DEFECT. `if runtimeInbound == nil { if err2 := rt.DelInbound(ctx, &oldSnapshot); err2 == nil { logger.Debug("Disabled inbound removed on", ...) } ; return }` — no else; the error falls out and the closure returns having recorded nothing.
- internal/web/service/inbound.go:1644-1647 — the in-function contrast, 3 lines below in the same closure: `} else { logger.Debug("Unable to update inbound on", rt.Name(), ":", err2); needRestart = true }`. The shape to mirror.
- internal/web/service/inbound.go:1615-1621 — the SECOND state that reaches `runtimeInbound == nil`: `if inbound.Enable { runtimeInbound, err2 = s.buildInboundForLocalRuntime(...); if err2 != nil { logger.Debug(...); needRestart = true } }`. Why the log must not say "disabled".
- internal/web/service/inbound.go:1739-1783 — `buildInboundForLocalRuntime` returns `nil, err` on all four error paths (:1742, :1747, :1756, :1778). Proof the enabled-build-failure path really does land on the nil branch.
- internal/web/service/inbound.go:1433, 1578, 1603, 1620, 1646, 1676, 1694 — `needRestart := false` and ALL SIX assignments in `UpdateInbound`. :1694 is the one the original plan missed (PropagateInboundTagRename, guarded by `if tag != oldInbound.Tag` at :1690).
- internal/web/service/inbound.go:1683-1685 and :1697 — `postCommitApply()` runs before `return inbound, needRestart, applyWarn, nil`, so the closure's assignment is observed by the caller.
- internal/web/service/inbound.go:1611 — the branch gate `} else {` on `cores.ServedByXray(old) && cores.ServedByXray(new)`, which is what makes `needRestart` (not `applyWarn`) the correct signal here.
- internal/cores/internal/xray/core.go:192-205 — `DropInstance` returns only "xray: core is not running" or "xray: inbound %q cannot be removed in place"; never "not found". Why no string guard is added.
- internal/web/runtime/local.go:113-120 — `Local.DelInbound` → `bound.Apply.DropInstance(ctx, InstanceOf(ib))`, the path from the service layer to that error.
- internal/web/service/xray.go:246-247 — `if inbound == nil || !inbound.Enable || inbound.NodeID != nil || !servedByXrayCore(...) { return nil, nil }`. Proof the restart `needRestart` schedules is a COMPLETE repair for the disable case.
- internal/web/job/core_supervise.go:44 (`if bound.Supervise == nil || id == j.panelConverged { continue }`) with internal/cores/cores.go:162 (`const PanelConvergedCore = xray.ID`) — proof no background pass reconciles Xray, so the swallowed error is never repaired on its own.
- internal/web/service/inbound_update_restart_polarity_test.go:112-138 — `TestDisablingAnXrayInboundRemovesItFromTheCore`, the test the adversary found and the original plan's sibling sweep missed. Line 128 discards `needRestart` as `_`.
- internal/web/service/inbound_apply_warning_test.go:57-81 — `TestAnXrayApplyFailureStillJustSchedulesTheRestart`, which pins needRestart-over-applyWarn for Xray-served failures.
- internal/web/service/node_bulk_dispatch_test.go:19-35 and :45-48 — `fakeNodeRuntime`'s error fields (`updateInboundErr`, `addUserErr`) and its `DelInbound`, which currently returns nil unconditionally.
- internal/web/service/inbound.go:1350-1352 — SIBLING, ALREADY HANDLED. `SetInboundEnable`'s DelInbound has the `!strings.Contains(err.Error(), "not found")` guard and DOES set needRestart. The repo already made this call once.
- internal/web/service/inbound.go:1146-1150 vs :1159-1163 — SIBLING, NOT THE SAME DEFECT. `DelInbound`'s node branch logs Warning with no needRestart (correct: a local restart cannot apply a node inbound); its local branch does raise it.
- internal/web/service/inbound_traffic.go:831 and :904 — SIBLING, DEAD CODE. `DelDepletedClients` has zero callers in tracked files (`git grep -n "DelDepletedClients" -- '*.go'` → one line, the definition), so its `_, _ = s.DelInbound(...)` drops nothing.
- internal/web/runtime/local.go:132 — SIBLING, INTENTIONAL. `_ = l.DelInbound(ctx, oldIb)`, documented two lines above as deliberately non-fatal.

**Re-check notes:**
- Description nit, not a code defect: the prose says "drop the word 'disabled' from both log lines", but only ONE existing line carries it (inbound.go:1638, "Disabled inbound removed on"). The second line is newly authored text in the added else. The code block as written is correct; only the sentence describing it is imprecise. Nothing to change in the diff.
- The sites list is presented as exhaustive but omits a fifth rt.DelInbound call site: internal/web/service/inbound_disable.go:49, in disableInvalidInbounds (the quota/expiry depletion pass). Verified CLEAN — it already does `if dropErr := rt.DelInbound(...); dropErr != nil { logger.Debug(...); needRestart = true; continue }`. So it is not a live sibling of the defect; the omission is an incompleteness in the survey, not a hole in the fix.
- The proposed new test omits seedClientTraffic(t, ...), which all three sibling VLESS tests call after seeding. If it turns out to matter, UpdateInbound returns an error and the test t.Fatalf's loudly at authoring time — it cannot make the test falsely green. Non-blocking; adjust at write time if the run says so.


### F236 — ship-it (small)
**Root:** The adversary is RIGHT on the main point and WRONG on one supporting claim; I verified both from source.

RIGHT — the root is the save site, not the report site. `PropagateInboundTagRename` (xray_setting_routing_sync.go:285) and `RemoveInboundTagReferences` (:322) both persist through `SaveXraySetting`, which re-runs `CheckXrayConfig` (xray_setting.go:35) on a template the panel itself already stored. `CheckXrayConfig` runs the FULL xray-core `OutboundDetourConfig.Build()` per outbound (internal/xray/api.go:231-240). Confirmed strictness mechanism: `currentXrayProcess()` is `xray.GetManager().Current()` (xray.go:39) and is nil while the core is down or crashed, so `coreVersion = "Unknown"`, `parseXrayCoreVersionParts` returns ok=false, `compareXrayCoreVersions` returns ok=false, and `shouldSkipLegacyUnencryptedOutboundRejection` returns false — i.e. STRICT. In xray-core v1.260327.1 (infra/conf/xray.go:246-266, 359-362), `validateOutboundTransportSecurity` runs BEFORE `rawConfig.Build()` and rejects any plain vless (`encryption:"none"`, no TLS) or trojan outbound whose address is not private (private list is compiled in at common/geodata/consts.go:9-30, no asset files needed). So an operator cascade outbound accepted while an older core ran becomes a hard rejection the moment the core is stopped — and the panel's own mechanical tag rewrite is then refused FOREVER, with a log line as the only trace. The escape hatch's existence is itself the repo admitting this skew is real.

WRONG — the adversary's second mechanism does not exist. It claims `EnsureStatsRouting`/`EnsureDnsServerRouting` mutating after `CheckXrayConfig` means "the stored value is not the validated value" and therefore a rejection path. It is not: `CheckXrayConfig` validates ONLY `outbounds` (xray_setting.go:68-89), and both Ensure* functions touch only `routing`. They cannot produce a stored template the validator would later reject. Drop that argument; the coreVersion one carries the finding alone.

The key property the fix rests on, which I verified: the rewrite is VALIDITY-MONOTONE. It substitutes `inboundTag` strings in `routing.rules` and `settings.inboundTag` on `loopback` outbounds only (xray_setting_routing_sync.go:70-96, 152-200). It never touches an outbound's protocol, address, or streamSettings, and `ValidateOutboundConfig` builds each detour in isolation with no tag-existence check — so the rewrite can NEVER make a template newly invalid. Every rejection at this save site is about content that was already in the database before the call. Re-validating there does no work; it only blocks the repair.

Two secondary facts that close follow-up objections: `mutateXrayTemplateRouting` already calls `UnwrapXrayTemplateConfig` at :203, so `updated` is unwrapped and a raw `saveSetting` skips nothing on that front; and `reservedTagOwner` (port_conflict.go:355-372) forbids an inbound tag of "api"/"direct"/"blocked", so the rewrite can never delete or displace the api rule that `EnsureStatsRouting` exists to pin.

The report-site silence the original plan found is real but is the RESIDUAL, not the root: it is what is left after the save site can no longer fail for validator reasons.
**Ships now:** Two edits, one commit, no signature change, no new file, no model/i18n/route change.

(1) SAVE SITE — `internal/web/service/xray_setting_routing_sync.go`. In BOTH `PropagateInboundTagRename` (:285) and `RemoveInboundTagReferences` (:322), replace

    if err := s.SaveXraySetting(updated); err != nil {
        return false, err
    }

with a validate-first, store-on-rejection form. To avoid duplicating four lines twice, add one unexported 6-line helper in the same file and call it from both:

    // A tag substitution cannot make a stored template newly invalid, so a
    // validator rejection here is about content that was already saved.
    func (s *XraySettingService) storeRoutingSync(updated string) error {
        if err := s.SaveXraySetting(updated); err == nil {
            return nil
        }
        return s.saveSetting("xrayTemplateConfig", updated)
    }

`saveSetting` is reachable because `XraySettingService` embeds `SettingService` (xray_setting.go:19-21) — the test helper `seedXrayTemplate` already uses it. Normal (valid-template) behaviour is BYTE-IDENTICAL: `SaveXraySetting` still runs, so `EnsureStatsRouting`, `EnsureDnsServerRouting` and the post-save `PruneOutbound` all still run on every successful rename and delete. Only the previously-permanent-failure path changes. This repairs the rename path AND the confirmed DelInbound sibling in the same two-line-per-site change.

(2) REPORT SITE — `internal/web/service/inbound.go:1690-1696`. Use the `applyWarn` channel the function already returns at :1697, joining rather than clobbering (the mtproto branches at :1594 and :1606 set it and can co-occur with a rename):

    if routingChanged, syncErr := (&XraySettingService{}).PropagateInboundTagRename(tag, oldInbound.Tag); syncErr != nil {
        logger.Warning("UpdateInbound: sync routing on tag rename failed:", syncErr)
        // Nothing later rewrites the template, so the rules keep matching a tag
        // no inbound answers to until an operator repairs them by hand.
        warn := "routing rules still name " + tag + ": " + syncErr.Error()
        if applyWarn == "" {
            applyWarn = warn
        } else {
            applyWarn += "; " + warn
        }
    } else if routingChanged {
        needRestart = true
    }

`tag` is captured at inbound.go:1429 (`tag := oldInbound.Tag`) BEFORE the transaction resolves the new tag, so it is the pre-edit tag — which is exactly what the stale rules still name. Correct as written. No new i18n key: the controller already wraps this in `pages.inbounds.toasts.inboundSavedButNotApplied` (controller/inbound.go:276-279) and line :1606 already puts an English error string into the same channel.
**Deferred:** 1. THE UNCONDITIONAL SWAP (the adversary's own primary: always `saveSetting`, never `SaveXraySetting`). Deferred because it changes the SUCCESS path of every rename and every delete — it drops `EnsureStatsRouting`, `EnsureDnsServerRouting` and `PruneOutbound` from the sync path permanently. I believe those are no-ops for this mutation's shape (order is preserved; `reservedTagOwner` makes `deletedTag == "api"` impossible; the rewrite changes no outbound tag so `TemplateDestinations` is unchanged), but "I believe they are no-ops" is a preference about the healthy path, not a repair of breakage. The fallback form gets 100% of the harm removal with none of that blast radius.

2. MAKING THE FAILURE REACH NON-BROWSER CALLERS. `applyWarn` reaches only a human watching that browser tab: `runtime/remote.go:66-70`'s `envelope` decodes success/msg/obj and drops `warning`, so a sub-node push and any API/automation caller still sees nothing. Fixing that means changing the node envelope and every decode site — larger than the bug, and it matters far less once the save site can no longer fail for validator reasons.

3. `DelInbound` HAS NO WARN CHANNEL. Its signature is `(bool, error)` and threading one out ripples to `DelInbounds` and `controller/inbound.go`. Deferred deliberately: edit (1) removes the actual breakage on that path, leaving only the DB-error residual unreported.

4. `RoutingService.PruneInbound` LOG-ONLY at inbound.go:1218-1224. Same shape, different subsystem (the panel's ip rules, not the template), lower severity, and it has a stated comment. Out of scope.

5. `UpdateWarpXraySetting` (xray_setting.go:220) DELIBERATELY EXCLUDED from the fallback — naming it so the next sibling hunt does not reopen it. It rewrites outbound CONTENTS (secretKey, addresses, peer endpoints), so it is NOT validity-monotone and `CheckXrayConfig` is doing real work there. It must keep `SaveXraySetting`.

6. `SaveXraySetting`'s own `PruneOutbound` failure (xray_setting.go:53-58) is log-only by design with a written safety argument. Closed, not deferred.
**Test:** Go, stdlib testing, real Postgres (PUI_DB_DSN), added to internal/web/service/xray_setting_routing_sync_test.go. Three tests. The seeded template is stored via the existing `seedXrayTemplate` helper, which calls `saveSetting` directly and so bypasses validation — the same way a template stored under an older core reaches the DB.

SHARED SEED (tests 1 and 2) — a rule the operator wrote plus a cascade outbound the embedded validator refuses:
  {"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"},{"type":"field","inboundTag":["in-22435-tcp"],"outboundTag":"cascade"}]},
   "outbounds":[{"tag":"cascade","protocol":"vless","settings":{"address":"1.2.3.4","port":443,"id":"00000000-0000-0000-0000-000000000000","encryption":"none"}}]}
PRECONDITION ASSERT, first statement of both tests: `if err := (&XraySettingService{}).CheckXrayConfig(tmpl); err == nil || !strings.Contains(err.Error(), "prohibited unless the server address is a private IP or domain") { t.Fatalf(...) }`. This pins the test to the real rejection so it cannot quietly go green if xray-core's validator ever loosens. Verified from source that it fires with no running core and no geodata assets: validateOutboundTransportSecurity runs before rawConfig.Build() (infra/conf/xray.go:359-362) and the private-IP list is compiled in (common/geodata/consts.go:9-30); the id is never parsed.

TEST 1 — TestPropagateInboundTagRename_StoresTheRewriteWhenTheTemplateFailsRevalidation. setupSettingTestDB(t); seed; call PropagateInboundTagRename("in-22435-tcp","in-33000-tcp"); assert err==nil, changed==true, and that the re-read template's cascade rule names in-33000-tcp.
  RED WITHOUT THE FIX, exactly: SaveXraySetting returns at xray_setting.go:36, so the function returns (false, err) and the test dies on its first assertion with
    PropagateInboundTagRename: xray core rejects outbound "cascade": vless without TLS or other encryption is prohibited unless the server address is a private IP or domain
  and the stored rule still reads in-22435-tcp. GREEN WITH IT: the fallback stores, err is nil.

TEST 2 — TestRemoveInboundTagReferences_StoresTheRewriteWhenTheTemplateFailsRevalidation. Same seed; call RemoveInboundTagReferences("in-22435-tcp"); assert no rule in the re-read template still names in-22435-tcp and the api rule survives.
  RED WITHOUT THE FIX with the same rejection string, then `routing rule still names the deleted inbound "in-22435-tcp"`. This is the test that proves the DelInbound sibling is covered.

TEST 3 (the warn half) — TestUpdateInbound_WarnsWhenTheRoutingSyncFails. setupConflictDB(t); seedXrayTemplate(t, `["not","a","config"]`); seedInboundConflict(t,"in-22435-tcp","0.0.0.0",22435,model.VLESS,`{"network":"tcp"}`,`{"clients":[],"decryption":"none"}`); read the row; UpdateInbound with Port=33000 (forces the rename). A top-level JSON array cannot unmarshal into map[string]any, so mutateXrayTemplateRouting (:205) errors BEFORE any save — the one failure the fallback cannot repair, which is precisely what the warn is for. Assert applyWarn contains "in-22435-tcp".
  RED WITHOUT THE FIX: `UpdateInbound reported no warning; the operator is never told the routing rules still name "in-22435-tcp"`.
  IMPLEMENTER MUST CONFIRM THE RED IS FOR THE RIGHT REASON — that UpdateInbound returns a nil error and applyWarn == "", i.e. it fails AT the applyWarn assertion, not earlier. I verified the two template readers on this path tolerate garbage (reservedAPIPort port_conflict.go:109-128 and templateReservedTags port_conflict.go:374-388 both fall back on an unmarshal error) but could not run the suite here; if some other reader on the UpdateInbound path chokes on the array, swap the seed for a syntactically valid template whose top level is a JSON string (`"\"not a config\""`) or drop test 3 and rely on tests 1-2. Do not accept a red that comes from a different assertion.

Revert-check protocol: apply the fix, run all three green; revert only edit (1) — tests 1 and 2 go red with the rejection string above, test 3 stays green; revert only edit (2) — test 3 goes red, tests 1 and 2 stay green. Each edit is pinned by its own test.

Run: PUI_DB_DSN=... go test ./internal/web/service/ -run 'RoutingSync|StoresTheRewrite|WarnsWhenTheRoutingSync' -count=1, then make test-go.
**Sites:**
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray_setting_routing_sync.go:285 — PRIMARY. `if err := s.SaveXraySetting(updated); err != nil { return false, err }` in PropagateInboundTagRename: a validity-monotone tag rewrite is re-gated by a validator that can reject what the panel itself stored.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray_setting_routing_sync.go:322 — PRIMARY, CONFIRMED SIBLING. Identical `SaveXraySetting(updated)` in RemoveInboundTagReferences; same one-line change, zero signature ripple, repairs the DelInbound path in the same commit.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/inbound.go:1690-1696 — RESIDUAL. `if tag != oldInbound.Tag { if routingChanged, syncErr := ...PropagateInboundTagRename(tag, oldInbound.Tag); syncErr != nil { logger.Warning(...) } ... }` — log-only, applyWarn untouched.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/inbound.go:1697 — `return inbound, needRestart, applyWarn, nil` — the channel that exists and is not used by the branch above it.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/inbound.go:1211-1217 — SIBLING REPORT SITE. `RemoveInboundTagReferences(ib.Tag)` failure is log-only in DelInbound; no warn channel exists on `(bool, error)`, so the save-site fix is the only repair that lands here.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray_setting.go:28-46 — SaveXraySetting: CheckXrayConfig gate at :35, then Ensure*Routing at :38-43 (routing only — they cannot cause a rejection, refuting the adversary's second mechanism), then saveSetting at :44.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray_setting.go:62-103 — CheckXrayConfig + shouldSkipLegacyUnencryptedOutboundRejection: coreVersion "Unknown" (core down) => compareXrayCoreVersions ok=false => STRICT.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray.go:39 — `func currentXrayProcess() *xray.Process { return xrayState().Current() }` — nil while the core is stopped or crashed, which is the trigger.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/xray/api.go:231-240 — ValidateOutboundConfig runs the full xray-core OutboundDetourConfig.Build() per outbound.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/port_conflict.go:355-372 — reservedTagOwner forbids an inbound tag of api/direct/blocked, so the rewrite can never delete the api rule EnsureStatsRouting pins. Closes the main objection to bypassing SaveXraySetting.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/inbound.go:1429 — `tag := oldInbound.Tag`, captured pre-transaction: `tag` is the OLD tag, which is what the stale rules still name and what the warn text must say.
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/controller/inbound.go:276-279 — the applyWarn renderer (jsonMsgObjWarn keeps Success:true; frontend/src/utils/index.ts:56 shows a 10s toast).
- C:/Users/Arman/Desktop/hakir/Projects/Penhoon Panel/internal/web/service/xray_setting.go:220 — UpdateWarpXraySetting: EXCLUDED from the fallback on purpose (rewrites outbound contents, not validity-monotone).


### F238 — ship-it (small)
**Root:** Two independent writers do an unserialized read-modify-write over the single Xray config snapshot (`Process.GetConfig()` -> compute a hot diff -> `ApplyHotDiff` over gRPC -> `Process.SetConfig()`). `internal/xray/process.go:306` and `:315` each lock `p.mu` individually, so there is no critical section spanning the pair and no owner for it. Writer 1 is `XrayService.RestartXray` (`internal/web/service/xray.go:1226`), serialized only by the service package's `lock` (xray.go:30, taken at 1227). Writer 2 is the Xray core (`internal/cores/internal/xray/core.go`), which holds nothing — `Core.mu` (core.go:44-46) guards only `api`/`apiPort`/`pendingTags` and `hotApply` never takes it. `internal/xray.Manager`, the object both packages reach, has only `mu sync.RWMutex` over `process`/`result` (manager.go:23-27).

The adversary is RIGHT on its headline point and I verified it: the original plan's placement (applyMu after `GetXrayConfig()`) cannot close the cross-writer race, because `RestartXray` builds its target from the DB at xray.go:1235, outside any lock. Interleaving: RestartXray's `GetAllInbounds` snapshot misses inbound Z -> a concurrent HTTP add commits Z and its post-commit `rt.AddInbound` (inbound.go:1091 -> local.go:100 -> `Core.ApplyInstance`) hot-adds Z -> RestartXray takes the lock, sees `Equals` false at 1242, and hot-applies its stale target, whose diff REMOVES Z.

But I also found what the adversary did not, and it is decisive: the obvious repair — take the lock at the top of `RestartXray`, before the build — DEADLOCKS. `GetXrayConfig` calls `s.inboundService.AddTraffic(nil, nil)` (xray.go:137), which enters the serial traffic writer via `submitTrafficWrite` (traffic_writer.go:142) and waits on `req.done` with NO timeout (only the enqueue has one). Inside that writer, `addTrafficLocked` calls `disableInvalidInbounds` (inbound_traffic.go:109), which calls `rt.DelInbound` (inbound_disable.go:49) -> `Core.DropInstance` -> the same apply lock. RestartXray would hold the lock waiting for the writer while the writer waits for the lock. This is the identical shape the repo already documents at inbound_disable.go:238. That is why the staleness recheck is the only viable mechanism for the service half — not gold-plating — and why the service half carries a real policy question.

Two corrections to the adversary: (1) `Core.StopAll` (core.go:149) is live via `Server.stopCores` (web.go:788-801, no PanelConvergedCore skip) but only after `StopXray` already stopped the process, so it is an invariant to keep, not a repair; (2) for the xray core `Reconcile` (core.go:103) is production-dead — `CoreSuperviseJob` skips `cores.PanelConvergedCore` (core_supervise.go:44) and `RestartCore("xray")` routes to `RestartXrayService()` (core_control.go:46) — it is live only in `internal/core/coretest`.

Observable harm of the core-vs-core half, which is what ships now: concurrent `ApplyInstance`/`DropInstance` (two admin inbound ops, or an admin op against the depletion cron's `DropInstance` from the traffic writer) each read the same base snapshot and the last `SetConfig` wins, so tags the gRPC API really did install vanish from the panel's snapshot. A lost tag then either (a) keeps serving forever after the panel deleted the inbound, because every later diff is computed against a snapshot that never had it, or (b) is recomputed as an "added inbound" on the next apply, and `addInboundReconciling` (hot_apply.go:90-103) reconciles an existing tag with `DelInbound`+`AddInbound` — killing every live connection on an inbound nobody touched.
**Ships now:** Serialize the core's converge-and-apply on a dedicated mutex owned by `internal/xray.Manager`. Core-side ONLY — no `internal/web/service` edit at all. ~15 lines, 2 files, no new package, no column, no job, no config knob.

1. `internal/xray/manager.go` — add to `Manager` (currently lines 23-27):

    applyMu sync.Mutex

   with a 2-line comment saying it cannot be `mu`: `Replace` (manager.go:53) and `Snapshot` (manager.go:41) are called from inside the critical section and would self-deadlock on the RWMutex. Then two methods:

    // LockApply serializes converge-and-apply over the running process. Not
    // reentrant: hotApply must not take it, every caller of it already holds it.
    func (m *Manager) LockApply()   { m.applyMu.Lock() }
    func (m *Manager) UnlockApply() { m.applyMu.Unlock() }

2. `internal/cores/internal/xray/core.go` — `c.mgr.LockApply(); defer c.mgr.UnlockApply()` at four sites:
   - `ApplyInstance` (core.go:180): as the FIRST statements, above the `IsRunning` gate at :181. It must cover `withInbound(process.GetConfig(), inst)` at :185, not just `hotApply`, or a stale `next` still reverts a concurrent add.
   - `DropInstance` (core.go:193): same, above :194, covering `withInbound` at :200.
   - `Reconcile` (core.go:103): NOT at the top — at line 113, immediately before `process := c.mgr.Current()`. This deliberately leaves `c.deps.BaseConfig()` (:104) outside the lock so no DB/config build is ever held under it; that is the same discipline the deferred half needs, and xray's Reconcile is production-dead anyway.
   - `StopAll` (core.go:149): as the first statements. An invariant, not a repair — its only live caller stops an already-stopped process.
   - NOT in `hotApply` (core.go:412). The mutex is not reentrant and all four callers above already hold it.

Why this is undisputed: it closes the entire core-vs-core race with a lock that is never held across a DB read, a transaction, a `process.Start()`, or the serial traffic writer — so it cannot deadlock and cannot stall a traffic write for more than one gRPC round trip. No operator-visible behaviour changes; "the panel sometimes forgets an inbound it just installed, then drops every connection on it to re-add it" is not a semantic anyone can prefer.
**Deferred:** The service side is deliberately left untouched, and this must be stated plainly: **shippableNow does NOT fix the adversary's headline scenario.** `RestartXray` racing `Core.ApplyInstance` still loses inbound Z. What ships fixes core-vs-core: two concurrent HTTP inbound ops, and an admin op against the depletion cron's `DropInstance`.

1. Deferred — the staleness recheck at `internal/web/service/xray.go:1235`. Capture the running snapshot identity before `GetXrayConfig()`, re-check it after taking the apply lock at ~1239; a mismatch means another writer changed the running config while this target was being built and the target must not be applied by EITHER branch. Design note: pointer identity on `process.GetConfig()` is a smaller mechanism than a generation counter, since `SetConfig` always swaps the pointer — but the policy question below is identical either way. What to DO on a mismatch is a behaviour choice, so the whole piece waits.

2. Deferred with it — taking the lock in `RestartXray` (~1239) and `StopXray` (:1316) at all. I am REVERSING the original plan here, which extended the lock to `StopXray`. Reasons: (a) it does not close the race, so it buys no correctness on its own; (b) it introduces a new failure mode — the serial traffic writer's `DropInstance` would block behind a multi-second `process.Stop()`/`Start()`/`xray -test`, and `submitTrafficWrite` answers a full queue with "traffic writer queue full", i.e. dropped traffic deltas. That is a disputable trade, so it fails the undisputed bar. `StopXray` in particular needs nothing today: the package `lock` already serializes it against the only other service-side writer, and a stop racing a core apply fails closed (the gRPC call errors, `hotApply` returns false, the caller escalates).

3. Deliberately NOT pre-added: a generation counter bumped in `UnlockApply()` with no reader. An unused counter is exactly the dead seam this repo audits for; it lands with the recheck that reads it or not at all.

4. Adjacent, same file, NOT bundled (each is its own finding): `Core.Reconcile` (core.go:121-127) restarts with no `engine.TestConfig` pre-flight while the service path refuses at xray.go:1252; and `Core.hotApply` never calls `kickEgressAfterCoreRestart`, which the service's `tryHotApply` does at xray.go:1311 and `internal/arch/egress_kick_test.go` pins.
**Test:** Do NOT rely on `make race`: GetConfig and SetConfig are each mutex-guarded, so the detector sees no data race — this is a logical lost update. And a pure unit test cannot reach it: `hotApply` (core.go:417) needs a real gRPC HandlerService for any non-empty diff, and `ApplyInstance` gates on `process.IsRunning()` (core.go:182), so a fake child cannot get past it.

New file `internal/cores/internal/xray/apply_race_e2e_test.go`, package `xray` (internal test — it needs `New(Deps{})` and `c.mgr`). Gated exactly like `internal/xray/api_e2e_test.go:25`: `if os.Getenv("XRAY_E2E_BINARY") == ""  { t.Skip(...) }`.

Setup: copy the binary into `t.TempDir()` as `engine.GetBinaryName()` and `t.Setenv("PUI_BIN_FOLDER", dir)` so `engine.GetBinaryPath()` (process.go:40) resolves; build a minimal `*engine.Config` with the api inbound + freedom/blackhole outbounds + the api routing rule + stats, exactly as api_e2e_test.go:30-61, on a free port; `p := engine.NewProcess(cfg); p.Start()`; `t.Cleanup` stops it; `engine.GetManager().Replace(p)` (note in the test that the manager is a process-wide singleton and the cleanup leaves it nil); wait for the api port.

The race — and this is the correction the adversary demanded: BOTH sides are real core methods, so nothing is hand-rolled and nothing runs unlocked after the fix. Because the service half is deferred, the test needs no stand-in for `RestartXray` at all. Release 16 goroutines from one `close(start)` barrier, each calling `c.ApplyInstance(ctx, inst_i)` with a distinct tag `race-<i>`, a distinct free port, kind "vless", one client.

Assertion — the only one:

    got := map[string]bool{}
    for _, ib := range engine.GetManager().Current().GetConfig().InboundConfigs { got[ib.Tag] = true }
    // collect missing race-<i>
    t.Fatalf("the stored config lost %d of %d concurrently applied inbounds: %v", len(missing), n, missing)

RED WITHOUT THE FIX, and here is exactly why: all 16 goroutines read the same base snapshot at core.go:185 before any of them reaches `SetConfig` at core.go:428, because between the two sits a real gRPC round trip to the child (tens of milliseconds — a very wide window). Each then overwrites the snapshot with base+its own tag. Last writer wins. Typical failure: `the stored config lost 14 of 16 concurrently applied inbounds: [race-0 race-1 race-2 ...]`. GREEN WITH THE FIX: each goroutine reads the previous winner's snapshot, so all 16 tags accumulate and `missing` is empty.

Explicitly NOT asserted, and this is worth a comment in the test: a follow-up `ApplyInstance` on a tag the snapshot lost does NOT return `xray: inbound %q cannot be applied in place`. `addInboundReconciling` (internal/xray/hot_apply.go:90-103) tolerates `IsExistingTagErr` by doing `DelInbound`+`AddInbound`, so that path SUCCEEDS — it would be green either way, which is the failure mode this exercise is meant to eliminate. (That tolerance is also the real-world harm: it silently drops every live connection on the inbound.)

Honest limit: `grep XRAY_E2E_BINARY .github/workflows/*.yml` returns nothing, so CI runs this skipped. Run it locally with the api_e2e_test.go recipe. To keep the lock from being deleted silently in a suite where the e2e test skips, add a ~10-line source guard beside `internal/arch/egress_kick_test.go` asserting that the bodies of `ApplyInstance` and `DropInstance` contain `LockApply` — same precedent as `internal/web/service/depletion_lost_update_test.go:110-125`, which pins lock placement by parsing the source.
**Sites:**
- internal/xray/manager.go:23-27 — `type Manager struct { mu sync.RWMutex; process *Process; result string }`: the shared owner both packages reach, with no apply mutex. Add applyMu here. It CANNOT be mu — Replace (manager.go:53) and Snapshot (manager.go:41) are called inside the critical section.
- internal/cores/internal/xray/core.go:180 — `func (c *Core) ApplyInstance(...)`: lock as the first statements; the read-modify-write starts at :185 `next := withInbound(process.GetConfig(), inst)`.
- internal/cores/internal/xray/core.go:193 — `func (c *Core) DropInstance(...)`: same shape, read at :200. Reachable from the depletion cron inside the serial traffic writer (internal/web/service/inbound_disable.go:49 <- inbound_traffic.go:109).
- internal/cores/internal/xray/core.go:103 — `func (c *Core) Reconcile(...)`: lock at line 113, immediately before `process := c.mgr.Current()`, leaving `c.deps.BaseConfig()` at :104 outside it. Production-dead for xray (core_supervise.go:44 skips cores.PanelConvergedCore; core_control.go:46 routes RestartCore away), live in internal/core/coretest.
- internal/cores/internal/xray/core.go:149 — `func (c *Core) StopAll(...)`: unguarded `process.Stop()`. Live via Server.stopCores (internal/web/web.go:788-801, no PanelConvergedCore skip) but only after StopXray already stopped the process — a one-line invariant, not a repair. This is the sibling the adversary found and the original plan missed.
- internal/cores/internal/xray/core.go:412-430 — `func (c *Core) hotApply(...)`: the GetConfig/ApplyHotDiff/SetConfig body. Protected via its four callers; it must NOT take the lock itself (non-reentrant).
- internal/cores/internal/xray/core.go:44-46 — `mu sync.Mutex; api *engine.XrayAPI; apiPort int`: the core's only mutex, taken solely by connect/noteRestart/stashTagTraffic/CollectTagTraffic. Untouched.
- internal/xray/process.go:306,315 — `GetConfig`/`SetConfig`, each under p.mu with no spanning section. Untouched: the fix gives the pair an owner rather than changing them.
- internal/web/service/xray.go:1235 — DEFERRED anchor: `xrayConfig, err := s.GetXrayConfig()`, the DB build that must happen outside the apply lock (deadlock proof in rootCause) and is therefore the staleness window.
- internal/web/service/xray.go:1283-1308 — DEFERRED anchor: `tryHotApply`, the service's copy of the same read-modify-write, doc line at 1281 already says callers must hold the package lock.
- internal/web/service/xray.go:1316 — DEFERRED anchor: `StopXray`; the original plan extended the lock here, this plan does not (see deferred).
- SEARCHED AND CLOSED: `grep -rn 'SetConfig(' --include=*.go internal/ | grep -v _test` returns exactly four writers — core.go:418, core.go:428, xray.go:1289, xray.go:1308 — plus the definition at process.go:315. No fifth writer.
- SEARCHED AND CLOSED: no lockInbound holder reaches the apply path today. client_inbound_apply.go and client_bulk.go hold lockInbound only around AddUser/RemoveUser/UpdateUser, which go to Core.AddUser/RemoveUser (core.go:231-245) and never touch the config snapshot. So the new lock introduces no lockInbound cycle.
- EXCLUDED, not a sibling: Core.AddUser/RemoveUser (core.go:231-245) hit the gRPC API without touching the snapshot; that lag is deliberate and reconciled by addInboundReconciling/addUserReconciling.

**Re-check notes:**
- Q1 — REAL BREAKAGE, and worse than the finding states. The race is live: InboundService.AddInbound runs rt.AddInbound in a postCommitApply closure (internal/web/service/inbound.go:1091) with NO lock — the package `lock` in internal/web/service/xray.go:30 is taken ONLY at xray.go:1227 (RestartXray) and xray.go:1317 (StopXray), so two concurrent HTTP inbound ops genuinely reach Core.ApplyInstance in parallel, as does the depletion cron's DropInstance (inbound_disable.go:49 <- inbound_traffic.go:109). The harm is not merely a forgotten snapshot: hotApply RE-READS process.GetConfig() at core.go:413, not the `next` its caller built at core.go:185. So if the winner's SetConfig lands first, the loser's diff contains the winner's tag in HotDiff.RemovedInboundTags (hot_diff.go:135, and the api-tag guard at :121 does not cover a normal tag) and ApplyHotDiff calls api.DelInbound on it (hot_apply.go:36) — the loser gRPC-DELETES the winner's live listener. A port goes dark until a restart. The finding's own headline (silent re-add dropping connections via addInboundReconciling's IsExistingTagErr tolerance, hot_apply.go:90-103) is the milder half of the harm.
- Q2 — NO behaviour choice smuggled in. The change only serializes the core's own read-modify-write over a snapshot it already owns. No operator-visible semantic changes: no error text, no new failure mode, no timing an operator could prefer. `applyMu` is provably a LEAF lock — everything inside the critical section is c.mgr (RWMutex), process.GetConfig/SetConfig (process.mu), c.mu via connect/noteRestart, and gRPC; the compiler fence (cores live under internal/cores/internal/) makes it impossible for anything under it to take lockInbound or the serial traffic writer, so no lock-order cycle exists in any direction.
- Q3 — GENUINELY RED. Failing assertion: the `missing` set collected over `engine.GetManager().Current().GetConfig().InboundConfigs` is non-empty — typically ~15 of 16 tags absent. Mechanism verified by code trace: all 16 goroutines read the same cfg0 at core.go:185, and between that read and SetConfig at core.go:428 sit ComputeHotDiff + connect() + a real gRPC round trip, so every goroutine writes cfg0+its-own-tag and the last writer wins. The plan is correct that `make race` is BLIND to this — GetConfig (process.go:306) and SetConfig (process.go:315) are each fully mutex-guarded, so this is a logical lost update with no data race. The test also stays hot-appliable: a fresh tag skips every `exists &&` guard in diffInbounds (hot_diff.go:118-134), so REALITY/reverse-client rejections cannot make it red for a bogus reason. Verification here is mechanical code-trace, not a run — no XRAY_E2E_BINARY on this box, and per repo policy a Windows run is not a verification gate.
- Q4 — NO un-named sibling in the core. The SetConfig census is exhaustive and I reproduced it: exactly four writers (core.go:418, core.go:428, xray.go:1289, xray.go:1308) plus the definition at process.go:315. The four core-side call sites cover both core writers. StopAll (core.go:149) is correctly characterised as an invariant, not a repair — web.go:808-810 runs StopXray() before stopCores(), so it always hits the `!process.IsRunning()` early return. The two SERVICE-side writers stay racy, but the finding declares that in bold rather than hiding it, and the core-side lock cannot worsen them (the service path never takes applyMu, so no new interleaving is created).
- Q5 — NOT bigger than the bug. One mutex field, two 1-line methods, four `Lock/defer Unlock` pairs. No new package, column, job, or knob. Placing applyMu on internal/xray.Manager rather than on Core is justified, not gold-plating: internal/web/service cannot import internal/cores/internal/xray (compiler fence), and Manager is the shared owner of the Process both sides write — it is where the deferred half must reach it. Both LockApply and UnlockApply ship with live callers, so this is not the dead seam item 3 rightly refuses.
- NON-BLOCKING inaccuracy #1 (rationale prose only, zero shipped lines): the sentence "a lock that is never held across ... a `process.Start()`" is FALSE at the Reconcile site. Lock at core.go:113 plus `defer` means it IS held across `process.Stop()` (:121) and `process.Start()` (:127). Harmless as specified: xray's Reconcile is production-dead — web.go:370 constructs the supervise job with `cores.PanelConvergedCore` (= xray.ID, cores.go:162) and core_supervise.go:47 skips exactly that id, while core_control.go:46 reroutes RestartCore to RestartXrayService. Where Reconcile IS live (internal/core/coretest) holding the lock across a restart is the correct semantics, so the CODE should stay as written. Reword only if this prose becomes the commit body — it is the load-bearing sentence for "cannot stall a traffic write for more than one gRPC round trip".
- NON-BLOCKING inaccuracy #2 (sites-list search, conclusion unaffected): "no lockInbound holder reaches the apply path today" is imprecise. `applyOneDisable` (internal/web/service/pending_client_disable.go:66) holds lockInbound and, on the remote branch, reaches `rt.UpdateInbound` via `disableRemoteClients` (internal/web/service/inbound_disable.go:351). That resolves to the remote runtime by construction so it never reaches the xray Core, and applyMu is a leaf lock regardless — the no-deadlock conclusion stands; only the search was non-exhaustive.


### F273 — ship-it (small)
**Root:** The adversary is correct on all four objections; I verified each against the source and am adopting its correction.

Corrected root cause: `sendBackupData` (internal/web/service/tgbot/tgbot_report.go:420-462) opens by unconditionally asserting a backup happened — `t.I18nBot("tgbot.messages.hostname")` + `t.I18nBot("tgbot.messages.backupTime", "Time=="+time.Now()...)` sent at :423 — and then has FOUR exits that produce no chat output at all: the dump was never built (`dbData == nil`, from `GetDb` -> `exportPostgresDB` returning `nil, err` at server.go:1519 and :1524), the dump upload was rejected (:434-436, `logger.Error` only), the config upload was rejected (:456-458, `logger.Error` only), and the config could not be built (:459-461, `logger.Error` only). The operator receives "Hostname / Backup Time" and nothing else, and reasonably concludes the file is on its way.

The original plan located the fix in the header and inferred failure from `len(dbData) == 0`. That is the wrong point, and it is the wrong point for a structural reason, not a stylistic one: the header is emitted BEFORE the two sends, so anything computed there can only know about failures that already happened. It can address exactly one of the four exits. "Fix at the sink, once, and both chat paths are covered" is true of the two CALL paths and false of the failure modes.

I also confirmed the premise the original plan used to exclude :434-436 is inverted by the code. `SendMsgToTgbot` (tgbot_send.go:78-140) wraps every send in `maxRetries := 3` with exponential backoff over a 30s context; the document send at :427-433 has a bare 30s context and zero retries. The message path is strictly more robust than the document path, so a document failure is evidence the chat still works, not evidence it is down. :434-436 is the likelier production failure of the two — the dump was built fine and Telegram rejected a large upload — and it was the one exit the original plan argued was unreachable.

Two audit corrections: (a) the original plan's "SEARCHED AND CLEAN" verdict on tgbot_client.go:416 is wrong. The `errorOperation` reporting it cites exists only at :353 and :365 (the sub and subjson QRs). The individual-link loop at :404-418 does `_, _ = bot.SendDocument(...)` discarding the error outright, and its `if png, err := createQR(...); err == nil` has no `else` — so a failure there is silent with not even a log line, after "QRCode for client X:" was already sent. Same claim-then-silence shape, different feature family. (b) `GetDb` can only return `nil` on failure (`bytes.Buffer.Bytes()` on an empty buffer is nil), so `len(dbData) > 0` vs `!= nil` is defensive rather than a live bug — it is free on a line already being edited, and it does stop a zero-length upload attempt.

One premise of a possible objection I checked and can rule out: the periodic path (`SendReport` -> `SendBackupToAdmins`, scheduled by `StatsNotifyJob` from the `tgBotRuntime` cron setting, defaulting to `@daily` per web.go:419-422) cannot spam. At most one notice per admin per configured run, and an operator who asked for a daily backup wants to know it did not arrive. So no alert-fatigue decision blocks the DB arm.
**Ships now:** Two one-line additions inside `sendBackupData`, reusing ONE new i18n key. No extraction, no new function, no signature change, no call-site change, no DB/model change, no new endpoint. Both Telegram entry points (`SendBackupToAdmins` and the `get_backup` button) are covered because both funnel through this sink.

EDIT 1 — internal/web/service/tgbot/tgbot_report.go:426, change the guard and give it an else:
    if len(dbData) > 0 {
        ... existing SendDocument block, unchanged ...
    } else {
        t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.backupDbFailed"))
    }
Covers the pg_dump-missing / bad-DSN / non-zero-exit case, and stops a zero-length upload attempt.

EDIT 2 — internal/web/service/tgbot/tgbot_report.go:434-436, one line appended inside the branch that already exists:
    if err != nil {
        logger.Error("Error in uploading backup: ", err)
        t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.backupDbFailed"))
    }
Same key, no second key. This is the site the original plan excluded and the likelier production failure: the message path retries three times with backoff (tgbot_send.go:118) while this document send has zero retries, so a document failure is evidence the chat is up.

EDIT 3 — the key, in BOTH locale files, inserted immediately after `"backupTime"` (line 2091) under `tgbot.messages`:
  internal/web/translation/en-US.json:
    "backupDbFailed": "⚠️ Database backup failed to send — check the panel log.\r\n",
  internal/web/translation/fa-IR.json: the same sentence in real Persian (not a placeholder, not the English string) — e.g.
    "backupDbFailed": "⚠️ ارسال پشتیبان پایگاه‌داده ناموفق بود — گزارش پنل را بررسی کنید.\r\n",
The wording deliberately fits BOTH modes ("never built" and "built but did not arrive") so one key serves two sites. The Go literal is the same-commit reference `frontend/src/test/i18n-dead-keys.test.ts` requires — verified it scans `internal/**/*.go` (i18n-dead-keys.test.ts:69, `collectSources(join(repoRoot, 'internal'), ['.go'], goSources)`), so no frontend reference is needed.

Deliberately NOT passing `err` into the message: it would force a signature change up through the `SendBackupToAdmins` loop, and it would ship raw `pg_dump` stderr — which `exportPostgresDB` builds from the DSN-derived connection (server.go:1524) — into a chat. The detail already lands in `logger.Error`.

Undisputed: the header at :423 has already told the admin a backup is being taken. No reasonable operator prefers being told a backup happened and then silently receiving nothing. Every alternative wording still beats the status quo, so the wording is not a gate.
**Deferred:** Three things, deliberately out of this commit.

1. THE CONFIG.JSON ARM of the same function (:456-458 upload rejected, :459-461 build failed). Left silent pending the decision below. Reason it is not shippable alongside the DB arm: `configBackup` falls back to `os.ReadFile(xray.GetConfigPath())` (:473) when no core is running, so on a panel that has never started Xray the build failure at :459 is a NORMAL state, not an incident. Wiring a notice there turns every scheduled report on such a panel into a recurring false alarm. It also cannot reuse `backupDbFailed` — that string says "database" — so it needs a second key in both locale files, and which of the two exits earns it is a behaviour choice, not a repair. The DB arm removes the harm the finding actually names; this arm is the smaller, ambiguous remainder.

2. THE QR LOOP, internal/web/service/tgbot/tgbot_client.go:404-418. Same claim-then-silence shape (the "QRCode for client X:" header at :349 promises images; a `createQR` failure inside the loop has no `else` and the send error is discarded with `_, _ =`), but it is the client-links feature, not the backup family, and fixing it means deciding whether a per-link failure is worth a chat line at all when up to five are sent in a burst. Correcting the record matters more than fixing it here: the original plan listed this line under "SEARCHED AND CLEAN" on the strength of `errorOperation` handling that exists only at :353 and :365. It should be raised as its own finding.

3. `POST /panel/api/backuptotgbot` — internal/web/controller/api.go:152-155 writes no response body and returns 200 regardless. Real, but it is an HTTP contract change (a `jsonMsg` body, whether the handler becomes synchronous, and the resulting refresh of `frontend/src/pages/api-docs/endpoints.ts` + `make gen` + the docs/public/openapi.json copy step). That is a different fix at a different layer from a chat notice, and bundling it would violate fix-size-matches-bug-size.

Also deliberately not done: no `backupHeader` extraction (the original plan's, introduced only so a test could exist — the httptest design above removes the need), no error text threaded into chat, no new column, job, config knob or abstraction.
**Test:** A real behavioural test that drives `sendBackupData` end to end against a fake Bot API — NOT a table over a helper mirroring the implementation's own condition. New file `internal/web/service/tgbot/tgbot_backup_notice_test.go`, package `tgbot`, with an F273 `/* */` header in the style this package already uses (draft_sender_guard_test.go).

Feasibility verified, not assumed:
- `telego.WithAPIServer(url)` exists (bot_options.go:111) and is already used by this repo (tgbot.go:430). `telego.NewBot` performs NO network call (bot.go:93-116) and only regex-checks the token against `^\d+:[\w-]{35}$` (bot.go:26), so `"123456:" + strings.Repeat("A", 35)` is accepted.
- Request URL is `apiURL + "/bot" + token + "/" + methodName` (bot.go:240), so one `httptest.Server` handler dispatches on the path suffix `sendMessage` / `sendDocument`.
- `bot` and `isRunning` are package-level vars (tgbot.go:32, :43); the test sets both and restores both in `t.Cleanup`. `SendMsgToTgbot` returns early on `!isRunning` (tgbot_send.go:82), so `isRunning = true` is mandatory or the test is vacuous.
- Determinism leak closed: `configBackup(nil, xray.GetConfigPath())` runs unconditionally at :447. `config.GetBinFolderPath()` reads `PUI_BIN_FOLDER` per call (config.go:131-137), so `t.Setenv("PUI_BIN_FOLDER", t.TempDir())` guarantees the config arm always errors out and never emits a stray `sendDocument`. (`t.Setenv` forbids `t.Parallel()` — this package uses neither, fine.)
- `service.XrayProcess()` returns nil with nothing initialised, and `t.I18nBot` -> `locale.I18n` returns the key itself when `LocalizerBot` is nil (locale.go:90-93). I grepped: no test in the repo calls `InitLocalizer`, so the localizer is nil in this test binary.

Two subtests, both counting `sendMessage` bodies the fake server received:
- A, "the dump was never built": handler answers `sendMessage` with `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`. Call `(&Tgbot{}).sendBackupData(1, nil, "x.dump")`. Assert exactly 2 `sendMessage` bodies, the second != the first, and 0 `sendDocument` calls (proves the `len()>0` guard also stops the empty upload).
- B, "the dump was built and Telegram rejected it": same handler, but `sendDocument` returns HTTP 400 with `{"ok":false,"error_code":413,"description":"Request Entity Too Large"}`. Call `sendBackupData(1, []byte("PGDMP\x00"), "x.dump")`. Assert exactly 2 `sendMessage` bodies and 1 `sendDocument`.

RED WITHOUT THE FIX, stated exactly. Revert either edit and the corresponding subtest goes red on the current code, not on a compile error:
- A on today's code: `dbData != nil` is false, no else exists, so only the header is sent. The recorder holds 1 body. Failure message: `backup failed silently: got 1 sendMessage (the Hostname/Time header), want 2 (header + failure notice); the admin was told a backup was taken and nothing arrived`.
- B on today's code: the header is sent, `SendDocument` returns the 400, `:435` logs and falls through. The recorder holds 1 body. Same message, distinguished by subtest name.
Reverting only EDIT 1 reds A alone; reverting only EDIT 2 reds B alone — so the two subtests pin the two edits independently, which is precisely what the header-extraction test could not do.

Assertion is on message COUNT and distinctness rather than on the literal key string, so it stays green if a future test ever initialises the localizer and `I18nBot` starts returning translated text instead of the key. Optionally also assert the second body is non-empty; do NOT assert on the English wording.

Not covered by design (see deferred): the two config.json exits at :456-461. The test must not silently appear to cover them — the F273 docblock says so.
**Sites:**
- internal/web/service/tgbot/tgbot_report.go:426 — `if dbData != nil {` — EDIT SITE 1. The guard whose missing `else` turns a failed pg_dump into silence; also the wrong predicate (a zero-length slice would be uploaded).
- internal/web/service/tgbot/tgbot_report.go:434-436 — EDIT SITE 2, the sibling the original plan wrongly excluded. `_, err := bot.SendDocument(ctx, document)` / `if err != nil { logger.Error("Error in uploading backup: ", err) }` — no chat output. Zero retries here, unlike the message path.
- internal/web/service/tgbot/tgbot_report.go:421-423 — the header that asserts success (`hostname` + `backupTime` + `t.SendMsgToTgbot(chatId, output)`). NOT edited; it is what makes the four silent exits actively misleading rather than merely quiet.
- internal/web/service/tgbot/tgbot_report.go:456-458 — DEFERRED sibling: `if err != nil { logger.Error("Error in uploading config.json: ", err) }` — config archive rejected by Telegram, silent in chat.
- internal/web/service/tgbot/tgbot_report.go:459-461 — DEFERRED sibling: the `else` of `configBackup`, `logger.Error("Error in building config.json backup: ", err)` — silent, and reached normally on a panel that has never started Xray (os.ReadFile of a nonexistent GetConfigPath()).
- internal/web/service/tgbot/tgbot_report.go:52-55 — `SendBackupToAdmins`: `dbData, err := t.serverService.GetDb()` / `if err != nil { logger.Error(...) }` with no `return`; nil is passed to the sink for every admin in the loop at :57-63. NOT edited — the sink fix covers it.
- internal/web/service/tgbot/tgbot_report.go:413-416 — `sendBackup`, the `get_backup` button path (tgbot_router.go:1177). Identical three lines, same nil hand-off. NOT edited — the sink fix covers it.
- internal/web/service/server.go:1519 and :1524 — `exportPostgresDB` returns `nil, common.NewError("pg_dump not found on the server; …")` and `nil, common.NewErrorf("pg_dump failed: %v: %s", …)`. Confirms nil dump == failure, and confirms the stderr must not be forwarded to chat (it can carry DSN detail).
- internal/web/service/tgbot/tgbot_send.go:82 and :118 — `if !isRunning { return }` and `maxRetries := 3` with backoff. The evidence that inverts the original plan's exclusion of :434-436, and the reason the test must set `isRunning`.
- internal/web/service/tgbot/tgbot_client.go:404-418 — CORRECTED sibling (the original audit mis-described this line): the individual-link QR loop does `_, _ = bot.SendDocument(...)` discarding the error, and `if png, err := createQR(cleaned[i], 320); err == nil` has NO `else`. Silent with no log line, after "QRCode for client "+email+":" at :349 already promised images. DEFERRED — different feature family.
- internal/web/controller/api.go:152-155 — `func (a *APIController) BackuptoTgbot(c *gin.Context) { a.Tgbot.SendBackupToAdmins() }` — no JSON body written at all; the HTTP caller gets 200 with an empty body whether or not anything was sent. DEFERRED — HTTP contract change.
- internal/web/translation/en-US.json:2091 and internal/web/translation/fa-IR.json:2091 — the `"backupTime"` line under `tgbot.messages` in both files; the new key goes immediately after it in each.
- internal/web/service/tgbot/draft_sender_guard_test.go and message_sender_test.go — precedent, checked: this package already carries F-numbered `/* */` docblock test headers, so the test header style below follows repo precedent rather than violating the 2-line comment rule.

**Re-check notes:**
- Q1 — Real breakage, not cosmetic. Verified tgbot_report.go:421-423 unconditionally sends the Hostname + backupTime header, then :426 guards the dump on `dbData != nil` with no else, and :434-436 logs an upload failure with no chat output. Verified server.go:1518-1534 `exportPostgresDB` returns `nil, err` on both pg_dump-missing and non-zero-exit, and server.go:1371 `GetDb` is a bare passthrough — so nil dump is exactly the failure signal, and the admin is told a backup was taken and receives nothing.
- Q2 — No smuggled behaviour choice. The notice fires only on paths the admin opted into: SendReport gates on `GetTgBotBackup()` (tgbot_report.go:41-44) or the explicit `get_backup` button. Withholding `err` is correct, not conservative: server.go:1534 builds the error from `stderr` of a pg_dump run whose connection comes from the DSN, so forwarding it into a chat would leak connection detail. The `!= nil` → `len() > 0` change is strictly safer, not a choice — a successful custom-format pg_dump is never zero-length, and a 0-byte upload would be rejected by Telegram anyway.
- Q3 — Genuinely RED, and the two subtests pin the two edits independently. Subtest A: on today's code `dbData != nil` is false with no else, so the recorder holds 1 sendMessage; the assertion `got 1 sendMessage, want 2` fails. Subtest B: the header sends, SendDocument gets the 400, :435 logs and falls through — same 1-vs-2 count failure. Revert only EDIT 1 and A reds; revert only EDIT 2 and B reds.
- Q3 feasibility — every mechanism the test depends on was checked, including the two places this plan could have been quietly wrong. locale.go returns the KEY (not "") when the localizer is nil, so the header is non-empty and SendMsgToTgbot's `msg == ""` early-return (tgbot_send.go:85) does not fire and the test is not vacuous; the failure notice is a distinct key, so the count is genuinely 2. i18n-dead-keys.test.ts:69 does scan `internal/**/*.go`, and its GO_KEY_DECLARATION stripper only removes bare `ident = "dotted.key"` lines — a key inside a `t.I18nBot(...)` call survives as a token, so the Go literal alone satisfies the same-commit reference rule. telego v1.11.1 confirmed: NewBot (bot.go:93-115) constructs the struct and applies options with no network call, tokenRegexp is `^\d+:[\w-]{35}$`, WithAPIServer exists at bot_options.go:111, and the URL is `apiURL + "/bot" + token + "/" + methodName` (bot.go:240). Determinism holds: xray.GetConfigPath() = config.GetBinFolderPath() + "/config.json" and GetBinFolderPath reads PUI_BIN_FOLDER per call, and xray.Manager's zero value means service.XrayProcess() is nil in a bare test binary — so the config arm always errors into the log-only else and emits no stray sendMessage/sendDocument. No TestMain exists in the tgbot package, so nothing gates the new test behind PUI_DB_DSN. logger is initialized to a usable default at package var (logger.go:34), so logger.Error is safe uninitialized.
- Q4 — Yes, siblings stay live, all three disclosed rather than smuggled, and I confirmed each. tgbot_report.go:456-458 and :459-461 (both config.json exits, still log-only). The deferral reasoning holds up on inspection: configBackup falls back to os.ReadFile(GetConfigPath()) at :473, so on a panel that never started Xray the :459 failure is a normal state, and wiring a notice there would make every scheduled report a false alarm — that is a behaviour choice, unlike the DB arm. Note the shipped wording ("Database backup failed") does not over-claim, so the fix does not mask the still-silent config arm. tgbot_client.go:404-418 confirmed exactly as re-described: the individual-link loop discards the send error with `_, _ =` and `if png, err := createQR(...); err == nil` has NO else — the errorOperation handling exists only on the sub/subjson QRs at :353 and :365, so the original plan's "SEARCHED AND CLEAN" was wrong and the correction is right. api.go:152-155 confirmed to write no body at all.
- Q5 — Not bigger than the bug. Two lines in one function, one key in the two locale files, one test file. No extraction, no signature change, no new endpoint, no migration, no config knob.
- Nit, not a correction and not a gate: have the fake Bot API handler set `Content-Type: application/json` on its responses. Cheap insurance for telego's response decoding; it does not affect the verdict.


### F292 — ship-it (small)
**Root:** The adversary is right on all four counts; I re-verified every line and found no error in its claims. Corrected root cause: `tgBotMutex` (tgbot.go:37) was introduced to guard `botCancel` and was then extended to `adminIds` and `isRunning` on the WRITE side only, so the mutex documents an invariant the reads do not keep. `adminIds` (tgbot.go:42) is written under the lock at tgbot.go:290 (Start) and tgbot.go:460 (Stop) and read with nothing at five sites, one of which is the authorisation gate `checkAdmin` (tgbot_router.go:1598). `isRunning` (tgbot.go:43) is written under the lock at tgbot_router.go:34 and tgbot.go:473, read under the lock at tgbot.go:325 and tgbot.go:440 (`IsRunning`), and read BARE at tgbot_send.go:82 and tgbot_send.go:177 — the identical shape, one line below in the same var block, same mutex. They are one bug, not two.

The trigger is live, not theoretical: `SetReloadTgbotFunc` (internal/web/web.go:722-741) runs on an admin saving Telegram settings and calls either `Stop()` (adminIds = nil) or `Start()` (adminIds = fresh slice). The reason "Start drains everything first" does not save it: `Start` calls `StopBot()` (tgbot.go:243), `StopBot` waits on `botWG` (tgbot.go:487), but `botWG` covers only the dispatch goroutine (`go func(){ defer botWG.Done(); ... _ = h.Start() }()`, tgbot_router.go:41-183). The goroutines that call `checkAdmin` are bare `go func(){...}()` spawned INSIDE the handler closures at tgbot_router.go:61-67 and :73-80, never added to botWG and carrying no recover — the only `recover()` in the package is tgbot.go:337 (`trySetBotCommands`). So workers outlive StopBot by design, and a slice-header write races their read.

Correction to the original plan's framing (adversary objection b, which I accept): this fix removes the torn-slice-header crash at the auth gate. It does NOT retire worker-goroutine process death in general. `bot` (tgbot.go:31) is assigned unlocked at tgbot.go:315 by `bot, err = t.NewBot(...)`, and telego returns a nil *Bot on error, so a reload with a malformed proxy URL or token sets `bot = nil` while old workers are still live; `editMessageCallbackTgBot` (tgbot_send.go:190) and `editMessageTgBot` (tgbot_send.go:202) have neither a nil check nor an `isRunning` guard, unlike `deleteMessageTgBot` (tgbot_send.go:252) and `TestConnection` (tgbot_send.go:267-272) which both do. That remaining path is what the decision question covers.

Deadlock analysis (load-bearing, re-verified): `tgBotMutex` is a non-reentrant `sync.Mutex` held in exactly nine regions — tgbot.go:289-291, :324-326, :438-439, :459-461, :467-474; tgbot_router.go:25-27/:38, :45-47; tgbot_send.go:268-270. None of them calls `checkAdmin`, `SendMsgToTgbotAdmins`, `SendBackupToAdmins`, `sendExhaustedToAdmins`, `SendMsgToTgbot` or `sendCallbackAnswerTgBot`, so both the new accessor and the switch to `t.IsRunning()` are deadlock-free. `StopBot` also unlocks at tgbot.go:474 before `botWG.Wait()` at :487, so a reader blocking on the mutex cannot wedge shutdown.
**Ships now:** Close `adminIds` and `isRunning` completely, in one commit. No behaviour choice anywhere in this set; every write site already takes the mutex, so this only makes the readers keep the invariant the writers already assert.

1) One accessor in internal/web/service/tgbot/tgbot.go, next to the writes:

    // Both writes publish a fresh slice and nothing appends to the published
    // one, so the live header can be returned without a copy. F292.
    func currentAdminIds() []int64 {
        tgBotMutex.Lock()
        defer tgBotMutex.Unlock()
        return adminIds
    }

(That comment is the fix for adversary objection d: the no-copy premise is now pinned in the source, not only in a plan. I re-grepped — no `append(...adminIds...)` exists anywhere in the repo.) Leave both writes at tgbot.go:289-291 and :459-461 exactly as they are.

2) Snapshot once into a local at each of the five reads — never inline the call into a loop header:
 - tgbot_router.go:1598 → `return slices.Contains(currentAdminIds(), tgId)`
 - tgbot_report.go:56-62 → `admins := currentAdminIds()` above the loop, then `for i, adminId := range admins` and `if i < len(admins)-1`. This also removes a live inconsistency: today the range target and the `len()` at :60 are two separate reads of a var that can change between them.
 - tgbot_report.go:71 → `for _, adminId := range currentAdminIds()`
 - tgbot_send.go:161-170 → hoist `admins := currentAdminIds()` above the `if len(replyMarkup) > 0` branch and range over it in both arms.

3) The sibling, decided and not conditional — `isRunning` ships here:
 - tgbot_send.go:82 → `if !t.IsRunning() {`
 - tgbot_send.go:177 → `if !t.IsRunning() {`
`IsRunning()` already exists at tgbot.go:437-441 and already takes the mutex, so this adds no new code. These are the only two bare reads; after them `isRunning` is fully guarded, which is what makes the source ratchet in the test strategy a real ratchet rather than a partial one.

Net diff: one 5-line function plus its 2-line comment, five snapshot edits, two token substitutions. No new column, job, config knob, i18n key or abstraction; no runtime.Runtime involvement (this is bot-internal state, not an inbound/client op); nothing to migrate.
**Deferred:** `bot` (tgbot.go:31) is deliberately left out even though it is the same disease and the higher-severity one, because closing it requires a behaviour choice — see decisionQuestion. Three genuinely different fixes exist (don't clobber `bot` on a failed reload / nil-guard the two edit helpers / full locked accessor), they have different reload semantics, and picking one silently is exactly what the fix-size rule forbids. Shipping the adminIds+isRunning commit does not make the `bot` fix harder: the accessor pattern and the ratchet test both extend to it in one line each.

`hostname` (tgbot.go:448/:451), `hashStorage` (tgbot.go:245), `messageWorkerPool` (tgbot.go:248) and `optimizedHTTPClient` (tgbot.go:251) are all real unsynchronised reassign-on-Start races and all confirmed, but each is its own finding with its own blast radius and none is a torn read on a security decision: `hashStorage` degrades to an already-handled "hash not found in storage!" error, `messageWorkerPool` degrades to an eroded concurrency limit, `optimizedHTTPClient` to a stale connection pool, `hostname` to a torn string header in a display field. Pulling four more variables into an F292 commit would be new architecture wearing a bug fix's clothes. They should be filed as their own findings — none exists in audit-appendix today, which is why dependsOn is empty.

Also explicitly NOT claimed: this commit does not retire "a worker goroutine can kill the panel". It retires the torn-slice-header path at the auth gate. The nil-`bot` path survives until the decision lands. The original plan implied otherwise and was right to be rejected for it.
**Test:** Two tests, and the order is deliberate: the one that runs in the repo's own gate comes first, because `make verify` is `gen-check lint typecheck test build build-storybook` (Makefile:86) and does NOT include `-race`.

TEST 1 — the source ratchet, RED under plain `go test` / `make test-go` / `make verify`. New file internal/web/service/tgbot/bot_state_guard_test.go, same shape and same justification as the package's existing draft_sender_guard_test.go and message_sender_test.go:67 (`readRouterSource`): the property is "no unsynchronised read of this variable exists anywhere in the package", and absence of a racy read cannot be proved by driving one code path — it is a source-level property, so a source assertion is the correct shape here, not a shortcut. Walk every non-`_test.go` .go file in the package directory and fail on `regexp.MustCompile(\`\badminIds\b\`)` matching any file except tgbot.go, and on `\bisRunning\b` matching any file except tgbot.go and tgbot_router.go. Word boundaries make this exact: `currentAdminIds` and `parsedAdminIds` carry a capital A and `t.IsRunning()` a capital I, so neither matches.

What it prints TODAY, before the fix (four separate failures, one per offending file):
    bot_state_guard_test.go:NN: unsynchronised read of adminIds in tgbot_router.go; call currentAdminIds() instead (F292)
    bot_state_guard_test.go:NN: unsynchronised read of adminIds in tgbot_report.go; call currentAdminIds() instead (F292)
    bot_state_guard_test.go:NN: unsynchronised read of adminIds in tgbot_send.go; call currentAdminIds() instead (F292)
    bot_state_guard_test.go:NN: unsynchronised read of isRunning in tgbot_send.go; call t.IsRunning() instead (F292)
Green only after all five snapshot edits AND both isRunning substitutions land — a half-applied fix stays red, which is the point. It needs no PUI_DB_DSN and no bot, so it runs on every box.

TEST 2 — the actual race, the proof. `TestCheckAdminIsSafeWhileTheBotStops` in the same package. Seed `adminIds` under the lock, run four goroutines calling the real `checkAdmin(4242)` in a loop, and on the main goroutine alternate `tgBotMutex.Lock(); adminIds = []int64{1,2,3}; tgBotMutex.Unlock()` with 200 iterations of the real production writer `tg.Stop()`; then close the stop channel and Wait. `(&Tgbot{}).Stop()` is safe with no bot and no database — I verified each step: StopBot finds `botCancel == nil` and `botHandler == nil` so it neither cancels nor calls botWG.Wait(); `userStateMgr.reset()` (tgbot.go:123-127) only takes its own mutex over a map initialised at package scope (tgbot.go:102); `logger.Info` is safe before InitLogger because internal/logger/logger.go:33 initialises the logger to a usable default for exactly this reason. `t.Cleanup` restores `adminIds = nil` under the lock, because the suite runs `-shuffle=on`. It uses only symbols that exist both before and after the fix (`checkAdmin`, `adminIds`, `tgBotMutex`, `Tgbot.Stop`), so the red/green flip is honest and not a compile error masquerading as a failure.

STATE PLAINLY, because prior plans were rejected for hiding this: TEST 2 IS RED ONLY UNDER `-race`. Without the detector it passes both before and after the fix. Its gate is `make race` (Makefile:57) and CI's race job, which runs `go test -race -shuffle=on -count=1 -timeout 30m -skip 'Scale$'` over every package including this one (.github/workflows/ci.yml:233). Under `-race` it fails today with:
    ==================
    WARNING: DATA RACE
    Read at 0x0000... by goroutine 8:
      github.com/Arman2122/p-ui/internal/web/service/tgbot.checkAdmin()
          .../internal/web/service/tgbot/tgbot_router.go:1598 +0x...
    Previous write at 0x0000... by goroutine 1:
      github.com/Arman2122/p-ui/internal/web/service/tgbot.(*Tgbot).Stop()
          .../internal/web/service/tgbot/tgbot.go:460 +0x...
    ==================
    --- FAIL: TestCheckAdminIsSafeWhileTheBotStops (0.NNs)
        testing.go:1490: race detected during execution of test
    FAIL    github.com/Arman2122/p-ui/internal/web/service/tgbot
and passes clean after, because `checkAdmin` then acquires the same mutex the write holds. That asymmetry is precisely why Test 1 exists: Test 1 is the gate-visible ratchet a future refactor trips locally, Test 2 is the evidence the race is real.
**Sites:**
- internal/web/service/tgbot/tgbot.go:42 — `adminIds []int64`, package-level, no accessor
- internal/web/service/tgbot/tgbot.go:289-291 — guarded write `tgBotMutex.Lock(); adminIds = parsedAdminIds; tgBotMutex.Unlock()` in Start
- internal/web/service/tgbot/tgbot.go:459-461 — guarded write `tgBotMutex.Lock(); adminIds = nil; tgBotMutex.Unlock()` in Stop
- internal/web/service/tgbot/tgbot_router.go:1598 — `return slices.Contains(adminIds, tgId)`, unguarded read; this is checkAdmin, the auth gate
- internal/web/service/tgbot/tgbot_report.go:57 — `for i, adminId := range adminIds {` unguarded (SendBackupToAdmins)
- internal/web/service/tgbot/tgbot_report.go:60 — `if i < len(adminIds)-1 {` a SECOND unguarded read of the same var in the same loop
- internal/web/service/tgbot/tgbot_report.go:71 — `for _, adminId := range adminIds {` unguarded (sendExhaustedToAdmins)
- internal/web/service/tgbot/tgbot_send.go:163 and :167 — `for _, adminId := range adminIds {` unguarded, both arms of SendMsgToTgbotAdmins
- internal/web/service/tgbot/tgbot_router.go:61-67 and :73-80 — bare `go func(){ messageWorkerPool <- struct{}{}; ...; checkAdmin(...) }()`, never added to botWG, no recover; the only recover in the package is tgbot.go:337
- internal/web/service/tgbot/tgbot.go:483-487 — StopBot's `botWG.Wait()` covers only the dispatch goroutine registered at tgbot_router.go:36/:42, not those workers
- internal/web/web.go:722-741 — SetReloadTgbotFunc: the live trigger, an admin saving Telegram settings calls Stop() or Start()
- SIBLING isRunning (SHIPS IN THIS COMMIT) internal/web/service/tgbot/tgbot.go:43 — declaration, one line below adminIds
- SIBLING isRunning internal/web/service/tgbot/tgbot_router.go:34 and tgbot.go:473 — guarded writes; tgbot.go:325 and tgbot.go:440 guarded reads (IsRunning accessor already exists)
- SIBLING isRunning internal/web/service/tgbot/tgbot_send.go:82 — `if !isRunning {` BARE read in SendMsgToTgbot
- SIBLING isRunning internal/web/service/tgbot/tgbot_send.go:177 — `if !isRunning {` BARE read in sendCallbackAnswerTgBot
- SIBLING bot (DEFERRED, see decision) internal/web/service/tgbot/tgbot.go:315 — `bot, err = t.NewBot(...)` UNLOCKED write; telego returns nil on error, so a failed reload nils the live bot
- SIBLING bot reads, all unlocked: tgbot.go:317; tgbot_router.go:40, :43; tgbot_send.go:128, :184, :196, :212, :226, :259. Guarded/nil-checked only at tgbot_send.go:252 (deleteMessageTgBot) and :267-272 (TestConnection)
- SIBLING bot: tgbot_send.go:190 (editMessageCallbackTgBot) and :202 (editMessageTgBot) have NO isRunning guard and NO nil check — the reachable nil-deref in a recover-less worker
- SIBLING hashStorage (DEFERRED) internal/web/service/tgbot/tgbot.go:245 — unlocked reassign on every Start; unlocked reads at tgbot.go:189, :499, :504, :508
- SIBLING hostname (DEFERRED) internal/web/service/tgbot/tgbot.go:448 and :451 — unlocked writes in SetHostname (called from Start at :262); unlocked string-header reads at tgbot_report.go:119, :421, :479, tgbot_router.go:212, tgbot_client.go:172-173
- SIBLING messageWorkerPool (DEFERRED) internal/web/service/tgbot/tgbot.go:248 — unlocked reassign on every Start; read twice per worker at tgbot_router.go:62/:63 and :74/:75, so a reload between acquire and release releases into the NEW channel and permanently inflates its free slots
- SIBLING optimizedHTTPClient (DEFERRED) internal/web/service/tgbot/tgbot.go:251 — unlocked reassign on every Start; unlocked reads at tgbot_client.go:273 and :384


### F304 — ship-with-the-noted-correction (small)
**Root:** Three panel functions unmarshal a config block into a Go map and then WRITE BACK into it, without ever checking that the unmarshal produced a non-nil map. `json.Unmarshal` of a literal JSON `null` into a map leaves it nil with a nil error, so the write is `assignment to entry in nil map` — a panic, not a fallthrough.

Two of the three sit on the config-generation path (`GetXrayBaseConfig`, xray.go:107-120) which `Server.Start -> startTask -> RestartXray` (web.go:744 -> 350) reaches SYNCHRONOUSLY on the main goroutine. There is no `recover()` anywhere in `internal/web/web.go` and `main.go:76` calls `server.Start()` bare, so the panic kills the process. Under systemd that is a boot crash loop with no reachable web UI to undo the template. (Cron is safe — `cron.Recover` is wired at web.go:553; HTTP is safe — `gin.Default()` at web.go:169 includes Recovery.)

The `"policy": null` / `"api": null` shape is not exotic: `internal/xray/config.go:18-20` declares Policy/API/Stats as `json_util.RawMessage` with NO `omitempty`, and `internal/util/json_util/json.go:13-17` marshals an empty RawMessage as literal `null`. So the panel itself writes `"policy": null` into `GetBinFolderPath()/config.json` (xray/process.go:46) for any template lacking the key — the exact file operators are asked to paste back into Xray Settings. `CheckXrayConfig` (xray_setting.go:62-65) accepts it: `json.Unmarshal([]byte("null"), &xray.Config{})` returns nil error (verified).

The original plan's framing — "ensureStatsPolicy must GUARANTEE per-user counting" — is where it went wrong. Verified in the module cache (xray-core@v1.260327.1-0.20260728075948): `app/dispatcher/default.go:163` gates on `p.Stats.UserUplink` AND on `d.stats.GetOrRegisterCounter(name)` returning non-nil; `infra/conf/xray.go:564` builds a stats app only `if c.Stats != nil`; `core/xray.go:219` then installs `stats.NoopManager{}`, whose `GetOrRegisterCounter` returns `(nil, "not implemented")` (features/stats/stats.go:140). Nothing in the tree ever sets `Config.Stats` (only outbound/probe_http.go:437, an unrelated probe). So synthesizing `policy` alone does not deliver counting — it is half a pair.
**Ships now:** THREE nil-map guards, one atomic commit, ZERO behaviour change for any input that does not panic today. Verified in a byte-identical replica: every existing pinned case produces identical output; only the three `null` cases change, from panic to unchanged pass-through.

1. internal/web/service/xray.go:914 — `if err := json.Unmarshal(policy, &parsed); err != nil || parsed == nil {` (add `|| parsed == nil`). Kills the panic at :940.
2. internal/web/service/xray.go:857 — `if err := json.Unmarshal(api, &parsed); err != nil || parsed == nil {`. Kills the twin at :877.
3. internal/web/service/xray_setting.go:298 — after the unmarshal, insert `if cfg == nil { return raw, nil }` (2 lines). Kills the twin at :347.

Idiom already shipped in this repo at internal/web/service/inbound.go:868.

Doc-comment touch-up only, no growth (2-line rule): xray.go:906 "An entirely missing or unparsable policy block" -> "A missing, null, or unparsable policy block".

THEY MUST LAND TOGETHER, and this is the load-bearing reason: today EnsureStatsRouting's panic is what ACCIDENTALLY blocks a literal-`null` template from ever reaching the DB (SaveXraySetting passes CheckXrayConfig, then panics at xray_setting.go:347 into gin's Recovery -> 500, save aborted). Guard #3 alone lets `null` persist and the next boot bricks on #1. Guards #1+#2 alone leave the recovered 500. Only all three are safe.

No new file, no helper, no config knob, no i18n key, no endpoint, no migration. Nothing goes near runtime.Runtime — these are pure config-text functions.
**Deferred:** Everything that changes output for an input that currently passes through. All of it is one behaviour question (see decisionQuestion), and none of it is needed to stop the crash loop.

1. SYNTHESIZING an absent/null `policy` block. Reverses a punt that is written down (xray.go:906-907) and pinned by two tests: config_integrity_test.go:97 `TestAnEmptyPolicyIsLeftAlone` and xray_config_inject_test.go:119 (`ensureStatsPolicy(nil)` must stay nil). Reversing pinned, commented intent is the owner's call.

2. SYNTHESIZING an absent `stats` block. This is the adversary's decisive point and it is correct: without `"stats": {}` xray-core installs NoopManager and every counter is nil regardless of the policy flags, so #1 without #2 ships a guarantee that does not hold. #2 alone is also the larger intrusion — it adds an app the operator removed.

3. The `{"levels":{"0":null}}` hole (xray.go:921-926). Verified: `_, ok := levels["0"]` is TRUE for a null value, so no level is inserted; the range loop's `raw.(map[string]any)` then fails and `changed` stays false, so the input returns unchanged, xray-core skips the nil level (infra/conf/policy.go:80 `if p != nil`), and `app/policy/manager.go:39` falls through to `SessionDefault()` — all three flags false. The adversary called this "a hole in the declared contract", but the contract it punctures is the ORIGINAL PLAN'S REFRAME ("must GUARANTEE"), and that reframe is itself deferred item #1. Under the conservative guards no new contract is declared, so there is no hole to fill — and if the owner picks KEEP THE CARVE-OUT, a null level should stay untouched for the same reason an absent block does. It rides with option A or not at all.

4. Rejecting a literal-`null` template in CheckXrayConfig. That is new validation, i.e. a choice, not a repair. Out of scope.

5. A repo-wide sweep of the nil-map-write pattern. `grep 'var [a-z]* map\[string\]'` over internal/ returns 60+ sites; that is a separate finding, not this fix. I did sweep the F304 blast radius and it is clean: `resolveXrayLogPaths` (xray.go:948) and `stripDisabledRules` (xray.go:986) both bail before any write on nil input, and `EnsureDnsServerRouting` (xray_setting_dns_routing.go:330-332) already returns early when routing is nil. Only the three named functions are unsafe.
**Test:** One new test in the existing internal/web/service/xray_config_inject_test.go (package service, pure unit — no Postgres, no PUI_DB_DSN, no XRAY_E2E_BINARY):

  func TestNullConfigBlocksDoNotPanic(t *testing.T) {
      t.Run("policy", ...)   ensureStatsPolicy(json_util.RawMessage("null"))  must == "null"
      t.Run("api", ...)      ensureAPIServices(json_util.RawMessage("null"))  must == "null"
      t.Run("template", ...) EnsureStatsRouting("null")                       must == ("null", nil)
  }

WHY IT CANNOT PASS EITHER WAY: today each subtest does not fail an assertion — it CRASHES THE TEST BINARY. A panic inside t.Run is re-raised by tRunner after marking the test failed, so the process exits and later subtests never run. That means red must be demonstrated one subtest at a time:

  go test ./internal/web/service -run 'TestNullConfigBlocksDoNotPanic/policy'
    --- FAIL: TestNullConfigBlocksDoNotPanic/policy
    panic: assignment to entry in nil map [recovered]
      .../internal/web/service/xray.go:940
    FAIL  github.com/Arman2122/p-ui/internal/web/service

  .../api      -> same panic, .../internal/web/service/xray.go:877
  .../template -> same panic, .../internal/web/service/xray_setting.go:347

Post-fix all three pass, asserting byte pass-through of "null". I reproduced all three panics and all three post-fix pass-throughs in a byte-identical replica at C:/Users/Arman/AppData/Local/Temp/claude/C--Users-Arman-Desktop-hakir-Projects-Penhoon-Panel/0252ae85-efbe-4d74-9920-366570056806/scratchpad/f304.

NO CHURN TEST, deliberately. The rejected plan's determinism/idempotence case was green pre-fix and certified nothing; the conservative guards make it vacuous by construction, since they change output for zero inputs. Regression cover comes from the EXISTING pins staying green — I ran all of them through the conservative variants and every one is byte-identical: config_integrity_test.go:37 (all four table rows), :86 (already-correct passes through unrewritten), :97 (empty stays empty), xray_config_inject_test.go:63 (four shapes), :114, :119 (nil stays nil), :125 (unparsable untouched), and xray_setting_test.go:158-246 (all six EnsureStatsRouting cases; `cfg == nil` is unreachable for every object input).

Gate: make test-go. No frontend test needed (no i18n key, no endpoint, no generated types).
**Sites:**
- internal/web/service/xray.go:914 — `if err := json.Unmarshal(policy, &parsed); err != nil {` — no `|| parsed == nil`; SHIP THE GUARD HERE
- internal/web/service/xray.go:940 — `parsed["levels"] = levels` — the nil-map write; PANICS on `"policy": null` (reproduced)
- internal/web/service/xray.go:910-912 — `if len(policy) == 0 { return policy }` — the absent-key bail; UNCHANGED by shippableNow, it is deferred item #1
- internal/web/service/xray.go:906-907 — the doc comment stating the punt; extend "missing or unparsable" to "missing, null, or unparsable"
- internal/web/service/xray.go:857 — `if err := json.Unmarshal(api, &parsed); err != nil {` — CONFIRMED TWIN; SHIP THE GUARD HERE
- internal/web/service/xray.go:877 — `parsed["services"] = services` — PANICS on `"api": null` (reproduced)
- internal/web/service/xray_setting.go:296-299 — `var cfg map[string]json.RawMessage` + unmarshal; the map type the original plan's sweep missed; SHIP `if cfg == nil { return raw, nil }` HERE
- internal/web/service/xray_setting.go:347 — `cfg["routing"] = routingJSON` — PANICS on a literal `null` template (reproduced); today this panic is the only thing stopping `null` being persisted
- internal/web/service/xray.go:107-120 — GetXrayBaseConfig, the sole caller of both xray.go guards
- internal/web/web.go:350 and 744 — `s.startTask(...)` -> `s.xrayService.RestartXray(true)` on the MAIN goroutine inside Server.Start; no recover in web.go, main.go:76 calls it bare => process death, systemd crash loop
- internal/web/web.go:169 `gin.Default()` and web.go:553 `cron.Recover(...)` — why the HTTP and cron paths only 500/log while the boot path kills the process
- internal/web/service/xray_setting.go:62-65 — CheckXrayConfig; `json.Unmarshal([]byte("null"), &xray.Config{})` returns nil error, so `null` and `"policy": null` both pass validation (verified)
- internal/xray/config.go:18-20 — Policy/API/Stats RawMessage with no omitempty; internal/util/json_util/json.go:13-17 marshals empty as literal `null`; internal/xray/process.go:46 writes that to bin/config.json — how operators acquire the poisoned shape
- internal/xray/config.go:20 `Stats json_util.RawMessage` — DEFERRED SIBLING: nothing in the tree ever sets it (only internal/web/service/outbound/probe_http.go:437, unrelated)
- module cache xray-core@v1.260327.1-0.20260728075948 infra/conf/xray.go:564 `if c.Stats != nil` + core/xray.go:219 `stats.NoopManager{}` + features/stats/stats.go:140 — proof policy flags are necessary but NOT sufficient
- module cache app/dispatcher/default.go:163,172,182 — the counter/onlinemap gates: policy flag AND non-nil counter
- internal/web/service/xray.go:921-926 — DEFERRED SIBLING: `_, ok := levels["0"]` is true for a null value, so `{"levels":{"0":null}}` returns unchanged (reproduced); module cache infra/conf/policy.go:80 `if p != nil` then skips it
- internal/web/service/config_integrity_test.go:96-99 and internal/web/service/xray_config_inject_test.go:118-120 — the two tests pinning the absent-key punt; both stay GREEN under shippableNow, both must be reversed for option A
- internal/web/service/xray.go:948 resolveXrayLogPaths, xray.go:986 stripDisabledRules, xray_setting_dns_routing.go:330-332 EnsureDnsServerRouting — swept and CLEAN, all bail before any nil-map write

**Correction from the re-check:** Ship all three guards, exactly as written, in one commit. Change only the rationale, not the code.

Strike the 'THEY MUST LAND TOGETHER / only all three are safe' interlock argument -- it is falsified in both directions (see problems 1 and 2). Replace the commit body's why with:

  - Guards #1 (xray.go:914) and #2 (xray.go:857) stop a live boot crash-loop. A template containing `"policy": null` or `"api": null` passes CheckXrayConfig and passes EnsureStatsRouting today, so it can already be saved; json_util.RawMessage.UnmarshalJSON copies the literal bytes, so Policy/API arrive as the 4-byte string "null", clear the `len(...) == 0` bail, and hit the nil-map write at xray.go:940 / :877. That panic lands on main.go's bare server.Start() -> start() -> startTask (web.go:744, synchronous; the defer at web.go:525 checks err and does not recover) -> RestartXray(true) (web.go:350), which is process death and a systemd crash loop. This is the fix that matters and it stands alone.
  - Guard #3 (xray_setting.go:298) stops a panic-derived 500 on save for a literal-`null` template, which UnwrapXrayTemplateConfig passes through untouched and CheckXrayConfig accepts. A panic is not a validation mechanism; real rejection is deferred item #4.
  - State plainly that a literal-`null` template, once persisted, does NOT panic at boot -- Policy and API unmarshal to len 0 and both functions bail -- it simply produces a template xray-core will not start.
  - Note the one system-level consequence of #3: the save now completes, so PruneOutbound may disable routing rules naming destinations the template no longer has. That is the same outcome as saving `{"outbounds":[]}` today, so it is not a new capability, but do not claim the save path is unchanged.

Everything else in the plan is verified and should ship as proposed: the three guards, the xray.go:906 comment swap to "A missing, null, or unparsable policy block", the single new test in xray_config_inject_test.go, and the deliberate omission of a churn test. Gate on make test-go.
**Re-check notes:**
- The stated load-bearing reason for atomicity is empirically FALSE. Claim: 'Guard #3 alone lets null persist and the next boot bricks on #1.' Reproduced in a verbatim replica of xray.Config + json_util.RawMessage: json.Unmarshal([]byte("null"), &xray.Config{}) returns err=nil and leaves Policy="" len=0 and API="" len=0, because JSON null into a struct kind is a no-op (only the RawMessage UnmarshalJSON at internal/util/json_util/json.go:21 copies literal bytes, and it is never invoked for a top-level null). Both ensureStatsPolicy and ensureAPIServices therefore bail at their own `if len(...) == 0` (xray.go:910, xray.go:851). A persisted literal-null template does NOT panic at boot; it yields a config xray-core refuses to start, which RestartXray returns as an error and web.go:352 logs as a warning. The panel stays up.
- The claim mislocates the danger, which UNDERSTATES the urgency of guards #1 and #2. The shape that actually bricks boot is `{"policy":null}` / `{"api":null}` (an object whose member is null -> RawMessage copies the 4 bytes "null" -> len 4 -> falls past the length bail -> nil-map write). Reproduced: that shape passes CheckXrayConfig (xray_setting.go:62-65, null into &xray.Config{} is a no-op, no error) AND passes EnsureStatsRouting today WITHOUT panicking (verified: returns `{"api":null,"outbounds":[],"policy":null,"routing":{...}}`, err=nil). So it is savable right now, and the boot crash-loop is LIVE today with no involvement from guard #3. Guards #1 and #2 are independently load-bearing; the three are not an interlock, and 'only all three are safe' is wrong. Landing them together is good packaging, not a safety requirement.
- 'ZERO behaviour change for any input that does not panic today' is true at the function level (I proved it byte-for-byte) but is function-level framing applied to a save path. At system level, guard #3 changes SaveXraySetting for the literal-null input from panic->recovered 500->save aborted, to 200->stored. Execution then continues past saveSetting into TemplateDestinations (routing.go:1049, returns a non-nil empty map for "null") and PruneOutbound (routing.go:1004), which DISABLES every enabled routing rule whose DestTag is absent, with persisted DB writes plus a converge. This needs no decision -- the identical end state is already reachable today by saving `{"outbounds":[]}`, which passes both gates -- but the commit body must not assert the save path is unchanged.


### F309 — ship-it (small)
**Root:** The panel writes `autoOutboundsInterface` into every tun inbound it saves even when the operator never touched the field, and writes the one value that flips a process-global switch. Upstream types the field as a POINTER (`infra/conf/tun.go:22`, `AutoOutboundsInterface *string`) precisely so ABSENT is the neutral value: `Build()` copies it only when non-nil (tun.go:35-37), then re-derives `"auto"` itself when `len(AutoSystemRoutingTable) > 0 && AutoOutboundsInterface == nil` (tun.go:38-40). The panel makes absent unreachable. `proxy/tun/handler.go:92` branches on `AutoOutboundsInterface != ""` — never on the routing table — and at handler.go:103 calls `internet.RegisterDialerController`, which appends to the package-global `Controllers` slice (`transport/internet/system_dialer.go:196-215`) that the default dialer runs on EVERY outbound socket in the Xray process; on Linux the controller is `unix.BindToDevice` (`proxy/tun/tun_linux.go:231`). So one tun inbound with no system routes SO_BINDTODEVICEs every VLESS/VMESS/Trojan/etc. outbound in the process to the auto-detected default NIC, defeating the panel's own policy-routed egress. The repo already knows this: `internal/egress/drivers/xraytun/xraytun.go:83-85` forbids the key on its generated tun front and calls it "a total outage plus a routing loop", enforced by `TestFrontNeverAsksXrayToRouteForIt` (xraytun_test.go:63-77). The user-facing tun form does exactly what that driver refuses to do.

CORRECTION TO THE REJECTED PLAN — the adversary is RIGHT on the site. `inbound-from-db.ts:26` is NOT in the write path: every `inboundFromDb` caller is read-only projection (InboundsPage.tsx:270, InboundInfoModal.tsx:121, QrCodeModal.tsx:62, tests). The edit form loads through `rawInboundToFormValues` (inbound-form-adapter.ts:161), which takes `settings` verbatim via `coerceJsonObject` and applies no protocol defaults. The real re-injection is on SUBMIT: `InboundFormModal.tsx:530` runs `InboundFormSchema.safeParse(values)`, and `InboundFormSchema = InboundFormBaseSchema.and(InboundDbFieldsSchema).and(InboundSettingsSchema)` carries `TunInboundSettingsSchema`. I verified this empirically against the repo's own zod 4.4.3 (scratch script, intersection + discriminated union of the same shape): a tun payload missing the key comes back with `autoOutboundsInterface: "auto"`, and an explicit `""` survives safeParse untouched. So there are two writers — the Zod default at tun.ts:10 (fires on BOTH add and edit, every save) and the factory literal at inbound-defaults.ts:249 — and the empirical result also proves `""` alone does not solve it.
**Ships now:** Frontend only. Five one-liners, no Go, no DB, no new i18n key, no `make gen` churn (verified: `autoOutboundsInterface` appears in neither `frontend/public/openapi.json`, `frontend/src/generated/`, nor `docs/`). Stop the panel GENERATING a value nobody chose; leave every stored row exactly as it is.

1. `frontend/src/schemas/protocols/inbound/tun.ts:10` → `autoOutboundsInterface: z.string().default(''),`
2. `frontend/src/lib/xray/inbound-defaults.ts:249` → `autoOutboundsInterface: '',`
3. `frontend/src/lib/xray/inbound-form-adapter.ts`, inside `dropLegacyOptionalEmpties` next to the `settings.ip` line at :309 → `if (settings.autoOutboundsInterface === '') delete settings.autoOutboundsInterface;`

Why `''` in the form but ABSENT on the wire, and why (3) is correctness rather than cosmetics: I verified against the repo's zod 4.4.3 that an explicit `''` survives `safeParse` unchanged, and `pruneEmpty` keeps `''` by design (inbound-form-adapter.ts:229-230). An `''` on the wire is a NON-NIL pointer in Go, so it would suppress upstream's own routes-based loop protection at tun.go:38-40 — strictly worse than today for a full-system tun. Absent is the only safe target: xray then re-derives `"auto"` itself exactly when `autoSystemRoutingTable` is non-empty. This `default('') + delete-when-empty` pair is the repo's already-shipped pattern for this defect shape (sockopt.ts:72 + stream-wire-normalize.ts:335).

4. `frontend/src/pages/inbounds/form/protocols/tun.tsx:79` → `<Input placeholder="eth0" />`. An empty field now means "let xray decide"; a `auto` placeholder claims the opposite. Hard-coded string like the sibling `xray0` placeholder at tun.tsx:43 — no i18n key.
5. `frontend/src/pages/inbounds/info/InboundInfoModal.tsx:617` → replace the `|| 'auto'` fallback with upstream's own rule: show the stored value; when it is empty show `'auto'` only if `autoSystemRoutingTable` is non-empty, else `'—'`. Concretely: `{(inbound.settings.autoOutboundsInterface as string) || (Array.isArray(inbound.settings.autoSystemRoutingTable) && (inbound.settings.autoSystemRoutingTable as string[]).length > 0 ? 'auto' : '—')}` — same `Array.isArray` guard the adjacent rows at :619 use. No i18n key.

Why this is an undisputed repair, not a behaviour choice: no operator chose `"auto"` — the panel typed it for them on every save, and upstream's own default for an untouched field is absent. An operator who DID type a value keeps it verbatim (`eth0` is never touched; even a typed `auto` still rides the wire and still works). Nothing about a stored row changes. The only behaviour that disappears is a process-global SO_BINDTODEVICE the panel installed on the operator's behalf in the case upstream would not have.

Commit: `fix(inbounds): stop a tun inbound binding every Xray outbound to one NIC`.
**Deferred:** TWO things, both gated on the same decision.

1. The Go heal — `case Tun:` in `HealedConfig` (internal/database/model/model.go:422) plus a `StripTunAutoOutboundsInterface` helper. Deferred because it is genuinely a behaviour choice, not a repair: a stored `"auto"` written by the panel and a stored `"auto"` typed by an operator are byte-identical in the DB, so no heal can tell them apart. Worse, the panel's own UI actively instructs operators to type it — `tun.tsx:79` placeholder `auto` and `en-US.json:442` "Use 'auto' to detect; auto-enabled when Auto system routes is set." A render-time heal would delete exactly the value the panel taught them to enter, silently, with no error and no way to express the intent again — the adversary's third objection, which I confirmed by reading both files. It also changes what a currently-running inbound does on the next config render, which is not something to ship unasked next to a generation fix.

2. The tooltip rewrite at en-US.json:442 / fa-IR.json:442. I DISAGREE with the adversary on this one and want the disagreement on the record rather than silently dropped. Its correction promoted the tooltip rewrite into the shipping commit; I verified it is not needed there and is actively wrong there. Under the frontend-only fix the tooltip stays TRUE in both halves: a typed `auto` rides the wire verbatim and still works, and the value still IS auto-enabled when Auto system routes is set, because tun.go:38-40 re-derives it from an absent key. The tooltip becomes a lie ONLY under option A, which strips a typed `auto`. So it is not a shippable honesty fix — it is a consequence of a decision not yet made, and rewording both locales now would make the UI describe behaviour the panel does not have. The placeholder is different and does ship: it is the only string the frontend fix alone makes misleading, and it costs no i18n key.

Also deliberately not in scope: any new column, job, config knob or abstraction. Five one-liners in four files is the whole repair.
**Test:** One table-driven test appended to the existing `frontend/src/test/inbound-form-adapter.test.ts` — that file already imports `rawInboundToFormValues`, `formValuesToWirePayload`, `type RawInboundRow` and `InboundFormSchema` (lines 4-9) and already runs the safeParse shape at :138-142. It drives the REAL submit path the rejection named: raw row → `rawInboundToFormValues` → `InboundFormSchema.safeParse` → `formValuesToWirePayload` → `JSON.parse(payload.settings)`. `InboundPortSchema` is `min(0)` (primitives/port.ts:6), so a tun row with `port: 0` parses.

`describe('a tun save never writes an autoOutboundsInterface the operator did not type')`, four rows, each starting from `{ protocol: 'tun', port: 0, settings: { name: 'xray0', mtu: 1500, gateway: [], dns: [], userLevel: 0, autoSystemRoutingTable: [] } }`:

Row A "legacy row with no key" — settings omit the key entirely. Assert `expect(parsedSettings).not.toHaveProperty('autoOutboundsInterface')`. PINS tun.ts:10 ALONE. Red today, and red with ONLY tun.ts:10 reverted while the factory sits at `''` — the exact pair the rejected plan's tests failed to pin. I verified the redness empirically (zod 4.4.3, same intersection+discriminated-union shape): safeParse returns `settings: { name: 'xray0', autoOutboundsInterface: 'auto' }`, `pruneEmpty` keeps it, `dropLegacyOptionalEmpties` has no rule for it, so it reaches the wire. Vitest fails with: `expected { name: 'xray0', mtu: 1500, gateway: [], dns: [], userLevel: 0, autoSystemRoutingTable: [], autoOutboundsInterface: 'auto' } not to have property "autoOutboundsInterface"`.

Row B "add-mode factory" — `settings: createDefaultTunInboundSettings()` (add the `@/lib/xray/inbound-defaults` import). Same absence assertion. PINS inbound-defaults.ts:249 (the factory sets the key explicitly, so the Zod default never fires — this row isolates the literal) AND the delete. Red today with `… autoOutboundsInterface: 'auto' …` in the same message.

Row C "operator cleared the field" — settings carry `autoOutboundsInterface: ''`. Same absence assertion. PINS THE DELETE ALONE: I verified `''` survives safeParse untouched and `pruneEmpty` preserves `''`, so without the `dropLegacyOptionalEmpties` line it fails with `expected { …, autoOutboundsInterface: '' } not to have property "autoOutboundsInterface"`.

Row D "operator typed a NIC" — settings carry `autoOutboundsInterface: 'eth0'`; assert `expect(parsedSettings.autoOutboundsInterface).toBe('eth0')`. Stated honestly as a GUARD, not a proof: it is green before and after. It exists to stop a later "just always delete the key" and to prove the escape hatch survives.

Red-without-fix map: A ⇒ tun.ts:10. B ⇒ inbound-defaults.ts:249 (+ delete). C ⇒ the delete. D ⇒ guard only. Every functional edit in `shippableNow` has at least one row that goes red without it, and no row is green under a partial revert of the pair the adversary called out. Run with `cd frontend && npx vitest run src/test/inbound-form-adapter.test.ts`, then `npm test` for the full gate (edits 4 and 5 are display-only; no existing snapshot or golden fixture mentions `autoOutboundsInterface` — verified repo-wide).
**Sites:**
- frontend/src/schemas/protocols/inbound/tun.ts:10 — `autoOutboundsInterface: z.string().default('auto'),` — THE root writer; fires on every save via InboundFormSchema
- frontend/src/pages/inbounds/form/InboundFormModal.tsx:530 — `const parsed = InboundFormSchema.safeParse(values);` — the TRUE re-injection site the rejected plan missed (add and edit both)
- frontend/src/schemas/forms/inbound-form.ts:56-58 — `InboundFormSchema = InboundFormBaseSchema.and(InboundDbFieldsSchema).and(InboundSettingsSchema)` — why the tun default reaches submit
- frontend/src/lib/xray/inbound-defaults.ts:249 — `autoOutboundsInterface: 'auto',` — the second writer (add-mode + protocol switch at InboundFormModal.tsx:483 + clone fallback at InboundsPage.tsx:457)
- frontend/src/lib/xray/inbound-form-adapter.ts:309 — `if (settings.ip === '') delete settings.ip;` — insertion point and in-file precedent inside `dropLegacyOptionalEmpties`
- frontend/src/lib/xray/inbound-form-adapter.ts:229-230 — `Primitive values (including 0, false, '') are kept verbatim.` — pruneEmpty will not drop `''`, so the delete is required
- frontend/src/pages/inbounds/form/protocols/tun.tsx:79 — `<Input placeholder="auto" />` — after the fix an empty field means "let xray decide", so this placeholder misreports the effective value
- frontend/src/pages/inbounds/info/InboundInfoModal.tsx:617 — `{(inbound.settings.autoOutboundsInterface as string) || 'auto'}` — reads the RAW row (via `buildInboundInfo`, helpers.ts:86-87, no Zod defaults), so absent renders as "auto": already wrong today, wrong-by-default after the fix
- frontend/src/lib/xray/inbound-form-adapter.ts:161 — `rawInboundToFormValues` takes `settings` verbatim; proves the edit path injects on SUBMIT, not on LOAD
- frontend/src/lib/xray/inbound-from-db.ts:26 — NOT a fix site (rejected plan's error): read-only link/QR/info projection only
- frontend/src/pages/inbounds/InboundsPage.tsx:457 — clone's parse-failure fallback calls the factory (covered by the factory fix); clone's happy path (InboundsPage.tsx:452-455) and importInbound (InboundsPage.tsx:350) POST verbatim JSON with no Zod parse — not sites
- frontend/src/schemas/protocols/stream/sockopt.ts:72 + frontend/src/lib/xray/stream-wire-normalize.ts:335 — `interface: z.string().default('')` paired with `if (nonEmptyString(out.interface) === false) delete out.interface;` — the SHIPPED in-repo template for this exact defect shape (see the comment at sockopt.ts:60-63 about xray-doc example values written into every config)
- internal/egress/drivers/xraytun/xraytun.go:83-85 and internal/egress/drivers/xraytun/xraytun_test.go:63-77 — in-repo corroboration that the harm is real and already acknowledged
- internal/database/model/model.go:420-443 — `HealedConfig()` has no `case Tun` (DEFERRED, not shipped)
- xray-core infra/conf/tun.go:22,35-40; proxy/tun/handler.go:92-103; proxy/tun/tun_linux.go:231 (`unix.BindToDevice`); proxy/tun/tun_linux.go:339-362 (`findOutboundInterface`); transport/internet/system_dialer.go:196-215 (global `Controllers`) — module cache for github.com/xtls/xray-core v1.260327.1-0.20260728075948-5ca6f4b7d4dc
- internal/web/translation/en-US.json:442 / fa-IR.json:442 — `autoOutboundsInterfaceTooltip`; UNCHANGED by shippableNow, stays true (see deferred)

**Re-check notes:**
- Line-number nit only: the tun.tsx placeholder is at :78, not :79. Every other cited line matched exactly (tun.ts:10, inbound-defaults.ts:249, inbound-form-adapter.ts:309, InboundInfoModal.tsx:617, sockopt.ts:72, stream-wire-normalize.ts:335).
- Edit 5 keeps one residual display inaccuracy, inherited not introduced: a legacy row that stores an explicit '' (an operator who cleared the field pre-fix) will render 'auto' when autoSystemRoutingTable is non-empty, while xray actually installs NO binding — '' is a non-nil *string so tun.go:38 skips the promotion. Today's `|| 'auto'` is equally wrong for that row, and the fix stops producing that shape, so this is not a regression. An exact version would have to distinguish undefined from '', which costs more than a one-liner for a row shape the fix eliminates. Non-blocking.
- Disclosed scope limit, worth restating so nobody reads the commit as a full fix: stored rows keep their 'auto' and keep the process-global binding. Re-saving such a row through the form does NOT clear it either — rawInboundToFormValues (:164) passes settings verbatim and safeParse preserves a non-undefined 'auto'. Only NEW saves and rows whose key is absent are repaired. That is the correct place to stop, but 'stop the bleeding' is the honest description.
- Small imprecision in the ARGUMENT, not the code: 'no operator chose auto' is exact for the edit/legacy path but generous for add mode, where the factory pre-fills the Input so the operator does see 'auto' on screen. It does not change the outcome — after the fix xray re-derives 'auto' whenever routes are set, so add-mode effective behaviour is identical or strictly safer, and this is precisely the 'xray-doc example value written into every config' shape the sockopt.ts:60-63 comment already names.
- The one genuine behaviour delta, and the claim discloses it in its own reasoning rather than hiding it: re-saving a hand-cleared row changes '' to absent, so a routed tun moves from 'no binding' to xray's own 'auto'. Reasonable, because a 0.0.0.0/0 tun with no outbound binding is a routing loop, not a preference, and nothing in the UI ever offered '' as an off switch (placeholder 'auto', tooltip "Use 'auto' to detect"). Not a smuggled choice.
- Endorsing the deferred call against the adversary: the tooltip rewrite genuinely does NOT belong in this commit. Under the frontend-only fix both halves stay true — a typed 'auto' rides the wire verbatim and works, and the value still is auto-enabled when Auto system routes is set, because tun.go:38-40 re-derives it from an absent key. Rewording it now would make the UI describe behaviour the panel does not have.


### F316 — ship-with-the-noted-correction (small)
**Root:** The adversary is RIGHT on all four points; I confirmed each against the pinned source, and I found two more facts that change the plan further.

Confirmed mechanism (xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc proxy/dokodemo/dokodemo.go:78-107): with followRedirect false, dest starts as {rewriteAddress, rewritePort}; a nil address becomes net.LocalHostIP (or LocalHostIPv6 when the local host string has no "."), a zero port becomes the connection's OWN local port, and only then is portMap consulted as `d.portMap[port]` — keyed by that same own port. So "no destination" is never an error in xray, it is a silent self-reference. infra/conf/dokodemo.go:34 copies RewriteAddress only `if v.RewriteAddress != nil` and validates nothing but portMap VALUES, so validateInboundForXray (internal/web/service/xray.go:424) can never catch it.

The original predicate (emptiness of rewriteAddress + portMap) is therefore the wrong test, exactly as charged: it waves through `{"rewriteAddress":"127.0.0.1"}` (port stays 0 -> own port -> loop), waves through an off-port portMap, and falsely refuses `{"rewritePort":9090}` (127.0.0.1:9090 is a legitimate local forward).

TWO CORRECTIONS THE ADVERSARY ALSO MISSED:
1. The stated consequence is wrong. internal/web/service/config.json:73-79 ships a GLOBAL `ip:["geoip:private"] -> blocked` routing rule, and operator rules splice in above it but below the pinned api rule (internal/web/service/routing.go:353-361, splitPinnedAPIRule). So on a stock panel 127.0.0.1:<own port> is BLACKHOLED, not looped. The real harm is a silently dead inbound the panel reported as saved. An actual fd/goroutine loop needs the operator to have removed that rule AND to exit via an unhardened freedom outbound.
2. There is a THIRD legitimate destination-less idiom neither the plan nor the adversary named: per-inbound sniffing with destOverride (model.Inbound.Sniffing, model.go:78; frontend/src/lib/xray/forms/SniffingFields.tsx:36). The dispatcher rewrites dest.Address from sniffed content before routing, so a destination-less tunnel on 443 with destOverride ["tls"] works. That widens the blast radius of any hard refusal beyond what the original decision text claimed.

The one part that needs no judgement: portMap's lookup key can ONLY ever be the inbound's own listen port, because model.Inbound.Port is a single int (model.go:73) emitted verbatim by GenXrayInboundConfig (model.go:463) — the panel binds one port per inbound. Any portMap key that is not that port is configuration that provably can never be read. The repo's own golden fixture encodes the wrong mental model (frontend/src/test/golden/fixtures/inbound/tunnel-basic.json:6-9 maps "8080" and "8443" with no relation to any listen port).
**Ships now:** Refuse a portMap that can never fire. Undisputed: it removes dead configuration the operator wrote and the panel accepted, it touches none of the three destination-less idioms (followRedirect / sniffing destOverride / routed-to-dns), and no operator prefers a port mapping the code guarantees will never be consulted.

Four edits, ~20 lines total, no new column, job, config knob, i18n key or frontend change.

1. internal/core/caps.go:82-84 — widen the capability to carry the instance:
   `ValidateInstance(kind Kind, inst Instance) error`
   Required, not cosmetic: the guard needs inst.Port and the string-only signature cannot carry it. bind.go:62-64 and cores.go:96-104 need no change.

2. internal/cores/internal/amneziawg/core.go:108-114 — the only existing implementer SHRINKS: it already builds `core.Instance{Settings: settings}` internally just to call ParamsOf, so it becomes
   `func (c *Core) ValidateInstance(_ core.Kind, inst core.Instance) error { params, err := ParamsOf(inst); if err != nil { return err }; return params.Validate() }`

3. internal/web/service/inbound_account_auth.go:145 — the single caller passes
   `core.Instance{Kind: kind, Listen: inbound.Listen, Port: inbound.Port, Settings: inbound.Settings}`
   the same shape inbound.go:1951-1953 and routing.go already build.

4. NEW `ValidateInstance` on internal/cores/internal/xray/core.go (which already owns kind "tunnel", core.go:29-32). For any kind other than "tunnel", return nil. For "tunnel": unmarshal settings; if followRedirect is false AND len(portMap) > 0 AND no key equals strconv.Itoa(inst.Port), refuse, naming inst.Port as the only key that can ever be read. A malformed settings blob returns nil (unchanged behaviour — this guard is not the JSON validator).

Checked and clear: the dispatch ratchet (internal/arch/dispatch_ratchet_test.go:44-64) counts `model.<Const>` selectors and expressions suffixed `.Protocol`; this guard compares a `core.Kind` parameter named `kind`, so no budget moves.
**Deferred:** 1. THE F316 HEADLINE ITSELF — refusing a destination-less tunnel inbound. It needs the decision below, because the corrected predicate (resolved destination == the inbound's own listener) still refuses a shape that demonstrably works: internal/web/service/config.json:11-19 is the panel's OWN api inbound in exactly that shape (listen 127.0.0.1:62789, `{"rewriteAddress":"127.0.0.1"}`, no rewritePort), correct only because the pinned `inboundTag:["api"] -> api` rule captures it before anything dials. A user row can be in the same position via a routing rule to a dns outbound (proxy/dns/dns.go:165-174 replaces both address and port from rewriteServer), and routing.go:353-361 puts operator rules above the private-block rule, so that path is reachable today. shippableNow deliberately ACCEPTS the exact panel default `{"portMap":{},"allowedNetwork":"tcp,udp","followRedirect":false}` and asserts it, so shipping it does not quietly take this decision.

2. A non-empty portMap with followRedirect TRUE — equally dead (the whole `!FollowRedirect` block is skipped) and equally provable, but refusing it breaks the operator who toggles followRedirect on and leaves the map behind. Left out to keep the new refusal to one shape.

3. SIBLING internal/routing/plan.go:160-170 — `guardRule(tag, blackhole)` (private -> blackhole) is emitted only inside the `tag == ""` L3-front branch, so an L7 subject with routing rules gets no private-destination guard. Not shippable: adding it would refuse every operator who deliberately routes an inbound to a LAN service. Its own finding, its own decision.

4. SIBLING internal/database/db.go:1802-1827 hardenFreedomFinalRules — hardened the TEMPLATE freedom outbound only; a hand-edited or user-created freedom outbound has no private block, which is the condition that turns this blackhole into a real self-dial loop. Separate finding.

5. Frontend mirror (a superRefine on TunnelInboundSettingsSchema, or `rules` on the form field). Cosmetic once the Go seam exists, and it inherits whichever answer the decision gets. Not worth an i18n key in both locale files until then.
**Test:** New table-driven test in internal/web/service/inbound_instance_validation_test.go, same style as the file (stdlib testing, initTestDB(t), drives validateInstanceForKind directly — no core process, no xray binary).

TestATunnelPortMapThatCanNeverFireIsRefusedAtTheSave, every case on `&model.Inbound{Protocol: model.Tunnel, Listen: "0.0.0.0", Port: 10001, Settings: ...}`:
- `{"portMap":{"8080":"10.0.0.5:80"},"followRedirect":false}` -> REFUSED, and the message must contain "10001". (This is the golden fixture's own shape, tunnel-basic.json:6-9.)
- `{"portMap":{"8080":"10.0.0.5:80","10001":"10.0.0.5:443"},"followRedirect":false}` -> accepted; one key can fire, a dead sibling key does not earn a refusal.
- `{"portMap":{"10001":"1.1.1.1:443"},"followRedirect":false}` -> accepted.
- `{"portMap":{},"allowedNetwork":"tcp,udp","followRedirect":false}` -> accepted ON PURPOSE. This is the exact panel default and the deferred decision; asserting it green is what stops shippableNow from taking that decision silently.
- `{"rewritePort":9090}` -> accepted. This is the adversary's false positive, pinned shut.

WHAT FAILS WITHOUT THE FIX, EXACTLY: today no Xray core implements core.InstanceValidator — the interface is declared at internal/core/caps.go:82 and the single implementer is internal/cores/internal/amneziawg/core.go:108. So cores.InstanceValidatorFor("tunnel") returns ok=false, validateInstanceForKind (inbound_account_auth.go:141-143) returns nil, and case 1 fails with: `accepted a portMap whose only key (8080) can never be read: dokodemo looks up the connection's own local port, which is always 10001`. The four accept cases pass either way — that is deliberate, they are the regression fence; the red lives entirely in case 1.

Plus one save-path case mirroring TestAddInboundRefusesObfuscationTheModuleWouldReject: `(&InboundService{}).AddInbound(&model.Inbound{UserId:1, Enable:true, Remark:"dead-portmap", Tag:"dead-portmap-1", Listen:"0.0.0.0", Port:10001, Protocol: model.Tunnel, Settings: '{"portMap":{"8080":"10.0.0.5:80"},"followRedirect":false}'})` must return a non-nil error containing "10001". Red today for the same reason, and it proves the refusal reaches the seam both save paths call (inbound.go:931, :1394) rather than only the helper.

Revert-and-watch protocol: delete only edit 4 (the tunnel branch), leave the widened signature. Case 1 and the AddInbound case must go red; the four accept cases and every AmneziaWG case must stay green.
**Sites:**
- internal/core/caps.go:82-84 — `type InstanceValidator interface { ValidateInstance(kind Kind, settings string) error }` — the signature that must widen to carry Instance
- internal/cores/internal/amneziawg/core.go:108-114 — the only implementer today; shrinks because it already builds core.Instance{Settings: settings} internally for ParamsOf
- internal/web/service/inbound_account_auth.go:136-146 — validateInstanceForKind, the seam; line 145 passes only inbound.Settings, which is why no validator can see the port
- internal/cores/internal/xray/core.go:29-32 — `var kinds = []core.Kind{... "tunnel", "tun"}`; the new ValidateInstance goes on this Core
- internal/web/service/inbound.go:931 (AddInbound) and :1394 (UpdateInbound) — the two save paths already calling the seam
- internal/web/service/xray.go:424-448 validateInboundForXray — delegates entirely to xray-core, which accepts every shape here; also skips disabled and node rows, so it is the wrong home for this guard
- internal/database/model/model.go:73 `Port int` and :463 `Port: i.Port` — one port per inbound, emitted verbatim: the proof that a portMap key other than the listen port can never be read
- xray-core@v1.260327.1-0.20260728075948-5ca6f4b7d4dc proxy/dokodemo/dokodemo.go:84-107 — nil address becomes LocalHostIP/LocalHostIPv6, zero port becomes the connection's own local port, and portMap is looked up by that same own port
- xray-core .../infra/conf/dokodemo.go:34-42 — RewriteAddress copied only `if v.RewriteAddress != nil`; only portMap VALUES are validated, never the keys
- frontend/src/lib/xray/inbound-defaults.ts:233-238 createDefaultTunnelInboundSettings — seeds {portMap:{}, allowedNetwork:'tcp,udp', followRedirect:false}, no destination
- frontend/src/schemas/protocols/inbound/tunnel.ts:13,19,20 — rewriteAddress/rewritePort optional, portMap a free-form Record<string,string>
- frontend/src/pages/inbounds/form/protocols/tunnel.tsx:11-16,26 — both destination fields and the HeaderMapEditor rendered with no rules
- frontend/src/test/golden/fixtures/inbound/tunnel-basic.json:6-9 — the repo's own fixture maps "8080" and "8443" with no relation to any listen port: the wrong mental model, in-tree
- internal/web/service/config.json:11-19 — the panel's own api inbound IS the self-dial shape (listen 127.0.0.1:62789, rewriteAddress 127.0.0.1, no rewritePort) and works only because the pinned api routing rule captures it
- internal/web/service/config.json:73-79 — the global `ip:["geoip:private"] -> blocked` rule that makes the self-dial a silent blackhole rather than the loop the original plan claimed
- internal/web/service/routing.go:353-361 splitPinnedAPIRule — operator rules land above the private-block rule, which is what keeps the routed-to-dns idiom reachable and therefore disputed
- xray-core .../proxy/dns/dns.go:165-174 — the dns outbound replaces dest.Address and dest.Port from rewriteServer, so a destination-less tunnel routed here genuinely works
- internal/database/model/model.go:78 `Sniffing string` + frontend/src/lib/xray/forms/SniffingFields.tsx:36 — destOverride, the third legitimate destination-less idiom missed by both the plan and the adversary
- SIBLING internal/routing/plan.go:160-170 — guardRule(tag, blackhole) emitted only inside the `tag == ""` L3-front branch; L7 subjects get no private-destination guard
- SIBLING internal/database/db.go:1802-1827 hardenFreedomFinalRules — hardened the template freedom outbound only, not a user-created one
- internal/arch/dispatch_ratchet_test.go:44-64 — checked: the detector counts model.<Const> and `.Protocol`-suffixed expressions, so a `kind == "tunnel"` comparison on a core.Kind moves no budget
- internal/web/service/inbound_instance_validation_test.go:24,80,97 — the template for the new test, and the two existing tests whose meaning shifts

**Correction from the re-check:** Edit 3 must also pass `StreamSettings: inbound.StreamSettings` (core.Instance already has the field — internal/core/core.go:82), and edit 4's predicate needs a fourth conjunct: refuse only when the inbound's `streamSettings.sockopt.tproxy` is NOT "tproxy" (absent, "off" and "redirect" all stay refusable).

Why: under TProxy the accepted socket's local address IS the original destination, so `port` in the `!FollowRedirect` branch is not the listen port. Xray proves this itself at app/proxyman/inbound/worker.go:67-78 — `case internet.SocketConfig_TProxy: dest = net.DestinationFromAddr(conn.LocalAddr())`, while `case internet.SocketConfig_Redirect:` must call `tcp.GetOriginalDestination(conn)` (SO_ORIGINAL_DST) precisely because under REDIRECT the DNAT has already rewritten LocalAddr to the listener. IP_TRANSPARENT is what makes the difference and it is set for tproxy mode at transport/internet/sockopt_linux.go:211-215. So with `sockopt.tproxy:"tproxy"` + `followRedirect:false`, `d.portMap[port]` (proxy/dokodemo/dokodemo.go:101) is keyed by the ORIGINAL destination port, and `{"80":"10.0.0.5:80","443":"10.0.0.5:443"}` on a listener at 10001 is the working config, not dead config.

Test correction: add a sixth case — case 1's settings plus `StreamSettings: '{"sockopt":{"tproxy":"tproxy"}}'` -> ACCEPTED, and keep `{"sockopt":{"tproxy":"redirect"}}` -> REFUSED. Without that pair the guard's whole predicate is untested and the next reader will "simplify" the tproxy conjunct away. Case 1 stays red today exactly as the plan says (its StreamSettings is empty, so tproxy is off).
**Re-check notes:**
- THE PREMISE IS FALSE IN ONE REACHABLE MODE. "a portMap key other than the listen port can never be read" holds only while sockopt.tproxy is off or redirect. proxy/dokodemo/dokodemo.go:85 takes `port` from `conn.LocalAddr()`, and under IP_TRANSPARENT + a TPROXY target that is the ORIGINAL DESTINATION port, not the listener's. Xray's own inbound worker relies on exactly this distinction (app/proxyman/inbound/worker.go:75-78: TProxy reads conn.LocalAddr(); Redirect must call SO_ORIGINAL_DST). The model.Port evidence (model.go:73, :463) proves the LISTEN port is single and verbatim — it does not prove the accepted socket's local port equals it.
- REACHABILITY IS NOT THEORETICAL — THE PANEL'S OWN UI BUILDS THIS SHAPE. internal/core/capability.go:197-201 puts "tunnel" in CapStream with the comment "tunnel for sockopt, notably sockopt.tproxy in TProxy/redirect mode". InboundService.normalizeStreamSettings (internal/web/service/inbound.go:520-525) therefore PRESERVES StreamSettings for tunnel. frontend/src/pages/inbounds/form/InboundFormModal.tsx:838-840 says it outright: "Transport masks don't apply to tunnel (a transparent forwarder), so its stream tab is just sockopt + TProxy", and frontend/src/pages/inbounds/form/transport/sockopt.tsx:196-202 renders the TProxy select with the 'tproxy' option. The operator sets portMap on one tab and TProxy on the next, in the same modal.
- IT IS A BEHAVIOUR CHOICE, AND A DESTRUCTIVE ONE, BECAUSE THE SEAM IS ALSO ON THE EDIT PATH. validateInstanceForKind is called from UpdateInbound (internal/web/service/inbound.go:1394) as well as AddInbound (:931). An operator running a working tproxy port-forwarder today would not merely be refused a new save — the existing row becomes uneditable through the panel: every subsequent edit, including toggling Enable or fixing an unrelated field, is rejected naming a port that is irrelevant to their setup. That is exactly the "an operator could reasonably disagree" test, failed.
- THE FIXTURE IS EVIDENCE AGAINST THE PLAN, NOT FOR IT. frontend/src/test/golden/fixtures/inbound/tunnel-basic.json:5-9 maps "8080"->10.0.0.5:80 and "8443"->10.0.0.5:443. The plan calls that "the wrong mental model, in-tree". Under tproxy it is the RIGHT model — intercept transparently, ignore the original host (followRedirect:false forces 127.0.0.1), and dispatch by destination port. A repo fixture written in the shape the guard refuses is the cheapest possible warning that the predicate is under-specified.
- EDIT 3 AS WRITTEN CANNOT BE CORRECTED IN PLACE. The proposed Instance carries only Kind/Listen/Port/Settings, so even a corrected xray-side predicate would have nothing to read the sockopt from. StreamSettings has to be added to the constructed Instance at internal/web/service/inbound_account_auth.go:145 — one more field, but it is not optional.
- EVERYTHING ELSE IN THE PLAN CHECKED OUT, INCLUDING THE RED. Confirmed by reading: core.InstanceValidator is declared once (internal/core/caps.go:82-84), resolved once (internal/core/bind.go:62-64 -> Bound.Validate), faced once (internal/cores/cores.go:96-104), implemented exactly once (internal/cores/internal/amneziawg/core.go:108-114 — and it does build `core.Instance{Settings: settings}` internally, so it genuinely shrinks), and called from exactly one place. No Xray core implements it, so cores.InstanceValidatorFor("tunnel") returns ok=false and inbound_account_auth.go:142-144 returns nil today. Case 1 fails on `accepted a portMap whose only key (8080) can never be read`, and the AddInbound case fails on the nil error — both genuinely red, both green after edit 4. "tunnel" really is a dokodemo alias (xray-core infra/conf/xray.go:23), and "tun" is not, so the guard's kind check is correctly scoped. The four accept cases pass either way, as the plan itself states — that is a fence, not padding. Size is right (~20 lines, ~6 more for the correction); no new column, job, knob, i18n key or frontend change. The four deferrals are correctly deferred and none of them is a same-defect sibling of this guard.


### F338 — ship-with-the-noted-correction (small)
**Root:** The adversary is right on all three counts; I confirmed each against the tree.

(1) OVER-FIRE — CONFIRMED. `disableInvalidInbounds` (inbound_disable.go:35-38) selects `doomed` with no protocol filter, and `result.RowsAffected` (:56-60) counts every depleted LOCAL inbound of any core. Four cores are registered (cores.go:170-179: mtproto, amneziawg, wireguard, xray). Feeding RowsAffected into the restart flag would force `RestartXray(true)` — a full local Xray process replacement dropping every live Xray session on the box — for a depleted mtproto or wireguard inbound, which buys nothing: mtproto's DropInstance (mtproto/core.go:151-154) stops the sidecar and wireguard's (wireguard/core.go:162-164) removes the device, and both already cut those sessions.

(2) VACUOUS TEST — CONFIRMED. `seedDepletableClient` (reset_reenable_convergence_test.go:86-89) ends with `Updates({"total":100,"up":60,"down":60})` on client_traffics. The client is already depleted, so `disableInvalidClients` fires and AddTraffic's second return is ALREADY true today. The original recipe would have passed without the fix.

(3) MECHANISM — CONFIRMED as a correction. `ApplyPendingRestart` (xray.go:1353-1359) calls `IsNeedRestartAndSetFalse()` — a CAS that clears `isNeedXrayRestart` — BEFORE `RestartXray(false)`. So inside RestartXray the guard `!isForce && configUnchanged && !isNeedXrayRestart.Load()` (xray.go:1242-1245) returns early because the flag is ALREADY false, not because configUnchanged wins alone. With the flag still set, that guard falls through to `process.Stop()` and a real replacement. The original plan's CONCLUSION (needRestart is not the lever) survives; its stated reasoning did not.

The actual root cause, corrected: an inbound that spends its own `total` or hits its own `expiry_time` is torn down with `rt.DelInbound` → xray `Core.DropInstance` (xray/core.go:193-205), which hot-applies a config with the inbound spliced out. That reaches xray-core `Manager.RemoveHandler` → `handler.Close()` → `tcpWorker.Close()`, which closes the listening hub and the proxy and never touches already-accepted connections — the exact measurement the landed F335 commit (8b23da39) carries: "Removing the whole inbound does not cut them either; the worker closes its listener and never tracks accepted connections." The DB flip to `enable=false` feeds nothing: `addTrafficLocked` returns `disabledClientsCount > 0` (inbound_traffic.go:116), and the inbound branch at :109-114 only logs. So the two jobs never see an auto-disable for an inbound and never force the restart that is the only lever the core exposes.

This does not self-heal. I checked the periodic supervise pass (`job/core_supervise.go:54` → `bound.Supervise.Reconcile`): Xray's `Reconcile` (xray/core.go:113-117) returns nil when `process.GetConfig().Equals(cfg)`, and DropInstance already stored that config — so the reconcile is a permanent no-op here. A session on a depleted inbound flows until the client disconnects, while the panel reports the inbound disabled.

This is the unshipped half of F335: identical condition, identical core limitation, identical lever, already-accepted cost — applied to the inbound entity instead of the client entity.
**Ships now:** Core-scope the existing signal. No new setting, column, job, route, i18n key or abstraction; it reuses `restartXrayOnClientDisable` (default true), whose title is already entity-neutral ("Restart Xray After Auto Disable"), and it is a no-op for an operator who has that setting off.

EDIT 1 — internal/web/service/inbound_disable.go, `disableInvalidInbounds`:
Signature becomes `(needRestart bool, disabled int64, xrayServed int64, err error)`. Inside the existing `for _, ib := range doomed` loop (:41), as the FIRST statement:

    if cores.ServedByXray(core.Kind(ib.Protocol)) {
        xrayServed++
    }

Add imports `internal/core` and `internal/cores` (same package already pairs them at inbound.go:1088, so no cycle). Both returns carry the new value.

LOAD-BEARING CONSTRAINT, not an implementation detail: count off `doomed`, BEFORE and INDEPENDENT of the `rt.DelInbound` outcome. The DB flip happens either way, and on a box with no running Xray `runtimeFor`/`DelInbound` error out (:43-51). If a later edit counts only successful drops, the fix stops working headless and the test below goes red for the wrong reason.

EDIT 2 — internal/web/service/inbound_traffic.go, `addTrafficLocked` (:96-116):
Rename `disabledClientsCount` → `autoDisabledCount`, and rename the named return `clientsDisabled` → `autoDisabled` on both `AddTraffic` (:24) and `addTrafficLocked` (:64) so the widened meaning is honest. Jobs bind positionally — no job churn. Replace the inbound branch at :109-114 with:

    needRestart2, count, xrayServedInbounds, disableInboundsErr := s.disableInvalidInbounds(tx)
    if disableInboundsErr != nil {
        logger.Warning("Error in disabling invalid inbounds:", disableInboundsErr)
    } else {
        if count > 0 {
            logger.Debugf("%v inbounds disabled", count)
        }
        autoDisabledCount += xrayServedInbounds
    }

The accumulate sits outside the `count > 0` log guard deliberately: the debug log stays truthful about every core (a depleted mtproto inbound is still logged), while only the Xray-served subset arms the restart. Return `autoDisabledCount > 0` at :116.

EDIT 3 — internal/web/service/reset_reenable_convergence_test.go:262: `_, _, err :=` → `_, _, _, err :=`. Mechanical, the one existing direct caller.

EDIT 4 — text-only, existing keys, both locale files (no key added or renamed, so no dead-key or `make gen` implication). The description otherwise lies about what the setting now does:
  internal/web/translation/en-US.json:1387 → "When a client or an inbound is automatically disabled due to expiration or traffic limit, restart Xray."
  internal/web/translation/fa-IR.json:1267 → "وقتی کاربر یا ورودی به‌صورت خودکار به‌دلیل اتمام زمان یا ترافیک غیرفعال می‌شود، Xray ری‌استارت شود."

Why this needs no behaviour choice: the operator tradeoff is already encoded in an existing, defaulted-on, entity-neutral setting; the owner explicitly accepted "a depletion event now briefly drops every connection on the box" for the identical condition in 8b23da39; and the status quo is not defensible alternative semantics — the panel bills the inbound as disabled, stops accepting on it, and keeps serving everyone already on it, forever. The arch guard `internal/arch/depletion_restart_test.go` stays green: it pins the `if restartOnDisable` branches to `RestartXray(true)`, and neither branch is touched.
**Deferred:** Three things are deliberately out, each a separate finding rather than an option in the question above.

1. MANUAL disable — the decision below. Toggling an inbound off in the UI goes `controller/inbound.go` → `UpdateInbound` → `Local.UpdateInbound` (runtime/local.go:132-138: `if !newIb.Enable { return l.DelInbound(ctx, newIb) }`) → the same hot apply, then `a.xrayService.SetToNeedRestart()` (~10 sites, e.g. inbound.go:199, :284). That flag reaches `ApplyPendingRestart`, which CAS-clears it and calls `RestartXray(false)`; DropInstance already stored the matching config, so `configUnchanged` is true and it early-returns. Manual disable therefore has the identical live-session hole. It is excluded because `restartXrayOnClientDisable` says "Auto Disable" — an admin toggle is not an auto-disable, and reusing the setting there WOULD be choosing new semantics. Same reasoning excludes the ~15 `SetToNeedRestart()` client sites in controller/client.go and `ldap_sync_job.go:305-330`'s `batchSetEnable` → `SetClientEnableByEmail`.

2. AWG has no `DropInstance` at all — `internal/cores/internal/amneziawg/core.go` implements Describe/Preflight/Reconcile/ValidateInstance/ApplyInstance and nothing else, so `applierFor` (runtime/local.go:63-72) errors with `core "amneziawg" cannot apply a single inbound` and a depleted AWG inbound is not torn down on the spot. Unlike Xray, AWG's `Reconcile` is not a no-op, so the supervise pass at `job/core_supervise.go:54` plausibly sweeps it on the next tick — the gap looks like a delay, not a permanent leak, but I did not confirm that end to end. Separate finding; scoping the count to Xray leaves AWG's behaviour exactly as it is today.

3. Remote-node inbound depletion. Both `disableInvalidInbounds` queries carry `node_id IS NULL`, so an inbound hosted on a remote node never enters `doomed` and its own total/expiry is never enforced by the master at all. Distinct from F338 (which is about live sessions on an inbound that WAS disabled) and larger — it needs the node push path, next to `disableRemoteClients` (inbound_disable.go:337-355), whose "do not bounce the node's Xray" choice is documented as deliberate (#5740).
**Test:** One test, in internal/web/service/reset_reenable_convergence_test.go beside the F028 sibling that already owns the inbound-quota path. It drives the real code path (`AddTraffic` → serial writer → `addTrafficLocked` → `disableInvalidInbounds`) and asserts the exact boolean the two jobs turn into `RestartXray(true)`.

`TestInboundDepletionReportsTheAutoDisableTheJobsRestartOn`:
1. `initTestDB(t)`; `ib := seedDepletableClient(t, 29030, "in-f338", "f338@x")` — VLESS, so `ServedByXray` is true.
2. UNDO the helper's client depletion — this is the step the rejected plan omitted: `db.Model(core.ClientTraffic{}).Where("email = ?", "f338@x").Updates(map[string]any{"total": 0, "up": 0, "down": 0, "expiry_time": 0})`. With total=0 and expiry_time=0 the client fails both halves of `depletedCondFrom`, so `disableInvalidClients` cannot fire.
3. Spend the INBOUND's own allowance: `db.Model(model.Inbound{}).Where("id = ?", ib.Id).Updates(map[string]any{"total": 100, "up": 60, "down": 60})`.
4. `_, autoDisabled, err := (&InboundService{}).AddTraffic(nil, nil)`.

Three asserts, in this order — the first two are the non-vacuity guards:
- `if reloadInbound(t, ib.Id).Enable { t.Fatal("the inbound was not disabled by its own quota; the rest of this test proves nothing") }` — proves the inbound pass actually ran.
- `if !readEnableLocations(t, ib.Id, "f338@x").traffic { t.Fatal("the client was disabled too, so a true return below would not be the inbound's") }` — proves `disableInvalidClients` did NOT fire, so the boolean can only have come from the inbound branch. This is precisely the hole that sank the previous plan.
- `if !autoDisabled { t.Fatal("AddTraffic reported no auto-disable after inbound in-f338 spent its own quota; both traffic jobs read this to force the Xray restart, so every session already accepted on that inbound keeps flowing") }`

WHAT FAILS TODAY, EXACTLY: the third assert. `addTrafficLocked` returns `disabledClientsCount > 0`; the client is not depleted so that counter stays 0, and the inbound branch at :109-114 never touches it. Output: `AddTraffic reported no auto-disable after inbound in-f338 spent its own quota; both traffic jobs read this to force the Xray restart, so every session already accepted on that inbound keeps flowing`. The first two asserts pass today (the inbound IS flipped, the client IS untouched), which is what makes the third one a real measurement rather than a restatement.

WHY IT GOES GREEN WITH THE FIX AND NOT BEFORE: `xrayServed` is counted off `doomed`, so it is 1 even though `rt.DelInbound` errors in the test (no Xray process, `runtimeFor` fails at :43-46). Verify by reverting Edit 1's counter alone and watching it go red.

NEGATIVE CASE, same test as a second subtest if cheap: seed an mtproto inbound the same way and assert `autoDisabled` is FALSE — that pins the core scoping and would fail on the naive RowsAffected version the adversary rejected. If the mtproto seed needs a sidecar and is not cheap in-package, skip it; the Xray-positive case is the one that must be red.

No env gate beyond the file's existing `initTestDB` PUI_DB_DSN skip. Run with `make test-go` against a live Postgres, not a bare `go test ./...`.
**Sites:**
- internal/web/service/inbound_disable.go:18 — `func (s *InboundService) disableInvalidInbounds(tx *gorm.DB) (bool, int64, error)`; signature grows a fourth return
- internal/web/service/inbound_disable.go:35-38 — the `doomed` SELECT: no protocol filter, `node_id IS NULL` only. This is the slice to count the Xray-served subset off
- internal/web/service/inbound_disable.go:41-53 — `for _, ib := range doomed` — insert the ServedByXray counter here, ahead of and independent of `rt.DelInbound`
- internal/web/service/inbound_disable.go:56-60 — `result := tx.Model(...).Update("enable", false)`; `count := result.RowsAffected` counts EVERY core's depleted inbounds. Keep it for the debug log; it must not arm the restart
- internal/web/service/inbound_traffic.go:24 — `func (s *InboundService) AddTraffic(...) (needRestart bool, clientsDisabled bool, err error)` — rename the second named return to `autoDisabled`
- internal/web/service/inbound_traffic.go:64 — `addTrafficLocked` same rename
- internal/web/service/inbound_traffic.go:98-107 — `disabledClientsCount := int64(0)` … `disabledClientsCount = count` — rename to `autoDisabledCount`
- internal/web/service/inbound_traffic.go:109-114 — the inbound branch that today only logs; this is the fix site
- internal/web/service/inbound_traffic.go:116 — `return needRestart0 || needRestart1 || needRestart2, disabledClientsCount > 0, renewedEmails, pending, nil`
- internal/web/service/reset_reenable_convergence_test.go:262 — `if _, _, err := svc.disableInvalidInbounds(db); err != nil` — the one existing direct caller; becomes `_, _, _, err`
- internal/web/service/reset_reenable_convergence_test.go:86-89 — `seedDepletableClient` ends by depleting the CLIENT (total=100, up=60, down=60); this is what made the rejected test vacuous
- internal/web/job/xray_traffic_job.go:82 — `needRestart0, clientsDisabled, err := j.inboundService.AddTraffic(traffics, clientTraffics)` — consumer 1, picks the widening up unchanged
- internal/web/job/xray_traffic_job.go:90-105 — `if clientsDisabled { … if restartOnDisable { … RestartXray(true) … } websocket.BroadcastInvalidate(MessageTypeInbounds) }` — the invalidate now also fires on inbound depletion, which is correct: the inbound row just changed
- internal/web/job/node_traffic_sync_job.go:134-147 — `_, clientsDisabled, err := j.inboundService.AddTraffic(nil, nil)` … `RestartXray(true)` … `j.structural.set()` — consumer 2; `structural` only drives a websocket invalidate at :234, so no added cost
- internal/web/service/xray.go:136 — `_, _, _ = s.inboundService.AddTraffic(nil, nil)` — third caller, discards all returns; unaffected. These three are ALL the callers
- internal/arch/depletion_restart_test.go:25 — `TestDepletionRestartIsForcedEverywhereItIsHonoured` pins both `if restartOnDisable` branches to `RestartXray(true)`; untouched by this fix, stays green
- internal/cores/cores.go:50 — `func ServedByXray(kind core.Kind) bool`, the sanctioned way to ask; :170-179 registers mtproto, amneziawg, wireguard, xray
- internal/cores/internal/xray/core.go:193-205 — `DropInstance` hot-applies the splice-out; never replaces the process
- internal/cores/internal/xray/core.go:113-117 — `Reconcile` returns nil on `Equals`, so the periodic supervise pass (internal/web/job/core_supervise.go:54) never heals this
- internal/cores/internal/mtproto/core.go:151-154 and internal/cores/internal/wireguard/core.go:162-164 — DropInstance already ends those sessions; the reason to scope the count
- internal/web/service/xray.go:1242-1245 and :1353-1359 — the RestartXray guard and the CAS in ApplyPendingRestart that clears the flag before it
- internal/web/translation/en-US.json:1386-1387 and internal/web/translation/fa-IR.json:1266-1267 — existing keys, value-only edit
- internal/web/runtime/local.go:63-72 `applierFor` — errors when `bound.Apply == nil`; the AWG sibling below
- internal/web/service/traffic_commit_test.go:38 — `svc.AddTraffic(...)` called directly in a test today, proving the serial writer needs no server (traffic_writer.go:149-152 falls back to `safeApply` inline when the queue is nil)

**Correction from the re-check:** Edit 1 as literally written does not compile, under either reading of its signature — fix mechanically, no design change. Verified in internal/web/service/inbound_disable.go: line 20 is `needRestart := false` and line 60 is `err := result.Error`. (a) If the four results are NAMED as the plan spells them — `(needRestart bool, disabled int64, xrayServed int64, err error)` — both of those lines become "no new variables on left side of :=". Repair: change them to `needRestart = false` and `err = result.Error`. (b) If the implementer instead keeps the results UNNAMED — `(bool, int64, int64, error)` — then the plan's own snippet `xrayServed++` references an undeclared variable. Repair: add `var xrayServed int64` immediately before the `for _, ib := range doomed` loop at :42. Either repair is fine; the load-bearing constraint is unaffected — the counter still goes in as the FIRST statement of the loop body, ahead of `s.runtimeFor(ib)` at :43, so it counts off `doomed` and not off a successful `rt.DelInbound`. Nothing else in the plan changes.
**Re-check notes:**
- COMPILE (see correction): Edit 1's signature/body collide. inbound_disable.go:20 `needRestart := false` and :60 `err := result.Error` break under named results; `xrayServed++` is undeclared under unnamed results. Mechanical, caught by `go build` in one second, does not touch the fix's substance.
- DISCLOSED SIBLING STILL LIVE (Q4): manual inbound disable has the identical hole and is not fixed here. Verified: controller -> UpdateInbound -> runtime/local.go:132-138 `if !newIb.Enable { return l.DelInbound(ctx, newIb) }` -> the same hot apply, then `SetToNeedRestart()`; ApplyPendingRestart (xray.go:1346-1358) CAS-clears the flag and calls RestartXray(false), and at xray.go:1242-1244 `!isForce && configUnchanged && !isNeedXrayRestart.Load()` is now all-true, so it early-returns. Live sessions survive a hand-disabled inbound exactly as they do a depleted one. The plan names this in `deferred` and the deferral holds: the setting's title is "Restart Xray After Auto Disable", so extending it to an admin toggle WOULD be the behaviour choice this subset is claiming to avoid. Not a blind spot — but the parent must file it, or F338 ships half the surface.
- Q1 REAL, NOT COSMETIC: every escape hatch is closed. In the SUCCESS path `disableInvalidInbounds` sets needRestart only on error (:44-52), and `count` at :61 is log-only, so nothing arms a restart. internal/cores/internal/xray/core.go:192-205 DropInstance hot-applies a splice-out and calls process.SetConfig (hotApply :412-430) — the process is never replaced, and per 8b23da39's measurement against Xray 26.7.28 the worker closes its listener and never tracks accepted connections. Nothing heals it later: Reconcile returns nil on `Equals` (core.go:113-117), and job/core_supervise.go:44 skips xray outright (`id == j.panelConverged`, cores.go PanelConvergedCore = xray.ID). The inbound is billed disabled, refuses new connections, and serves everyone already on it until something unrelated bounces Xray.
- Q2 NO SMUGGLED CHOICE: the tradeoff is already encoded in restartXrayOnClientDisable (setting.go:126 default "true"; title en-US.json:1386 "Restart Xray After Auto Disable" — entity-neutral). 8b23da39 (F335) accepted verbatim, for the identical condition, "a depletion event now briefly drops every connection on the box", and its message already lists inbound-level revocation among what the call failed to end. Scoping the count through cores.ServedByXray leaves mtproto/wireguard/AWG behaviour byte-identical (their DropInstance already ends sessions; AWG has none and is not counted). The arch guard internal/arch/depletion_restart_test.go pins both `if restartOnDisable` branches to RestartXray(true) and neither is touched; internal/arch/depletion_dispatch_test.go only forbids `.xrayApi.` selectors in these two files, and `cores.ServedByXray` is not one. Both stay green.
- Q3 GENUINELY RED, on the third assert: `if !autoDisabled`. Today addTrafficLocked returns `disabledClientsCount > 0` (inbound_traffic.go:116); with client_traffics zeroed to total=0/expiry_time=0 the client fails both halves of depletedCondFrom (:115-118) so disableInvalidClients contributes 0, and the inbound branch at :109-114 only logs. Returns false. The two guard asserts pass TODAY, which is what makes the third a measurement: the DB flip at :57-59 is independent of the rt.DelInbound failure above it — proven by the existing TestResetInboundTrafficRestoresAnInboundItsQuotaDisabled, which calls disableInvalidInbounds directly with no Xray running and asserts the same non-vacuity line. Goes green only because xrayServed is counted off `doomed`; count it off successful drops instead and it stays red headless.
- Q5 PROPORTIONATE: one extra return, a three-line counter, a named-return rename with zero job churn (all three callers verified — xray_traffic_job.go:82, node_traffic_sync_job.go:134, xray.go:136 which discards), one mechanical test-call update, two value-only locale edits, one test. No setting, column, job, route, key or abstraction. Self-limiting too: `doomed` filters `enable = true`, so a disabled inbound cannot re-enter it — this fires once per depletion, not once per poll.


### F352 — ship-it (one-line)
**Root:** Two distinct defects share one line of code, and the first plan conflated them.

(1) The enforcement defect (real, but bounded and DISPUTED): `smtpEncryptionType` is read as a hint, never a requirement. `sendPlain` (email.go:285-289) has `if ok, _ := client.Extension("STARTTLS"); ok { ... }` with no `else`, so an EHLO that omits STARTTLS falls straight through to MAIL/RCPT/DATA in cleartext; `Send`'s `case "starttls", "none":` (email.go:93-94) routes both settings to it, making them byte-identical. CORRECTED SEVERITY the first plan missed: `smtp.PlainAuth.Start` refuses to transmit credentials over an unencrypted connection unless the host is localhost/127.0.0.1/::1 (verified in GOROOT net/smtp/auth.go). So the SMTP password never leaves in the clear, and a credentialed non-localhost config against a STARTTLS-hiding relay ALREADY fails at auth today. What actually leaks is message content and recipient addresses, for credential-less or loopback relays only. That bounds the enforcement question and is why it is a behaviour choice, not an emergency.

(2) The divergence defect (UNDISPUTED, and the real live breakage): the policy is written twice and the two copies DISAGREE. `sendPlain` attempts the upgrade for BOTH "none" and "starttls"; `TestConnection` (email.go:172-179) attempts it only when `encryptionType == "starttls"`. The Test SMTP button therefore probes a transport the panel does not use, and lies in both directions (see shippableNow). Nothing about repairing that requires a policy decision — it only makes the diagnostic describe the code that actually ships.

Also corrected: the first plan's claim that the fix makes policy "expressed once" is false. Send's `default:` (95-96) errors on an unknown type while TestConnection's `default:` (151-152) dials plain and can report success; the stale comment at setting.go:173 names a value ("no") neither the UI nor Send accepts. Pre-existing, out of scope, not closed by this.
**Ships now:** ONE TOKEN in internal/web/service/email/email.go:173: `if encryptionType == "starttls" {` becomes `if encryptionType != "tls" {`. Body at 174-178 untouched, so a StartTLS handshake failure still returns `SMTPTestResult{false, "auth", classifySMTPError(err)}` — full classification parity preserved, which is precisely the collapse the adversary rejected. Replace the stale comment at 172 with one line: `// Mirror sendPlain: every transport but implicit TLS upgrades when the server advertises it.`

Nothing else. No helper, no sentinel, no `require` parameter, no signature change (email_deadline_test.go:38 compiles untouched), no new i18n key, no column, no knob. `!= "tls"` rather than deleting the condition is deliberate: sendWithTLS never calls Extension, so "tls" must keep skipping the probe for the mirror to be exact.

This is an undisputed repair because the Test SMTP button lies in BOTH directions today, and one token fixes both:

FALSE GREEN — encryption "none", no credentials, server advertises STARTTLS but the upgrade fails (self-signed or internal-CA cert; or smtpHost typed as a bare IP so `tls.Config{ServerName: host}` finds no matching SAN; or a 454). Test skips the upgrade, delivers the test mail in cleartext, returns `{true,"send","smtpTestSuccess"}`. Send's sendPlain DOES attempt it, hard-fails at 286-288, and every notification dies behind subscriber.go:47's `logger.Warning`. The alerting channel is dead and the panel's own diagnostic certifies it healthy.

FALSE RED — encryption "none", credentials set, server advertises STARTTLS with a valid cert. Send delivers fine (sendPlain upgrades, then auths over TLS). Test skips the upgrade, so PlainAuth.Start refuses on the unencrypted connection and Test reports `{false,"auth","pages.settings.smtpErrorUnknown: unencrypted connection"}` for a configuration that works.

No delivery path is touched: nothing that delivers today stops delivering, and no cleartext that flows today is stopped or newly permitted. The only change is that the diagnostic now describes the transport `Send` actually uses. No reasonable operator prefers a Test button that green-lights a dead channel and red-lights a working one.
**Deferred:** 1. THE ENFORCEMENT ITSELF — the missing `else` at email.go:285-289 and the `case "starttls", "none":` merge at 93-94. Making an unadvertised STARTTLS an error requires the decision below, because "starttls" is the shipped default (setting.go:173) with no save-time validation (entity.go:61): panels that enabled SMTP, never opened the dropdown, and point at a credential-less or loopback MTA that hides STARTTLS deliver today and would deliver nothing after, with subscriber.go:47's `logger.Warning` as the only trace.

2. THE SENTINEL AND HELPER the adversary proposed (`errNoStartTLS` + `startTLS(client, host, require bool)`, the `errors` import, the new `smtpErrorStarttlsUnsupported` key in en-US.json and fa-IR.json, the `requireTLS` parameter through sendPlain and its call site at email_deadline_test.go:38). Correct as a design, but it is machinery that exists only to serve whichever option is chosen — under option C it is shaped differently, under option B it degrades to a log line. Building it before the answer is building the wrong thing.

3. A `logger.Warning` on silent downgrade. Tempting as a "free" middle step, but it needs the same parameter plumbing and it quietly pre-commits to option B's semantics. It is the decision in weak form, not a repair.

4. NOT forward-compatible with every outcome, stated plainly rather than overclaimed: under option C the guard moves BACK to `== "starttls"` at both sites, and the new test moves with it. shippableNow is correct against today's Send, which is the only claim it makes.

5. The double-prefix rendering bug (EmailTab.tsx:120-122 vs classifySMTPError's `pages.settings.`-prefixed returns). Not blocked on any decision and worth its own one-commit fix, but it is a display-layer bug in a different layer from a transport-policy fix, and it is a separate finding — a focused diff keeps them apart.

6. The `default:`-branch disagreement (95-96 vs 151-152), the missing `validate:` tag on SmtpEncryptionType, and the stale `// no, starttls, tls` comment. Named, not claimed closed, not grown into.
**Test:** New test in internal/web/service/email/email_test.go, driving the real TestConnection code path.

Server helper — a variant of startFakeSMTPServer(t) at email_test.go:72 that: (a) answers EHLO with three lines, `250-fake\r\n` + `250-STARTTLS\r\n` + `250 OK\r\n`, so net/smtp's ehlo() parses lines[1:] into the ext map and `client.Extension("STARTTLS")` returns true; (b) answers a literal `STARTTLS` command with `454 TLS not available\r\n`; (c) otherwise behaves EXACTLY like the existing helper — DATA→`354 send`, a lone `.`→`250 ok`, QUIT→`221 bye`, everything else `250 ok`. Clause (c) is load-bearing and is where a careless version of this test loses its proof: without DATA→354 the pre-fix run dies at client.Data() and returns `{false,"send",...}`, a failure that looks like a pass of the assertion's spirit but proves nothing about STARTTLS.

Test body — initTestDB(t) (same setup as TestSendUsesBareAddressFromNameAddrSmtpFrom at :131): SetSmtpHost("127.0.0.1"), SetSmtpPort(fake port), SetSmtpFrom("panel@example.com"), SetSmtpTo("admin@example.com"), SetSmtpEncryptionType("none"), NO username/password. Then:
  got := NewEmailService(settingService).TestConnection()
  want := SMTPTestResult{Success: false, Stage: "auth", Message: "pages.settings.smtpErrorTls"}
  if got != want { t.Errorf("TestConnection() = %+v, want %+v", got, want) }

RED WITHOUT THE FIX, exactly: the unfixed guard `encryptionType == "starttls"` is false for "none", so STARTTLS is never attempted; the client walks MAIL/RCPT/DATA against the fake, the mail is accepted, and TestConnection returns success. The failure printed is:
  TestConnection() = {Success:true Stage:send Message:smtpTestSuccess}, want {Success:false Stage:auth Message:pages.settings.smtpErrorTls}

GREEN WITH IT: `!= "tls"` is true, Extension("STARTTLS") is true, client.StartTLS sends STARTTLS, gets 454, returns textproto error `454 TLS not available`; classifySMTPError falls to `strings.Contains(msgLower, "tls")` → "pages.settings.smtpErrorTls", returned at stage "auth". Flip the one token back and it prints the line above again.

Asserting the whole struct rather than just Success is deliberate and is the answer to the adversary's blocker: pinning Message to "pages.settings.smtpErrorTls" is what proves the StartTLS-handshake-failure path still runs through classifySMTPError and was not collapsed into one canned message.

Gate: TestConnection reads settings from the DB, so this test skips without PUI_DB_DSN, exactly like the existing TestConnectionReportsMissingFrom at :178. CI's live-Postgres job counts a SKIP as a failure, so it does gate. Run locally as `PUI_DB_DSN=... go test ./internal/web/service/email/ -run TestConnection -count=1`, and do the revert-and-watch-it-go-red cycle before committing.
**Sites:**
- internal/web/service/email/email.go:172-179 — TestConnection's `if encryptionType == "starttls" { if ok, _ := client.Extension("STARTTLS"); ok { ... } }`. THE SHIPPABLE SITE: the guard disagrees with sendPlain. Line 175-177 returns classifySMTPError(err) on a StartTLS handshake failure — this classification must survive the edit (the adversary's blocker).
- internal/web/service/email/email.go:285-289 — sendPlain's `if ok, _ := client.Extension("STARTTLS"); ok { ... }`, no else. The cleartext fallback itself. Deferred; also the reference behaviour the shippable fix mirrors.
- internal/web/service/email/email.go:93-96 — `case "starttls", "none":` sends both to sendPlain; `default:` errors. Deferred (splitting this is the enforcement change).
- internal/web/service/email/email.go:263-267 — sendPlain's doc comment, accurate as written today; must be rewritten only if enforcement lands.
- internal/web/service/email/email.go:145-153 — TestConnection's transport switch; its `default:` dials plain for unknown values Send rejects. Out of scope, named so it is not claimed closed.
- internal/web/service/setting.go:173 — `"smtpEncryptionType": "starttls", // no, starttls, tls` — the shipped default, which is what makes enforcement a behaviour change rather than a repair. Comment is stale ("no" vs "none").
- internal/web/entity/entity.go:61 — SmtpEncryptionType has no `validate:` tag, so nothing rejects a value Send will refuse at delivery time. Out of scope.
- internal/web/service/email/subscriber.go:47 — `logger.Warning("email subscriber: send failed:", err)` is the ONLY surface for a Send failure; this is why a broken transport is silent and why a lying Test button is the whole diagnostic.
- frontend/src/pages/settings/EmailTab.tsx:105-109 — the none/starttls/tls Select; two of three are one backend behaviour until enforcement lands.
- frontend/src/pages/settings/EmailTab.tsx:120-122 — `t('pages.settings.' + testResult.msg)` while classifySMTPError already returns a `pages.settings.`-prefixed key: every classified SMTP error renders as a raw key. Pre-existing, separate finding, NOT fixed here (see risk).
- internal/web/service/email/email_deadline_test.go:38 — the existing direct `s.sendPlain(...)` call site; untouched by shippableNow (no signature change), but any enforcement variant that adds a parameter must update it.
- internal/web/controller/setting.go:278 — the only caller of TestConnection (grep-verified), so the shippable change has exactly one consumer.


### F354 — ship-it (one-line)
**Root:** The restart signal in this repo is caller-owned, not service-owned: `OutboundSubscriptionService.Update/Delete/Move/Refresh` never signal — their controllers do. `updateOutboundSub` is the single handler in that family that omits the line: it calls `Update(...)` and falls straight to `jsonObj(c, "", nil)` (xray_setting.go:457-461), while its three siblings at :476, :493 and :510 all call `a.XrayService.SetToNeedRestart()`.

Subscription outbounds are never written into the stored template — they are recomputed from the DB inside `GetXrayConfig` (xray.go:158-161 -> `activeOutboundsSplit`, which filters `db.Where("enabled = ?", true)` at outbound_subscription.go:540). The merged outbound set therefore only changes when a config is regenerated, and the only thing that regenerates one is the need-restart flag being consumed by the 30s `ApplyPendingRestart` tick (web.go:359).

Consequence, verified end-to-end: `OutboundsTab.tsx:433` is the UI enable/disable toggle and it POSTs to this endpoint. Toggling a subscription OFF writes `enabled=false` to the DB, the row vanishes from the UI's active set — and its outbounds keep serving traffic in the running xray process. The state never self-heals: the cron's `RefreshAllEnabled` walks only `enabled = true` rows (outbound_subscription.go:276) and only signals `if count > 0` (outbound_subscription_job.go:41-43), so a row that was just disabled is exactly the row the cron will never touch again. The stale outbound survives until an unrelated edit or a panel restart.

Correction to the first plan: the root cause was right; the TEST was unrunnable. `initRouter` re-groups under `/xray` (xray_setting.go:40) and production hands it `/panel/api` (api.go:78, :124), so registering on `engine.Group("/panel/api/xray")` puts the route at `/panel/api/xray/xray/outbound-subs/:id`. The described POST 404s with an empty body, the envelope decode fails, and the test is red with and without the fix. The author copied `host_test.go`, which registers on the full path legitimately because `HostController.initRouter` (host.go:23) does not re-group.
**Ships now:** One line plus a one-line comment in `updateOutboundSub`, mirroring `deleteOutboundSub` exactly. At internal/web/controller/xray_setting.go, between the closing `}` of the `Update` error check (line 460) and `jsonObj(c, "", nil)` (line 461), insert:

    // Enabled/prepend/order feed the merged outbounds, so xray needs a reload.
    a.XrayService.SetToNeedRestart()

That is the whole change. No new column, no migration, no i18n key, no new route (so no `endpoints.ts` entry, no `make gen`, no openapi/docs regeneration), no service-layer move, no config knob.

Unconditional is correct and costs nothing on a no-op edit — verified, not assumed. `ApplyPendingRestart` consumes the flag with a CAS *before* calling `RestartXray(false)`, so by the time `RestartXray` reaches xray.go:1242 the flag is already false and the guard `if !isForce && configUnchanged && !isNeedXrayRestart.Load()` fires: `logger.Debug("It does not need to restart Xray"); return nil`. Renaming a subscription therefore builds one config object and bounces nothing.

When the outbound set genuinely changed, `tryHotApply` (xray.go:1254) carries an outbound add/remove over the core gRPC API without dropping connections. `diffOutbounds` (hot_diff.go:88-91) deliberately refuses when the *default* outbound or the tag set changes, falling back to a full restart — so the `prepend`-owns-outbound[0] case is a clean bounce, never a silently wrong default route.

Sibling coverage, all four handlers now consistent: update (fixed here), delete :476, refresh :493, move :510.
**Deferred:** Three things are deliberately left out. None of them blocks the line above, which is why the whole repair ships now.

1. MOVING THE SIGNAL INTO THE SERVICE LAYER. Tempting ("Update should signal itself"), but wrong here: Delete, Move and Refresh all deliberately leave it to the caller, so pushing it down would double-signal three working paths and touch four call sites to fix a one-line omission. That is a refactor, not this bug. Rejected, not deferred pending a decision.

2. THE UP-TO-30s LATENCY. Even with the fix, a disabled subscription's outbounds keep serving until the next `ApplyPendingRestart` tick (web.go:318, `@every 30s`). This is not introduced by the fix and not specific to it — delete, refresh and move have exactly the same window today. Making any of them synchronous is a family-wide behaviour change and belongs to its own finding. Worth knowing when judging severity: the fix takes the failure from unbounded to bounded-at-30s.

3. `createOutboundSub` (xray_setting.go:428-433) STAYS UNSIGNALLED. I verified this is correct rather than a second instance: `Create` performs no fetch (outbound_subscription.go:165-204), so the row has empty `LastFetchedOutbounds` and `activeOutboundsSplit` skips it at :544; the frontend then calls `refreshOne()`, which signals at :493. Adding a signal there would be noise. Same for `parseOutboundSubURL` (:526-543), whose temp row is `enabled=false` and therefore never merged.

NOTE FOR THE OWNER, not a question: when a `prepend` subscription owns outbound[0], toggling it changes xray's default outbound. `diffOutbounds` (hot_diff.go:88-91) refuses to hot-apply that and falls back to a full core restart — same as delete does today. That is the safe behaviour (the alternative would be a silently wrong default route), it is pre-existing, and it needs no decision.
**Test:** New file `internal/web/controller/outbound_sub_restart_test.go`, package `controller`, reusing the existing `initTestDB(t)` harness (testdb_test.go, PUI_DB_DSN-gated) and the `hostEnvelope` type already declared in host_test.go (same package, so it is directly reusable). No `t.Parallel()` — the need-restart flag is a package-level `atomic.Bool` (xray.go:31).

Shape:

    func TestUpdateOutboundSub_SignalsRestart(t *testing.T) {
        puilogger.InitLogger(logging.ERROR)
        gin.SetMode(gin.TestMode)
        initTestDB(t)

        sub := &model.OutboundSubscription{
            Remark: "s1", Url: "http://10.0.0.5/sub", Enabled: true, AllowPrivate: true,
            TagPrefix: "sub1-", UpdateInterval: 600,
            LastFetchedOutbounds: `[{"tag":"sub1-a","protocol":"freedom"}]`,
        }
        database.GetDB().Create(sub)  // t.Fatalf on err

        engine := gin.New()
        NewXraySettingController(engine.Group("/panel/api"))   // NOT ".../panel/api/xray"

        xraySvc := &service.XrayService{}
        xraySvc.IsNeedRestartAndSetFalse()   // drain whatever an earlier test armed

        form := url.Values{"remark": {"s1"}, "url": {"http://10.0.0.5/sub"},
            "tagPrefix": {"sub1-"}, "updateInterval": {"600"}, "enabled": {"false"},
            "allowPrivate": {"true"}, "allowInsecure": {"false"}, "prepend": {"false"}}
        req := httptest.NewRequest(http.MethodPost,
            "/panel/api/xray/outbound-subs/"+strconv.Itoa(sub.Id),
            strings.NewReader(form.Encode()))
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
        w := httptest.NewRecorder(); engine.ServeHTTP(w, req)
        // decode into hostEnvelope, t.Fatalf unless w.Code==200 && env.Success

        var stored model.OutboundSubscription
        database.GetDB().First(&stored, sub.Id)      // guard, see below
        if stored.Enabled { t.Fatalf("subscription %d still enabled after update", sub.Id) }

        if !xraySvc.IsNeedRestartAndSetFalse() {
            t.Fatalf("disabling outbound subscription %d did not raise the need-restart "+
                "flag; its outbounds stay in the running xray config", sub.Id)
        }
    }

EXACTLY WHAT FAILS WITHOUT THE FIX, AND WITH WHAT MESSAGE. The final assertion. Without the line, nothing in the request path touches `isNeedXrayRestart`: `Update` (outbound_subscription.go:207-235) only calls `s.Get`, `SanitizePublicHTTPURL` and `Save`; `nextDefaultSubPrefix` is skipped because tagPrefix is non-empty; `applyStagedOff` is on the Create path only. The flag was drained immediately before the POST, so `IsNeedRestartAndSetFalse()` returns false and the test fails with:

    disabling outbound subscription 1 did not raise the need-restart flag; its outbounds stay in the running xray config

With the one line, `SetToNeedRestart()` stores true, the CAS returns true, and the test passes. Nothing else in the process can set that flag during the test: no cron is running, no xray process exists, and `initTestDB`/`gin.New()`/route registration never reach the service package.

WHY EACH SETUP DETAIL IS LOad-BEARING (all verified in source, not assumed):
- Registration on `engine.Group("/panel/api")` — `initRouter` appends its own `/xray` (xray_setting.go:40), matching production (api.go:78, :124). This is the single change from the rejected plan; with the old `/panel/api/xray` group the request 404s and the test is red either way.
- `Content-Type: application/x-www-form-urlencoded` — the handler reads `c.PostForm` (xray_setting.go:443-450). A JSON body makes every field empty, `enabled := "" != "false"` becomes TRUE, and the test would then assert on a no-op update. This also matches the real frontend (http-init.ts:134-136).
- `allowPrivate=true` plus an RFC1918 URL — `SanitizePublicHTTPURL` short-circuits with `if allowPrivate { return clean, nil }` (url_safety.go:47-49) BEFORE `rejectPrivateHost`, so the test does zero DNS and zero network. Without it the private host is rejected, `Update` errors, `success:false`, and the test fails for the wrong reason.
- Non-empty `LastFetchedOutbounds` on the seed row — makes the row one that `activeOutboundsSplit` would actually merge (it `continue`s on empty at :544), so the scenario is the real harm, not a vacuous one.
- Zero-value `&service.XrayService{}` in the test observes the same flag as the controller's field because `isNeedXrayRestart` is package-level (xray.go:31) — verified, this is what makes the assertion valid across instances.

The `stored.Enabled` check is deliberately a GUARD, not the redness carrier: it passes with and without the fix (GORM `Save` writes zero values on a full update). It is there so a future refactor that breaks the persistence cannot leave a green test asserting only on a flag. State this in review so it is not mistaken for the failing assertion.

Run: `PUI_DB_DSN=... go test ./internal/web/controller/ -run TestUpdateOutboundSub_SignalsRestart -count=1`. Revert the one line and confirm it goes red with the message above before landing. Without PUI_DB_DSN it SKIPS, so a green local `make test-go` on a box with no PostgreSQL is not evidence — use the live-Postgres path.
**Sites:**
- internal/web/controller/xray_setting.go:457-461 — THE GAP. `if err := a.OutboundSubscriptionService.Update(...); err != nil { ... }` then straight to `jsonObj(c, "", nil)`. Insert the signal between :460 and :461.
- internal/web/controller/xray_setting.go:475-477 — sibling that gets it right (delete): `// Signal that xray should drop this subscription's outbounds on next reload.` / `a.XrayService.SetToNeedRestart()`
- internal/web/controller/xray_setting.go:492-493 — sibling (refresh): `// Signal that xray should pick up the new outbounds on next restart/reload` / `a.XrayService.SetToNeedRestart()`
- internal/web/controller/xray_setting.go:509-510 — sibling (move): `// Order affects the merged outbounds, so xray needs a reload.` / `a.XrayService.SetToNeedRestart()`
- internal/web/controller/xray_setting.go:40 — `g = g.Group("/xray")`: the re-group that broke the rejected test's route
- internal/web/controller/api.go:78 and :124 — `api := g.Group("/panel/api")` and `NewXraySettingController(api)`: the ONLY registration of this controller, so production paths are /panel/api/xray/*
- internal/web/controller/host.go:23 — `func (a *HostController) initRouter(g *gin.RouterGroup)` with NO re-group: why the copied host_test.go registration pattern misled the first plan
- internal/web/service/outbound_subscription.go:234 — `return database.GetDB().Save(sub).Error`: Update ends at the DB write, no signal (consistent with Delete/Move/Refresh, which also leave it to the caller)
- internal/web/service/outbound_subscription.go:540 — `if err := db.Where("enabled = ?", true).Order("priority asc, id asc").Find(&subs)`: the filter that makes `enabled` a config-generation input
- internal/web/service/outbound_subscription.go:276 — `db.Where("enabled = ?", true).Find(&subs)` in RefreshAllEnabled: the cron skips disabled rows, so the stale-outbound state never self-heals
- internal/web/job/outbound_subscription_job.go:38-43 — `if count > 0 { ... j.xraySvc.SetToNeedRestart() }`: the cron signals only when it actually refreshed something
- internal/web/service/xray.go:158-161 — `subSvc := &OutboundSubscriptionService{}` / `if prepend, appendList, err := subSvc.activeOutboundsSplit(); ...` -> `mergeSubscriptionOutbounds`: the merge that only re-runs on config regeneration
- internal/web/service/xray.go:1242-1245 — `if !isForce && configUnchanged && !isNeedXrayRestart.Load() { logger.Debug("It does not need to restart Xray"); return nil }`: proves the unconditional signal is free on a no-op edit
- internal/web/service/xray.go:1329-1330 and :1346-1347 — `SetToNeedRestart` / `IsNeedRestartAndSetFalse` over the package-level `isNeedXrayRestart atomic.Bool` (xray.go:31): any XrayService instance observes the same flag, which is what makes the test's zero-value service valid
- internal/xray/hot_diff.go:88-91 — `diffOutbounds` returns false on a default-outbound or tag-set change, so the prepend case falls back to a full restart rather than silently mis-routing
- internal/web/web.go:357-360 — `s.cron.AddFunc(cadenceXrayRestart, func() { s.xrayService.ApplyPendingRestart() })` at `@every 30s` (web.go:318): the only consumer of the flag
- frontend/src/pages/xray/outbounds/OutboundsTab.tsx:433 — `HttpUtil.post(`/panel/api/xray/outbound-subs/${sub.id}`, subBody({ ...sub, enabled: !sub.enabled }))`: the UI enable/disable toggle hits the broken handler
- frontend/src/pages/xray/outbounds/OutboundsTab.tsx:390-392 — the edit/save path hits the same endpoint, so a prepend or tagPrefix edit is equally stranded
- frontend/src/api/http-init.ts:134-136 — default `application/x-www-form-urlencoded`, confirming the handler's `c.PostForm` reads really do receive `enabled=false` (the write itself is NOT broken; only the signal is) and fixing the test's Content-Type
- internal/web/controller/xray_setting.go:428-433 (createOutboundSub) — CHECKED AND CLEARED, not a second instance: `Create` (outbound_subscription.go:165-204) does no fetch, so the new row has empty LastFetchedOutbounds and activeOutboundsSplit skips it at :544; the frontend then calls refreshOne(), which signals at :493
- internal/web/controller/xray_setting.go:526-543 (parseOutboundSubURL) — CHECKED AND CLEARED: creates a temp row with enabled=false, refreshes, deletes; a disabled row is never merged, so the absent signal is correct
- SEARCHED the whole mutation surface: `grep -rn OutboundSubscriptionService --include=*.go` shows `Update` has exactly ONE caller (xray_setting.go:457). No node-sync, tgbot or API duplicate. This is a singleton gap, not a pair.


### F355 — ship-with-the-noted-correction (small)
**Root:** The adversary is right; I verified every objection against the tree, and the original plan's REFRAME survives intact.

Confirmed unchanged from the original plan: setting.go:474-495 synthesizes only `fmt.Sprintf("socks5://127.0.0.1:%d", ...)` or "", so F355's "nothing enforces loopback" sub-claim is closed by construction and netproxy's http/https branch (netproxy.go:58-59) is unreachable for this caller. The panel does no destination resolution on the proxied path, so F355's stated TOCTOU mechanism is wrong. The comment at outbound_subscription.go:290-296 is ACCURATE for both branches — the original plan was wrong to call it a defect; netsafe.go:88-97 does resolve, check and dial the same IP. TestASuppliedTransportIsLeftAlone (subscription_fetch_transport_test.go:76-98) is real and forbids the naive dialer fix at the F355 site.

Confirmed against the original plan: the failure is COMPLETENESS, not root cause. The real root cause is a repo-wide pattern, not one branch: the panel expresses its SSRF policy as a *hostname* check (SanitizePublicHTTPURL, url_safety.go:42-60) and then hands the hostname to a *dialer that re-resolves it* — so every fetcher needs the guarded dialer to make the check binding. Three fetchers do not have it, and one of the three never runs the check at all.

Adversary objection 1 CONFIRMED — the missed anchor. internal/sub/external_subscription.go:24 `var subscriptionHTTPClient = &http.Client{Timeout: 6 * time.Second}` — no Transport, no guard. doFetchSubscriptionLinks:109-112 issues `http.NewRequestWithContext(context.Background(), GET, rawURL, nil)` straight from ClientExternalLink.Value. That value is validated ONLY by isHTTPURL (client_external_link.go:69-72: `u.Scheme == "http" || "https"` && `u.Host != ""` — no DNS, no private block); SanitizePublicHTTPURL is never called on this path. Reached from external_config.go:82 expandEntry, called by all three sub renderers (service.go:339, json_service.go:97, clash_service.go:58) on the token-only `GET /sub/:subid` routes (controller.go:283-296). It also follows up to 10 redirects with no per-hop check, unlike its sibling fetchAndStore.

Adversary objection 2 CONFIRMED — integration/warp.go:234 doWarpRequest was missing from the "cleared" list. It clears on the merits (warpAPIBase is hardcoded, warp.go:26), but the enumeration was presented as exhaustive and was not.

Adversary objection 3 CONFIRMED — the inform-client test collision is real. I disagree only with its conclusion that no workaround exists; see testStrategy.

TWO FINDINGS NEITHER PLAN HAD, and they decide the split:
(a) tgbot_test.go:95-101 TestTgbotProxyDialerNoneWhenEmpty asserts `client.Dial == nil` when no proxy — the literal inverse of the proposed fix. It must be replaced, and that replacement is itself the red-without-the-fix proof.
(b) ALL FIVE tests in external_subscription_test.go use httptest.NewServer, which binds 127.0.0.1. Installing SSRFGuardedDialContext on subscriptionHTTPClient turns all five red with "blocked private/internal address". That is not a test nuisance — it is the signal that blocking the initial URL there is a behaviour change, not a repair.
**Ships now:** Three edits. Each makes an ALREADY-DECIDED policy binding, or blocks a target no operator ever chose. None adds a column, job, abstraction or config knob.

(1) internal/web/job/xray_traffic_job.go:59-64 — add one field to the existing literal:
    Dial: func(addr string) (net.Conn, error) {
        return netsafe.SSRFGuardedDialContext(context.Background(), "tcp", addr)
    },
Undisputed because line 233 ALREADY runs SanitizePublicHTTPURL(informURL, false). A private inform URI is rejected today. The guard changes no reachable configuration — it only stops the hostname re-resolving to a different IP between the check and the dial. Signature verified: fasthttp v1.73.0 client.go:694 `type DialFunc func(addr string) (net.Conn, error)`.

(2) internal/web/service/tgbot/tgbot.go:387-393 — add the missing else to the existing if:
    } else {
        client.Dial = func(addr string) (net.Conn, error) {
            return netsafe.SSRFGuardedDialContext(context.Background(), "tcp", addr)
        }
    }
Strictly the else — installing it on the proxy branch would route around the operator's proxy, the exact trap outbound_subscription.go documents. Undisputed for the same reason as (1): tgbot.go:413 already runs SanitizePublicHTTPURL(apiServerUrl, false), so a loopback self-hosted Bot API server is ALREADY rejected today. This does not take away a working setup.

(3) internal/sub/external_subscription.go:24 — add a redirect guard to the existing client, and ONLY a redirect guard:
    var subscriptionHTTPClient = &http.Client{
        Timeout: 6 * time.Second,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) >= 10 {
                return errors.New("stopped after 10 redirects")
            }
            _, err := service.SanitizePublicHTTPURL(req.URL.String(), false)
            return err
        },
    }
internal/sub already imports internal/web/service (controller.go:23, links.go:7, service.go:30, sub.go:22), so this is no new dependency and no layering change. It reuses the exact policy function rather than restating it.

Why (3) is a repair and not a choice: the INITIAL url is typed by the operator — they chose that target. A REDIRECT target is chosen by the remote provider. No operator configures "let my subscription provider bounce my panel into my LAN," and this is exactly what the sibling fetchAndStore already does per hop. It is also the only variant that leaves all five existing httptest tests green, because none of them redirect — that is the evidence it takes away nothing that works today.
**Deferred:** TWO things, deliberately left out.

1. THE INITIAL external-subscription URL in internal/sub — whether an admin-typed ClientExternalLink.Value should be private-blocked at all. This is the severe half of the anchor and I am NOT shipping it, because the evidence says it is a behaviour choice, not a repair:
   - model.go:1285 OutboundSubscription.AllowPrivate exists. The repo has ALREADY decided that private subscription targets are a legitimate use case worth an explicit opt-in. ClientExternalLink (model.go:1091-1099) has no such column, so matching that pattern means a new column plus a migration in internal/database/db.go — precisely what "a small bug does not earn new columns" forbids without agreement.
   - All five existing tests in external_subscription_test.go fetch from httptest.NewServer on 127.0.0.1. A blanket guard turns them red. That is the codebase telling you loopback fetching is currently a supported shape.
   - The failure would be SILENT. fetchSubscriptionLinks (external_subscription.go:75-84) swallows every error with no log line at all and returns the stale cache or nil, by design. An operator with a LAN subscription would watch links disappear from their clients with nothing in the logs to explain it. A wrong guess here is invisible data loss, which is the worst possible shape for a guess.
   What ships instead closes the untrusted half (provider-controlled redirects) and leaves the trusted half (the target the admin typed) alone.

2. THE F355 SITE ITSELF — outbound_subscription.go:299-311 gets NO change. Verified: PanelEgressProxyURL can only return loopback or "", the panel resolves nothing on the proxied path (x/net emits AddrTypeFQDN for a non-IP host), the URL is SanitizePublicHTTPURL'd at :344 and every redirect hop is re-checked by fetchAndStore. The residual is that Xray resolves the destination under the panel-egress rule, so a rebind lands wherever panelOutbound points. Pinning the fetch to the validated IP would close it but makes Xray see an IP instead of a domain, so domain-based routing rules stop matching and CDN-hosted subscriptions break. The residual only bites when panelOutbound is aimed at a direct/freedom outbound, which defeats the feature's purpose. Cost of pinning greatly exceeds the benefit; I am not spending the owner's one decision on it. I am also WITHDRAWING the original plan's claim that the comment at :290-296 is defective — I read netsafe.go:88-97 and the comment is accurate.
**Test:** Three tests. I state exactly what fails and with what message, because the previous plans were rejected for tests that pass either way.

TEST 1 — new, internal/web/job/xray_traffic_inform_test.go, TestExternalInformClient_BlocksLoopback:
    if externalInformClient.Dial == nil {
        t.Fatal("the traffic-notify client dials unguarded")
    }
    _, err := externalInformClient.Dial("127.0.0.1:9")
    if err == nil || !strings.Contains(err.Error(), "blocked private/internal address") {
        t.Fatalf("loopback was not blocked: %v", err)
    }
WITHOUT the fix Dial is nil (verified: the literal at xray_traffic_job.go:59-64 has no Dial field), so it fails on the first branch with exactly "the traffic-notify client dials unguarded". It cannot pass either way — the field is literally absent today. The error substring is verified against netsafe.go:90 `fmt.Errorf("blocked private/internal address %s", ipAddr.IP)`.

COMPANION EDIT, not a new test — this is the adversary's objection 3, and it does have a workable answer it said did not exist. TestExternalInformClient_StalledReceiverTimesOut (xray_traffic_inform_test.go:38-67) must save and restore the field around its own call:
    saved := externalInformClient.Dial
    externalInformClient.Dial = func(a string) (net.Conn, error) { return net.Dial("tcp", a) }
    t.Cleanup(func() { externalInformClient.Dial = saved })
This keeps the PRODUCTION singleton as the subject of the boundedness property — ReadTimeout, WriteTimeout and DoTimeout are still the production values, so it does not degrade into TestExternalInformClient_HasDeadlines. Only the dial is substituted, and only for the one test that must reach a loopback stall. Safe under `-shuffle=on`: shuffle reorders, it does not parallelize, and no test in package job calls t.Parallel(). The adversary was right that "a non-loopback path" and "an explicit unguarded client" both fail; this third option was not considered.

TEST 2 — REPLACES internal/web/service/tgbot/tgbot_test.go:95-101 TestTgbotProxyDialerNoneWhenEmpty, whose assertion `if client.Dial != nil { t.Fatal("Dial must be nil when no proxy is configured") }` becomes exactly wrong. New TestTgbotDirectDialerIsSSRFGuarded:
    client := (&Tgbot{}).createRobustFastHTTPClient("")
    if client.Dial == nil {
        t.Fatal("the direct path dials unguarded")
    }
    _, err := client.Dial("127.0.0.1:9")
    ... same substring assertion
WITHOUT the fix Dial is nil, so it fails with "the direct path dials unguarded". The old test going red under the fix is itself the proof the behaviour changed; leaving it in place would be the test that passes either way.

TEST 3 — new, internal/sub/external_subscription_test.go, TestFetchSubscriptionLinksRefusesRedirectToPrivate:
    resetSubscriptionCache(t)
    var reached atomic.Bool
    target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        reached.Store(true)
        _, _ = w.Write([]byte("vless://leaked@example.com:443"))
    }))
    defer target.Close()
    redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Redirect(w, r, target.URL, http.StatusFound)
    }))
    defer redirector.Close()
    if links := fetchSubscriptionLinks(redirector.URL); links != nil {
        t.Fatalf("links = %#v, want nil — the redirect to a private address was followed", links)
    }
    if reached.Load() {
        t.Fatal("the panel connected to the redirect target")
    }
WITHOUT the fix the default client follows the 302 to the loopback target and returns one link, so it fails with `links = []string{"vless://leaked@example.com:443"}, want nil — the redirect to a private address was followed`. Both httptest servers bind 127.0.0.1, so the assertion is deterministic in CI with no unbindable address class needed — this is precisely why the redirect-only variant is testable where the initial-URL variant is not. Note resetSubscriptionCache(t) (line 14) is required or the package-level cache leaks across tests.

Run: `make test-go` and `cd frontend && npm test` are unaffected (no i18n keys, no generated types). No PUI_DB_DSN needed — none of the three tests touch the database.
**Sites:**
- internal/sub/external_subscription.go:24 — `var subscriptionHTTPClient = &http.Client{Timeout: 6 * time.Second}`. No Transport, no guard, no redirect check. THE MISSED ANCHOR.
- internal/sub/external_subscription.go:108-112 — doFetchSubscriptionLinks: `http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)` from an unvalidated ClientExternalLink.Value.
- internal/web/service/client_external_link.go:69-72 — `isHTTPURL`: scheme+host only. The write-time gate that SanitizePublicHTTPURL never sees.
- internal/web/job/xray_traffic_job.go:59-64 — `externalInformClient = &fasthttp.Client{...}` with no Dial field; nil Dial means fasthttp's unguarded default dialer.
- internal/web/job/xray_traffic_job.go:233 — `SanitizePublicHTTPURL(informURL, false)` already runs, then :252 DoTimeout dials unguarded. The check is non-binding.
- internal/web/service/tgbot/tgbot.go:387-393 — `if proxyUrl != "" { client.Dial = ... }` with no else; the direct path dials unguarded.
- internal/web/service/tgbot/tgbot.go:413 — `SanitizePublicHTTPURL(apiServerUrl, false)` already runs, then :423 builds the unguarded client.
- internal/web/service/outbound_subscription.go:299-311 — the F355 site itself. Verified correct as written; see deferred.
- internal/web/service/outbound_subscription.go:290-296 — the comment. Verified ACCURATE; the original plan's claim that it is the defect is withdrawn.
- internal/web/service/setting.go:474-495 — PanelEgressProxyURL, loopback-only by construction. Closes F355's stated sub-claim.
- internal/util/netsafe/netsafe.go:74-97 — SSRFGuardedDialContext; error string is `blocked private/internal address %s`.
- internal/web/service/url_safety.go:42-60 — SanitizePublicHTTPURL, the hostname-only policy the dialer must make binding.
- internal/database/model/model.go:1285 — OutboundSubscription.AllowPrivate. Proof private subscription targets are a supported opt-in use case.
- internal/database/model/model.go:1091-1099 — ClientExternalLink. No AllowPrivate column; adding one is a new column + migration.
- internal/web/service/integration/warp.go:234 — the enumeration member the original plan omitted. Clears on merits (warpAPIBase hardcoded, warp.go:26).
- internal/web/service/tgbot/tgbot_test.go:95-101 — TestTgbotProxyDialerNoneWhenEmpty asserts Dial == nil. Inverse of the fix; must be replaced.
- internal/web/job/xray_traffic_inform_test.go:38-67 — TestExternalInformClient_StalledReceiverTimesOut binds 127.0.0.1:0 through the production singleton.
- internal/sub/external_subscription_test.go:59,94,124,175,192 — five httptest.NewServer loopback tests that a naive guard would turn red.

**Correction from the re-check:** In edit (1) set fasthttp's `DialTimeout` (DialFuncWithTimeout), NOT `Dial`. Verified in fasthttp v1.73.0: `callDialFunc` (client.go:2221-2229) branches `if dialWithTimeout != nil { return dialWithTimeout(addr, timeout) }` BEFORE `if dial != nil { return dial(addr) }` — the `Dial` branch discards the request timeout entirely, which is what the field's own doc comment warns about at client.go:206-208. `AcquireConn` (client.go:1791) passes `req.timeout` straight into `dialHostHard` -> `dialAddr` -> `callDialFunc`, so today (Dial nil, timeout 3s) the dial is bounded by the 3s DoTimeout budget. With `Dial` set, an inform host that drops SYNs is bounded only by `netsafe.defaultDialer`'s 10s (netsafe.go:28) plus an unbounded `context.Background()` DNS lookup. The traffic job is `@every 5s` under `cron.SkipIfStillRunning` (web.go:319, 548-555) and `informTrafficToExternalAPI` is called synchronously from `Run()` (xray_traffic_job.go:109), so a 3s->10s dial turns into skipped traffic polls — exactly the budget `TestExternalInformClient_HasDeadlines` was written to protect ("must stay under the 5s traffic poll cadence"). Write it as:

    DialTimeout: func(addr string, timeout time.Duration) (net.Conn, error) {
        if timeout <= 0 {
            timeout = externalInformTimeout
        }
        ctx, cancel := context.WithTimeout(context.Background(), timeout)
        defer cancel()
        return netsafe.SSRFGuardedDialContext(ctx, "tcp", addr)
    },

(`cancel` after `net.Dialer.DialContext` returns does not affect the returned conn, so `defer cancel()` is safe; the ctx also bounds the DNS lookup, which `Dial` would not.) TEST 1 and the companion save/restore in `TestExternalInformClient_StalledReceiverTimesOut` must then target `externalInformClient.DialTimeout` instead of `.Dial` — assert `DialTimeout == nil` first (the field is absent from the literal today, so it is still genuinely red), then `DialTimeout("127.0.0.1:9", externalInformTimeout)` returns an error containing "blocked private/internal address". Edits (2) and (3) stand exactly as written.
**Re-check notes:**
- MECHANISM DEFECT in edit (1), the only blocking one. `Client.Dial` is the wrong fasthttp field. Verified fasthttp v1.73.0 client.go:2221-2229 `callDialFunc`: when `Dial` is set it calls `dial(addr)` and throws the timeout away; `DialTimeout` (DialFuncWithTimeout, client.go:709) takes precedence and receives it. Because `AcquireConn` (client.go:1791) hands `req.timeout` to `dialHostHard`, today's nil-Dial path is bounded at 3s by DoTimeout; the proposed `Dial` path is bounded only by netsafe's 10s dialer (netsafe.go:28) plus a `context.Background()` DNS lookup. `informTrafficToExternalAPI` runs synchronously in `Run()` (xray_traffic_job.go:109) on an `@every 5s` cron wrapped in `cron.SkipIfStillRunning` (web.go:319, 548-555), so an unreachable-but-public inform endpoint would start skipping traffic polls. See `correction`. Everything else about (1) checks out.
- Q1 — NOT cosmetic; it removes real breakage. The repo has already DECIDED this policy and written the rationale down: `internal/web/service/subscription_fetch_transport_test.go` (F363) states that `SanitizePublicHTTPURL` validates a hostname and only the dialer validates the resolved IP, and `outbound_subscription.go:309,327`, `node.go:87`, `tls_client.go:55,95` all install `netsafe.SSRFGuardedDialContext` for that reason. `xray_traffic_job.go:59-64` and `tgbot.go:387-393` are the two sites that call `SanitizePublicHTTPURL(_, false)` (lines 233 and 413) and then dial unguarded. Edits (1) and (2) make an existing check binding; they do not introduce a policy.
- Q2 — no behaviour choice smuggled in, verified at each site. (1) `informURL` is already rejected when private (line 233), so no reachable configuration changes. (2) `apiServerUrl` is already sanitized at :413 with allowPrivate=false and blanked on failure, so a loopback self-hosted Bot API server is already unreachable today; `createRobustFastHTTPClient` has exactly one production caller (`NewBot`, :423), so nothing else is affected; and putting the guard strictly in the `else` leaves both proxy dialers untouched, which is the property `TestASuppliedTransportIsLeftAlone` already pins for the sibling fetcher. (3) `CheckRedirect` is invoked only when `len(reqs) > 0` (Go client.go:699), i.e. never for the initial request, so the admin-typed target is untouched.
- Q3 — all three tests are genuinely RED, no test that passes either way. TEST 1: `externalInformClient` (xray_traffic_job.go:59-64) has fields ReadTimeout/WriteTimeout/MaxIdleConnDuration/MaxConnsPerHost and no dial field at all, so `if externalInformClient.Dial == nil { t.Fatal("the traffic-notify client dials unguarded") }` fires today (retarget to `DialTimeout` per the correction — equally absent, equally red). TEST 2: tgbot_test.go:98-100 currently asserts the exact inverse (`if client.Dial != nil { t.Fatal(...) }`), so `client.Dial == nil` is red today by construction. TEST 3: with `subscriptionHTTPClient` at its current `&http.Client{Timeout: 6 * time.Second}` the default CheckRedirect follows up to 10 hops, reaches the loopback target and `decodeSubscriptionBody` yields one link, so `links != nil` fires. The failure message quoted in the plan is accurate. Error substring verified against netsafe.go:48 `fmt.Errorf("blocked private/internal address %s", ipAddr.IP)`.
- Q3, supporting: the companion edit to `TestExternalInformClient_StalledReceiverTimesOut` is necessary and does not hide anything. That listener ACCEPTS the connection (external_subscription-style stall is in the read, xray_traffic_inform_test.go:27-31), so the property it covers is read/write boundedness, never dial boundedness — swapping only the dial while leaving ReadTimeout/WriteTimeout/DoTimeout at production values preserves exactly what the test was measuring. Verified no `t.Parallel` anywhere in `internal/web/job` or `internal/sub`, so mutating the package-level singleton is safe under `-shuffle=on`.
- Q4 — I enumerated every sibling independently and found NO unmentioned one. The full call set of `SanitizePublicHTTPURL(_, false)` is xray_setting.go:283/316, xray_traffic_job.go:233, outbound_subscription.go:166/212/344, tgbot.go:413. outbound_subscription is already guarded (:309, :327). xray_setting.go's `testURL` never reaches a Go dialer — `probeThroughRoute` (probe_http.go:461-473) dials a local SOCKS inbound and lets Xray resolve the host, the same shape as the deferred F355 residual. I also checked every other `NewProxiedHTTPClient` consumer the plan did not list (server.go:843/919/1754, panel/panel.go:349/411, integration/warp.go:234, nord.go:19): all fetch hardcoded api.github.com / github.com / warp / nord URLs, so there is no attacker- or operator-supplied hostname on any of them. The deferred sibling (the initial ClientExternalLink.Value) is real and knowingly left live.
- Q4, on the deferral's honesty — every load-bearing claim checks out. `OutboundSubscription.AllowPrivate` exists at model.go:1285 with `gorm:"default:false"`; `ClientExternalLink` (model.go:1091-1099) has no such column, so matching the pattern would need a column plus a db.go migration. All five tests in external_subscription_test.go do bind httptest loopback servers (lines 35, 69, 98, 148, 168) and none redirect, so the redirect-only variant leaves them green while a blanket guard would redden them. `fetchSubscriptionLinks` (external_subscription.go:76-82) does swallow the error with no log line and return the stale cache or nil. The deferral is correctly reasoned, not an excuse.
- Q4, one honesty caveat to state rather than a defect to fix: edit (3) is a CHECK-TIME guard, not the dialer guard the repo standardised in F363. A redirect to a hostname whose A record is 127.0.0.1 is caught (`rejectPrivateHost`, url_safety.go:71-82, resolves and checks every IP), but a true DNS rebind between the CheckRedirect lookup and the transport's own lookup still gets through. Installing a guarded Transport instead is not available here — Go builds each redirect request with the ORIGINAL request's context, so a `netsafe.ContextWithAllowPrivate` exemption for the initial hop would leak to every hop — so CheckRedirect is the right shape. Just describe it as closing the provider-chosen-target hole, not as closing rebinding.
- Q5 — not bigger than the bug. Three field/branch additions, one save-restore in an existing test, one replaced test, two new tests. No column, no migration, no job, no config knob, no new package, no layering change (`internal/sub` already imports `internal/web/service` at controller.go:23, links.go:7, service.go:30, sub.go:22, and `internal/web/service` does not import `internal/sub`, so no cycle). Proportionate.
- Two compile-level nits the plan's prose glosses (compiler catches both, listed only so they are not a surprise): Go imports are per-FILE, so external_subscription.go needs `errors` and `github.com/Arman2122/p-ui/internal/web/service` added to its own import block at lines 3-11 — the package-level import argument is about layering, not about this file compiling. And confirm the `net`/`context`/`time` imports land in xray_traffic_job.go and tgbot.go.
- One thing verified in the plan's favour that is easy to get wrong: setting a custom dial function does NOT break https on either fasthttp client. `dialAddr` (client.go:2199-2218) wraps the returned conn via `tlsClientHandshake` when `isTLS` and the conn has no `Handshake()` method, and `Client.Do` creates the HostClient with `Addr: AddMissingPort(host, isTLS)` (client.go:551), so the addr handed to the dial function always carries a port and `net.SplitHostPort` inside `SSRFGuardedDialContext` (netsafe.go:31) cannot fail on a portless `https://host/path` inform or Bot API URL.
- Also verified in the plan's favour: the withdrawal of the original claim about outbound_subscription.go:290-296 is correct, and `PanelEgressProxyURL` (setting.go:474-495) really is loopback-only by construction — both return paths are `fmt.Sprintf("socks5://127.0.0.1:%d", ...)` and every other path returns "". Go's `client.do` also closes the previous response body before returning a CheckRedirect error (client.go:708-726), so edit (3) leaks no body despite `doFetchSubscriptionLinks` returning `nil, err` without a Close.


### F362 — ship-it (small)
**Root:** ChangeWarpIP destroys the panel's only copy of the operator's WARP+ licence before any of the three code paths that could put it back. RegWarp does double duty: it registers a fresh device AND commits that registration to the single `warp` blob including `"license_key": license` taken from the brand-new device's own FREE account (warp.go:105-110, persisted at :120 via SetWarp). ChangeWarpIP reuses RegWarp for rotation (warp.go:188), so from line 191 onward the operator's key exists nowhere in the panel. Two exits then run with the throwaway key in storage and no restore: warp.go:202-204 (`UpdateWarpXraySetting` error -> `return "", err`, which does not even ATTEMPT the re-apply), and warp.go:206-210 (the re-apply itself fails -> `logger.Warning` + fall through). The first plan guarded only the second. VERIFIED against the code: (a) the sibling at :202-204 is real and is invisible on a default install because internal/web/service/config.json has no `tag:"warp"` outbound, so `updated` stays false and UpdateWarpXraySetting returns nil (xray_setting.go:215-223) -- it errors only for operators who actually added a warp outbound, i.e. exactly the operators using this feature, via GetXrayConfigTemplate, the template unmarshal at xray_setting.go:144, or SaveXraySetting/CheckXrayConfig rejecting the rewritten template. (b) The adversary over-claims one thing: the :197-199 unmarshal exit is unreachable -- `result` is marshalled by RegWarp from `map[string]any{"data": map[string]string, "config": json.RawMessage(body)}` where `body` was already unmarshalled into `map[string]any` at warp.go:83-86, so both fields are guaranteed to fit `parsed`. It needs no code. (c) The loss is irreversible from the panel and, worse, self-propagating: the stored free key becomes the source for the NEXT rotation's re-apply, so one failure silently retires WARP+ forever. I also DROP the first plan's "a transient failure self-heals" justification -- the adversary is right that the dominant trigger (the WARP+ licence's device limit; nothing in warp.go ever calls DELETE /reg/{id}, so every rotation leaks a device) is permanent until the operator prunes devices at Cloudflare.
**Ships now:** Restore the operator's licence into the stored blob ONCE, at warp.go:199 -- after the `parsed` unmarshal and BEFORE `UpdateWarpXraySetting` -- so it is in place ahead of both leaking exits. `parsed.Data` is content-identical to what RegWarp just persisted (same map, same MarshalIndent(..., "", "  ")), so this rewrites exactly one field:

  // RegWarp has just written the new device's own free licence over the
  // operator's stored WARP+ key; keep it so the re-apply below can retry.
  if license, ok := warpDataMap["license_key"]; ok && len(license) >= 26 {
      parsed.Data["license_key"] = license
      if restored, mErr := json.MarshalIndent(parsed.Data, "", "  "); mErr != nil {
          logger.Error("ChangeWarpIP: could not keep the stored WARP license: ", mErr)
      } else if sErr := s.SetWarp(string(restored)); sErr != nil {
          logger.Error("ChangeWarpIP: could not keep the stored WARP license: ", sErr)
      }
  }

Lines 201-212 are left exactly as they are. 8 lines, one file, one function, no new columns/jobs/knobs/i18n/abstractions; it reuses the identical `ok && len(license) >= 26` condition already at :206 so it introduces no new free-vs-premium discrimination.

DELIBERATE DEPARTURE from the adversary's correction, which said "moved earlier and made loud" but also proposed nothing about the re-apply's position -- I explicitly reject moving the SetWarpLicense call itself earlier (before UpdateWarpXraySetting), which is the other reading of "moved earlier". Linking a device to a WARP+ licence consumes one of that licence's device slots. On the UpdateWarpXraySetting failure path the freshly registered device is orphaned (the xray config still carries the OLD private_key, so xray keeps using the old device), and warp_ip_job.go:48 does not advance warpLastUpdate on failure, so the job retries hourly. Re-applying before the config save would therefore burn WARP+ device slots on orphaned devices every hour -- accelerating the exact exhaustion that is F362's dominant trigger. Only the RESTORE moves earlier; the re-apply stays last.

Why this is an undisputed repair: without it the panel permanently deletes an operator-supplied credential with no in-panel recovery path, and then carries the throwaway key forward as the source for every future rotation. No reasonable operator prefers "my WARP+ key is erased and never retried" over "my WARP+ key is retained and retried". It changes nothing about xray config generation -- UpdateWarpXraySetting (xray_setting.go:138-223) reads private_key, client_id and the config block only, never license_key.
**Deferred:** 1. REPORTING (the decision below). Whether a rotation whose licence re-apply failed should return an error instead of `result, nil`. Left at today's `logger.Warning` + success.

2. THE ADVERSARY'S CLAIM 3, ANSWERED AND PARTLY CONCEDED. It argued the restore removes the only observable tell that the tier was lost (WarpModal.tsx:335 renders `warpData?.license_key`, fed from GetWarp on every modal reopen at :167-176). Verified true, but the tell is already noise in the other direction: ChangeWarpIP discards SetWarpLicense's return value at :207 and returns `result`, which always carries the FREE key, so `changeIp()` (WarpModal.tsx:227-229) paints the free key into that row even when the re-apply SUCCEEDED. The row only becomes accurate after closing and reopening the modal, and the "tell" is one 36-char UUID silently replaced by another. The accurate tier check is untouched by this fix: the `config` action (GetWarpConfig -> GET /reg/{id}) returns `account.account_type`, already typed at WarpModal.tsx:47-53. So the honest tell survives; only a misleading one is lost. Surfacing the tier in the UI is option C of the decision, not part of this fix.

3. FREE/PREMIUM DISCRIMINATION. `len(license) >= 26` cannot tell a WARP+ key from a free UUID licence; `account.account_type` in the reg response is the real discriminator. Untouched -- changing it is a separate behaviour change. Accepted consequence: on a free-tier install the blob now carries the PREVIOUS device's free licence forward rather than the new device's own. Cosmetically stale in the modal; functionally identical to today, since :206-210 already re-applies whatever >=26-char string is stored regardless of tier.

4. STALE `data.license_key` IN THE SUCCESS RESPONSE (warp.go:207 discards SetWarpLicense's return). One line, but it changes what the UI shows immediately after a rotation, so it belongs with the reporting decision.

5. LEAKED DEVICES. Nothing ever calls DELETE /reg/{id} -- every rotation strands a device against the licence's limit, which is the root of the exhaustion that triggers F362. That is a feature, not this fix.

6. SPLIT-BRAIN ON THE :202 PATH. When the config save fails, storage points at the NEW device while the xray template still holds the OLD private_key. Pre-existing, orthogonal, untouched.
**Test:** New file internal/web/service/integration/warp_change_ip_test.go, two subtests, both gated by initTestDB(t) (skips without PUI_DB_DSN, per repo convention). Shared setup: fake Cloudflare via httptest; `warpAPIBase = srv.URL` with `t.Cleanup(func(){ warpAPIBase = old })`; seed storage with `(&WarpService{}).SetWarp(`{"access_token":"tok-old","device_id":"dev-old","license_key":"PLUS-1111-2222-3333-444455556666","private_key":"old"}`)`. The fake's `POST /reg` returns 200 with `{"id":"dev-new","token":"tok-new","account":{"license":"free-0000-0000-0000-000000000001"},"config":{"client_id":"AAAA","interface":{"addresses":{"v4":"172.16.0.2"}},"peers":[{"public_key":"pk","endpoint":{"host":"engage.cloudflareclient.com:2408"}}]}}`. Both subtests assert on STORAGE, re-read with GetWarp and unmarshalled — not on the return value — because the reporting behaviour is deferred and must not be baked into the test.

SUBTEST 1, "reapply rejected" (drives warp.go:206-210): fake returns 500 for `PUT /reg/dev-new/account`. Call ChangeWarpIP, then assert `stored["license_key"] == "PLUS-1111-2222-3333-444455556666"`.
RED WITHOUT THE FIX, traced through the actual code: RegWarp:120 persists the free key; the default template has no `tag:"warp"` outbound so UpdateWarpXraySetting returns nil at xray_setting.go:223 without touching storage; SetWarpLicense fails on the 500 and warp.go:208 only logs. GetWarp therefore returns the free key and the test fails with:
  `stored warp license_key = "free-0000-0000-0000-000000000001", want "PLUS-1111-2222-3333-444455556666": the rotation destroyed the operator's WARP+ key`
GREEN WITH IT: the restore at :199 already wrote the operator's key before SetWarpLicense was ever called.

SUBTEST 2, "config save fails" (drives the sibling exit warp.go:202-204, which subtest 1 cannot reach): before calling ChangeWarpIP, plant an unparseable xray template directly — `database.GetDB().Create(&model.Setting{Key: "xrayTemplateConfig", Value: "{"})`. It must bypass SaveXraySetting, which validates via CheckXrayConfig (xray_setting.go:34-35); a direct Create works because the schema is fresh (no row yet) and `key` carries only a non-unique index (model.go:851), and getString reads the DB with no cache (setting.go:343-355). GetXrayConfigTemplate then returns `{`, the unmarshal at xray_setting.go:144 fails, and ChangeWarpIP returns at :203. Assert BOTH that ChangeWarpIP returned a non-nil error (documenting that this exit does report failure, unlike the other) AND that `stored["license_key"] == "PLUS-1111-2222-3333-444455556666"`.
RED WITHOUT THE FIX: control never reaches :206, so nothing restores anything and the test fails with:
  `stored warp license_key = "free-0000-0000-0000-000000000001", want "PLUS-1111-2222-3333-444455556666": a failed config save destroyed the operator's WARP+ key`
Note this subtest is red against the FIRST plan's patch as well as against unpatched main — it is the regression test for the adversary's finding.

Verification protocol per CLAUDE.md: apply the fix, run `go test ./internal/web/service/integration/ -run TestChangeWarpIPKeepsOperatorLicense -v` with PUI_DB_DSN set, revert the 8 lines, watch both subtests go red with the messages above, restore. Imports `internal/database` and `internal/database/model`; the package's test files already import `internal/database` (testdb_test.go).
**Sites:**
- internal/web/service/integration/warp.go:26 — `warpAPIBase` is a `const`; the test seam. Change to `var` so a test can point RegWarp AND SetWarpLicense at a fake Cloudflare. Needed because doWarpRequest dials https://api.cloudflareclient.com and the panel-egress proxy cannot intercept it (an httptest proxy cannot terminate TLS for that host); warp_response_test.go already proves a plain-http httptest URL works through NewProxiedHTTPClient (setting.go:522-530).
- internal/web/service/integration/warp.go:105-110 — RegWarp builds the blob with `"license_key": license` taken from the BRAND-NEW device's own free account (`account.license`, read at :100-103). The source of the throwaway key.
- internal/web/service/integration/warp.go:120 — `if err := s.SetWarp(string(warpJSON)); err != nil {` — THE DESTRUCTIVE WRITE. The operator's WARP+ key ceases to exist in the panel here, before any exit below.
- internal/web/service/integration/warp.go:188-191 — `result, err := s.RegWarp(privKey, pubKey)`; every line after this runs with the operator's key already gone.
- internal/web/service/integration/warp.go:197-199 — the `parsed` unmarshal. UNREACHABLE as a failure exit (result is RegWarp's own marshal of a map[string]string plus a json.RawMessage of a body already proven to be a JSON object at :83-86), so it needs no guard — the adversary over-claimed here. This is the INSERTION POINT: the restore goes immediately after :199.
- internal/web/service/integration/warp.go:202-204 — THE SIBLING THE FIRST PLAN MISSED. `if err := xraySvc.UpdateWarpXraySetting(parsed.Data, parsed.Config); err != nil { return "", err }` — returns with the free key persisted and the re-apply never even attempted.
- internal/web/service/integration/warp.go:206-210 — `if license, ok := warpDataMap["license_key"]; ok && len(license) >= 26 { if _, licErr := s.SetWarpLicense(license); licErr != nil { logger.Warning(...) } }` — the only branch the first plan guarded. Left unchanged; the restore now sits ahead of it.
- internal/web/service/integration/warp.go:166-173 — SetWarpLicense's own success write (`warpData["license_key"] = license` then SetWarp). Makes the restore a harmless no-op re-write on the happy path.
- internal/web/service/integration/warp.go:207 — SetWarpLicense's returned blob is discarded, so `result` always carries the FREE licence even when the re-apply succeeded. Deferred item 4, and the evidence that the modal's licence row is already an unreliable tier tell.
- internal/web/service/xray_setting.go:138-223 — UpdateWarpXraySetting. Reads private_key, client_id and the config block ONLY, never license_key (so the restore cannot affect generated xray config). Its error paths: GetXrayConfigTemplate (:139), the template `json.Unmarshal` (:144), and SaveXraySetting (:220) inside `if updated`.
- internal/web/service/config.json:1-50 — the default template's only outbound tags are api/api/metrics_out/direct/blocked; no `tag:"warp"`. This is why `updated` stays false and the :202-204 exit is invisible on a default install but live for every operator actually using the feature.
- internal/web/job/warp_ip_job.go:43-50 — `_, err := j.warpService.ChangeWarpIP(); if err != nil { logger.Warning(...); return }` then `SetWarpLastUpdate(now)`. warpLastUpdate advances ONLY on success — this is what forces the decision question and what makes option B costly.
- internal/web/controller/xray_setting.go:196-202 — `case "changeIp"`: on nil error it calls SetToNeedRestart and SetWarpLastUpdate. The manual path that today green-toasts a silent tier loss.
- frontend/src/pages/xray/overrides/WarpModal.tsx:335 / :167-176 / :227-229 — the licence row, its GetWarp feed on modal reopen, and `changeIp()` overwriting it from the response. Read-only evidence for deferred item 2; no frontend change in this fix.
- frontend/src/pages/xray/overrides/WarpModal.tsx:47-53 — `account.account_type` is already typed on WarpConfig, fed by the `config` action (GetWarpConfig, warp.go:38-56). The accurate tier check that survives this fix untouched.

**Re-check notes:**
- Non-blocking accounting note, not a defect: the test needs `warpAPIBase` changed from `const` to `var` (warp.go:26) so the fake Cloudflare can be injected. The claim names this in `sites` but does not count it in "8 lines, one file, one function." It is a one-word change in the same file, all three uses (:44, :71, :140) are local, no arch guard in internal/arch touches warp, and warp_response_test.go already precedents the plain-http httptest path through NewProxiedHTTPClient. Disclosed, not smuggled — it does not change the verdict.


### F366 — ship-with-the-noted-correction (small)
**Root:** The adversary is right on all four grounds, verified against the tree:

(1) F366 is filed at email.go:81 — the **Send** path. Both original edits were inside TestConnection. Confirmed live: with encryption "none" + credentials against a server that does not advertise STARTTLS, email.go:285 falls through, email.go:290-293 still calls client.Auth, and GOROOT/src/net/smtp/auth.go:67-68 returns exactly `errors.New("unencrypted connection")` (isLocalhost is an exact match on "localhost"/"127.0.0.1"/"::1", so any real relay host takes this path). Send returns that bare string; its only consumer is subscriber.go:47-48 `logger.Warning("email subscriber: send failed:", err)`. SendTest() (email.go:314) has no non-test caller, so a log line reading `unencrypted connection` is the operator's entire signal.
(2) Closing TestConnection's open `default:` (email.go:151) is verbatim F353 (TRIAGE-2026-08-27.md:210), not F366.
(3) Changing email.go:173 to `!= "tls"` copies F352's defect (TRIAGE:206) into a second site, in the direction F352's triage argues against. Dropped entirely.
(4) classifySMTPError returns keys already namespaced (`pages.settings.smtpErrorAuth`, email.go:331-345) while EmailTab.tsx:124/126 renders `t('pages.settings.' + testResult.msg)`. i18n/react.ts:24-30 sets no nsSeparator, so i18next's default ':' applies: the seven named buckets become `pages.settings.pages.settings.smtpErrorAuth`, miss, and render as the literal key path — in fa-IR too. Verified `en.pages.settings.smtpErrorAuth` = "Authentication failed — check username and password" exists and is reachable only via the bare key.

Corrected framing: F366's **undisputed** harm is not that the config is accepted — that acceptance is F352/D1's call — it is that the doomed configuration fails **illegibly on both paths at once**: `unencrypted connection` in the log, and a raw i18n key path in the Test alert. Neither is a behaviour question, and both survive every F352 resolution.
**Ships now:** Make the auth-over-cleartext failure legible on both paths. No behaviour change: which configurations work is byte-for-byte identical before and after. No new columns, jobs, routes, config knobs, migrations, or abstractions. **No edit at email.go:145-153 or email.go:173** — TestConnection's transport switch and its STARTTLS gate are untouched (grounds #2 and #3 heard).

1. `internal/web/service/email/email.go:285-294` (sendPlain — the Send path F366 is filed against). Set a local `upgraded := false`, assign `upgraded = true` after a successful `client.StartTLS`, and on auth failure wrap only when the session stayed in cleartext:
```go
if auth != nil {
    if err = client.Auth(auth); err != nil {
        if !upgraded {
            return fmt.Errorf("smtp auth on a cleartext session (the server offered no STARTTLS): %w", err)
        }
        return err
    }
}
```
`upgraded` is exactly Go's `ServerInfo.TLS`, so the guard is precise, `%w` keeps the real cause, and the wrap stays correct whichever way F352 resolves. ~5 added lines, comment ≤2 lines or none.

2. `internal/web/service/email/email.go:325-347` (classifySMTPError). Add one case before the default: `case strings.Contains(msgLower, "unencrypted connection"): return "smtpErrorUnencrypted"` — verified it matches none of the eight existing arms today ("unencrypted connection" contains no "tls", "authentication", "dial", "eof", …), so it lands in the unknown bucket. And drop the `pages.settings.` prefix from all eight returns, since EmailTab already prepends it.

3. `internal/web/translation/en-US.json` and `fa-IR.json` — add `pages.settings.smtpErrorUnencrypted`, e.g. "The server offered no encryption, so the credentials were not sent — set encryption to STARTTLS or TLS". Plain sentence, **no `{{ }}`**: i18n/react.ts:28 sets `interpolation: { prefix: '{', suffix: '}' }`, which is why the existing `smtpErrorUnknown: "SMTP error: {{ .Error }}"` is already inert.

Why (2)'s prefix strip belongs here and is not scope creep: without it the new key renders as the literal `pages.settings.pages.settings.smtpErrorUnencrypted`, so the fix never reaches the operator. Verified safe: `grep -rn "smtpError" frontend/src/ docs/` returns nothing, no Go or TS test asserts the prefixed strings, SMTPTestResult is absent from tools/openapigen and from frontend/public/openapi.json (no `make gen` churn), and the dead-keys test keeps every `pages.settings.*` key alive via the prefix harvested from EmailTab's `'pages.settings.' +` concatenation (i18n-dead-keys.test.ts:92-97), so nothing goes dead either way. The unknown bucket's render is unchanged by the strip: `t('pages.settings.smtpErrorUnknown: <err>')` still splits on the first ':' into a missing namespace and renders the raw error tail, exactly as today. The seven named buckets go from a raw key path to their real localized sentence in both locales.
**Deferred:** (a) **The F352/D1 semantic choice** — whether "none" should keep opportunistically upgrading. Nothing in shippableNow touches the upgrade decision on either path, so both directions stay open at zero cost.
(b) **Rejecting or auto-correcting none+credentials at save time.** This is F366's "accepted configuration" half, and it is only correct under option B: under status-quo option A the combination genuinely works against any STARTTLS-capable relay, so rejecting it would break working panels. Follows the decision; also lands in entity.CheckValid, which is F353's territory.
(c) **F353 in full** — closing TestConnection's open `default:` at email.go:151 (today an empty or garbage smtpEncryptionType gives a green Test button while Send returns "unknown SMTP encryption type" and no notification ever ships), the missing `validate:` tag at entity.go:61, and the stale `// no, starttls, tls` comment at setting.go:173. Real, filed, and not landing under F366's number.
(d) **smtpErrorUnknown's shape** — `fmt.Sprintf("smtpErrorUnknown: %s", msg)` can never be localized while the wire format is a single string; fixing it means a `{key, detail}` response contract, bigger than this bug.
(e) **F352's own downgrade fix** and the "None (plain text)" label (en-US.json:1536), which is a lie only under option A.
**Test:** One Postgres-gated Go test in `internal/web/service/email/email_test.go`, three subtests, each red today with a named message. The existing `startFakeSMTPServer` (email_test.go:72) gains an address parameter (its one caller at :134 updated) because it hardcodes `127.0.0.1:0` and Go's `isLocalhost` would then let PlainAuth send credentials in cleartext, hiding the bug. Subtests A and B each get their own listener on `127.0.0.2:0` — the helper does a single `ln.Accept()`. Binding 127.0.0.2 is a Linux-ism; that is the only supported platform and the test already skips without `PUI_DB_DSN`. The fake server answers `250 ok` to EHLO and never advertises STARTTLS, which is exactly F366's scenario.

Setup per subtest: `initTestDB(t)`, SetSmtpHost("127.0.0.2"), SetSmtpPort(fake port), SetSmtpFrom/To, SetSmtpUsername("u"), SetSmtpPassword("p"), SetSmtpEncryptionType("none").

**A — the Send path (fails without edit 1).** `NewEmailService(settingService).Send("s", "b")` must return a non-nil error whose text contains "cleartext" and "STARTTLS".
Today it fails with: `send error = "unencrypted connection", want a message naming the cleartext session and the missing STARTTLS`. Nothing swallows it — client.Auth's Start-failure path returns before any command is sent.

**B — the Test path (fails without edit 2's new case).** `TestConnection()` must return `Stage == "auth"` and `Message == "smtpErrorUnencrypted"`.
Today it fails with: `Message = "pages.settings.smtpErrorUnknown: unencrypted connection", want "smtpErrorUnencrypted"`.

**C — pins the prefix strip, which A and B do not** (a partial fix that only added the bare new case would pass A+B). Open a listener on 127.0.0.1:0, record the port, close it immediately, point TestConnection at that dead port. Assert `Message == "smtpErrorRefused"`. No fake server needed.
Today it fails with: `Message = "pages.settings.smtpErrorRefused", want "smtpErrorRefused"` — i.e. the exact key the UI cannot resolve.

Revert-check: reverting edit 1 alone reddens A; reverting the new case alone reddens B; reverting the strip alone reddens C. None of the three can pass against the current tree.
**Sites:**
- internal/web/service/email/email.go:285-293 — sendPlain: `if ok, _ := client.Extension("STARTTLS"); ok { ... }` with no else, then `if auth != nil { if err = client.Auth(auth); err != nil { return err } }` — the Send-path site F366 is actually filed against (EDIT)
- internal/web/service/email/email.go:79-84 — `var auth smtp.Auth; if username != "" && password != "" { auth = smtp.PlainAuth(...) }` — F366's cited line 81; NOT edited, the acceptance decision is deferred
- internal/web/service/email/email.go:325-347 — classifySMTPError: eight returns carrying `pages.settings.` and a default `fmt.Sprintf("pages.settings.smtpErrorUnknown: %s", msg)` (EDIT: new unencrypted case + strip prefix)
- internal/web/translation/en-US.json:1556-1563 and internal/web/translation/fa-IR.json:1436-1443 — the smtpError* block (EDIT: add smtpErrorUnencrypted to both)
- frontend/src/pages/settings/EmailTab.tsx:124,126 — `t('pages.settings.' + testResult.msg)`, the double-prefix consumer (no edit; the Go side is corrected to match its convention)
- frontend/src/i18n/react.ts:24-30 — no nsSeparator override, so i18next's default ':' splits the unknown bucket (evidence only)
- internal/web/controller/setting.go:278-292 — testSmtp passes result.Message straight into gin.H and logs it; the sole consumer of the key (evidence only)
- internal/web/service/email/subscriber.go:47-48 — `logger.Warning("email subscriber: send failed:", err)`, the only place a Send error surfaces (evidence only)
- internal/web/service/email/email.go:145-153 — TestConnection's open `default:` transport arm — DEFERRED, this is F353
- internal/web/service/email/email.go:173 — `if encryptionType == "starttls"` — DEFERRED, touching it hardens F352
- internal/web/entity/entity.go:61 — `SmtpEncryptionType string` with no validate tag — DEFERRED, F353
- internal/web/service/setting.go:173 — `"smtpEncryptionType": "starttls", // no, starttls, tls` — comment names `no`, code accepts `none` — DEFERRED, F353
- internal/web/translation/en-US.json:1563 / fa-IR.json:1443 — `smtpErrorUnknown` uses Go `{{ .Error }}` against i18next's `{`/`}` interpolation, so the placeholder is inert — DEFERRED

**Correction from the re-check:** Subtest C as specified is ~0.5%-flaky even with the fix applied, and the flake asserts the exact string the test is meant to reject. classifySMTPError's third arm (`strings.Contains(msg, "465")`) is evaluated BEFORE the "connection refused"/"dial" arm, and the dial error embeds the port: `dial tcp 127.0.0.1:46512: connect: connection refused` classifies as smtpErrorTls, not smtpErrorRefused. On Linux's default ephemeral range 32768-60999, 158 of 28232 ports contain "465" as a substring (46500-46599; 34650-34659/44650-44659/54650-54659; 33465..60465 step 1000) — about 0.56% per run, doubled by CI's separate `make race` job. Fix: after `ln.Addr()` gives the port, re-pick the listener while `strings.Contains(portStr, "465")` (two lines); or, since C's only job is pinning the prefix strip and email_test.go is `package email`, drop the listener entirely and assert `classifySMTPError(errors.New("dial tcp 127.0.0.1:25: connect: connection refused")) == "smtpErrorRefused"` — zero network, zero flake, still red today (`"pages.settings.smtpErrorRefused"`). Nothing about the three code edits changes.
**Re-check notes:**
- Subtest C flake (see correction): arm 3 `Contains(msg, "465")` precedes the refused arm and the dial error embeds the ephemeral port, so ~0.5% of runs assert smtpErrorTls instead of smtpErrorRefused even with the fix applied. Test-spec only; the code edits are unaffected.
- Implementer note, not a defect in the plan: the locale-parity test in i18n-dead-keys.test.ts checks key sets only, so nothing enforces that fa-IR's smtpErrorUnencrypted is actually Persian — write a real translated sentence, not the English string.


### F369 — ship-with-the-noted-correction (small)
**Root:** Verified by reading the code; the adversary is right on all four points and the original plan's core diagnosis and its rebuttal of the finding's suggested fix both survive.

`LdapSyncJob.Run()` decides who to enable/disable from a PANEL-WIDE client index and never bounds that decision by `ldapInboundTags`. At ldap_sync_job.go:107-113 every inbound's clients are folded into one `email -> *model.Client` map (`allClients`), built BEFORE the configured tags are resolved at :121-131. The decision loop at :137-152 then does a bare `existing := allClients[email]` with no membership test, so any email anywhere on the panel matches a directory entry and is queued under every configured tag. The tag in the queue key is decoration: `batchSetEnable` (:305-330) passes only the email to `SetClientEnableByEmail` (:312) and uses `ib` solely for log lines (:314, :325); `applyClientFieldByEmail` (client_inbound_apply.go:1161) resolves `GetInboundIdsForEmail` and patches every inbound hosting that identity.

Precise statement of the defect — this is IDENTITY membership, not write scoping: the writer is deliberately identity-wide (#5039, client_inbound_apply.go:1156-1160) and stays that way. The bug is that an identity with NO presence on any configured inbound is still governed by LDAP. A governed identity that also sits on a non-LDAP inbound will still be written there after the fix, by design.

Blast radius bound (setting.go:148): `ldapInboundTags` defaults to `""`, and an empty value makes `resolvedTags` empty, so NO enable/disable happens at all. Every panel that can hit F369 is one where an operator deliberately typed tags — i.e. deliberately narrowed scope and got panel-wide governance instead.

The finding's own suggested fix ("scope the map build to resolvedInboundIds") is wrong, re-verified: `buildClient` (:264-275) never sets SubID, `Create` mints one at client_crud.go:92-93, and the `existing.SubID == "" || existing.SubID != client.SubID` branch at client_crud.go:107-109 then returns `email already in use`. Scoping `allClients` makes a client living only on a non-LDAP inbound look absent, so a flagged-true alice falls into `clientsToCreate` and Create fails every tick. `allClients` must stay panel-wide — it is the auto-create existence gate at :139.

The three substantive adversary corrections are confirmed:
(a) Test seam. `InboundService.GetClients` (inbound.go:456) is `return ParseInboundSettingsClients(inbound.Settings)`, and `ParseInboundSettingsClients` (inbound_settings_clients.go:11-32) is a pure JSON parse with no DB. A DB-free, LDAP-free planner over `[]*model.Inbound` is therefore possible, so the set-building — not just the guard — can be driven by a real test.
(b) Silent description. `pages.settings.ldap.inboundTagsDesc` (en-US.json:1419, fa-IR.json:1299) says only "auto-create or auto-delete", and it is the only operator-facing statement of scope. It is rendered at frontend/src/pages/settings/GeneralTab.tsx:369, so both files are a value edit of an already-referenced key — no i18n-dead-keys exposure.
(c) Swallowed error. `clients, _ := j.inboundService.GetClients(ib)` at :109 discards the error. Once the read defines LDAP's authority, a parse failure on a configured inbound silently means "LDAP governs nobody here this tick" with no log line.
(d) Minor, corrected: the original plan's sibling claim ("`ldapInboundTags` is the only tag/inbound-scope setting") is FALSE. `model.Node.InboundTags` (model.go:873) with `InboundSyncMode == "selected"` is a second one — but it is correctly enforced (inbound_node.go:147-148 builds the `selected` set before sweeping; node.go:666-667 `nodeSelectedTagSet`). No live sibling, but the claim must be restated as "two inbound-scope settings, the other one enforced."
**Ships now:** Scope the ENABLE direction to identities LDAP actually governs, and stop the read that now defines that authority from failing silently. The disable direction keeps today's panel-wide behaviour — see `deferred`.

The undisputed principle: the panel must not revert an administrator's manual disable of a client that has no presence on any inbound the operator placed under LDAP control. Today that revert happens on every tick (default `@every 1m`) and the admin has no workaround — they disable the client, LDAP re-enables it, forever.

Three edits, all in `internal/web/job/ldap_sync_job.go` plus two locale strings. Net new logic ~10 lines.

1. EXTRACT the index-and-decide block (currently ldap_sync_job.go:105-152) into one unexported, DB-free, LDAP-free function in the same file. This is code movement, not new architecture — no new columns, jobs, config knobs or cross-layer abstractions. It exists so the new code can be driven by a real test; `Run()` itself is untestable because `ldaputil.FetchVlessFlags` needs a live directory.

```go
type ldapPlan struct {
	inboundMap   map[string]*model.Inbound
	resolvedIds  []int
	resolvedTags []string
	createEmails []string
	enable       map[string][]string // tag -> emails
	disable      map[string][]string
}

func planLdapSync(inbounds []*model.Inbound, inboundTags []string, flags map[string]bool, autoCreate bool) ldapPlan
```

It calls `service.ParseInboundSettingsClients(ib.Settings)` directly (the exact function `GetClients` delegates to) instead of going through the `InboundService` receiver, so the error is visible and no DB handle is needed. `Run()` keeps `buildClient` and its defaults, mapping `plan.createEmails` through it before `j.createClients(newClients, plan.resolvedIds, plan.resolvedTags)`, then drives `plan.enable` / `plan.disable` into the untouched `batchSetEnable`.

2. INSIDE the planner, build the governed set alongside the panel-wide index, and gate only the enable branch on it:

```go
tagSet := make(map[string]struct{}, len(inboundTags))
for _, tag := range inboundTags {
	tagSet[tag] = struct{}{}
}
// ... in the inbound loop, after parsing clients:
_, configured := tagSet[ib.Tag]
for i := range clients {
	allClients[clients[i].Email] = &clients[i]
	if configured {
		governed[clients[i].Email] = struct{}{}
	}
}
```

and in the decision loop, `existing` STILL read from `allClients` (behaviour-preserving; the auto-create gate at :139 is unchanged):

```go
_, isGoverned := governed[email]
for _, tag := range resolvedTags {
	switch {
	case allowed && !existing.Enable && isGoverned:
		enable[tag] = append(enable[tag], email)
	case !allowed && existing.Enable:
		disable[tag] = append(disable[tag], email)
	}
}
```

`governed` is a `map[string]struct{}` membership set, NOT a second `*model.Client` map — swapping the read source for `existing` would add a divergence-healing semantic nobody asked for.

3. STOP THE SILENT AUTHORITY LOSS. Replace `clients, _ := ...` with a captured error and warn when the failure is on a configured tag, since that read now decides who LDAP may enable:

```go
clients, err := service.ParseInboundSettingsClients(ib.Settings)
if err != nil && configured {
	logger.Warningf("LDAP sync cannot read clients of inbound %s; it governs nobody there this tick: %v", ib.Tag, err)
}
```

Do NOT escalate to skipping the tick — that is new behaviour beyond the bug.

4. LOCALE, both files, same commit (value edit of an already-referenced key, so no i18n-dead-keys risk). Word it as identity membership, never as write scoping — the #5039 writer is still identity-wide, so a governed client who also sits on a non-LDAP inbound IS still written there:

- internal/web/translation/en-US.json:1419 → "Inbounds that LDAP sync may auto-create or auto-delete clients on. Only clients present on these inbounds are re-enabled; a directory disable still applies panel-wide."
- internal/web/translation/fa-IR.json:1299 → "ورودی‌هایی که همگام‌سازی LDAP اجازه دارد روی آن‌ها کاربر بسازد یا حذف کند. فقط کاربرانی که روی این ورودی‌ها حضور دارند دوباره فعال می‌شوند؛ غیرفعال‌سازی از سمت دایرکتوری همچنان روی کل پنل اعمال می‌شود."

Untouched: `batchSetEnable`, `SetClientEnableByEmail`, `applyClientFieldByEmail` and the #5039 contract, `TestSetClientEnableByEmail_MultiInbound` (client_apply_field_test.go:85), `createClients`, `deleteClientsNotInLDAP`, every setting, every model, every route. No migration, no `make gen`, no OpenAPI or docs step.
**Deferred:** SCOPING THE DISABLE DIRECTION. After `shippableNow`, `!allowed && existing.Enable` still queues a disable for any email anywhere on the panel, and the identity-wide writer still applies it everywhere. That stays, deliberately, because narrowing it is a fail-OPEN change: an operator who off-boards someone in the directory today loses access panel-wide, and `ldapAutoDelete` is off by default, so panel-wide disable is very plausibly the off-boarding mechanism in use. Silently shrinking a revocation's reach on upgrade is exactly the change that needs the owner, not me.

THE CONSEQUENCE THE OWNER MUST SEE — a one-way ratchet that `shippableNow` creates and does not remove: LDAP can still disable a directory-known client who lives only on a non-LDAP inbound, but can never re-enable them there. Their remedy is one manual re-enable in the panel. Pre-fix the two directions were symmetric (both panel-wide); post-fix they are not, and that asymmetry lives until the decision lands.

ALSO DEFERRED, and out of scope for F369: `ldapInboundTags` defaulting to `""` means a panel that never sets it gets NO LDAP enable/disable at all, silently. That is arguably its own finding (an operator who enables LDAP sync and configures nothing gets a job that only auto-creates). Not touched here — it is a separate behaviour question and bundling it would make this fix a redesign.

NOT DEFERRED, explicitly rejected as out-of-size: scoping the writer, adding a per-inbound enable column, a new "LDAP-managed" flag, or making `batchSetEnable` inbound-aware. All of those fight the #5039 contract, which is correct and deliberate.
**Test:** New pure test file `internal/web/job/ldap_sync_scope_test.go`. No Postgres, no LDAP server, no `initTestDB` — `planLdapSync` takes `[]*model.Inbound` with literal `Settings` JSON and calls `ParseInboundSettingsClients`, which is a pure parse (inbound_settings_clients.go:11-32). So these run on a bare `make test-go`, unlike the existing `TestLdapCreateClients_AttachesToAllConfiguredInbounds`, which skips without `PUI_DB_DSN`.

Shared fixture: `ldapIn` = tag `ldap-in`, `{"clients":[{"email":"bob@corp","enable":false}]}`; `vipIn` = tag `vip-in`, `{"clients":[{"email":"alice@corp","enable":false},{"email":"dave@corp","enable":true}]}`. Configured tags: `[]string{"ldap-in"}`.

TEST 1 — THE RED ONE. `TestLdapSyncDoesNotReEnableAClientOutsideItsInbounds`:
`planLdapSync([]*model.Inbound{ldapIn, vipIn}, []string{"ldap-in"}, map[string]bool{"alice@corp": true}, false)` → assert `len(plan.enable["ldap-in"]) == 0` and `len(plan.createEmails) == 0`.

WHAT "RED WITHOUT THE FIX" MEANS OPERATIONALLY — stated precisely because `planLdapSync` does not exist pre-fix: extract the planner, then delete the `isGoverned` condition from the enable case (equivalently, populate `governed` unconditionally — the exact copy-paste slip the adversary named). The test then fails with:

  plan.enable["ldap-in"] = [alice@corp], want empty: alice@corp lives only on vip-in, which the operator never listed in ldapInboundTags; the admin's manual disable is reverted on every tick

That failure is the bug verbatim, and it is produced by BOTH ways the new code can be wrong (condition missing, or set populated unconditionally), which is what the adversary rejected the previous seam for not covering. The `createEmails` assertion is not decoration — it pins the reason `allClients` must stay panel-wide: scope that map instead and alice looks absent, falls into auto-create, and `ClientService.Create` returns "email already in use" (client_crud.go:107-109) every minute.

TEST 2 — the anti-over-correction control, HONESTLY passes both ways. `TestLdapSyncStillEnablesAClientOnAConfiguredInbound`: flags `{"bob@corp": true}` → assert `plan.enable["ldap-in"]` is exactly `["bob@corp"]`, failing with `plan.enable["ldap-in"] = %v, want [bob@corp]: scoping must not stop LDAP governing its own inbounds`. It is not evidence the fix works; it is the guard against the opposite slip — a `governed` set built from an empty/inverted tag test, which would make LDAP govern nobody and would otherwise ship green.

TEST 3 — characterization, HONESTLY passes both ways. `TestLdapSyncStillDisablesPanelWide`: flags `{"dave@corp": false}` → assert `plan.disable["ldap-in"]` is exactly `["dave@corp"]`. dave is enabled on `vip-in` only. This pins the deferred behaviour in code, so whichever way the owner decides, Option A arrives as a deliberate red test on this line rather than an unnoticed drift. Its comment must say it documents an open question, not a desired invariant.

Also run `npm test` (i18n-dead-keys) for the two locale value edits, and `make lint`. No `make gen`, no migration, no OpenAPI/docs step — nothing in the change touches a route, a struct with `example:` tags, or a model.
**Sites:**
- internal/web/job/ldap_sync_job.go:107-113 — the unscoped index: `for _, ib := range inbounds { inboundMap[ib.Tag] = ib; clients, _ := j.inboundService.GetClients(ib); for i := range clients { allClients[clients[i].Email] = &clients[i] } }`. Built before tags are resolved; also the swallowed error at :109.
- internal/web/job/ldap_sync_job.go:137-152 — the decision with no membership test: `existing := allClients[email]` then `for _, tag := range resolvedTags { if allowed && !existing.Enable { clientsToEnable[tag] = ... } else if !allowed && existing.Enable { clientsToDisable[tag] = ... } }`.
- internal/web/job/ldap_sync_job.go:121-131 — tag resolution, which happens AFTER the index is built and produces `resolvedInboundIds`/`resolvedTags` that only `createClients` honours.
- internal/web/job/ldap_sync_job.go:305-330 — `batchSetEnable(ib *model.Inbound, emails []string, enable bool)`: `ib` reaches only the log lines at :314 and :325; :312 calls `SetClientEnableByEmail(&j.inboundService, email, enable)`. UNTOUCHED by this fix.
- internal/web/service/client_inbound_apply.go:1156-1160 — the deliberate identity-wide contract (#5039). UNTOUCHED; it is the reason the fix must gate on identity membership rather than try to scope writes.
- internal/web/service/client_inbound_apply.go:1161-1181 — the fan-out: `GetInboundIdsForEmail` then a loop patching every inbound. UNTOUCHED.
- internal/web/service/client_crud.go:92-93 and :107-109 — `client.SubID = uuid.NewString()` then `if existing.SubID == "" || existing.SubID != client.SubID { return false, common.NewError("email already in use:", client.Email) }`. Proof that `allClients` must stay panel-wide.
- internal/web/service/setting.go:148 — `"ldapInboundTags": ""`. The default that bounds the blast radius to tag-configured panels only.
- internal/web/translation/en-US.json:1419 — `"inboundTagsDesc": "Inbounds that LDAP sync may auto-create or auto-delete clients on."` — silent on enable/disable.
- internal/web/translation/fa-IR.json:1299 — the same key in Persian; must be edited in the SAME commit.
- frontend/src/pages/settings/GeneralTab.tsx:369 — `t('pages.settings.ldap.inboundTagsDesc')`, the reference that keeps the key alive for i18n-dead-keys.test.ts.
- SIBLING, CHECKED AND CLEARED — internal/database/model/model.go:873 `Node.InboundTags` + internal/web/service/inbound_node.go:147-148 + internal/web/service/node.go:666-667: the second inbound-scope setting, correctly enforced. Corrects the original plan's false 'only scope setting' claim.
- SIBLING, CHECKED AND CLEARED — internal/web/job/ldap_sync_job.go:278-303 `createClients`: correctly scoped, creates against `resolvedInboundIds` only. Contrast evidence that scoping was the original intent.
- SIBLING, CHECKED AND CLEARED — internal/web/job/ldap_sync_job.go:333-389 `deleteClientsNotInLDAP`: correctly scoped via `if ib.Tag != inboundTag { continue }` at :344-346 and per-inbound `DetachByEmail(..., ib.Id, ...)` at :370.
- SIBLING, CHECKED AND CLEARED — internal/web/service/client_bulk.go BulkSetEnable / BulkAdjust: take an explicit email list from an admin's selection, so identity-wide is the asked-for semantic.
- SIBLING, CHECKED AND CLEARED — internal/web/service/tgbot/tgbot_router.go ResetClientTrafficLimitByEmail / ResetClientExpiryTimeByEmail / ResetClientIpLimitByEmail / ToggleClientEnableByEmail: admin acts on one named identity.

**Correction from the re-check:** In TEST 1, pass autoCreate=true — as written (autoCreate=false) the `createEmails==0` assertion passes under the scoped-allClients slip it claims to pin (alice can't fall into auto-create when autoCreate is false); that slip is only caught by TEST 3's disable assertion.
**Re-check notes:**
- TEST 1's second assertion (`len(plan.createEmails) == 0`) is inert as specified, and the plan's stated rationale for it is false. It is credited with catching the scoped-`allClients` slip, but with `autoCreate=false` in the fixture, that slip makes alice `existing == nil`, hits the `if allowed && autoCreate` gate at internal/web/job/ldap_sync_job.go:140, and falls through — so both of TEST 1's assertions pass under exactly the slip the assertion exists to pin. The plan's sentence 'scope that map instead and alice looks absent, falls into auto-create' cannot occur when autoCreate is false. That slip is in fact caught only by TEST 3 (dave absent from a scoped map means `disable["ldap-in"]` is empty, failing the `[dave@corp]` assertion). Fix: call planLdapSync with autoCreate=true in TEST 1 — the correct implementation still passes (alice is present in the panel-wide allClients, so no create is queued), slip C then reds assertion 2 with `createEmails = [alice@corp]`, and slips A and B still red assertion 1. Strictly stronger, nothing else changes. This does not block: the headline red claim survives independently — TEST 1 fails without the fix on `len(plan.enable["ldap-in"]) == 0` getting `[alice@corp]`, under both slip modes the plan names.


### F372 — ship-it (small)
**Root:** The LDAP sync builds ONE email->client map from GetAllInbounds() (ldap_sync_job.go:105-113) and then uses it to answer two questions that need two different scopes.

(a) "Should I create this user?" — panel-wide is CORRECT here. ClientService.Create (client_crud.go:107-116) rejects an email already held by any client record ("email already in use"), because buildClient (:264-275) mints no SubID so Create's `existing.SubID != client.SubID` branch always trips.

(b) "Should I flip this user's Enable?" — panel-wide is WRONG. applyClientFieldByEmail (client_inbound_apply.go:1161-1178) deliberately mutates on EVERY inbound the email is attached to (one logical identity, #5039). So at :145-151 a client that lives only on a non-LDAP-tagged inbound enters clientsToEnable/clientsToDisable purely because its email appeared in the directory answer, and batchSetEnable -> SetClientEnableByEmail flips it panel-wide. This fires with auto-delete OFF and with a single resolved tag. en-US.json:1419 already scopes the feature to "Inbounds that LDAP sync may auto-create or auto-delete clients on" — enable/disable reaching outside that list contradicts the panel's own stated contract.

The original plan's stated root ("nothing records who created a client") is the DELETE question only (:353-359), and it is a policy choice, not a repair. The adversary is right on all four rejections: branch A changes no code path, restates en-US.json:1419, and leaves (b) live.

CORRECTION TO THE ADVERSARY'S OWN EDIT — verified, load-bearing: its proposal ("build allClients from resolvedInboundIds") is UNSAFE as written. It would also re-scope the create gate (a), so an email present only on a non-tagged inbound would newly be fed to Create, which returns "email already in use: <addr>" — a warning logged on every sync tick, forever, with nothing created. The correct shape is two views, not one narrower one.
**Ships now:** Scope only the enable/disable membership test to the LDAP-tagged inbounds. Keep the create gate panel-wide. No column, no migration, no i18n, no service change, no touch to applyClientFieldByEmail (so no #5039 risk). Entirely inside internal/web/job/ldap_sync_job.go.

EDIT 1 — second map, same single pass (replaces :105-113). `inboundTags` is already read at :98, before the inbound loop, so no reorder is needed and no extra GetClients round trip is added:

    taggedSet := map[string]struct{}{}
    for _, t := range inboundTags {
        taggedSet[t] = struct{}{}
    }
    allClients := map[string]*model.Client{}    // email -> client, panel-wide: Create rejects an email taken anywhere
    taggedMembers := map[string]*model.Client{} // email -> client on an LDAP-tagged inbound
    inboundMap := map[string]*model.Inbound{}
    for _, ib := range inbounds {
        inboundMap[ib.Tag] = ib
        clients, _ := j.inboundService.GetClients(ib)
        _, tagged := taggedSet[ib.Tag]
        for i := range clients {
            allClients[clients[i].Email] = &clients[i]
            if tagged {
                taggedMembers[clients[i].Email] = &clients[i]
            }
        }
    }

EDIT 2 — the guard, in the selection loop (:137-152). Create still consults allClients; enable/disable consults taggedMembers:

    for email, allowed := range flags {
        if allClients[email] == nil {
            if allowed && autoCreate {
                clientsToCreate = append(clientsToCreate, j.buildClient(email, defGB, defExpiryDays, defLimitIP))
            }
            continue
        }
        member := taggedMembers[email]
        if member == nil {
            continue
        }
        for _, tag := range resolvedTags {
            if allowed && !member.Enable {
                clientsToEnable[tag] = append(clientsToEnable[tag], email)
            } else if !allowed && member.Enable {
                clientsToDisable[tag] = append(clientsToDisable[tag], email)
            }
        }
    }

EDIT 3 — the 2 permitted comment lines above EDIT 2, naming the residual honestly:

    // A client on a tagged AND an untagged inbound is still flipped panel-wide:
    // Enable is one value per identity (applyClientFieldByEmail, #5039).

EDIT 4 — test scaffolding, behaviour-identical code motion. Move Run()'s post-fetch half (:97-185) verbatim into `func (j *LdapSyncJob) applyFlags(flags map[string]bool)`; Run() keeps the settings reads + FetchVlessFlags + the "Fetched %d LDAP flags" log and ends with `j.applyFlags(flags)`. This is not a new abstraction: LdapSyncJob already carries buildClient/createClients/batchSetEnable/deleteClientsNotInLDAP/fetchLooksComplete/recordBaseline, and it is the only seam that lets a test reach the bug without a live LDAP server. Move the `believable := j.fetchLooksComplete(...)`-before-`j.recordBaseline(...)` ordering and its comment at :164-166/:184-185 VERBATIM — that ordering is a prior fix.

WHAT THIS FIXES, most indefensible direction first: LDAP no longer RE-ENABLES a client an admin deliberately disabled by hand on an inbound that is not in ldapInboundTags. It also no longer disables one. Auto-delete semantics are untouched — deleteClientsNotInLDAP already scopes itself per tag (:343-346) and DetachByEmail (client_crud.go:735) detaches from that one inbound id only.

No reasonable operator prefers the status quo: today's behaviour is not a coherent "global offboarding" contract — it does nothing at all when ldapInboundTags is empty or resolves to nothing, so it only escapes the tag list when at least one tag happens to resolve.
**Deferred:** 1. THE F372 CORE — delete policy (:353-359, :370). The sweep still removes every client on a tagged inbound the directory did not return, hand-made ones included. Blocked on the decision below. Not shipped: it needs a provenance column, a migration in internal/database/db.go, and a backfill that must pick a side.

2. UNDER-PROVISIONING / the create-skip (:138-143). An email that exists anywhere in the panel is never attached to the tagged inbounds, so turning LDAP on over an existing client population silently under-serves the directory. Deliberately NOT in shippableNow, and deliberately NOT fixed the adversary's way: re-scoping the create gate produces per-tick "email already in use" warnings (buildClient mints a fresh SubID; client_crud.go:107-116). The real fix is AttachByEmail(email, resolvedInboundIds) — adopting a pre-existing local identity onto LDAP inbounds is an ACCESS GRANT and an identity-collision policy, the same ownership axis as the delete question. Both hang off one decision.

3. BRANCH A DROPPED ENTIRELY. The proposed `autoDeleteDesc` restates its neighbour: en-US.json:1419 `inboundTagsDesc` already reads "Inbounds that LDAP sync may auto-create or auto-delete clients on." and is already wired as a description on the row above the switch. It would have closed a live defect with a tooltip. No i18n change in this commit.

4. The swallowed GetClients error at :109 (`clients, _ :=`). Separable one-liner, needs no decision, just not this diff: if it fails on a tagged inbound, taggedMembers under-reports and (with autoCreate on) the job attempts Create and logs "email already in use" per tick.

5. batchSetEnable's per-tag keying (:157-162, :311-323) is vestigial now that the executor is attachment-wide — a client on 3 tagged inbounds gets 3 SetClientEnableByEmail calls, 2 of which early-return on `current == enable`. Wasteful, harmless, and a refactor. Left alone.
**Test:** New file internal/web/job/ldap_scope_test.go, following the ldap_baseline_test.go / ldap_auto_delete_guard_test.go naming already in that directory. Uses initLdapJobDB(t) (skips without PUI_DB_DSN; CI's live-Postgres job counts a SKIP as a failure, so it really runs).

TestLdapApplyFlags_LeavesClientsOutsideTaggedInboundsAlone

Setup:
- Two VLESS inbounds via db.Create: Tag "ldap-tagged" (port 42090) and Tag "personal" (port 42091), Settings `{"clients": []}`.
- db.Create(&model.Setting{Key: "ldapInboundTags", Value: "ldap-tagged"}). ldapAutoCreate/ldapAutoDelete resolve to "false" from defaultValueMap (setting.go:149-150); seed them explicitly anyway so the test's meaning does not depend on a default.
- Three clients via (&service.ClientService{}).Create with distinct SubIDs:
  * member@corp   -> [ldap-tagged], Enable: true
  * outsider@corp -> [personal],    Enable: true
  * pinned@corp   -> [personal],    Enable: false   (hand-disabled by an admin)

Act: j := NewLdapSyncJob(); j.applyFlags(map[string]bool{"member@corp": false, "outsider@corp": false, "pinned@corp": true})

Assertions (read Enable back with (&service.ClientService{}).ListForInbound(nil, id), the accessor the existing job test already uses):

1. RED WITHOUT THE FIX — the re-enable direction, the one with no defensible reading:
   if enabled(personalId, "pinned@corp") { t.Errorf("pinned@corp was disabled by hand on inbound %q, which is not in ldapInboundTags; LDAP sync re-enabled it", "personal") }
2. RED WITHOUT THE FIX — the disable direction:
   if !enabled(personalId, "outsider@corp") { t.Errorf("outsider@corp lives only on inbound %q, which is not in ldapInboundTags; LDAP sync disabled it anyway", "personal") }
3. OVER-SCOPING GUARD — passes both before and after; it exists so the fix cannot be "turn the feature off":
   if enabled(taggedId, "member@corp") { t.Error("member@corp is on the LDAP-tagged inbound and the directory revoked it; LDAP sync must still disable it") }

Why this is genuinely red, mechanically: without EDIT 1+2, `allClients["pinned@corp"]` is non-nil (it is on `personal`), resolvedTags = ["ldap-tagged"], allowed=true, existing.Enable=false -> clientsToEnable["ldap-tagged"] = ["pinned@corp"] -> batchSetEnable -> SetClientEnableByEmail("pinned@corp", true) -> applyClientFieldByEmail -> GetInboundIdsForEmail returns [personalId] -> enabled on `personal`. Assertion 1 fires with exactly the message above. Same chain with allowed=false drives assertion 2.

The revert is an ASSERTION FAILURE, not a compile failure: revert = delete taggedSet/taggedMembers and the `member := taggedMembers[email]; if member == nil { continue }` guard, reading `allClients[email]` again INSIDE applyFlags. The applyFlags seam (EDIT 4) stays, so the test still compiles and prints the messages. That is the "write it, revert the fix, watch it go red, restore" cycle CLAUDE.md requires.

Run it with `make test-go` on a box with PUI_DB_DSN set (or the CI live-Postgres job). No frontend test is involved — no i18n key is added.
**Sites:**
- internal/web/job/ldap_sync_job.go:105-113 — the root: one `allClients` map built over `GetAllInbounds()`, serving two questions that need two scopes
- internal/web/job/ldap_sync_job.go:98 — `inboundTags := splitCsv(mustGetString(j.settingService.GetLdapInboundTags))`; already read before the inbound loop, so `taggedSet` costs no reorder
- internal/web/job/ldap_sync_job.go:138-143 — `existing := allClients[email]; if existing == nil { ...create... }`; the create-skip / under-provisioning instance (DEFERRED, and NOT fixable by re-scoping this map)
- internal/web/job/ldap_sync_job.go:145-151 — `for _, tag := range resolvedTags { if allowed && !existing.Enable {...} else if !allowed && existing.Enable {...} }`; the enable/disable instance, the one that ships
- internal/web/job/ldap_sync_job.go:157-162 and :305-330 — `batchSetEnable` -> `SetClientEnableByEmail`, the executor that carries the miss out of the tag list
- internal/web/service/client_inbound_apply.go:1132-1146 — `SetClientEnableByEmail`; early-returns when `current == enable`, which is why the redundant per-tag calls are harmless
- internal/web/service/client_inbound_apply.go:1161-1178 — `applyClientFieldByEmail`: `GetInboundIdsForEmail` then a loop over every attached inbound. Attachment-wide BY DESIGN (#5039). This is the mechanism, and it must NOT be changed
- internal/web/job/ldap_sync_job.go:65-186 — `Run()`; the `applyFlags` cut line is between :95 (`Fetched %d LDAP flags`) and :97
- internal/web/job/ldap_sync_job.go:164-166 and :184-185 — the `believable`-before-`recordBaseline` ordering and its comment; must move verbatim in the extraction, it is a prior fix
- internal/web/job/ldap_sync_job.go:264-275 — `buildClient` sets no SubID; this is why re-scoping the create gate turns a silent skip into a per-tick `email already in use`
- internal/web/service/client_crud.go:107-116 — `Create`'s `emailTaken` branch: `if existing.SubID == "" || existing.SubID != client.SubID { return false, common.NewError("email already in use:", client.Email) }`
- internal/web/job/ldap_sync_job.go:333-359 and :370 — `deleteClientsNotInLDAP`; already per-tag scoped (`if ib.Tag != inboundTag { continue }`) and `DetachByEmail` hits one inbound id. The delete BREADTH within a tagged inbound is the deferred policy question
- internal/web/translation/en-US.json:1419 — `"inboundTagsDesc": "Inbounds that LDAP sync may auto-create or auto-delete clients on."`; both the reason branch A is dropped and the stated contract that (b) violates
- internal/web/service/setting.go:343-380 — VERIFIED: `getString`/`getBool`/`getInt` fall back to `defaultValueMap`, and ldapAutoCreate/ldapAutoDelete/ldapDefault* are all in it (:149-153), so the test's `mustGet*` calls cannot panic on unseeded keys
- internal/web/job/testdb_test.go and internal/web/job/ldap_sync_job_test.go:11 — `initLdapJobDB(t)` / `initTestDB(t)`, the per-schema PostgreSQL harness the new test uses


### F373 — ship-it (small)
**Root:** ADVERSARY CONFIRMED ON ALL FOUR COUNTS — verified by reading the code, not conceded. (1) Fail-open: ldap_sync_job.go:161 `j.batchSetEnable(inboundMap[tag], emails, false)` is the off-boarding half and is UNGATED (only the delete half at :167 sits behind GetLdapAutoDelete), so an unscheduled sync means a revoked LDAP account keeps a working proxy forever. Three lines of Warningf annotate that; they do not repair it. (2) Error is the house level: auth_posture.go:30-43 logs "enabled but cannot work" at Errorf with remediation every boot. (3) CLI sibling real: main.go:295 `settingService.SetTgbotRuntime(tgBotRuntime)` → setting.go:560-562 `setString("tgRunTime", ...)`, never near UpdateAllSetting. (4) The premise steering away from CheckValid was FALSE: check_valid_test.go:6-8 builds `&AllSetting{WebPort: 2053, SubPort: 2096}` with no TimeLocation, and `time.LoadLocation("")` returns UTC with nil error, so the template needs no extra preconditions.

Corrected root cause: an operator-supplied cron spec is never measured against the SCHEDULER'S parser anywhere — not at save (UpdateAllSetting, setting.go:1219-1237, runs preserveRedactedSecrets → refuseTwoFactorWithoutASecret → validateSettingsURLs → validateSubUserAgentRegexes → CheckValid, and CheckValid at entity.go:158-264 touches neither LdapSyncCron nor TgRunTime), not at boot (web.go:411 `_, _ = s.cron.AddJob(runtime, j)`), and not even in the guard built to catch it (cadence_test.go:30 uses the 5-field `cron.ParseStandard` against specs the scheduler parses with the 6-field parser from `cron.WithSeconds()`, option.go:19-23 → Second|Minute|Hour|Dom|Month|Dow|Descriptor, no SecondOptional, so parser.go:187 rejects a 5-field spec with "expected exactly 6 fields, found 5"). The consequential defect is not the silence but the INCOMPLETE FALLBACK LADDER: web.go:405-408 already handles two of three failure modes (`if err != nil || runtime == "" { runtime = "@every 1m" }`); the third — unparseable — falls off the end and the job is never scheduled.

ONE CORRECTION TO THE ADVERSARY: its suggested message advertises `p-ui setting ...` for the LDAP spec. No such flag exists — main.go:613-632 registers ldapAdminFilter and tgbotRuntime but NO -ldapSyncCron. Adopt its structure; point remediation at Settings only.
**Ships now:** Three edits, one commit, no behaviour choice. All three close the same hole: nothing measures a spec against the scheduler's parser.

(1) web.go:403-412 — CLOSE THE LADDER, don't just log. Add `defaultLdapSyncCron = "@every 1m"` (the literal already at :407 and the seed at setting.go:144), use it at :407, and replace :411 with `scheduleLdapSync(s.cron, runtime, j)`. New ~9-line unexported helper in web.go:

  // LDAP off-boarding (ldap_sync_job.go:161) is fail-open when the job is not
  // scheduled, so an unparseable spec falls back to the seeded default.
  func scheduleLdapSync(c *cron.Cron, spec string, j cron.Job) {
      _, err := c.AddJob(spec, j)
      if err == nil {
          return
      }
      logger.Errorf("LDAP is enabled but ldapSyncCron %q is not a valid cron spec (%v); falling back to %q. Fix it in Settings > LDAP > sync schedule.", spec, err, defaultLdapSyncCron)
      if _, err := c.AddJob(defaultLdapSyncCron, j); err != nil {
          logger.Errorf("LDAP sync could not be scheduled at all (%v); client sync, disable and off-boarding will not run", err)
      }
  }

Error, not Warning, per auth_posture.go:30-43. NOT under [AUTH_POSTURE]: that prefix is a grep contract for the login path whose own LDAP message reads "Client sync is unaffected" (auth_posture.go:41). Not-that-prefix does not imply not-that-severity.

(2) web.go:548-555 — NAME THE PARSER. Add `var cronSpecParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)` and swap `cron.WithSeconds()` → `cron.WithParser(cronSpecParser)`. Provably identical — option.go:19-23 IS that expression. Pure wiring; it exists so (3) and the test cannot measure against a different grammar than production.

(3) cadence_test.go:30 — `cron.ParseStandard(spec)` → `cronSpecParser.Parse(spec)`. Test-only, zero runtime effect. Today's cadences are all `@every` descriptors so it stays green, but the guard currently accepts `*/5 * * * *` (which the scheduler silently drops) and rejects `0 */5 * * * *` (which the scheduler accepts) — it measures the wrong thing in both directions.

WHY THE FALLBACK IS AN UNDISPUTED REPAIR, NOT A POLICY CHOICE: "@every 1m" is not invented — it is the literal four lines above for the other two failure modes AND the seeded default at setting.go:144, so the panel lands in exactly the stock-install state every operator who never touched the field is already in. The job's own safety rails are intact: it returns early on fetch failure (ldap_sync_job.go:90) and skips deletion on an unbelievable count or no baseline (:163-180). Resuming it introduces no harm class a stock install does not already carry.
**Deferred:** 1. WRITE-TIME REFUSAL — the decision below. Deferred because it is the only piece with a real blast radius: the settings page posts the whole AllSetting, so a panel ALREADY storing a bad spec could no longer save ANY setting until that field is corrected. Checked: the field is reachable (GeneralTab.tsx:367 sits in the LDAP tab item at :301, an unconditional tab — not gated on ldapEnable), so it is recoverable, but that is still a live-panel behaviour change the owner must accept.

2. THE TG TWIN (web.go:417-428) — deliberately NOT given the same fallback, and this asymmetry is the answer to "one of a pair", not an oversight. It already logs on parse failure, so the silence the finding names is not there. Its consequence is a missing stats report, not a fail-open security control — and unlike LDAP, defaulting it to "@daily" would start SENDING Telegram messages a live panel currently does not send. That is a behaviour choice (~3 lines) the owner can have on request. Its Warning level stays: a missed notification is not auth_posture-grade.

3. server.go:91 and :99 — `_, _ = c.AddFunc("@every 2s", ...)` / `("@every 1m", ...)`: same discarded-error shape, but compile-time literals with zero operator input, so they cannot produce the outage. Hoisting them into web.go's cadence const block (:317-338) so cadence_test.go covers them is a refactor, not this fix.

4. FRONTEND (schemas/setting.ts:86 `ldapSyncCron: z.string().optional()`) — no client-side parse, deliberately left alone. A JS cron parser is a second independent implementation of the scheduler's grammar that will drift from it — precisely the defect cadence_test.go:30 already is. Refusal belongs server-side, which is the decision.

5. TIMING LIMIT, stated honestly: shippableNow takes effect at the NEXT PANEL RESTART. startTask is the only reader of the schedule, and saving settings does not restart the panel (restartPanel is a separate operator-triggered endpoint, controller/setting.go:72 and :203). A panel with a bad spec keeps not syncing until it is restarted. That gap is what write-time refusal closes, and it is the honest argument for the decision.
**Test:** New file internal/web/ldap_schedule_test.go, package web, stdlib testing, no DB. Precedent: cadence_test.go is already a scheduler-shaped unit test in package web (needs `make dist-stub` for the //go:embed all:dist; runs under `make test-go`).

  type noopJob struct{}
  func (noopJob) Run() {}

  func TestLdapSyncFallsBackWhenTheSpecCannotParse(t *testing.T) {
      c := cron.New(cron.WithParser(cronSpecParser))   // MUST be the shared parser
      scheduleLdapSync(c, "*/5 * * * *", noopJob{})
      if got := len(c.Entries()); got != 1 {
          t.Fatalf("LDAP sync was not scheduled after an unparseable spec: got %d entries, want 1", got)
      }
  }

Plus a companion subtest proving the fallback does not MASK a working spec: schedule "@daily" on a fresh cron and assert `c.Entries()[0].Schedule.Next(base)` is the next midnight, not base+1m — entry count alone cannot distinguish "used the spec" from "used the fallback".

RED PROCEDURE, EXACTLY: revert scheduleLdapSync's body to the current line `_, _ = c.AddJob(spec, j)`. AddJob returns cron/v3 parser.go:187 "expected exactly 6 fields, found 5: [*/5 * * * *]", nothing is appended, and the test fails with:
  LDAP sync was not scheduled after an unparseable spec: got 0 entries, want 1
Verified mechanically, not assumed: cron.go:148-153 returns early on parse error; cron.go:158-173 appends to c.entries when !running; cron.go:177-186 Entries() returns entrySnapshot() when !running. The cron is never Start()ed, so no job body executes.

THE TEST CANNOT PASS EITHER WAY, and edit (2) is what guarantees that: built with a bare `cron.New()`, the DEFAULT 5-field parser ACCEPTS `*/5 * * * *`, one entry is added with or without the fix, and the test certifies nothing. Using cronSpecParser pins the test to production's grammar.

PREEMPTING THE EARLIER REJECTION ("a direct-call test doesn't pin the wiring"): the rejected validateSettingsCronSpecs was a NEW parallel path that dodged an existing real enforcement point (CheckValid) and could rot un-wired. scheduleLdapSync is not parallel — it IS the replacement for web.go:411, its only caller is three lines away in the same function, and the genuinely-real path (startTask) needs a DB, a live SettingService and a core registry. If the decision lands on (A)/(B), that test goes in entity/check_valid_test.go on the TestCheckValidSmtpFrom template, which needs no extra preconditions.
**Sites:**
- internal/web/web.go:403-412 — the LDAP block; ladder at :406-408, discarded error at :411 `_, _ = s.cron.AddJob(runtime, j)`
- internal/web/web.go:411 — the discarded error, 17 lines above :427 which logs the same failure for the twin
- internal/web/web.go:548-555 — `s.cron = cron.New(` with `cron.WithSeconds(),` at :550; the 6-field parser every spec is measured against
- internal/web/web.go:417-428 — the tgRunTime twin; falls back on read-error/empty, logs at Warning on parse error, no fallback (deferred, see deferred)
- internal/web/cadence_test.go:30 — `if _, err := cron.ParseStandard(spec); err != nil` — the guard measures with the 5-FIELD parser
- internal/web/job/ldap_sync_job.go:161 — `j.batchSetEnable(inboundMap[tag], emails, false)`, UNGATED off-boarding: this is why an unscheduled job is fail-open
- internal/web/job/ldap_sync_job.go:167-180 — the delete half, gated by GetLdapAutoDelete and by fetchLooksComplete/autoDeleteAllowed
- internal/web/job/ldap_sync_job.go:90 — early return on LDAP fetch failure; the rail that makes resuming the job safe
- internal/web/entity/entity.go:158-264 — CheckValid, end to end: listens, ports, cert pairs, 4 URI paths, TrustedProxyCIDRs, TimeLocation, SmtpFrom. Neither cron field.
- internal/web/entity/entity.go:46 — `TgRunTime string \`json:"tgRunTime"\`` and entity.go:123 — `LdapSyncCron string \`json:"ldapSyncCron"\``
- internal/web/entity/check_valid_test.go:6-8 — `base := func() *AllSetting { return &AllSetting{WebPort: 2053, SubPort: 2096} }` — proves the abandoned plan's precondition warning was false
- internal/web/service/setting.go:1219-1237 — UpdateAllSetting: four validators then CheckValid; no cron parse on any of them
- internal/web/service/setting.go:144 — `"ldapSyncCron": "@every 1m"` — the seeded default, i.e. the fallback target is not invented
- internal/web/service/setting.go:560-562 — `SetTgbotRuntime` → `setString("tgRunTime", time)`, the CLI-side writer that skips every validator
- main.go:295 — `err := settingService.SetTgbotRuntime(tgBotRuntime)` (flag registered main.go:630) — MISSED SIBLING WRITE PATH
- main.go:613-632 — the settingCmd flag block: ldapAdminFilter and tgbotRuntime exist, NO -ldapSyncCron; remediation text must not advertise one
- internal/web/controller/server.go:91 and :99 — `_, _ = c.AddFunc("@every 2s"/"@every 1m", ...)`; same shape, compile-time literals (deferred)
- internal/web/controller/setting.go:72 and :203-205 — restartPanel is a separate endpoint; saving settings does NOT re-run startTask
- frontend/src/schemas/setting.ts:86 — `ldapSyncCron: z.string().optional()` — no client-side parse (deliberately deferred)
- frontend/src/pages/settings/GeneralTab.tsx:301 and :367 — the LDAP tab item and the ldapSyncCron Input; unconditional, so a save-time refusal is recoverable in the UI
- internal/arch/job_growth_test.go:25-33 — perCoreJobBudget{"xray": 4}; the LDAP job names no core, so the extra AddJob does not touch it
- NEGATIVE RESULT: grepped `cron.New(|AddJob(|AddFunc(|WithSeconds|ParseStandard` across internal/ + main.go — exactly ONE scheduler (web.go:548). Separately grepped SETTING WRITERS (`setString(`, `Set*Runtime`, `Set*Cron`) — that is what surfaced main.go:295, which the first plan's API-only grep could not have found.

**Re-check notes:**
- NON-BLOCKING, pre-existing, out of scope: cadence_test.go's map omits cadenceEgressReconcile, cadenceReapOrphans, cadenceXrayLogPrune and cadenceMemoryAlarm, so edit (3) fixes the guard's grammar but not its coverage. That is a different property from the one this finding names, and it belongs with the server.go:91/:99 hoist the plan already defers. Do not expand this commit for it.


### F376 — ship-it (small)
**Root:** `dial()` (internal/util/ldap/ldap.go:43-59) models LDAP transport as one boolean, so only two states are reachable: cleartext `ldap://` and implicit-TLS `ldaps://`. There is no `StartTLS` call anywhere in `internal/util/ldap` (the only `StartTLS` calls in the tree are the SMTP client's), so a directory that offers encryption only via StartTLS-on-389 has no reachable way to get it. That one bool is faithfully projected downstream into the setting key, the getter, both `ldaputil.Config` construction sites, the frontend default and the UI label "Use TLS (LDAPS)".

CORRECTED EXPOSURE (v1 overstated this and the adversary was right). Nothing ships insecure out of the box: `ldapEnable` defaults `"false"` (setting.go:132) and web.go:404 registers the sync cron only inside `if ldapEnabled`, so an untouched panel never dials LDAP at all. The narrower, real finding is the pair of shipped defaults an operator lands on the *moment* they enable LDAP — `ldapPort=389` + `ldapUseTLS=false` (setting.go:134-135) — combined with the fact that nothing tells them. The panel already has a "reported, never repaired" contract for exactly this class (`LogAuthPosture`, `TLSPostureLogPrefix`), and this state is not covered by it.

ALSO CORRECTED: v1's Half B implementation was deterministically broken and I verified why. go-ldap v3.4.14 `(*Conn).StartTLS` calls `tls.Client(l.conn, config)` (modcache conn.go:430) and the library contains ZERO occurrences of `ServerName` in any .go file. `tlsConfig(cfg)` (ldap.go:31-33) returns `&tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}` — no ServerName — so with the shipped `ldapInsecureSkipVerify=false` crypto/tls refuses before a byte moves (handshake_client.go:46, "tls: either ServerName or InsecureSkipVerify must be specified in the tls.Config"). The existing ldaps path escapes this ONLY by luck of a different code path the adversary did not name: `DialContext.dial` uses `tls.DialWithDialer` (conn.go:204), and crypto/tls tls.go:162-167 clones the config and fills `ServerName` from the hostname. That asymmetry — DialWithDialer infers, tls.Client does not — is the trap, and it means any future StartTLS work must first add `ServerName: cfg.Host` to `tlsConfig`.
**Ships now:** Add a third branch to the existing `if enabled, err := s.GetLdapEnable(); err == nil && enabled` block at internal/web/service/auth_posture.go:38, firing when `GetLdapUseTLS()` returns false — plus the widened doc sentence and the test. Nothing else. No new setting, no migration, no route, no i18n key (the string is Go-side log text), no frontend change, no new setter, no ServerName edit, no email change.

The message must be assembled from clauses whose truth matches their gate — this is exactly where v1 died:

BASE (unconditional; a statement about configured transport, which is true even when ldapHost is empty): name `ldapUseTLS` explicitly as the setting to look at, state that the panel connects to the directory in cleartext, that every account identifier and flag attribute the sync reads (ldapUserAttr, default `mail`, plus the flag field — ldap.go:103) is readable on the wire, and that it repeats on the ldapSyncCron interval. Include the honest capability note: this panel has no StartTLS mode, so encryption means LDAPS — verifiable from ldap.go alone, and it stops an operator hunting a toggle that does not exist.

CLAUSE 1, appended only when `GetLdapBindDN()` is non-empty: the ldapBindDN service password is sent in the clear on every one of those connections. Gated because ldap.go:76 and :133 bind only then.

CLAUSE 2, appended only when `GetLdapAdminFilter()` is non-empty: panel administrators signing in through LDAP send their own passwords in the clear. Gated because panel/user.go:73-78 returns `invalid credentials` before the Config is ever built when the filter is empty.

Doc comment: widen the FIRST SENTENCE IN PLACE at auth_posture.go:19 from "an authentication setting that is on but cannot work" to also cover "on but unprotected". Do not add lines to that block — it is already 12 lines and the 2-line rule forbids growing it.

This is an undisputed repair: it breaks no operator, changes no behaviour, turns nothing off on the operator's behalf, and is the exact "reported, never repaired" contract the function's own doc already sets and that `TLSPostureLogPrefix` extends to the listeners. No reasonable operator prefers being told nothing.
**Deferred:** 1. THE STARTTLS CAPABILITY ITSELF (v1's "Half B"). Deliberately out: making a third transport state reachable is new setting surface plus a migration, a UI rewrite and two translation files — new architecture next to a fix, which the hard rules forbid unasked. Recording the corrected shape so it is costed honestly next time: `tlsConfig` MUST gain `ServerName: cfg.Host` first (a no-op for the ldaps path, since tls.DialWithDialer already infers exactly that value, but mandatory for StartTLS because tls.Client does not), and the test needs a NEW harness that decodes the ExtendedRequest's messageID off the wire and encodes an ExtendedResponse — ldap_timeout_test.go's stub answers nothing and cannot be reused. Coupling to remember: the shipped fix's "no StartTLS mode" clause becomes stale the day this lands and must be removed in the same commit.

2. THE SHIPPED-DEFAULT FLIP (ldapPort 389→636, ldapUseTLS false→true). A behaviour choice, folded into the decision below. Its blast radius is genuinely small — `UpdateAllSetting` (setting.go:1253-1268) writes a row for every AllSetting field on save, and configuring LDAP requires saving that page, so every install with working LDAP already has an explicit `ldapUseTLS=false` row that no default override reaches; only NEW installs change. Against it: first-time setup against a plain-389 directory then fails with a bare connection error. If taken it MUST land in frontend/src/models/setting.ts:76 in the same commit or the next settings save writes `false` straight back.

3. THE EMAIL OPPORTUNISTIC-STARTTLS PAIR (email.go:285-290 and :172-179). Same defect family, but a DIFFERENT finding with its own behaviour choice: enforcing StartTLS when the operator explicitly selected "starttls" would break installs whose mail currently flows in cleartext against a non-advertising server. Not shippable as an undisputed repair, and not this finding's scope. Flagging the one-of-a-pair hazard for whoever takes it: `TestConnection` holds an independent second copy of the check, so a fix landing in only one makes the panel's own test button certify a transport the send path does not use.

4. A POSTURE LINE FOR ldapUseTLS=true + ldapInsecureSkipVerify=true (TLS on, certificate unverified, so a MITM still reads everything). Explicitly out: the UI already carries `skipTlsVerifyDesc` warning it is insecure, the operator opted in deliberately, and adding it here is scope creep past the bug.

5. ONE LINE OF TLS GUIDANCE IN docs/content/docs/en/config/panel.mdx. Out of the shipped commit because docs-ci.yml fires only on `docs/**` and mixing it in muddies a focused diff; worth doing whichever way the decision below goes.
**Test:** New Postgres-gated test in `package service` (so it can call the unexported `s.setBool`/`s.setString` directly — no new production setters), using the `initTestDB(t)` template in internal/web/service/testdb_test.go, which already does `t.Cleanup(func() { _ = database.CloseDB() })` and drops its schema.

CAPTURE, and this part is load-bearing: `logger.Errorf` always mirrors into the package-global ring buffer via `addToBuffer` (logger.go:184-187), readable with `logger.GetLogs(n, "error")` — newest-first. That buffer is process-global and never reset, and with `-shuffle=on` other tests have written to it. So scope every assertion by DIFF: take `before := len(logger.GetLogs(10240, "error"))`, call `(&SettingService{}).LogAuthPosture()`, take `after := ...`, and examine ONLY the newest `after-before` entries. A naive scan of the whole buffer passes either way and is exactly the failure mode that got prior plans rejected.

Subtest 1 — RED WITHOUT THE FIX. Seed ldapEnable=true, ldapUseTLS=false, ldapBindDN="", ldapAdminFilter="". Assert one of the newly emitted lines contains BOTH `[AUTH_POSTURE]` and the literal setting name `ldapUseTLS`. Reverting the branch makes this fail at `t.Fatalf("LDAP is enabled with ldapUseTLS off and nothing said so; the operator has no way to know the directory connection is cleartext")`, because the diff window then contains only the pre-existing ldapAdminFilter-empty line. NOTE THE TRAP: that existing line ALSO carries `[AUTH_POSTURE]`, so asserting the prefix alone would pass without the fix — the assertion must key on `ldapUseTLS`, which appears in no other posture string.

Subtest 2 — RED WITHOUT THE CLAUSE GATING, which is the defect the adversary caught in v1. Same seed (ldapBindDN=""), assert the emitted line does NOT contain the bind-password clause, failing with `t.Fatalf("posture line claims the ldapBindDN password crosses the network, but ldapBindDN is empty and ldap.go:76 skips the bind entirely")`. Then a sibling case with ldapBindDN="cn=svc,dc=example,dc=com" asserting the clause IS present. An ungated message fails the first and an always-absent clause fails the second, so neither shortcut survives.

Subtest 3 — the off switch. ldapEnable=true, ldapUseTLS=true: assert no emitted line mentions `ldapUseTLS`, so the branch cannot fire on a correctly configured panel.

Subtest 4 — ldapAdminFilter non-empty adds the admin-password clause; empty omits it (mirrors subtest 2 for panel/user.go:73-78).

Honest note on prior coverage: `grep -rn "authPostureLogPrefix|AUTH_POSTURE" --include=*.go internal/` matches only auth_posture.go itself, and internal/arch/tls_posture_test.go parses ONLY web.go and sub.go (verified — it mentions LogAuthPosture in a doc comment only). Both existing posture branches have no test, so this is the first real coverage of the function and no arch guard is affected.
**Sites:**
- internal/util/ldap/ldap.go:43-59 — the root: `dial()` has two states only; `if cfg.UseTLS { scheme = "ldaps"; opts = append(opts, ldap.DialWithTLSConfig(tlsConfig(cfg))) }`
- internal/util/ldap/ldap.go:31-33 — `tlsConfig` returns `&tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}` with no ServerName; benign for ldaps (DialWithDialer fills it), fatal for any StartTLS branch (tls.Client does not)
- internal/util/ldap/ldap.go:76-80 — sync-path service bind, GUARDED by `if cfg.BindDN != ""`; with the shipped empty ldapBindDN no service password is on the wire
- internal/util/ldap/ldap.go:133-137 — login-path service bind, same guard, same conclusion
- internal/util/ldap/ldap.go:168 — the admin's OWN password: `conn.Bind(userDN, password)`; reachable only once ldapAdminFilter is non-empty
- internal/util/ldap/ldap.go:103 — `SearchWithPaging` returns ldapUserAttr (default `mail`) + the flag attribute for every matching entry; this is what unconditionally crosses in the clear once LDAP is enabled with TLS off
- internal/web/service/auth_posture.go:38-44 — the `if ldapEnable` block that already carries the ldapAdminFilter posture line; the new branch goes here
- internal/web/service/auth_posture.go:19 — doc comment first sentence says the function names a setting "that is on but cannot work"; the new branch is "on but unprotected", so the sentence needs widening in place
- internal/web/service/setting.go:132-142 — shipped defaults: ldapEnable=false, ldapPort=389, ldapUseTLS=false, ldapInsecureSkipVerify=false, ldapBindDN="", ldapAdminFilter=""
- internal/web/web.go:404 — `if ldapEnabled, _ := s.settingService.GetLdapEnable(); ldapEnabled` gates cron registration; proof nothing dials out of the box
- internal/web/service/panel/user.go:73-78 — returns `invalid credentials` BEFORE building the Config when ldapAdminFilter is empty; proof the admin-password clause must be gated
- internal/web/service/panel/user.go:88-100 — the login Config site carrying UseTLS through to AuthenticateUser
- SIBLING internal/web/service/email/email.go:285-290 — same defect FAMILY, live: `sendPlain` upgrades only opportunistically, `if ok, _ := client.Extension("STARTTLS"); ok { client.StartTLS(&tls.Config{ServerName: host}) }`. A server that does not advertise STARTTLS gets AUTH PLAIN in cleartext, silently. (Note it DOES pass ServerName — unlike anything in the ldap package.) DEFERRED, different finding.
- SIBLING internal/web/service/email/email.go:172-179 — one-of-a-pair hazard: `TestConnection` carries a second independent copy of the same opportunistic check, gated on `encryptionType == "starttls"`. Any enforcement change must land in BOTH or the test button certifies a transport the send path does not use.
- SIBLING frontend/src/models/setting.ts:75-77 — independent second copy of the shipped defaults (`ldapPort = 389; ldapUseTLS = false; ldapInsecureSkipVerify = false`). Any Go-side default flip that does not also land here evaporates on the next settings save.
- SIBLING frontend/src/pages/settings/GeneralTab.tsx:314-326 — the LDAPS-only assumption in the UI: a `<Switch>` bound to `allSetting.ldapUseTLS` labelled `t('pages.settings.ldap.useTls')` (= "Use TLS (LDAPS)"), with the skip-verify switch `disabled={!allSetting.ldapUseTLS}`. A boolean→enum change rewrites this block and both translation files.
- SIBLING internal/web/translation/en-US.json:1399 + its fa-IR twin — `pages.settings.ldap.useTls` = "Use TLS (LDAPS)"; the key itself encodes the two-state model.
- SIBLING docs/content/docs/en/config/panel.mdx — documents the LDAP knobs with no TLS guidance and never says the shipped transport is cleartext.
- internal/util/ldap/ldap_timeout_test.go:39-49 — the existing stub listener accepts and closes; it decodes no BER and writes no bytes, so it CANNOT be reused to answer a StartTLS ExtendedRequest.


### F396 — ship-it (one-line)
**Root:** Corrected, and narrower than the original plan claimed.

The enable branch (SecurityTab.tsx:218-220) fires an unconditional green toast whose text is a completed-state assertion — en-US.json:1473 "Two-factor authentication has been successfully established" — and then only mutates the in-memory draft: `updateSetting` (useAllSettings.ts:42-48) is a pure `setDraft`, no HTTP anywhere. The toast asserts a persistence the branch never attempts, on a security control that the hardening checklist (docs/.../first-login.mdx:96-98) tells operators to tick off.

The draft-only mutation itself is NOT the defect. It is the page's own documented contract: SettingsPage.tsx:250 renders a Save button gated on `saveDisabled = !isDirty`, and SettingsPage.tsx:253 renders a permanent warning Alert carrying `pages.settings.infoDesc` = "Every change made here needs to be saved." Every other setting on the page behaves exactly this way. The toast contradicts the banner sitting a few hundred pixels above it.

The original plan's "the disable branch proves immediate persistence is the contract" inference is wrong, exactly as the adversary said. Disable commits immediately because `twoFactorCode` is request-scoped (controller/setting.go:30-40: "must never land in the settings table") and cannot ride in the page draft; it is verified only on the on→off transition (setting.go:132-137). That constraint does not exist on enable, so the disable branch's shape carries no intent about enable.

So: the undisputed defect is a false success claim. Whether enable should ALSO commit immediately is a separate behaviour choice, because there is only one shape available for it (see `deferred`).
**Ships now:** Stop the enable branch asserting a completed state change. The claim lives entirely in the string, so this is a two-line edit in the two locale files and zero TypeScript change:

- internal/web/translation/en-US.json:1473
  "twoFactorModalSetSuccess": "Two-factor authentication has been successfully established"
  → "twoFactorModalSetSuccess": "Code verified — press Save to enable two-factor authentication"

- internal/web/translation/fa-IR.json:1353
  "twoFactorModalSetSuccess": "احراز هویت دو مرحله‌ای با موفقیت برقرار شد"
  → "twoFactorModalSetSuccess": "کد تأیید شد — برای فعال‌سازی احراز هویت دو مرحله‌ای، ذخیره کنید"

Why this and not `messageApi.success(...)` deletion: the toast is the only confirmation that the operator's TOTP code actually verified, and deleting the call orphans the key, so i18n-dead-keys.test.ts then forces removing it from both locales anyway — same two files touched, less information for the operator. Keeping the key referenced at SecurityTab.tsx:219 keeps that test green with no key churn. `messageApi.success` (green) stays correct under the new wording: the code verification did succeed.

Why it is undisputed: the current English sentence is factually false on every run — nothing was established, no request was sent. No reasonable operator prefers a success toast for an action that did not happen, on the one control the hardening checklist tells them to turn on. The new wording tells them precisely what the page's own permanent banner (`pages.settings.infoDesc`, SettingsPage.tsx:253) already says, and the Save button is already lit because the draft is dirty — so the instruction is actionable the moment it appears, with no other change.

Constraints satisfied: no new i18n key (both locales edited in the same commit, key still referenced in the same commit), no schema/DB/migration, no new route or endpoints.ts entry, no openapigen change, no runtime.Runtime path (settings are not an inbound/client op), no comment added. `make verify` scope: frontend `npm test` only.
**Deferred:** Deferred: making the enable branch persist immediately (the original plan's whole six-line code change).

It is deferred because there is exactly one implementable shape for it and that shape carries two effects an operator may reasonably refuse:

1. It must POST the entire draft. `UpdateAllSetting` (setting.go:1219-1271) writes every reflect field of `entity.AllSetting` and Gin binds omitted JSON as zero values, so a partial POST zeroes every other setting. `saveSetting` is `savePayload`, which builds from `allSetting` (the draft), so `{...allSetting, twoFactorEnable: true, twoFactorToken}` silently commits every unrelated pending edit the operator has open in another settings category — and, because `savePayload` runs only `AllSettingSchema.partial().safeParse` + `console.warn` while the page's Save button runs the full parse (SettingsPage.tsx:126-134), an invalid unrelated pending field turns "enable 2FA" into a server error naming a completely different setting, after the QR has already been scanned. Posting a pristine server snapshot instead would need a new prop threaded from useAllSettings and would make `markSaved(saved)` wrongly clear the dirty flag — architecture, not a bug-sized fix.

2. It fires `BumpLoginEpoch` on modal close. controller/setting.go:144-148 → user.go:146-157 bumps `login_epoch` for all users, and session.go:96 invalidates every session whose stored epoch no longer matches — including the operator's own. Under the status quo that logout lands when they press Save; under immediate-commit it lands the instant they click Confirm in the modal, taking the rest of their unsaved draft with it.

Also deferred, deliberately:
- `hasTwoFactorToken: true` on the enable patch, for symmetry with SecurityTab.tsx:243. Confirmed write-only (models/setting.ts:109, schemas/setting.ts:97, SecurityTab.tsx:243 are the only non-generated mentions); the server refetch supplies it. Fix-size rule.
- TwoFactorModal.tsx:70-73 accepting any 6 digits for a 'confirm' modal with no token, reachable by toggling a draft-enabled 2FA back off. Same root; resolves under either decision option, so it should not be patched separately before the decision.
- OutboundFormModal.tsx:184's hardcoded English toast. Unrelated finding.
**Test:** New file: frontend/src/test/two-factor-enable-claim.test.tsx (the `components` vitest project — jsdom, `src/test/**/*.test.tsx`, setup.components.ts already initialises i18next with the real en-US.json, so `t()` renders the real English string). Harness copied from frontend/src/test/api-token-date.test.tsx.

NO fake timers anywhere. The adversary is right that `vi.setSystemTime` without `vi.useFakeTimers()` throws `assertFakeTimers`, and a grep for `useFakeTimers|setSystemTime` across frontend/src returns zero files, so there is no precedent that faked timers survive an AntD Modal render. The clock is removed from the test entirely instead:
`vi.spyOn(OTPAuth.TOTP.prototype, 'generate').mockReturnValue('123456')` — the modal's check at TwoFactorModal.tsx:76 then passes for the literal '123456' regardless of wall time.

Steps:
1. `render(<SecurityTab allSetting={{ twoFactorEnable: false } as AllSetting} updateSetting={updateSetting} saveSetting={vi.fn()} />)` with `const updateSetting = vi.fn()`.
2. `fireEvent.click(screen.getByRole('tab', { name: /Two-factor authentication/ }))` (SecurityTab tab key '2', label from `pages.settings.security.twoFactor`).
3. `fireEvent.click(screen.getByRole('switch'))` — SecurityTab.tsx:295.
4. `fireEvent.change(await screen.findByLabelText('Code'), { target: { value: '123456' } })` — the modal input's aria-label is `t('twoFactorCode')` = "Code"; the change is required because the Confirm button is `disabled` until TotpCodeSchema passes.
5. `fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))`.
6. ANTI-VACUOUS GUARD (this is the repair for the adversary's second objection — the absence assertion must be anchored to proof that the path ran):
   `await vi.waitFor(() => expect(updateSetting).toHaveBeenCalledWith(expect.objectContaining({ twoFactorEnable: true })))`.
   If the tab never opened, the switch never fired, or the TOTP spy did not take, this line fails first with "expected spy to be called with arguments: [ ObjectContaining{twoFactorEnable: true} ] … Number of calls: 0", and the test cannot pass by accident.
7. THE ASSERTION, polling rather than synchronous so it is not green merely because the portal has not painted:
   `await expect(screen.findByText(/successfully established/i)).rejects.toThrow()`.

RED WITHOUT THE FIX, exactly: on unmodified HEAD the toast at SecurityTab.tsx:219 renders the en-US string "Two-factor authentication has been successfully established" into the `{messageContextHolder}` portal that SecurityTab renders inline, so `findByText` RESOLVES and vitest fails the last line with `promise resolved "<div class="ant-message-notice-content" …>" instead of rejecting`. After the string edit that literal no longer exists in en-US.json, `findByText` times out and rejects with "Unable to find an element with the text: /successfully established/i", and `rejects.toThrow()` passes.

MANDATORY before committing (CLAUDE.md's revert-and-watch-it-go-red protocol, and the one residual risk here): run the test against unmodified HEAD and confirm that exact "promise resolved … instead of rejecting" failure. No test in this suite currently asserts an AntD `message` toast, so if it passes on unmodified HEAD, the toast is not reaching the DOM in jsdom and the test is vacuous — in that case do not ship it; first prove the toast renders on HEAD with a bare `await screen.findByText(/successfully established/i)` and rebuild the assertion from whatever that proves.

Note the test asserts the INVARIANT "no completed-state claim before a save", not the new sentence, so it stays valid and stays red-without-fix whether the toast is reworded (shippableNow) or deleted, and it becomes the regression guard if the owner later picks option (A).
**Sites:**
- frontend/src/pages/settings/SecurityTab.tsx:219 — THE DEFECT: `messageApi.success(t('pages.settings.security.twoFactorModalSetSuccess'))`, fired unconditionally, before and instead of any persistence
- frontend/src/pages/settings/SecurityTab.tsx:220 — `updateSetting({ twoFactorToken: newToken, twoFactorEnable: true })` — draft-only; correct per the page contract, not a bug on its own
- internal/web/translation/en-US.json:1473 — "twoFactorModalSetSuccess": "Two-factor authentication has been successfully established" — the false claim lives in the string
- internal/web/translation/fa-IR.json:1353 — the same key, same false claim
- frontend/src/api/queries/useAllSettings.ts:42-48 — `updateSetting` is `setDraft` only; confirmed no HTTP call on this path
- frontend/src/api/queries/useAllSettings.ts:50-57 — `savePayload` (what SecurityTab receives as `saveSetting`) runs only `AllSettingSchema.partial().safeParse`, `console.warn`s on failure, and POSTs the raw object anyway — it does NOT enforce the page's own gate
- frontend/src/pages/settings/SettingsPage.tsx:126-134 — `onSave` runs a FULL `AllSettingSchema.safeParse(allSetting)` and refuses with a field-level toast; this is the gate `savePayload` bypasses
- frontend/src/pages/settings/SettingsPage.tsx:250 — the Save button, `disabled={saveDisabled}` i.e. lit the moment the draft is dirty; this is the feedback the enable path already produces
- frontend/src/pages/settings/SettingsPage.tsx:253 + en-US.json:1237 `pages.settings.infoDesc` — the permanent banner "Every change made here needs to be saved", visible on the Security tab, which the toast contradicts
- frontend/src/pages/settings/SecurityTab.tsx:232-245 — disable branch; commits the WHOLE draft today (`{...allSetting, …}`), so the whole-draft blast radius already exists in one direction
- frontend/src/pages/settings/TwoFactorModal.tsx:70-73 — `if (type === 'confirm' && !token) { close(true, codeOk.data); }` — a 'confirm' modal with no token accepts ANY 6 digits unverified. Reached when an operator toggles a draft-enabled (unsaved) 2FA back off; cosmetic wart of the same root, resolves under either decision option
- frontend/src/pages/settings/TwoFactorModal.tsx:76-77 — `if (totpRef.current.generate() === codeOk.data) close(true);` — the modal is a local verifier by design, correct as-is
- internal/web/service/setting.go:1219-1271 — `UpdateAllSetting` iterates every reflect field of `entity.AllSetting` and writes it; Gin binds omitted JSON as zero values, so a partial POST would zero every other setting. This is why 'post only the 2FA fields' is not available
- internal/web/service/setting.go:1332-1337 — `preserveRedactedSecrets` carries the STORED token forward when enable is true and the posted token is blank; setting.go:1212-1217 `refuseTwoFactorWithoutASecret` then refuses only when nothing was ever stored
- internal/web/service/setting.go:271,281 — GET redacts `TwoFactorToken` to "" and reports `HasTwoFactorToken` instead, so the draft's freshly generated token is the only copy the browser holds
- internal/web/controller/setting.go:144-148 — on the off→on transition `BumpLoginEpoch()` fires
- internal/web/service/panel/user.go:146-157 — `BumpLoginEpoch` does `Where("1 = 1").Update("login_epoch", login_epoch + 1)` for ALL users
- internal/web/session/session.go:96 — any session whose stored epoch no longer matches is invalidated, INCLUDING the operator's own. Enabling 2FA logs the operator out on their next request
- frontend/src/test/i18n-dead-keys.test.ts:1-90 — every en-US key must be referenced from frontend/src or internal/**/*.go, and every locale must carry the identical key set; deleting the toast call would orphan the key and turn this test red unless both locale files lose it too
- frontend/src/models/setting.ts:109 + frontend/src/schemas/setting.ts:97 + SecurityTab.tsx:243 — the only three mentions of `hasTwoFactorToken` outside `src/generated/`; confirmed write-only, never read. The adversary and the original plan are both right to leave it alone
- frontend/src/test/api-token-date.test.tsx — the harness precedent: renders SecurityTab directly with `updateSetting={vi.fn()} saveSetting={vi.fn()}`, no page wiring
- frontend/src/pages/xray/overrides/NordModal.tsx:236,251 — near-miss siblings (`messageApi.success` on `onAddOutbound`/`onUpdateOutbound`); those callbacks do reach a mutation, so not this defect. Separate finding at best
- frontend/src/pages/xray/outbounds/OutboundFormModal.tsx:184 — `messageApi.success('Link imported successfully')`, a hardcoded English string bypassing i18n. Unrelated to F396; do not fold in

**Re-check notes:**
- Not a blocker, but the test's step 5 needs a tighter query than `getByRole('button', { name: 'Confirm' })`. SecurityTab.tsx:279 has a second Confirm button in the Admin tab (key '1'), which is the defaultActiveKey and therefore already mounted when the test clicks over to tab '2'; AntD keeps rendered panes mounted and merely hides them. Whether the role query excludes it depends on rc-tabs putting display:none/hidden on the inactive pane in jsdom. If it does not, the test dies with 'Found multiple elements' on BOTH HEAD and the fixed tree - red for the wrong reason, not vacuous. Scope the query to the dialog (`within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' })`). The plan's own mandated HEAD run catches this, since the required failure text is specifically 'promise resolved ... instead of rejecting'.
- Minor divergence from the cited harness: api-token-date.test.tsx mocks `HttpUtil.get`, and the new test does not. SecurityTab's useEffect at :141-144 calls `loadApiTokens()` -> `HttpUtil.get` on mount unconditionally. Verified harmless - utils/index.ts:84 wraps it in try/catch, so the unmocked call is swallowed and `msg?.success` is falsy - but adding `vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: [] })` removes a source of jsdom noise for free.
- The plan's flagged residual risk is the real one and it is correctly gated: no test in this suite currently asserts an AntD `message` toast (grep of frontend/src/test for `ant-message|messageApi` returns nothing), so it is unproven that `messageContextHolder` paints into a `screen`-queryable node in jsdom. The plan already requires the revert-and-watch-it-go-red run and already says do not ship the test if HEAD passes. Follow that literally; do not accept a green HEAD run as 'the fix must already be there'.
- Scope note, not a defect: `make verify` is broader than 'frontend npm test only' - i18n-dead-keys.test.ts reads the locale JSONs, but it compares KEY SETS only (flattenKeys at :30-41), never values, so a reworded value cannot move it. Confirmed no Go source references twoFactorModalSetSuccess (the only non-translation hit is the gitignored internal/web/dist build artifact).


