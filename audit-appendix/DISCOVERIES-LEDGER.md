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

---

## L1 — `PruneInbound` rewrites a rule's ids and enable flag but never its scope

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

## L6 — `KeySelfLoop` is declared and emitted nowhere

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
