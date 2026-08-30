# Open decisions — 2026-08-30

Everything the remediation campaign has reached that is **not mine to settle**,
in one place. Each entry says what the question is, what it costs either way,
and what it unblocks. Nothing here is a defect left unfixed by oversight; every
one is a point where the code cannot tell me what the panel is supposed to do.

Answer them in any order. Where an answer unblocks findings, the IDs are named
so the work can start the moment it lands.

---

## 1. The tier-1 batch: sixteen questions, already written up

`TIER1-WORK-ORDER-2026-08-27.md` §B holds Q1–Q16 in full, with the trade-off and
the finding IDs each one releases. They are not repeated here — that document is
the authority. The short version of what is still blocked by them:

| question | what it settles | still blocked |
| --- | --- | --- |
| **Q1** | whether a new save-time refusal applies to rows that already violate it | **F316** |
| **Q2** | how far LDAP's authority reaches (disable, delete, adoption) | **F372** |
| **Q10** | what happens when a node renames a pushed tag | **F182** |

The other thirteen were answered implicitly by shipping the narrow half of each
finding; they stay listed because the wider half is still available to take.

## 2. Clearing a WireGuard pre-shared key (F179 — ledger L27)

A pre-shared key can be set in the client editor and never removed. Three layers
read empty as "the caller did not send this", and the middle one drops the field
from the JSON entirely, so there is no way to say "cleared" today.

- **An additive flag** — `clearPreSharedKey` on the client payload. Rides the
  existing marshal to the apply layer; roughly five small sites. Costs a new
  field in the public API and its generated types.
- **A pointer field** — `PreSharedKey *string`, nil = omitted, `&""` = cleared.
  No new API surface, correct by construction, touches ~15 sites.

`keepAlive` has the identical disease and should take whichever signal is chosen.

## 3. A tun inbound's port (F319 — ledger L28)

An Xray TUN device binds no socket, but a tun inbound is offered in the form,
requires a port, and is counted as holding it — so the panel refuses a real
inbound on that port. The one-line arm does not work: `inboundTransports`
deliberately turns an empty answer back into TCP so a core cannot disable the
port-conflict check for its own kind.

- **A field on `core.Transports`** (`None` / `BindsNoPort`) — four small sites.
- **A capability rule** — heavier, but it lives in the table that already
  answers "may this inbound do X", and the TypeScript twin gets it free.

Either widens the multi-core contract, which is why it is here.

## 3b. Two host fields nothing reads (F141 — ledger L29)

`MihomoX25519` and `ShuffleHost` are stored, round-tripped and shown in the
OpenAPI examples, and no renderer anywhere reads either one. The two switches
are gone from the form — a control that does nothing reads as a setting that
failed — and the columns, the API fields and any stored value are untouched.

- **Implement them.** Neither has an obvious Clash/Mihomo counterpart and no
  hint in either locale says what they meant, so someone has to decide first.
- **Drop the columns.** A migration; the API fields go with them.
- **Leave it.** Two booleans, and the panel no longer claims they do anything.

## 4. The installer's custom-certificate path (ledger L26)

Case 3 of `prompt_and_setup_ssl` discards `p-ui cert`'s exit status and prints a
green tick regardless, so a certificate the panel rejects still raises the
scheme to https. Cases 1 and 2 branch on their issuer's status; only this one
does not. Fixing it means deciding what the installer does when the operator's
own files are rejected mid-install: **re-prompt for paths**, **fall back to the
skip-SSL branch**, or **abort**.

## 5. Bulk operations: ADD or SET

The bulk-adjust dialog adds days and gigabytes to what a client already has.
Whether it should also offer SET semantics — "make it exactly this" — is a
product question, not a repair. It changes what an operator's muscle memory
does, so it is not something to slip in beside a fix.

## 6. The mtg-proxy fork: steps 2–11

Step 1 shipped as v1.0.1 (it closed a remotely triggerable process kill). The
remaining transport work is planned but unstarted. Separately: **v1.0.0 is
superseded but still downloadable** — delete that release, or leave it?

## 7. Feature gaps

`feature-gaps.md` holds 18 entries. These are things the panel does not do
rather than things it does wrong, and the standing instruction is that they come
LAST. Listed here only so the queue is complete.

## 8. One live change on a test box

The GAME inbound on 91.107.151.123 is on port 39412; it was 50120 before a test
moved it. Left for the operator to undo or keep.

---

## Answered, 2026-08-31

- **F249 — the operator chose: keep serving the links, and say it on the page.**
  My first fix withheld a disabled client's link, which made an all-disabled
  subscription answer 404. Reverted. The body is unchanged for every client
  whatever its state, so it keeps parsing in every app; the subscription page
  now dims that entry, strikes the name through and tags it "Ended". Better
  reasoning than mine had: a subscription that suddenly serves nothing reads as
  a broken URL rather than as an ended account.
- **F179 — a pointer field.** `PreSharedKey *string`: nil is omitted, a pointer
  to empty is cleared. No new API surface, correct by construction, ~15 sites.
  `keepAlive` takes the same treatment.
- **The multi-core group (F060, F089, F120, F302, F314) — extend the contract.**
  An optional Sessions capability in `internal/core`; cores that can report
  sessions implement it, cores that cannot declare so, and the panel stops
  offering limitIp and online status for kinds that cannot back them.
- **Order of work — the decisions above come before tier 3.**

Still open: items 3b (F141) and 3 (F319) below, plus 4 through 8.
