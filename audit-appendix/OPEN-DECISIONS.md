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

## 9. A first-class "this kind serves no clients" declaration (F314's leftover)

F314 is fixed as error classification: the panel now recognises Xray's "proxy is
not a UserManager" as a refusal no restart repairs, so a depleted legacy client
on an http/mixed/tunnel/tun inbound stops knocking over every live connection on
the box.

What it does NOT do is stop the panel from asking. `runtime.Local.RemoveUser`
and `AddUser` still round-trip to Xray for a kind that can hold no user, and
learn the answer from a string.

The contract already knows: `ClientCredentials(kind)` returns nil for exactly
those four kinds, and the six kinds it names are exactly the six proxies that
implement `proxy.UserManager` in the pinned core. But `CredentialDeclarer`'s
documented meaning for nil is "declares nothing, keep the form as it was" — not
"has no users" — so the xray core may read its own nil that way (F313 does) and
the runtime may not. Honouring it in the runtime means either changing that
meaning or adding a declaration, which is new architecture next to a low finding.

**The question:** worth a declaration, or leave it? Cheap either way. A
declaration would let AddUser and RemoveUser refuse locally instead of asking,
and would give the client form a reason for greying those kinds out that is not
a protocol branch. Leaving it costs one string match that a reworded upstream
error would break — which the test pins, so it breaks loudly.

## 10. The legacy inbound-level allowInsecure (F025's residual)

A Hysteria inbound can carry `tlsSettings.settings.allowInsecure`, and exactly
one renderer honours it: the Clash proxy's `skip-cert-verify`. The raw link does
not emit it, the frontend's link generator does not (its own comment says the
field is not on the schema), `streamData` drops it before every other protocol
sees it, and xray-core replaced it with `verifyPeerCertByName`. The panel's form
cannot set it.

So the only rows that carry it are ones an older build wrote, and for those the
two subscription formats disagree: Clash skips certificate verification, the raw
link does not.

Setting it **per host** is the supported way and reaches both formats — that half
of F025 is fixed and shipped.

**The question:** drop the Clash arm, or keep it? Dropping makes the panel
consistent with every decision it has already made about this key, and turns
certificate verification back on for any legacy row that has it — which breaks a
self-signed deployment that has been working, silently, in the direction that
looks like a network fault. Keeping it leaves one renderer honouring a field
nothing else does. Neither is reinterpreting the operator's data quietly, which
is why this is here rather than decided.

## Answered, 2026-08-31 (the full round)

Every open question was put to the operator and answered. Nothing on this page
is waiting on them any more; what is left is work.

- **Q1 — SHIPPED. A save-time refusal fires only when the edit WORSENS the
  violation.** Written once as `refuseOnlyIfWorsened` rather than decided per
  refusal. Every finding it gated is now closed:

  | finding | state |
  | --- | --- |
  | **F316** | fixed — a tunnel inbound with no destination dials itself; refused, and Q1's first user |
  | **F067** | fixed — the host-address check now runs on edit too, worsens-only |
  | **F007** | fixed — the template save asks the core itself, not just the outbounds array |
  | **F035** | fixed — the mtproto bridge port is checked against the panel's own inbounds |
  | **F216** | already fixed — injectMtprotoEgress scans for the collision and skips |
  | **F045** | already fixed — and it had ALREADY implemented Q1's rule ("only an edit that CHANGES the identity is checked") |
  | **F373** | already fixed — scheduleLdapSync falls back to the default spec and logs loudly |

  One deliberate exception: `refuseUnenforcedAccounts` is NOT worsens-only.
  Forcing the fix on operator contact is that guard's whole repair path, its own
  test says so, and applying Q1 there made that test fail. Ledger L32.
- **Q2 — SHIPPED. LDAP may disable, and has a provenance column.** The disable
  direction needed no code: it was already panel-wide, and the answer ratified
  that rather than narrowing it — the comment says so now. **F369** closed.

  The sweep got the limit instead. `clients.origin` records what created a row,
  stamped by the LDAP job itself (SyncInbound serves every caller, so stamping
  there would tag admin-made rows too) and read by the sweep, which now fails
  CLOSED: a question it cannot answer authorises no detach. **F372** closed.

  Two things worth knowing. Adoption was declined, so there is NO backfill:
  every pre-existing row is grandfathered as hand-made forever, which the
  changelog states plainly. And the finding says "delete" where the code says
  DETACH — the sweep unlinks from the inbound and never removes the clients row;
  the account survives and loses its access.
- **Q10 — DEFERRED to the node-mode refactor, on the operator's call
  (2026-08-31).** The answer stands — converge, the panel adopts the node's tag —
  but building it now would be plumbing a fix through a subsystem due to be
  rewritten, and it would have to be redone.

  The deferral is cheap, which is why it is the right call. F182 is a LOW
  finding whose current behaviour is a documented decision with a test stating
  its reason: a node used standalone may already own the tag the central panel
  picked, so the regeneration lets the inbound land and the central picks the
  resolved tag up from the AddInbound response. The finding's own mechanism is
  wrong on one point too — there is no free-text tag field in the inbound form;
  a tag is auto-composed, and an explicit one can only arrive through the JSON
  editor or the REST API, both of which get the resolved tag back. The case
  where silent replacement WOULD be wrong — a reserved tag — already returns an
  error.

  So the live harm is a stale fingerprint on a diff, not a broken inbound.
  **Carry it into the refactor's scope** rather than closing it: tag ownership
  between central and node is exactly the kind of thing that rewrite has to
  settle, and settling it there is one decision instead of two.
- **F319 — a field on `core.Transports`** (`None` / `BindsNoPort`), read by the
  port-conflict check that already asks the question. Four small sites, and a
  core still cannot disable the check by answering empty. The last open tier-2
  finding.
- **F141 — SHIPPED. The research changed the question, and the operator picked
  the answer it pointed at.** Neither field works as a per-host switch:
  `shuffleHost`'s analogue (mihomo's `shuffle-proxies-order`) is a proxy-GROUP
  key, and order belongs to the list rather than to one member of it; and
  `mihomoX25519`'s documented meaning is "include the derived X25519 public key
  in the Mihomo output", which the Clash renderer already does unconditionally,
  so a gate for it could only ever turn correct output off.

  So the capability was built where it actually lives — a subscription setting,
  `subClashShuffleProxies` — and both columns retire through a real deprecation:
  still stored, still returned, marked `deprecated` in the published schema
  (openapigen now reads Go's own `Deprecated:` doc convention), removal named for
  the release after v1.1.0.
- **The installer's custom-certificate path — SHIPPED, re-prompt as chosen.**
  And the root cause was one layer down: `p-ui cert` stored two paths without
  opening them and returned normally even on a failed write, so the status the
  installer was told to check did not exist. It loads the pair now and exits
  non-zero, which is what makes the re-prompt possible at all. Both `install.sh`
  and `update.sh` carried the case; both are fixed and both are guarded.
- **§9 — add the declaration, and make it a real one.** The operator asked for an
  advanced solution rather than the pinned string match. A first-class
  "this kind serves no clients" answer in `internal/core`, honoured by
  `runtime.Local` so AddUser/RemoveUser refuse locally instead of round-tripping,
  and surfaced so the client form has a reason to grey those kinds out that is
  not a protocol branch.
- **§10 — keep the Clash arm.** Dropping it would turn certificate verification
  back on for a legacy row and break a working self-signed deployment silently.
  F025 is closed as it stands.
- **Bulk operations — add SET beside ADD, behind an explicit mode toggle.** ADD
  stays the default so existing muscle memory is unchanged; the confirmation says
  which mode is about to run and on how many clients.
- **mtg-proxy v1.0.0 — DONE, the release is deleted.** It carried the remotely
  triggerable process kill v1.0.1 closed. Nothing pinned it: `release.yml`
  resolves `/releases/latest`, and the release had 11 assets and zero downloads
  in the three minutes before v1.0.1 replaced it. The git tag is kept — it is the
  history, and a tag ships no binaries.
- **The test box's GAME inbound — leave it on 39412.**

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

Nothing is still open: every item below is answered in the block at the end.
