# Discoveries ledger

Things found while fixing something else, and deliberately **not** folded into
that fix. Each one was a judgement that the change belonged outside the slice —
scope, risk, or "this is a different finding wearing a familiar coat".

Separate from `RUNTIME-RESULTS-2026-08-21.md` on purpose: that file records what
was measured and is finished when written. This one records open work and is
dispositioned at campaign end, the same way the three finding pools are. An entry
leaves here by being fixed, folded into a slice, or argued down in writing — not
by going quiet.

Each entry names: what it is, where it was found, why it was left, and what
closing it would cost.

**Dispositioned 2026-08-26.** Every entry now carries close / fix / keep-open
with a reason and, for the fixes, a size. The summary table and the argument
for each disposition are in `CAMPAIGN-CLOSE-2026-08-26.md` §3; the full
reasoning stays here.

---

## L1 — `PruneInbound` rewrites a rule's ids and enable flag but never its scope

**Status 2026-08-26: CLOSED, and the disposition changed on measurement.** It was
dispositioned *fix* on my summary, which overstated the harm: the row is disabled,
its remark already says *"disabled: its last inbound was deleted"*, and
`TestAnOrphanedRuleCanBeRepairedByNamingALiveInbound` proves against a live
database that an operator can name a live inbound and save. Re-enabling it empty
is still refused, which is F048 working. Both proposed fixes cost more than the
odd-looking row does — one destroys the operator's rule, the other adds a schema
value every reader must learn. The test is the evidence and stays.

**Found:** during F048's shape work.
**Where:** `internal/web/service/routing.go:399-403`.

Deleting an inbound leaves any rule scoped to it as `ingress_scope='selected'`,
`ingress_ids='[]'`, `enable=false`. The row is now self-contradictory: it claims
a selected scope and names nothing.

That state is what makes F048's third population possible — re-enabling such a
rule fed `[]` through `ruleToIntent` and came back `all`. F048 closes the
*widening*; it does not fix the row. After F048 the same toggle fails visibly,
because the server refuses a `selected` rule that names nothing.

**Why not folded in:** F048's fix is "a scope is never inferred from a failed
lookup". Rewriting what `PruneInbound` stores is a different change to a
different function, and doing both at once would make the red test ambiguous
about which half it was proving.

**Cost to close:** `PruneInbound` sets `ingress_scope='all'` — no, that widens
by another route. Either it leaves the rule disabled with a scope that says so,
which probably means a third scope value, or it deletes the rule outright and
says so in the response. That choice is the work; the code is small.

---

## L2 — `TestConfig` cannot be pointed at a staged binary

**Status 2026-08-26: FIXED.** `TestConfigWith(binary, cfg)` split out; `UpdateXray`
stages the download, asks it about the running config, and renames only then.

**Found:** during F280.
**Where:** `internal/xray/preflight.go:51` — `exec.CommandContext(ctx, GetBinaryPath(), "-test", ...)`.

F280 downloads and stages the new Xray while the old one is still serving, then
lets `RestartXray(true)` run the pre-flight before it stops anything. So a
binary that verifies but cannot load the current config is refused with the old
core still up — which is the property that made position 3 the right choice.

The residual: by then the new binary has already been renamed into place. It
stays there, unused, with the old process running on its old inode. Every later
restart is refused rather than starting a core that would exit, which is loud
and safe, but the panel is left carrying a binary it will not run.

**Why not folded in:** closing it means testing the staged file *before* the
rename, which needs `TestConfig` to accept a binary path. That is a signature
change to the pre-flight — the one piece of machinery every restart in the panel
now depends on — for a residual that is strictly better than the bug being
fixed.

**Cost to close:** extract `testConfigWith(binary string, cfg *Config) error`,
have `TestConfig` call it with `GetBinaryPath()`, and have `UpdateXray` call it
with the temp path before renaming. Perhaps 15 lines plus a test that the
pre-flight still fails open when the staged binary is unrunnable.

---

## L3 — a missing AmneziaWG device cannot be told from a refused parameter set

**Status 2026-08-26: FIXED.** The presence question goes to the plane, which
answers it without an error; only a device that exists is configured.

**Found:** during F085, recorded in RUNTIME-RESULTS §11.
**Where:** `internal/cores/internal/amneziawg/core.go` `applyParams`.

The module answers a missing device with EINVAL, byte for byte the errno it uses
for parameters it refuses. So `applyParams` reports both, and the doc comment
claiming a missing device is non-fatal was removed rather than made true.

§11 deferred the alternative — a presence check rather than an errno test — to
F266's slice, on the grounds that it only matters once these errors reach an
operator. **F266 has since landed and the presence check was not built**, so
this is now genuinely open rather than scheduled.

**Why not folded in:** F266's slice was already the largest in its batch, and a
per-inbound netlink round trip every 10s pass is a cost that deserves its own
argument rather than riding along.

**Cost to close:** ask the plane whether the link exists before configuring
parameters, and return nil when it does not. One extra netlink call per AWG
inbound per supervise pass.

---

## L4 — `destResolves`' doc comment describes behaviour the pinned core no longer has

**Found:** during the F064 synthesis review.
**Where:** `internal/web/service/routing.go`, the `destResolves` doc comment.

It says Xray does not fail an unknown `outboundTag` and falls back to the first
outbound. In the pinned core that is false: `app/dispatcher/default.go:466-471`
logs "non existing outTag", closes the writer and interrupts the reader, under a
`DO NOT CHANGE` comment.

**Why not folded in:** it is a comment, and correcting it inside an unrelated
fix would bury it. It is also load-bearing for F075, which is about exactly this
failure — so it should be corrected by the change that acts on it.

**Cost to close:** two lines, inside F075.

---

## L5 — two staticcheck deprecations

**Found:** during the lint reconciliation.
**Where:** `internal/web/service/tgbot/tgbot.go` (`fasthttp.RetryIf`) and
`tools/openapigen/walker.go` (`go/parser.ParseDir`).

Dispositioned by the operator already: they stay untouched until real work
reaches those files, because a deprecation swap is work on the file rather than
lint silence. Recorded here so "why are there two lint issues" has an answer
that is not "nobody looked".

**Cost to close:** whatever the replacement APIs cost, at the time something
else is being changed in those files.

---

---

## L6 — `KeySelfLoop` is declared and emitted nowhere

**Status 2026-08-26: CLOSED — probed, then deleted.** The probe answered it:
`Dest` is `{Kind, Tag, ExitID}` and no destination kind names an ingress, so a
self-loop is **not expressible as a rule shape** and the planner — which sees
handles, never addresses — could not detect one if it were. The key is deleted
and nothing replaces it, per the disposition.

The one adjacent shape that IS constructible: an operator egress whose *target*
is one of this panel's own inbounds. That belongs to egress validation, not to
routing diagnostics, and is recorded here rather than dropped.

`internal/arch/diag_keys_emitted_test.go` now fails on any `Key*` constant with
no emit site outside its declaration. **Its first version passed while the bug
was present**, because the guard's own doc comment quoted the key it was looking
for — the same disease, in the guard written to cure it. Tests are excluded from
the scan now, and that story is in its comment.

**Found:** while gathering F049's message keys.
**Where:** `internal/routing/diag.go:43`.

Six of the seven refusal keys have emit sites in `plan.go`.
`pages.xray.routing.refuse.selfLoop` has none — a repo-wide search for the
constant, for the string, and for "self-loop" in any casing finds only the
declaration. It has been that way since `e30b1841`, the commit that introduced
the whole diag vocabulary, so it is an intent that was declared and never built
rather than a detector that was later removed.

F049 ships the six that have emitters. Writing an eighth diagnostic inside it
would grow the slice past its own finding, whose job is making the diags that
*exist* refuse.

**The disposition question, which needs a probe rather than an opinion:** can a
self-loop actually be constructed through the panel's own forms — a rule whose
destination resolves back to the ingress it is scoped to, or a chain of rules
that does — or do the planner's existing refusals make it structurally
impossible? `bounceable`, `destResolves` and the exit-handle checks already
reject several shapes that would look like one.

If a probe shows it is unreachable, the key is deleted for good and nothing
replaces it. If it is reachable, the detector is its own small finding with its
own description, and the key gets a string then.

**Cost to close:** the probe. Constructing the candidate shapes against the
compiler in `internal/routing` is cheap — `Plan` is pure and its test harness
already builds subjects, exits and rules directly.

---

## L7 — the dead-keys test cannot tell a live key from its own declaration

**Status 2026-08-26: FIXED, and this entry's own claim was wrong.** It said
excluding declaration blocks "would have caught `selfLoop`". It would not:
the scan starts from the **locale file**, and `selfLoop` never had a shipped
string, so it was invisible to this test in both directions. L6 needed the Go
guard instead.

What the tightening is genuinely worth is the other direction — a key that HAS a
string and whose only reference is its own declaration, i.e. one whose emitter
was deleted. Measured during the fix: stripping declarations outright condemned
**five live keys**, because they are emitted through their Go identifier and
never through the dotted string. So a declaration counts as a use only when the
identifier it declares is used somewhere else.

**Found:** while landing F049's keys.
**Where:** `frontend/src/test/i18n-dead-keys.test.ts`.

The test reads every `.ts`/`.tsx` under `frontend/src` and every `.go` under
`internal/`, concatenates them, and tokenises the whole thing with
`/[A-Za-z][A-Za-z0-9_.]*/g`. A key passes if its dotted form appears anywhere in
that token set — and a Go constant declaration like

```go
KeyNoSnat = "pages.xray.routing.refuse.noSnat"
```

produces exactly that token. So a key is "referenced" by the line that declares
it. F049's six passed the gate before a single one of them had a reader.

That is false assurance, and it is how `selfLoop` (L6) survived from
`e30b1841` until someone read the file: it had a constant, so it looked used.

**Why not fixed here:** the tokeniser is the gate every locale change in the repo
runs through. Tightening it will fail keys that are legitimately referenced in
ways it cannot see — template literals, the prefix-harvesting fallback it
already carries for them — and triaging that is a task of its own, not a rider
on a routing fix.

**Disposition question:** can the scan distinguish a declaration from a use
cheaply enough to be worth it? Excluding `internal/routing/diag.go`-style
constant blocks is a one-line glob and would have caught `selfLoop`; excluding
every declaration everywhere is a different and much larger problem. Answer the
narrow version first.

**Cost to close:** small if the answer is the narrow one, unknown otherwise —
and the honest first step is running the tightened scan to see how many existing
keys it fails.

---

## L8 — nothing reads the compiler's diagnostics at generate time

**Decided 2026-08-26, not yet built.** The surface is **a status field on the
existing routing list response** — the smallest of the three options, and truth
on a page the operator already reads. No new endpoint. `KeyNoFront`'s held
translation ships with it, which is when it stops being a string with no reader.

**Found:** while landing F049.
**Where:** `internal/web/service/routing.go` `applyRoutingPlan`.

F049 gave `Compiled.Refusals()` its first production caller, at **save** time: a
rule the compiler would drop is now refused before it is stored. Generate time
is untouched — `applyRoutingPlan` reads `Fronts`, `Outbounds` and `XrayRules`
and never looks at `Diags`.

That gap is not theoretical. A rule that was routable when it was saved goes
inert later, when the inbound it names is disabled, when an exit stops
resolving, or when the operator edits the Xray template out from under it. Those
are exactly the rules an operator most needs told about, and they arrive after
the only check that exists.

F049 added a `logger.Warning` per generate-time refusal so the condition is at
least discoverable. That is a floor, not a surface: it is a log line carrying a
message key, not a translated sentence, and nobody watches the log for a rule
that quietly stopped working.

**`KeyNoFront` is the concrete casualty.** It can only fire at generate time —
the save-time compile answers `FrontIDFor` unconditionally on purpose, because a
missing front is infrastructure the operator cannot fix by editing the rule. So
its **string was deliberately not shipped with the other six**: a translated
string with no reader is L7's disease, and adding one while writing L7 would be
absurd. It gets its string when it gets a surface.

**Disposition question:** where do generate-time diags belong? F266's warning
field is the obvious candidate and does not fit — it rides an HTTP save
response, and generation happens on restart where there is none. The real
options are a panel-status field the Routing page polls, a preview endpoint
(which the audit costed as the full four-step endpoint dance), or a persisted
per-rule health column. Pick one before writing any of it.

**Cost to close:** the smallest honest version is a status field on the existing
routing list response, so the Routing page can mark a stored rule inert without
a new endpoint. Not costed further, because the choice comes first.

---

## L9 — a boolean that defaults to true cannot be created false

**Found:** by a test fixture that would not stay disabled, while writing F075.
**Where:** `internal/database/model/model.go` — five models.
**Status:** reachability audit complete. **Four of five are reachable, one of
them on an access-control path.** Promoted out of "ledger" and awaiting a fix
description.

GORM omits a zero-valued field from the INSERT when its column carries a
`default`, so the database supplies the default instead. Every one of the five
exhibits it, measured against the throwaway Postgres rather than inferred:

```
ApiToken.Enabled              created=false  read back=true
Node.Enable                   created=false  read back=true
ClientRecord.Enable           created=false  read back=true
OutboundSubscription.Enabled  created=false  read back=true
RoutingRule.Enable            created=false  read back=true
```

The mechanism is uniform. What is not uniform is whether anything creates the
row with an intended `false`, and that is what decides severity.

### ClientRecord — REACHABLE, and it is an access-control defect

Measured end to end through the operator's own path, not through a raw
`db.Create`: an inbound is created, a client is added to it with the enable
switch **off**, and the stored row is read back.

```
operator added the client with enable OFF; stored Enable=true
```

`Client.ToRecord()` carries `Enable: false` into `toCreate`, and
`client_link.go` writes it with `CreateInBatches` — same omission, same default.
The row is then enabled, so `buildInboundForLocalRuntime` does not strip it and
the client is pushed to the core. **A client an operator staged as disabled can
connect immediately.** The enable field is real and editable on the add form; it
is not a control that only exists on edit.

### Node — REACHABLE

`NodeMutationRequest.Enable` (`node_contract.go:126`) maps through `toNode()`
(`:174`) into `NodeService.Create`, which is a bare `db.Create(n)`
(`node.go:427-432`). A node added with the enable switch off is stored enabled,
so the panel begins heartbeating and syncing inbounds to a node the operator
staged as off. Operational rather than access-control, but it contacts a remote
host that was not meant to be contacted yet.

### OutboundSubscription — REACHABLE

`createOutboundSub` reads `enabled := c.PostForm("enabled") != "false"`
(`xray_setting.go:417`) and passes it to `Create`, which builds the row and
inserts it. A subscription created disabled is stored enabled, so the panel
immediately fetches the remote URL and merges whatever outbounds it returns into
the generated config. Config-injection surface rather than access control, and
the URL is operator-supplied, so the blast radius is bounded by who could set it.

### ApiToken — NOT reachable

`ApiTokenService.Create(name)` hardcodes `Enabled: true` (`api_token.go:78`)
and takes no enable parameter at all. There is no path that intends `false` at
creation; disabling is a later `Updates(map)`, which writes the column
explicitly and works. The mechanism is present and inert.

### RoutingRule — REACHABLE

The original finding. `RoutingService.Add` cannot create a disabled rule, so a
rule staged with the switch off starts routing traffic on save.

### The fix, and the two shapes that do not work

**Status: FIXED.** Recorded here rather than deleted, because two of the three
candidate shapes were measured NOT to work and the next person will reach for
them.

```
plain Create                    -> true   (the bug)
Select("*").Create              -> true   does NOT fix it
Select(the column).Create       -> true   does NOT fix it
Omit(the column).Create         -> true   does NOT fix it
Select("*").CreateInBatches     -> true   does NOT fix it

Create + an explicit Update     -> false  works
Create from a map               -> false  works
*bool field, set to &false      -> false  works, and nil still takes the default
plain bool with NO default tag  -> false  works
```

GORM skips a default-tagged field at the schema level; no call-level `Select`
or `Omit` reaches it. **And it writes the default back into the struct after the
insert**, so a fix that reads the field afterwards to decide whether to repair
it sees `true` and does nothing — which is how the first attempt at this passed
review and failed its own test.

What landed is the second row: capture the intended value *before* the insert,
then write the column explicitly when it was false. One helper,
`applyStagedOff`, at the four reachable creates. Chosen over the two structural
shapes because dropping the tag silently flips what an omitting caller gets
(true today, false after) across every construction site, and `*bool` is correct
by construction but rewrites every reader of a field the codebase reads
constantly. Both remain available if this proves too easy to forget.

The guard against forgetting is `internal/arch`'s
`TestEveryTrueDefaultedBooleanHasACreateProof`, which derives its model set from
the tags rather than a list — and immediately found two fields a hand-written
grep had missed (`Host.IsDisabled`, `Host.IsHidden`, both `default:false` and
therefore safe, which is why the scan narrowed to `default:true`). It cannot
check the call sites, because proving a given `Create` is safe needs the
argument's type and these tests parse rather than type-check; so it requires the
checkable next-best thing, a named regression test per model.

**Not repaired: rows already created this way.** There is no record of intent to
repair from — a row stored enabled is indistinguishable from one deliberately
enabled. The changelog says so and tells operators what to re-check by hand.

---

## L10 — the same mechanism on every non-boolean defaulted column

**Found:** asked for after L9, on the reasoning that the mechanism is not about
booleans at all.
**Where:** `internal/database/model/model.go`.
**Sized as a ledger entry, not work:** the triage below found nothing reachable.

GORM's omission is about the zero value, not the type: any column whose
`default` differs from its Go zero value cannot be created with that zero value.
The tag scan L9's guard already builds lists them in seconds.

**31 non-boolean defaulted columns. 18 declare a zero-agreeing default and are
structurally safe.** The 13 that do not:

| model | column | type | default |
|---|---|---|---|
| Egress | Owner | string | `'operator'` |
| Host | Security | string | `same` |
| Inbound | ShareAddrStrategy | string | `node` |
| Inbound | SubSortIndex | int | `1` |
| Inbound | TrafficReset | string | `never` |
| Inbound | TrafficResetDay | int | `1` |
| Node | InboundSyncMode | string | `all` |
| Node | Status | string | `unknown` |
| Node | TlsVerifyMode | string | `verify` |
| OutboundSubscription | UpdateInterval | int | `600` |
| RoutingRule | Criteria | string | `'{}'` |
| RoutingRule | DestKind | string | `'direct'` |
| RoutingRule | IngressScope | string | `'selected'` |

### Where zero is plausibly a meaningful stored value — triaged

Three of the thirteen have a zero value an operator could genuinely mean. All
three turn out to be normalised in code before the insert, so the column default
never gets a chance to disagree with intent:

- **`Inbound.SubSortIndex` (`default:1`)** — 0 is a legitimate sort position.
  `normalizeSubSortIndex` runs on both the create and the update path
  (`inbound.go:936`, `:1366`), so the stored value is already non-zero.
- **`RoutingRule.DestKind` (`default:'direct'`)** — the sharpest-looking one,
  because an unset destination silently becoming "send it out directly" is
  exactly L9's shape. `normalizeRule` refuses an unrecognised kind outright
  (`routing.go:678`), so `""` cannot reach the insert.
- **`OutboundSubscription.UpdateInterval` (`default:600`)** — `Create` clamps
  `updateInterval <= 0` to 600 before building the row.

`Egress.Owner` deserves a line because it looks worse than it is: an ownerless
egress becomes an *operator* egress, which is the kind `RoutingService.Exits`
offers as a destination. Panel-owned fronts set it explicitly
(`routing.go:150`), and 'operator' is the designed fallback for a row the
operator created, so this is intent rather than accident.

One is safe in the useful direction: **`Node.TlsVerifyMode` (`default:verify`)**
— an empty value becomes `verify`, not `skip`. Had the default been the other
way round it would have been a security finding.

**Disposition question for the campaign-end pass:** the triage above covers the
three columns where zero is plausibly meaningful. The remaining ten have zero
values that are not meaningful for their column (an empty security mode, an
empty sync mode, an empty JSON blob), so the question is whether a per-column
reachability audit is worth running at all, or whether "no meaningful zero"
retires them. My reading is that it retires them, and that L9's arch guard is
what actually protects the future — but a boolean guard does not cover these,
and extending it would mean deciding what "meaningful zero" means per column
rather than deriving it.

**Cost to close:** the scan is minutes and is already written; the judgement is
the work.

---

## L11 — the LDAP safety guard has no surface, and that is already filed

**Found:** while describing F367.
**Where:** `audit-appendix/feature-gaps.md`, the LDAP section — **not a new
entry.** Cross-checked before writing this, which is the point of the pointer.

Two of the 114 gaps already cover it:

- *"No 'Test LDAP connection' action, unlike SMTP and Telegram which both have
  one"* — and LDAP has strictly more ways to be misconfigured than either.
- *"No visibility into LDAP sync state: last run, last result, users fetched,
  clients created/enabled/disabled/deleted"* — the job computes every one of
  those numbers and only logs them.

What F367 adds is a reason to raise their priority rather than a new gap. After
this slice the panel has a guard that will **refuse to delete and say so only in
the log**, and a baseline that lives in memory and resets on restart. Both are
correct; neither is visible. An operator whose auto-delete has quietly stopped
acting — because their directory legitimately shrank — has no way to see that
from the UI, and the escape path (restart, let one sync record the new size) is
something they can only learn from the changelog.

The second gap's own *Suggested* text already anticipates this: it proposes that
"the same settings row that should hold the retention baseline can carry a JSON
blob". That row does not exist yet, which is why the baseline is still in memory.

**Disposition question:** does the retention baseline become persistent as part
of building that status surface, or before it? Persisting it changes behaviour —
it would survive restarts, which removes the escape path this slice just
documented — so the two decisions are the same decision and should be made
together.

**Cost to close:** the feature-gaps entries carry their own suggested shapes.
This entry exists so the campaign-end pass does not treat the LDAP guard's
invisibility as an unfiled discovery.

---

## L12 — 15 tests fail on the maintainer's own machine, every run

**Status 2026-08-26: FIXED.** `requirePosixHost` on all 15. Windows now exits 0;
all 15 still execute and pass on Linux, 0 skipped there.

**Found:** while gathering the gate results for the p-ui.sh pass.
**Where:** `internal/cores/internal/mtproto`, `internal/cores/internal/xray`,
`internal/mtproto`, `internal/web/job`, `internal/xray`.
**Disposition: fix.** ~20 lines, one chore commit.

`go test ./...` on the maintainer's Windows box fails 15 tests across five
packages. All of them pass on Linux; all of them are Linux-*target* tests in a
project whose CLAUDE.md declares Linux-only:

- process management that sends SIGTERM and expects a graceful exit
- stand-in binaries written as `#!/bin/sh` scripts, which Windows cannot exec
- file-mode and atomic-rename assertions with no Windows equivalent

They were verified as pre-existing by checking out `9d433de0` and reproducing
the same failures, so none is campaign damage — though this campaign widened
`TestFail2BanProbe` from one failing subtest to three by adding cases that also
execute the stand-in.

**Why it is worth fixing rather than tolerating.** The failures are harmless.
The habit is not: a maintainer who sees 15 red on every local run stops reading
red. That is exactly how the mid-campaign `&&`-chain incident happened — a
commit landed while a panic was on screen — and why gate-on-exit-code became a
standing rule afterwards. A gate nobody believes is not a gate.

**Cost to close:** an explicit `runtime.GOOS != "linux"` skip with a stated
reason on each, so a clean local run means something and an unexpected failure
is visible again. The alternative — a ledger entry saying "ignore these" —
documents the noise instead of removing it.

---

## L13 — every inbound save refusal is English-only

**Found:** while shaping F317's refusal, deciding whether it needed a locale key.
**Where:** `internal/web/service/inbound.go` and its neighbours.
**Disposition: keep open**, and it is a surface decision rather than a defect.

F317's refusal was described as needing a message key in both locales. It does
not — and shipping one would have made it the **only** localised refusal on that
path. Every sibling is a plain English `common.NewError`: the port conflict, the
REALITY combination, the Shadowsocks cipher, the share-address check. The
inbound controller has no `RefusalError` handling either; only
`controller/routing.go` does, from F049.

So one Persian sentence among a dozen English ones would read as a bug, not as
care. F317 follows the convention it landed in.

**Disposition question:** localising this surface means a `RefusalError`-style
typed error for the inbound path, a key per refusal, and the controller plumbing
to render them — perhaps a dozen keys in two locales. That is a feature-sized
piece of work with a clear owner-facing benefit, and it belongs in
`feature-gaps.md` rather than in a defect list. Cross-check that file before
opening it, the way L11 was cross-checked.

**Cost to close:** the typed error and controller plumbing are small; the
translation pass over every existing refusal is the actual work.

---

## L14 — the two TOTP verifiers disagree about whitespace

**Found:** while shaping F208, and kept out of it on external review.
**Where:** `internal/web/service/panel/user.go` (login) vs
`internal/web/service/setting.go` `VerifyTwoFactorCode` (settings).
**Disposition: keep open** — a deliberate decision, not a defect.

The settings-side verifier trims the submitted code
(`gotp.NewDefaultTOTP(token).Verify(strings.TrimSpace(code), ...)`). The login
path compares raw (`...Now() != twoFactorCode`). A code pasted with a trailing
space is accepted on one path and refused on the other.

**Why it did not ride with F208.** The first draft of that fix mirrored the
settings verifier onto the login path — switching to `Verify` and trimming — and
justified it as symmetry. That was wrong twice over:

- `Verify` is `otp == t.At(timestamp)`. Measured: no skew tolerance, not
  constant-time. Switching to it buys nothing, and claiming a security benefit
  for it would have been a claim the measurement contradicts.
- Trimming changes how an authentication input is handled. That is a behaviour
  change on the login path, arriving as a rider on a security fix, which is the
  shape this campaign refuses everywhere else.

F208 therefore adds the empty-secret guard and changes nothing else, and
`TestLoginStillComparesTheSubmittedCodeRaw` pins that so the trim cannot happen
by accident.

**Disposition question:** should both paths trim? Almost certainly yes — a
pasted code is a real user event and the settings page already accepts one — but
it is a UX decision about an auth input and deserves to be made once, on both
paths, with its own line in the changelog. Not smuggled.

**Cost to close:** two lines and a test, once the decision is made.

---

## L15 — the Telegram ownership gate lives in a `default:` branch

**Found:** while re-verifying F288 for campaign 2, which turned out to be
already closed by `b700ae49`.
**Where:** `internal/web/service/tgbot/tgbot_router.go`, the callback switch.
**Disposition: keep open** — latent, not a live defect.

`b700ae49` closed the callback auth bypass by adding `ownsEmail`, which every
email-carrying callback passes through. But that gate sits in the switch's
`default:` branch, reached by `CutPrefix` over the three known prefixes
(`client_sub_links `, `client_individual_links `, `client_qr_links `).

**A future email-carrying callback added as an explicit `case` sidesteps it
entirely** — and would look completely ordinary next to the cases already there.
The bug is not present; the shape that reintroduces it is one plausible edit
away.

Secondary: `isClientSelfCallback`'s doc comment and
`TestIsClientSelfCallback` still read as if the payload prefix were the
authorisation. Both are now true statements about a *whitelist* whose second
stage is `ownsEmail`, and neither says so.

**Disposition question:** is a guard cheap enough? An `internal/arch` or unit
test asserting that every email-carrying callback prefix routes through
`ownsEmail` would turn "latent" into "cannot regress". If resolving which
prefixes carry an email needs more than a source scan, the doc-comment fix plus
this entry is the right weight instead.

**Cost to close:** one clarifying comment either way; the guard is small if the
prefix set is derivable, unknown if it is not.

---

## L16 — `internal/web/service` runs against Go's default test timeout

**Found:** while gating F381, when the full suite failed at `600.302s`.
**Where:** `internal/web/service`, the package as a whole.
**Disposition: keep open** — a fragility, not a defect, and the fix is a choice.

Measured on the VM against live PostgreSQL, `-skip 'Scale$'`:

```
at the F158/F272 commit      ~395s
with F381 + F241 added       ~600s, i.e. over Go's 10-minute default
```

The first failure read as a deadlock and cost real time to diagnose. It was not:
the package is simply slow — several hundred DB-backed tests, each creating and
dropping its own PostgreSQL schema — and the additions pushed it past the line.

**Why it matters more than the number suggests.** A suite that fails on duration
produces the same red as a suite that fails on a bug, and the first instinct is
to look for a hang. That is the L12 problem wearing different clothes: a gate
whose failures are not trustworthy stops being read. It also means every future
test added to this package is spending a budget nobody is tracking.

**Disposition question — three shapes, none obviously right:**

- **Raise the timeout** in CI and in the documented command. Cheapest, and it
  concedes that the number will keep growing.
- **Split the package's tests**, e.g. move the DB-backed reset/depletion/traffic
  cluster into its own package. Buys parallelism across packages, and is a real
  refactor of test-only code.
- **Share one schema per package instead of per test.** The biggest win —
  `CREATE SCHEMA` + full `AutoMigrate` per test is most of the cost — and by far
  the riskiest, because per-test isolation is exactly what makes `-shuffle=on`
  meaningful here.

**Cost to close:** the first is minutes and settles nothing; the third is the
real fix and needs its own description. Measure before choosing: the split
between schema setup and test body has not been profiled, and the third option
is only worth its risk if setup dominates.

---

## L17 — REALITY `sni` and `sid` are randomised per fetch in two of four renderers

**Found:** while fixing F023, once the pinned-`serverName` half was closed.
**Where:** `internal/sub/service.go` `applyShareRealityParams`,
`internal/sub/json_service.go` `realityData`.
**Disposition: keep open** — a posture question, not a defect, and the answer
changes what every existing client holds.

F023 bundled two claims. One was unambiguous and is fixed: a pinned
`realitySettings.settings.serverName` was ignored by all three Go renderers
though the frontend has always preferred it. The other is this.

With nothing pinned, the four renderers disagree about which member of the list
to hand out:

```
raw link (Go)      random member of serverNames, random member of shortIds, per fetch
JSON (Go)          random member of both, per fetch
Clash (Go)         serverNames[0], shortIds[0]
frontend builder   serverNames[0], shortIds[0]
```

**Why it was left alone.** Spreading clients across the declared names is a
plausible deliberate choice — it is what the lists are for, and a single name
handed to every client is a single thing to block. But randomising *per fetch*
rather than per client means one client's link changes every time the
subscription is polled, which is why the same code was already changed once for
`spiderX` (#5718): a value that moves under a client is a value that cannot be
diffed, cached, or supported.

**The three shapes:**

- **Deterministic, first entry.** Matches Clash and the frontend, and makes all
  four agree. Loses the spread entirely.
- **Deterministic per client**, derived from the subscription id the way
  `deriveSpiderX` already is. Keeps the spread across the client base, makes each
  client's own link stable, and is the shape this file already reaches for.
- **Leave it.** Defensible only if the per-fetch churn is the point.

**Cost to close:** small — `deriveSpiderX` is the template and the seam is two
functions. What it needs first is a decision, because option two changes the
`sni` and `sid` in every REALITY link already in circulation.

---

## L18 — a Host's Final Mask is dropped entirely from a Hysteria raw link

**Found:** while fixing F138, reading what else the Hysteria fan-out skips.
**Where:** `internal/sub/service.go` `genHysteriaLink`, the salamander block.
**Disposition: keep open** — out of F138's size, and one shape of it is arguably
correct.

The Hysteria generator does not use the unified endpoint builder, so
`applyEndpointFinalMask` never runs for it — deliberately, because the `fm=`
JSON dump it writes breaks mihomo. Instead the generator reads the *inbound's*
`finalmask` directly to pull out the salamander obfs password.

F138 fixed the port-hop range for a Host by resolving it through the same
`mergeFinalMask` the JSON renderer uses. The salamander block was left reading
the inbound's masks only, so a Host row carrying its own Final Mask with a
different obfs password renders a link with the inbound's password instead.

**Why it was not folded into F138.** The fix is one more call to the same
helper, but it changes output for a configuration that is currently rendered
wrong rather than not at all, and F138's claim was about `mport`. Shipping a
second behaviour change unasked next to a named one is the thing the size rule
forbids.

**Cost to close:** small, and it should be described before it lands: a host
overriding an obfs password is a different operator intent from a host
overriding an address, and it is worth confirming the Hosts page means to offer
it.
