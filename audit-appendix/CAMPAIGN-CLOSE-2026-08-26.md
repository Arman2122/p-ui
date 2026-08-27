# Closing the audit campaign

**Baseline `219a6de4` → `351114e1`, the last commit of campaign work; this
document and its corrections follow it. 69 commits, 206 files, 51 new test
files, 8 new architecture guards.**
Started 2026-08-20 from `AUDIT-2026-08-20.md` (394 verified findings: 3 Critical,
62 High, 187 Medium, 142 Low) and `IP-LIMIT-ACTUATOR-RESEARCH.md` (2 further
Criticals). Closed 2026-08-26.

This document exists so someone opening this repo months from now can answer
three questions without reading 69 commit messages: **what is now guaranteed,
what was deliberately left, and what to take next.**

---

## 1. What this codebase now guarantees

Each item below is a property that did not hold at `219a6de4`, holds now, and
has a test that fails if it stops holding. Grouped by the harm prevented,
because that is how someone triaging a future bug will look for them.

### It will not silently do nothing

The recurring failure in this codebase was not crashing. It was **reporting
success while acting on nothing** — the class that survives every smoke test and
gets discovered by a customer.

- **A config the core would refuse is refused at the save.** `xray -test` runs
  before a hot apply and before a row is enabled, so a save that would produce a
  core that cannot boot fails visibly (F004, `ae782f57`). A save the core
  rejected is no longer reported as success (`5d782373`).
- **A routing rule the compiler would drop is refused when written**, not
  discarded silently at generate time (F049, `5f45b322`). A rule with no
  outbound, or with criteria the core cannot read, is refused rather than
  emitted as an empty tag (`31377179`, `09f081c2`).
- **An IP-limit cap reports enforceable only if a ban would land.** The probe
  now asks the daemon about p-ui's jail instead of asking the client binary
  whether it exists — `exit 0` versus `exit 255` on a box whose jail this
  panel's own menu deleted (H2, `dc0c1eb9`).
- **Share links come from the one renderer that knows about hosts.** The browser
  built them locally with no concept of a Host row, so a CDN-fronted inbound
  handed out the origin address from the very page listing the hosts (F136,
  `5df0fb20` + `5c5d976d`). When the server cannot answer, the panel says so —
  it does **not** fall back to the local builder, because a wrong address that
  looks like a right one is exactly the failure being removed.

### It will not destroy or reinterpret what the operator configured

- **A row created with its switch off is stored off.** GORM omits a zero-valued
  field whose column carries a `default`, so five models could not be created
  disabled. Four were reachable; one — `ClientRecord` — meant *a client staged
  as disabled could connect immediately* (L9, `a493f4b9`).
- **The upgrade seeder no longer destroys configured IP limits** (C1, `732b486d`).
- **LDAP will not delete without a trusted baseline**, and the user search is
  paged, so a truncated fetch cannot read as a shrunken directory (F367/F370,
  `f615f84f`).
- **The fail2ban menu deletes only what p-ui owns.** It ran `rm -rf
  /etc/fail2ban`, and a range-delete that ate `[sshd]` and `[recidive]` on a
  routine update (H6/H7, `dc0c1eb9`).
- **Stored rows are never silently reinterpreted.** Where a stored value is
  indistinguishable from a deliberate one — the mixed inbound's `127.0.0.1`, a
  row already created enabled — the fix is scoped to new writes and the
  changelog tells operators what to change by hand. Applied consistently; it is
  why several fixes are narrower than their findings.

### It will not lock the operator out

- **A firewall never enables without the operator's own SSH port in the rules.**
  Both entry points read the panel's real ports and refuse otherwise. Menu
  option 5 was a bare `ufw enable` with no rules at all (F156, `9d433de0`).
- **The panel refuses a web port it cannot bind** (`b1d1a47f`), and an update
  stages its management script before tearing the old install down (`c3b879b9`).
- **Sessions are revoked on logout and `sessionMaxAge` is enforced server-side**
  (`7971b1e8`); a Telegram user is served only the links they own (`b700ae49`).

### It will run on every platform it claims to support

- **The Xray download asks for an asset name that exists.** Every armv5/v6/v7
  panel had been requesting a filename that has never been published — a 404,
  every time, on three documented platforms. The correct name is stamped at
  build time because it cannot be derived afterwards and must not be guessed:
  the v7a build **SIGILLs** on an armv5 board (F281, `9d433de0`).
- **Tunnel teardown works on the nft every target ships.** `nft destroy` arrived
  in 1.0.8; Ubuntu 22.04 ships 1.0.2 and Debian 12 ships 1.0.6, where it is a
  *parse error* — so on two of five supported targets the masquerade table was
  never removed (`d2092619`).

### Eight things the build now refuses to let anyone undo

`internal/arch/` holds no runtime code. Each file parses the repo and fails when
a property erodes. These exist because the properties have **no other test
leverage** — most are shell, or an ordering constraint no unit test observes.

| Guard | Refuses |
|---|---|
| `restart_preflight_order_test.go` | a restart path that applies before it pre-flights |
| `depletion_restart_test.go` | a depletion path that revokes without ending the connection |
| `default_boolean_test.go` | a `default:true` boolean with no per-model create proof |
| `install_ownership_test.go` | handing the install tree to the release archive's uid |
| `installer_teardown_test.go` | an update that tears down before it has staged |
| `awg_module_build_test.go` | an upgrade that skips the AmneziaWG module rebuild |
| `firewall_guard_test.go` | any `ufw enable` that bypasses the SSH-port check |
| `iplimit_shell_test.go` | deleting fail2ban config p-ui does not own; PyPI in the installer; env loaded after the paths that read it |

`default_boolean_test.go` is worth singling out: it derives its model set **from
the struct tags**, not from a list, and immediately found two fields a
hand-written grep had missed. It cannot verify call sites — proving a given
`Create` is safe needs type information these AST tests do not have — so it
demands the checkable next-best thing, a named regression test per model. That
limitation is stated in its own comment, and stating it is the right design.

---

## 2. Method, and why it produced different answers than reading would

Three rules shaped every fix. All three changed outcomes.

**Verify before fixing.** The audit is static analysis and was never executed.
`F234` was **refuted** by three clean REALITY key rotations on a real box. Two
of `F351`'s prescribed fixes were wrong in *opposite* directions. `F085`'s
diagnosis named the wrong errno. Every correction is filed in
`RUNTIME-RESULTS-2026-08-21.md` — 23 sections, each a measurement rather than an
argument.

**Write the test, revert the fix, watch it go red.** This caught real mistakes: a
`Select("*")` that measurably does *not* fix L9 (GORM skips default-tagged
fields at the schema level and writes the default back into the struct, so a
post-insert repair reads `true` and does nothing); an F063 layer that was too
broad and turned an egress-id test red; a toggle test that passed either way.

**Fix size matches bug size.** Where a fix genuinely needed new architecture —
F136's server-side link endpoint — it was described and agreed before it was
built, and the WireGuard path was deliberately left local, because a kernel peer
is not host-fronted and inventing an endpoint for it would be new surface for a
case with no defect.

**What measurement was worth, concretely.** The AWG padding boundary; the exact
set of cleared header values; `ip route get` proving a marked packet with no
rule takes the server's own route byte-for-byte; LDAP paging against a directory
that actually enforces a page limit; ARM v7a SIGILLing under qemu on an emulated
ARMv5; `fail2ban-client -h` returning 0 with the daemon stopped; `nft 1.0.2`
failing to *parse* `destroy`. None are derivable from source. Four changed the fix.

---

## 3. Discoveries ledger — dispositions

Twelve entries. **Two closed already, four to fix, four to close with reason,
two held behind a decision.**

| # | What | Disposition | Size |
|---|---|---|---|
| L1 | `PruneInbound` leaves a rule claiming `selected` scope while naming nothing | **fix** | ~30 lines, after one decision |
| L2 | `TestConfig` cannot pre-flight a staged binary before the rename | **fix** | ~15 lines + 1 test |
| L3 | A missing AWG device is indistinguishable from a refused parameter set | **fix** | ~10 lines, 1 netlink call per pass |
| L4 | `destResolves`' comment described behaviour the pinned core lost | **closed** | done inside F075 |
| L5 | Two staticcheck deprecations | **close** | operator's call; not lint silence |
| L6 | `KeySelfLoop` is declared and emitted nowhere | **fix, probe first** | probe ~1h, then delete or build |
| L7 | The dead-keys test cannot tell a live key from its own declaration | **fix, narrow version** | ~1 line + triage of new failures |
| L8 | Nothing reads the compiler's diagnostics at generate time | **keep open** | blocked on a design choice |
| L9 | A boolean defaulting to true cannot be created false | **closed** | fixed in `a493f4b9` |
| L10 | The same mechanism on every non-boolean defaulted column | **close** | triaged; nothing reachable |
| L11 | The LDAP guard has no operator-visible surface | **keep open** | already filed in `feature-gaps.md` |
| L12 | 15 tests fail on the maintainer's Windows box | **fix** | ~20 lines, one chore commit |

### The fix-now entries

**L1 — a self-contradictory routing row.** Deleting an inbound leaves a rule with
`ingress_scope='selected'` and `ingress_ids='[]'`. After F048 the toggle fails
*visibly* instead of silently widening to `all`, so the danger is gone — but the
row is still nonsense and an operator hitting it gets a refusal with no way to
repair it from the UI. **The decision is the work:** either delete the rule
outright and say so in the response, or introduce a third scope value meaning
"orphaned". Pick one and the code is small.

**L2 — the staged binary tested only after it is renamed.** F280's residual: a
downloaded Xray that verifies but cannot load the current config is refused with
the old core still serving (good) — but the new binary has already been renamed
into place and stays there, unused, refusing every later restart. Loud and safe,
yet the panel carries a binary it will not run. Extract
`testConfigWith(binary, cfg)`; call it on the temp path before the rename.

**L3 — EINVAL means two things.** The AmneziaWG module answers a missing device
with the same errno it uses for a refused parameter set, so `applyParams`
reports both identically. Ask the plane whether the link exists first. One extra
netlink round trip per AWG inbound per 10s pass — which is why it did not ride
along with F266.

**L6 / L7 — the two halves of one hole.** `KeySelfLoop` has a constant and no
emit site, and it survived from `e30b1841` because the dead-keys test tokenises
source and treats a Go constant *declaration* as a use. Do L7's narrow version
first (exclude declaration blocks; a one-line glob would have caught it), then
probe whether a self-loop is even constructible through the panel's forms —
`Plan` is pure and its harness builds subjects and rules directly. If it is
unreachable, delete the key; if reachable, the detector is its own finding.

**L12 — the Windows noise, decided.** Fifteen tests across five packages fail on
the maintainer's development machine: SIGTERM handling, `#!/bin/sh` stand-ins,
file modes, atomic rename. All pass on Linux; all are Linux-*target* tests in a
project whose `CLAUDE.md` declares Linux-only.

**The harm is not the failures — it is that a maintainer seeing 15 red on every
local run learns to ignore red.** That is precisely how the mid-campaign
`&&`-chain incident happened (a commit landed while a panic was on screen), and
why gate-on-exit-code became a standing rule afterwards.

*Fix:* give each an explicit `runtime.GOOS != "linux"` skip with a stated
reason, so a clean local run means something. ~20 lines, one chore commit.
Leaving it ambient is the option that trains the wrong reflex.

### The two held behind a decision

**L8 — generate-time diagnostics have no reader.** F049 gave
`Compiled.Refusals()` its first production caller at *save* time. Generate time
is untouched, so a rule that was routable when saved and went inert later — its
inbound disabled, its exit no longer resolving, the template edited underneath
it — refuses into a log line nobody watches.

`KeyNoFront` is the concrete casualty: it can *only* fire at generate time, so
its translated string was deliberately not shipped. A string with no reader is
L7's disease, and adding one while writing up L7 would be absurd.

**Blocked on a design choice, not effort.** F266's warning field rides an HTTP
save response; generation happens on restart, where there is none. The real
options are a status field on the existing routing list response (smallest), a
preview endpoint (the full four-step endpoint dance), or a persisted per-rule
health column. **Choose before writing any of it.**

**L11 — the LDAP guard is invisible, and that was already filed.** F367 added a
guard that refuses to delete and says so *only in the log*, plus a baseline that
lives in memory and resets on restart. Both correct; neither visible. Two of the
114 entries in `feature-gaps.md` already cover it; what L11 adds is a reason to
raise their priority. **The blocking decision:** does the retention baseline
become persistent as part of building that status surface, or before it?
Persisting it removes the documented escape path (restart, let one sync record
the new size). They are the same decision and must be made together.

---

## 4. The Medium table — three root causes

187 Medium findings. Ranked individually they are a list nobody will finish.
Clustered, **three causes account for roughly a third of them**, and each
cluster has one fix-shape that makes its members mechanical rather than
investigative.

### First: nine of them are already closed, and one report was wrong about it

The Medium pool lives in a separate appendix and was read at campaign end, not
at the start. That produced one error worth recording plainly.

**Correction.** `5c5d976d`'s commit body and §18 of `RUNTIME-RESULTS` both say
the Go per-client link endpoint ignoring hosts was "a defect the audit did not
name". **That is wrong. It is F137**, filed as a Medium under *Hosts*, naming
the same endpoint in the same words. The fix is right and the measurement
stands; the claim of novelty does not. The lesson is narrow and real: a High
finding's appendix twin can be sitting in a file you have not opened.

These nine Mediums were closed by this campaign's High-severity work and should
**not** be re-triaged as open:

| ID | Closed by |
|---|---|
| F046, F170 | L9 — a row created with its switch off is stored off (`a493f4b9`) |
| F051 | F075 — rules repaired when their outbound is renamed or deleted (`7e5a8849`) |
| F053, F074 | rules with no outbound refused instead of emitting an empty tag (`31377179`) |
| F137 | F136's server half — per-client links render the inbound's hosts (`5c5d976d`) |
| F258 | F085 — one bad AWG inbound no longer strips every sibling's obfuscation (`d31ba132`) |
| F301 | H2 — the probe asks the daemon about the jail (`dc0c1eb9`) |
| F303 | H4 — `p-ui.sh` loads the panel environment before deriving paths (`dc0c1eb9`) |

F062 (*"every compile refusal is silent"*) is **half** closed: F049 gave save-time
refusals a reader and a locale string; generate time is still unread and is
L8. Do not mark it done.

This is nine verified by reading the finding against the diff, not an exhaustive
re-audit of all 187. Assume a few more are closed and check before starting one.

### Cause 1 — one behaviour, three implementations, no shared contract

Share links and client configs are generated **three times** in this repo: the
frontend TS builder (`frontend/src/lib/xray/`), the Go subscription renderer
(`internal/sub/`), and a third copy in the docs site (`docs/lib/xray/`).
`CLAUDE.md` warns about the third; nothing enforces agreement between any of them.

**Members (14):** F019, F020, F021, F022, F023, F081, F102, F126, F129, F138,
F139, F143, F187, F214, F269. *(F137, the Go per-client endpoint ignoring host
overrides, is already fixed — see the correction below.)*

Every one is the same sentence with different nouns: renderer A emits a field
renderer B omits. AmneziaWG H1–H4 and timer ranges are missing from the panel's
own `.conf`; `presharedkey`/`keepalive`/`dns` are missing from the panel's
WireGuard link; mKCP `headerType`/`seed` are missing from the share link;
Hysteria passwords are interpolated raw so `/ # ? @` corrupt the URL.

**Fix-shape:** *one* golden-fixture corpus — a set of inbound rows with expected
output per renderer — and a test in each implementation that renders the corpus
and compares. The 14 fixes then become "add the missing field until the fixture
passes", which is mechanical and reviewable. The corpus is the work; the fixes
are not.

**Cost:** corpus + three harnesses ≈ 1–2 days. Then the members are hours, not
days, and — the actual return — **the fourth renderer somebody adds next year is
caught on its first commit.**

### Cause 2 — validation exists, but not on the path the data takes

A schema or validator is written, and the write path does not go through it.
Findings in this cluster read as absurd until you see the pattern.

**Members (13):** F031, F312 (import bypasses the request validator entirely —
port range and protocol enum unenforced), F193 (host validation declared in Zod,
never attached to the fields), F267, F104, F171, F190 (AWG parameters validated
only at the kernel, never at the API boundary or the form), F172, F174 (secrets
and subIds accepting characters that break the URL downstream), F328, F326,
F324, F325 (values the core rejects outright, accepted by the form), F009 (port
0 accepted, binds a random ephemeral port), F353.

**Fix-shape:** make the request validator the *only* door. Two concrete steps:
route `importInbound` through the same `BindAndValidate` path every other write
uses, and call `awg.Params.Validate()` at the API boundary instead of only at
device-apply. Then attach the schemas that were written and never wired.

**Cost:** the two structural steps are ~half a day each and cover the widest
blast radius (import bypassing *every* `validate:` tag is the single worst one).
The remaining per-field attachments are an afternoon.

### Cause 3 — the cron path talks to Xray directly

`CLAUDE.md` states the rule plainly: every state-changing inbound/client op
dispatches through `runtime.Runtime`, never straight to `internal/xray/api.go`,
never from a controller or cron job. **Seven findings violate it, all on the
traffic/depletion path, and nothing in the build notices.**

**Members (7):** F044, F086, F110, F121, F240, F247, F339.

The rule's own comment says why it matters: a direct call **passes every local
test and silently breaks every multi-node deployment**. This is the cluster with
the widest gap between "looks fine on the maintainer's box" and "is broken for
the customer" — the same shape as the four Criticals this campaign opened with.

**Fix-shape:** an `internal/arch` guard that fails when any file under
`internal/web/job/` or a `*_disable.go` / `*_traffic.go` service file imports
`internal/xray` — the dispatch ratchet already in that package is the template —
then convert the seven call sites.

**Cost:** guard ~30 lines; seven call sites are mechanical because
`runtime.Runtime` already exposes the operations. Half a day, and it is the
highest ratio of prevented-harm to effort in the whole Medium table.

---

## 5. Called out individually — data destruction and access control

Per the campaign's standing rule, these are named one by one even where they
cluster, because clustering an access-control defect hides it.

### Access control

| ID | What it means in one sentence |
|---|---|
| **F317** | A mixed inbound with `accounts` but no `settings.auth` is an **open proxy**, while the panel displays credentials that are not being enforced. |
| **F208** | 2FA enabled with an empty token makes the second factor a **publicly computable constant** — worse than no 2FA, because it reads as protection. |
| **F375** | LDAP fallback grants **full panel-admin login** with no group or flag check on the directory account. |
| **F207** | The subscription server trusts `X-Forwarded-*` from **any source IP** at the shipped `trustedProxyCIDRs` default. |
| **F205** | `getRemoteIp` takes the **leftmost** `X-Forwarded-For` entry, so the login rate limiter is spoofable behind a proxy. |
| **F206** | The login rate limiter is **not atomic** across the credential check, so a concurrent burst bypasses the 5-attempt cap. |
| **F288** | A test **asserts the callback auth bypass is correct behaviour** — the poisoned-test shape, where the suite defends the bug. |
| **F293** | In a Telegram group chat, **any member** can fill in the admin's in-progress client fields. |
| **F033** | Inbound tags are unvalidated and unreserved, so an inbound tagged `panel-egress` **silently disables the panel's own egress**. |
| **F306** | The IP cap is gated on `hosted[0].Enable` — an arbitrary first inbound — so a client active on a *second, enabled* inbound escapes its cap entirely. |

**F306 is still open in code this campaign touched.** C2 restructured that gate
and deliberately kept `hosted[0].Enable`; `policy.go:497` still reads
`if limit <= 0 || !hosted[0].Enable`. A cap that silently does not apply is
access-control-shaped, and it sits in a function the next person will already be
reading.

F317 and F208 are the two I would take first, and I would take them ahead of
every cluster above. Both are the same species: **a control that appears to be
protecting something and is not.**

F288 deserves its own note. A test that pins the bug is worse than no test,
because it converts the next person's correct instinct into a "regression". Fix
the bug and the test together, or the fix will be reverted by someone reading
the suite as the specification.

### Data destruction and lost writes

| ID | What it means in one sentence |
|---|---|
| **F381** | The depleted-client cron rewrites `inbounds.settings` **without `lockInbound`**, so a concurrent add can revert a quota disable — a re-enabled client that should be off. |
| **F109** | A failed commit of the traffic transaction is **swallowed**; `AddTraffic` returns success and the deltas are gone. |
| **F108 / F246** | `defer` LIFO ordering runs `resetCoreQuotas`' HTTP calls **inside** the open traffic transaction, against the comment claiming otherwise. |
| **F111** | Outbound traffic accumulates via read-modify-write **outside the serial writer**, so a reset can be silently undone. |
| **F241** | Deleting an inbound leaves `client_credentials` and `inbound_fallbacks` rows that a **recycled inbound id adopts** — credentials crossing between unrelated inbounds. |
| **F158** | `migrateRoutingIntent` has **no history gate**, so it re-consumes template routing rules on every run. |
| **F274 / F275** | DB restore swaps the package-level handle while 18 cron jobs call `GetDB()` unsynchronized; a failed `InitDB` afterwards leaves a dead handle and hides the real error. |
| **F272** | `pgConnEnv` drops every DSN parameter except `sslmode`, so Back Up / Import DB **break on the DSN the panel booted on**. |

F241 belongs in both tables: a recycled inbound id adopting a dead inbound's
credential rows is data destruction *and* an access-control crossover.

### Lockout

**F394** (a wrong `webDomain` 403s the whole panel after restart, with no
`p-ui setting` flag to repair it) and **F224** (a subscription-server bind
failure `log.Fatal`s the entire panel, with no CLI way back) are the same shape
as the firewall and web-port fixes this campaign already landed — an operator
locked out of their own box by a recoverable misconfiguration. They should be
taken with that family, not with the Mediums.

---

## 6. The verdict

> **Status, updated 2026-08-26 as campaign 2 works through this list.** All six
> items are closed. The ordering survived contact — nothing in it was re-ranked —
> but **two of §4's explanations did not**: `RUNTIME-RESULTS` §23 found Cause 3's
> seven-finding cluster misdiagnosed (multi-CORE, not multi-node, and understated),
> and §24 found the ground for deferring F138 simply false. In both cases the
> finding was right and the reasoning about it was not.
>
> | Item | State |
> |---|---|
> | 1 — access control (10) | **closed** |
> | 2 — `runtime.Runtime` bypass (7) | **closed**, with §23's correction |
> | 3 — data destruction (10) | **closed** |
> | 4 — validation off the path | **closed 2026-08-27**, later than this row first claimed — the import half was recorded closed while `importInbound` still bare-unmarshalled; see `RUNTIME-RESULTS` §27 |
> | 5 — lockout family | **closed** — F394, F224, plus the `p-ui setting` escapes |
> | 6 — renderer corpus (15) | **closed** — F023, F129/F143, F138, F139, F126/F214, F081, and the corpus itself |
>
> **What item 6 did NOT close, deliberately:** the corpus is Tier A only — two
> harnesses over inbound rows. `docs/lib/xray` turned out to be a URL assembler
> rather than a third renderer of an inbound, so it needs a second tier keyed on
> flattened inputs; ledger L19 carries the design and the `ci.yml` wiring it will
> need. Three further open questions came out of the same work: L17 (REALITY sni
> and sid randomised per fetch), L18 (a Host's Final Mask dropped from the
> Hysteria salamander block), L20 and L21.

**Take, in this order:**

1. **The ten access-control call-outs**, F317 and F208 first. Highest harm,
   individually small, and none of them benefits from clustering.
2. **Cause 3, the `runtime.Runtime` bypass** (7 findings). One arch guard plus
   seven mechanical call sites, half a day, and it closes the gap between "green
   locally" and "broken on every multi-node deployment".
3. **The eight data-destruction findings.** F381, F109 and F111 are lost writes
   in the accounting path — the numbers customers are billed on.
4. **Cause 2's two structural steps** — route import through the validator, move
   AWG validation to the API boundary. Not the whole cluster; just the two that
   carry the blast radius.
5. **F394 and F224**, with the lockout family.
6. **Cause 1's golden-fixture corpus** — 1–2 days, and it is the only item here
   that pays forward rather than just fixing what exists.

**Leave, with the reason on the record:**

The remaining ~140 Mediums are predominantly single-protocol cosmetics
(a missing link parameter for one transport), feature-gap-shaped items that
belong in `feature-gaps.md` rather than a defect list, and UI-lossiness findings
(F194, F188, F191) that are real irritations but destroy nothing and lock nobody
out. **They do not repay a campaign.** They repay being fixed by whoever is next
in that file for another reason — which is the disposition, not a deferral: the
golden-fixture corpus from Cause 1 turns most of the link-related ones into
one-line changes with a test already waiting.

The 142 Lows were not triaged in this campaign and are not recommended for one.

**In one sentence:** take the access-control list and Cause 3 now, the data
integrity findings next, and treat the rest of the Medium table as a reference
to consult when you are already in the file — not as a backlog.

---

## 7. Honest limits of this campaign

- **64 audit findings were cited across those 69 commits**, plus both Criticals and
  all eight Highs from `IP-LIMIT-ACTUATOR-RESEARCH` — including H5's rider, the
  false `BounceClient` doc comment and the test that pinned the wrong semantic as
  if it had been verified. The Criticals and the Highs carrying outage or lockout
  risk were the target; the Medium and Low pools were triaged, not fixed — though
  nine Mediums fell out as duplicates of High fixes (§4).
- **Everything was verified on one box** — the penhoon VM, Ubuntu 24.04. Ubuntu
  22.04, 26.04 and Debian 12 were reasoned about and, for the `nft` question,
  probed by running the older binary; they were not otherwise exercised.
- **Production was never touched**, by standing instruction. Where a check
  needed prod (§8 item 8's `ignoreip` audit), it is recorded as unanswered
  rather than assumed.
- **The §8 runtime checklist is 5 of 8 closed**, and says so in its own header
  so nobody later reads it as discharged. See §22 of
  `RUNTIME-RESULTS-2026-08-21.md`.
- **The frontend suite passed before the F136 test existed**, meaning nothing had
  ever exercised link rendering in those modals. Other surfaces very likely have
  the same shape of hole; a passing suite is not evidence of coverage.

## Where things live

| File | What it holds |
|---|---|
| `AUDIT-2026-08-20.md` | The original audit: Criticals, Highs, prioritised order |
| `IP-LIMIT-ACTUATOR-RESEARCH.md` | The fail2ban actuator study, its 2 Criticals, the §7 step order and §8 checklist |
| `audit-appendix/RUNTIME-RESULTS-2026-08-21.md` | 24 sections of measurement, plus every finding-correction and the one refutation |
| `audit-appendix/DISCOVERIES-LEDGER.md` | L1–L21 in full, with the reasoning this document summarises |
| `audit-appendix/findings-medium.md` | The 187 Mediums, grouped by area |
| `audit-appendix/feature-gaps.md` | 114 gaps in shipped features, with suggested shapes |
| `internal/arch/` | The eight guards. Read these first when a fix "stops working" |
| `testdata/sharelink/` | The share-link golden corpus, read by a harness in each renderer |
| `CHANGELOG.md` | Everything operator-visible this campaign changed |
