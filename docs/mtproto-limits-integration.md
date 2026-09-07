# Panel-Side Support for mtg-proxy v1.8.0 Per-User Governance

> **Status.** Superseded the same day: the work this brief describes landed, with the fork changed as well, and §12.5p of `docs/multi-core-architecture.md` records which of the ten decisions below were taken and how. This file stays as the analysis of record. It was produced 2026-09-07 against mtg-proxy `e58027c` (tag `v1.8.0`) and Penhoon Panel `a333f3c`. No code was changed in either repository; this file is untracked in the panel repo at `docs/mtproto-limits-integration.md`. It was assembled by parallel readers over both trees, with every claim re-checked against the source by adversarial passes; anything that could not be confirmed is marked UNVERIFIED inline. Read §1 (what blocks shipping) and §6 (the decisions only the user can make; Q8 is blocking) before touching code.

## 0. What this document is

mtg-proxy v1.8.0 (the fork at `Arman2122/mtg-proxy`) now governs each MTProto secret independently along four axes: a **quota ceiling** that cuts the data path at the exact byte, an **expiry clock** that closes live streams within five seconds, a **per-user speed cap** enforced by one shared token bucket per secret, and a **per-user address limit** that refuses new connections from more than N distinct places, where a place is one IPv4 address or one IPv6 /64. It also exposes a per-secret read-back endpoint, a whole-set external-address heartbeat so a multi-node panel can make the address limit global rather than per-process, and a `features` array on `GET /stats` that names exactly which of these the running binary understands.

Penhoon Panel today uses two of the four: it renders and hot-applies `quota` and `expires` per secret (`internal/mtproto/manager.go:761-772`, `:824-848`), and it resets the sidecar quota counter when a client's panel-side traffic is reset (`internal/mtproto/manager.go:647-688`). It emits no `speed` and no `ip-limit`, it never reads `speed_limit`, `ip_limit`, `active_ips` or `connected_ips`, it never calls the external-ips endpoints, and the mtproto core declares neither `RateEnforcer` nor `SessionReporter` (`internal/cores/internal/mtproto/core.go:57-71`), which is why the panel's Speed and IP-limit UI treats mtproto as an unsupported kind. This document is the complete implementation brief for closing that gap.

---

## 1. State of the two repositories, and the one thing that gates shipping

### 1.1 mtg-proxy: tags exist, releases do not

| Fact | Value | Evidence |
| --- | --- | --- |
| mtg-proxy HEAD | `e58027c` | `git rev-parse HEAD` |
| HEAD's tag | `v1.8.0` | `git describe --tags --exact-match` |
| origin/main | `e58027c` (in sync) | `git rev-parse origin/main` |
| Tags on origin | v1.0.0 through v1.3.0, **v1.4.0, v1.4.1, v1.5.0, v1.6.0, v1.7.0, v1.8.0** | `git ls-remote --tags` |
| Published GitHub Releases | v1.0.1, v1.1.0, v1.1.1, v1.1.2, v1.1.3, v1.2.0, v1.2.1, **v1.3.0 (marked Latest, 2026-09-04T19:34:42Z)** | `gh release list` |
| Releases for v1.4.0 and later | **none** | `gh release list` |

Tag to commit, derived from `git log <prev>..<tag>` for each tag rather than from memory. The "Adds" column is what that tag introduced, not a cumulative list. **The secrets API is older than any row here:** `git show v1.2.1:mtglib/proxy_api.go` already registers `PUT /secrets` (line 301) and `POST /secrets/{name}/reset-quota` (line 304), and `git show v1.2.1:internal/config/parse.go:18-22` already declares `quota`, `quota-reset`, `expires` and `disabled` on the `SecretLimits` struct. Everything the panel uses today predates v1.3.0.

| Tag | Commit | Adds (`git log <prev>..<tag>`) |
| --- | --- | --- |
| v1.3.0 | `f02c867` | the runtime view of a user on `GET /secrets` — `connections` and `last_seen` (`7509de5`); the web relay's own counters on its own socket (`00ee1d5`). The rest of the range is docs, tests and CI. |
| v1.4.0 | `5f2261f` | the `web-relay-watch` feature and `ad_tag_failed` counted apart from the skip (`14fbf10`); the relay follows its config file (`4247f1b`) |
| v1.4.1 | `194c668` | fixes and a `proxy.go` split only — no new surface |
| v1.5.0 | `a02122b` | **`GET /secrets/{name}`**, one commit, the whole tag |
| v1.6.0 | `e64456e` | the `speed` limit key and the `speed-limits` feature (`f3e3139`) |
| v1.7.0 | `d67bfb1` | the `ip-limit` key, the external-ips routes, and the `ip-limits` feature |
| v1.8.0 | `e58027c` | quota becomes a data-path ceiling, the 5s expiry-and-quota sweep, and unknown config keys named at startup and by `doctor` |

Two version facts matter to an implementer reading the wire: `GET /secrets/{name}` is **v1.5.0**, not v1.8.0, so a v1.5.0-or-later sidecar answers it; and unknown-key **reporting** is v1.8.0-only, which section 3.1 builds on.

### 1.2 The exact release-side dependency

The panel obtains the mtg-proxy binary by two independent paths, and **both resolve to a published GitHub Release, never to a tag**:

1. **Release build time.** `.github/workflows/release.yml:175-185` resolves the version by following the `/releases/latest` redirect on `Arman2122/mtg-proxy`, then downloads `mtg-proxy-${MTG_VER#v}-linux-<platform>.tar.gz`. It hard-fails when the redirect yields nothing. Today that redirect resolves **v1.3.0**.
2. **Runtime core installer.** `internal/cores/internal/mtproto/versions.go:74-106` lists versions from `https://api.github.com/repos/Arman2122/mtg-proxy/releases`, filters out prereleases, and `Install` refuses any version not on that list with `core.ErrUnknownVersion`. Asking it for `v1.8.0` today fails.

**Therefore: the panel ships and installs v1.3.0, a binary that predates `speed-limits` (v1.6.0) and `ip-limits` (v1.7.0), and predates the v1.8.0 quota ceiling.** No amount of panel work makes speed or address limits work until releases exist for the newer tags.

The release step itself is cheap, and the asset names line up on both sides by inspection:

- mtg builds `PKG="mtg-proxy-${version}-${{ matrix.platform }}"` (`mtg:.github/workflows/go.yml:99`), where `version` is `${GITHUB_REF_NAME#v}` (`:70-76`) and `matrix.platform` **already carries the `linux-` prefix** — `linux-amd64`, `linux-arm64`, `linux-armv7`, `linux-armv6`, `linux-386` (`:24-45`).
- The panel builds `mtg-proxy-<bare>-linux-<Deps.Platform>` at `internal/cores/internal/mtproto/versions.go:124`, with `Platform` being the bare `amd64` / `armv7` label. The two produce the same string.
- The checksums file is `mtg-proxy-${VERSION}-checksums.txt` (`mtg:.github/workflows/go.yml:184`) against the panel's `mtg-proxy-<bare>-checksums.txt` (`internal/cores/internal/mtproto/versions.go:126`). Same string again.
- The workflow's publish gate is `refs/tags/v*`, and none of this changed between v1.3.0 and v1.8.0.

**Nothing in the panel needs to change for the version bump itself.** The gate is purely "publish the releases".

### 1.3 A second, independent blocker: the GitHub account is invisible to anonymous clients

Verified on 2026-09-07 from two networks (this development host and the Hetzner test VM), every anonymous HTTPS request to the `Arman2122` account returns 404: the profile page `https://github.com/Arman2122`, both repositories `Arman2122/mtg-proxy` and `Arman2122/p-ui`, the `/releases/latest` redirect, `https://api.github.com/repos/Arman2122/mtg-proxy/releases`, and even `https://api.github.com/users/Arman2122`. A control request to `https://github.com/XTLS/Xray-core/releases/latest` from the same hosts answers 302 as normal. Authenticated `gh repo view Arman2122/mtg-proxy` reports the repository as `PUBLIC` and authenticated `gh release list` works, while `gh api users/Arman2122` still answers 404.

That pattern, every public surface of one account hidden from everyone but its owner while the owner's own tooling sees a public repository, is the signature of an account-level visibility flag on GitHub's side, not a network fault and not a repository setting. The cause is **UNVERIFIED** from the outside; the owner can confirm it by opening `github.com/Arman2122` in a logged-in browser and looking for a flag banner, and it is resolved through GitHub Support, not through any repository setting.

The blast radius is wider than this feature:

- Both mtg-proxy download paths in the panel are anonymous (§1.2), so they fail whatever releases exist.
- The panel's own `install.sh:1605-1644`, `update.sh:1141-1155` and `p-ui.sh:189-196` resolve `api.github.com/repos/Arman2122/p-ui/releases/latest` and download from `github.com/Arman2122/p-ui/releases/download/` anonymously. **New installs, updates and the menu's self-update of the panel itself fail while this holds.**
- `p-ui.sh:759` fetches the script itself from `github.com/Arman2122/p-ui/raw/main/p-ui.sh`, on the same host.

This must be cleared before publishing releases achieves anything, and it is the first thing to check on any box whose install or update starts failing.

### 1.4 What does not need the new release

One piece of the work below is version-independent and can land and ship against v1.3.0: **`SessionReporter`**. It needs `track-client-ips` plus the `connected_ips` array on `GET /stats`, and both are present in v1.3.0 — verified directly, not inferred from the feature list:

- `git show v1.3.0:internal/config/parse.go:53` declares `TrackClientIPs bool \`toml:"track-client-ips"\``.
- `git show v1.3.0:mtglib/proxy_stats.go:540` declares `ConnectedIPs []string \`json:"connected_ips,omitempty"\`` on the per-user stats object.
- `client-ips` is one of the seven features v1.3.0 advertises (§2.7).

Only the speed cap, the sidecar-side `ip-limit` pushdown, and external-ips require v1.6.0 or v1.7.0. Sequencing the work so `SessionReporter` lands first means panel-side IP limiting for mtproto becomes real before any release work completes.

---

## 2. What the sidecar now offers

All six per-secret keys live in one TOML table per secret and in one JSON object per secret over the API. The TOML key set is closed and defined at `mtg:internal/config/parse.go:22-29`; the JSON set is `limitFields` at `mtg:mtglib/proxy_api.go:629-636`.

Note the **three** naming skews, not one. TOML uses kebab-case and JSON uses snake_case; and the JSON key you *write* is not always the key you *read*, because `limitFields` (the request) and `SecretInfo` / `UserStatsJSON` (the responses) are different structs.

| TOML key (write) | JSON key on PUT/POST (write) | JSON key on GET (read) | Type in / out | Since | Zero value |
| --- | --- | --- | --- | --- | --- |
| `quota` | `quota` | `quota` | size string in / int64 out | ≤ v1.2.1 | absent or `""` means unlimited |
| `quota-reset` | `quota_reset` | `quota_reset` | string / string | ≤ v1.2.1 | absent means never |
| `speed` | `speed` | `speed_limit` | rate string in / int64 bytes-per-second out | v1.6.0 | absent or `""` means unlimited |
| `expires` | `expires` | **`expires_at`** | RFC3339-or-date string in / RFC3339 timestamp out | ≤ v1.2.1 | absent means never |
| `disabled` | `disabled` | `disabled` | bool / bool (`omitempty`, so `false` is absent) | ≤ v1.2.1 | `false` |
| `ip-limit` | `ip_limit` | `ip_limit` | integer / integer | v1.7.0 | `0` means unlimited |

The `expires` → `expires_at` skew is the one that bites: `limitFields.Expires` is tagged `json:"expires,omitempty"` (`mtg:mtglib/proxy_api.go:632`) while `SecretInfo.ExpiresAt` is tagged `json:"expires_at,omitempty"` (`mtg:mtglib/proxy_api.go:32`). A decoder written from the request struct silently reads zero.

### 2.1 Per-user speed limit

**Config surface.** A quoted string under `[secret-limits.<name>]`:

```toml
[secrets]
alice = "ee00000000000000000000000000000064756d6d792e636f6d"
bob   = "ee11111111111111111111111111111164756d6d792e636f6d"

[secret-limits.alice]
speed = "1250000"

[secret-limits.bob]
speed = "10Mbps"
```

**API surface.** A string field on the per-secret limits object (`mtg:mtglib/proxy_api.go:634`), parsed by the same function as the config file (`mtg:mtglib/proxy_api.go:660`, `mtg:internal/config/type_speed.go:17-23`), so **the grammar is identical on both surfaces**.

**Grammar.** `mtglib.ParseSpeed` (`mtg:mtglib/secret_limits.go:180-243`). A decimal number, optional whitespace, then an optional unit, with an optional `ps` or `/s` suffix that is stripped and ignored.

| Written | Means | Trap |
| --- | --- | --- |
| `"1250000"` | 1250000 bytes/s | a bare number is **bytes per second** |
| `"10Mbps"`, `"10mbit"`, `"10 Mbit/s"` | 1250000 bytes/s | bit units are **decimal** (10^6) |
| `"1MB/s"` | 1048576 bytes/s | byte units are **base-2** (2^20) |
| `"5MBps"` | **625000 bytes/s**, not 5 MiB/s | `MBps` matches the **bit** branch first and the `B` is consumed as part of `bps`. Write `5MB/s` when bytes are meant. |
| `"10Kb"` | **10240 bytes/s**, not 10 kbit | a bare `b` without `bit` or `bps` is a **base-2 byte** unit |
| `"10Kbit"` | 1250 bytes/s | explicit `bit` is the only unambiguous bit form |
| `"1TB/s"`, `"1Tbit"` | **rejected** | the speed unit maps stop at `g`/`gb`/`gib` (`mtg:mtglib/secret_limits.go:212`, `:221-223`); there is no tera form. Distinct from quota, which has `T`. |
| `""` | unlimited | |
| `"0"`, `"0Mbps"` | **rejected**, `speed must be positive (…); leave it out for no limit` | the zero guard at `mtg:mtglib/secret_limits.go:200` |
| `"-1"` | **rejected**, `incorrect speed "-1": want a number and a unit, like "5mbit" or "1MB/s"` | a **different** error and a different line. The digit scanner stops at the leading `-`, so `number == ""` and it fails at the ParseFloat guard, `mtg:mtglib/secret_limits.go:195-196`. It never reaches the zero guard. If the panel surfaces the sidecar's message, these two are not interchangeable. |
| `"1Kbps"` (125 B/s) | **rejected** | below the floor |

**Floor and ceiling.** `MinSpeedBytesPerSecond = 64_000 / 8` (64 kbit/s = 8000 B/s) at `mtg:mtglib/secret_limits.go:165`. A value below it is a **refusal, not a clamp** (`mtg:mtglib/secret_limits.go:238-239`). The ceiling refuses anything strictly greater than `1<<40` bytes/s; exactly `1<<40` is accepted (`mtg:mtglib/secret_limits.go:234-235`).

**Semantics.** One `golang.org/x/time/rate` limiter per secret, shared by every connection of that secret and by **both directions at once** (`mtg:mtglib/counting_conn.go:113` on read, `:135` on write; sharing across one user's connections is `TestSpeedLimitIsSharedByAUsersConnections` at `mtg:mtglib/speed_limit_test.go:137`, and the upload direction being paced too is `TestSpeedLimitHoldsUploadToo` at `:418`). It is **pacing only**: the limiter waits, it never drops bytes and never closes a connection (`mtg:mtglib/speed_limit.go:22-25`, `:87-95`). Burst is one second of the configured rate, capped at 64 MiB (`mtg:mtglib/speed_limit.go:11-14`, `:49-58`).

**Hot apply.** Changing `speed` through `PUT /secrets` swaps the limiter in place without touching connections (`mtg:mtglib/proxy_secrets.go:233-243`, called from `mtg:mtglib/proxy.go:256` and `mtg:mtglib/proxy_secrets.go:218`). Clearing the value **destroys the bucket**; setting it again creates a fresh one, which hands the user a full burst allowance immediately (`mtg:mtglib/speed_limit.go:43-58`). Rapid set, clear, set is therefore an exploitable pattern; the panel must not churn the field. Section 6's Q8 exists because the naive wiring of units 1 and 6 produces exactly that churn, twelve times a minute.

**Read back.** `speed_limit` appears on `/stats` users, on `GET /secrets`, and on `GET /secrets/{name}` as an **integer bytes per second, exactly as configured**. It is never a measured live rate. `throttled_ms` appears **only on `/stats`** (`mtg:mtglib/proxy_stats.go:624`). Correction to a claim in the fork's own source comments: `throttled_ms` accrues on **every** limiter wait that reaches the timer, including waits that returned a zero delay, because the accrual is a `defer` taken before `WaitN` (`mtg:mtglib/speed_limit.go:84-85`). A non-zero `throttled_ms` therefore does not prove the user actually hit the cap.

**Units, and the one conversion the panel must do.** The panel's rate contract is **bits per second**: `core.RateLimit` is "per direction, in bits per second from the client's side; 0 is unlimited", the mirror of `policy.Limits` (`internal/core/caps.go:382-384`). mtg's bare `speed` string is **bytes per second** (`mtg:mtglib/secret_limits.go:171`). Every value crossing from the ladder into the sidecar is divided by 8 and every read-back is multiplied by 8, exactly as the SSH core does (`internal/cores/internal/ssh/server.go:564`, `perSec := d.bps / 8`; `:540`, `* 8` on the way back). A `min(up, down)` pushed without the division is a cap eight times too generous. Name the engine field `SpeedBytesPerSecond`, mtg's own name for it (`mtg:mtglib/proxy_api.go:667`), never anything ending in `Bps`, so the unit cannot be misread.

**Feature flag.** `speed-limits`.

### 2.2 Per-user address limit

**Config surface.** A bare integer, not a string:

```toml
[secret-limits.alice]
ip-limit = 3
```

Quoting it is a fatal parse error, and a negative value is a fatal parse error (`mtg:internal/config/type_ip_limit_test.go:60-73`). `0` and absence both mean unlimited.

**API surface.** `ip_limit` as a JSON number (`mtg:mtglib/proxy_api.go:635`), rejected when negative at `mtg:mtglib/proxy_api.go:641-643`.

**What counts as one place.** An IPv4 address is one place. An IPv6 address is masked to its **/64** and the whole /64 is one place (`ipv6AddressPrefixBits = 64`, `mtg:mtglib/address_limit.go:44`; the mask itself at `keyOfAddr`, `:80-90`). IPv4-mapped IPv6 addresses collapse onto the plain IPv4 address, pinned by `TestAddressKeyNormalisation` at `mtg:mtglib/address_limit_test.go:19`. **This disagrees with the panel's own per-address accounting for IPv6.**

**The reporting side does not share that unit.** `connected_ips` — the array a `SessionReporter` would consume — is deduplicated on the **full address string**, `ip.String()`, not on the /64: `distinctClientAddresses` at `mtg:mtglib/address_limit.go:392-418`, with the dedup key at `:402`. So one IPv6 client rotating its low 64 bits (RFC 8981 privacy extensions, which is the case the /64 mask exists for) appears as N entries in `connected_ips` while counting as **one** place against `ip_limit`. A panel that counts sessions from `connected_ips` therefore over-counts an IPv6 client relative to the sidecar's own refusal. That is the Q1 option C divergence made concrete, and it is a property of the read path, not just of the two enforcement layers.

**Enforcement point.** Admission only. The check runs **after** the secret has matched and **after** the disabled, expired and quota checks. On refusal the client on the FakeTLS door has already received the TLS ServerHello and simply gets a close; it is **not** routed to the cover site (`mtg:mtglib/proxy_entry.go:78`, `mtg:mtglib/proxy_serve.go:75-88`). Lowering the limit **never evicts** an already-connected place; the excess drains as connections close (`TestIPLimitFollowsASecretSwapWithoutDroppingStreams`, `mtg:mtglib/address_limit_test.go:387`).

**Concurrency is exact.** With `ip-limit = 1` and two simultaneous distinct addresses, exactly one is admitted: `TestIPLimitAdmitsExactlyOneUnderContention` at `mtg:mtglib/address_limit_test.go:251`.

**Fail-closed with no address.** A connection whose source address cannot be determined is refused rather than admitted: `TestIPLimitFailsClosedWithoutAnAddress` at `mtg:mtglib/address_limit_test.go:354`.

**Signals on refusal.** A log line at Info naming the deny reason `ip_limit`, a per-user `ip_limit_refused` counter on `/stats`, an `EventIPLimited` on the event stream, and the global `mtg_ip_limited` Prometheus counter. Correction to the fork's own comment and wiki text: **the refusal log line does carry `client-ip`**, because every stream logger is bound with it at construction (`mtg:mtglib/stream_context.go:191-193`).

**Interaction with `track-client-ips`.** The refusal counters and the limit itself are never gated by that flag. Only the **reporting** of addresses is gated. When `track-client-ips` is off the limit still works, it just reports counts without addresses — which is precisely why §2.3's gating row is load-bearing for Q2 and Q7.

**Feature flag.** `ip-limits`.

### 2.3 The external-ips union

Three routes, all under one secret name (`mtg:mtglib/proxy_api.go:380-382`):

```
GET    /secrets/{name}/external-ips
PUT    /secrets/{name}/external-ips
DELETE /secrets/{name}/external-ips
```

**PUT body, verbatim:**

```json
{
  "ips": ["203.0.113.10", "2001:db8:aaaa:bbbb::1"],
  "ttl": "90s"
}
```

**Semantics.**

| Property | Behaviour | Evidence |
| --- | --- | --- |
| Replace vs merge | **Whole-set replace.** Every PUT discards the previous set. | `mtg:mtglib/address_limit.go:176-181` (`setExternal`) |
| TTL | One expiry shared by the whole set, not per address. A Go duration string. Must be in `(0, 1h]`; `MaxExternalAddressTTL = time.Hour`. Omitted means `DefaultExternalAddressTTL = 2 * time.Minute`. | `mtg:mtglib/address_limit.go:49` and `:53`, bounds enforced at `:268-278`, applied at `:301` |
| Size cap | `MaxExternalAddresses = 64` raw entries before canonicalization. | `mtg:mtglib/address_limit.go:57`, enforced at `:248-250` |
| Address form | Literal IP only. No hostnames, no CIDR prefixes, no zone identifiers. | `ParseExternalAddresses`, pinned by `TestParseExternalAddresses` at `mtg:mtglib/address_limit_test.go:129` |
| Clearing | `DELETE`, or a PUT with an empty `ips` array, or a PUT with body `{}`. | `mtg:mtglib/proxy_api.go:583-593` |
| Union | The admission check counts the union of locally connected places and the external set, deduplicated. A place already held locally is always readmitted even when the union is over the limit. | `TestIPLimitCountsReportedAddresses`, `mtg:mtglib/address_limit_test.go:309` |
| Expiry | **Lazily pruned** on the next read or admission check, not on a timer. | `prune` at `mtg:mtglib/address_limit.go:116`, called from `:137`, `:186`, `:194` |
| Durability | In memory, **per process, therefore per inbound**. Lost on restart. Fail-open: with no external set the limit degrades to purely local. | `mtg:mtglib/address_limit.go:291-305` |
| Locking | A PUT takes only `liveMu`; it never disturbs connections. | `mtg:mtglib/address_limit.go:298-299` |
| Unknown secret | 404 on all three verbs. A deleted secret's stale external set **cannot** be cleared until the secret is re-added. | `mtg:mtglib/address_limit.go:292-294` |
| Validation order | On PUT, body validation runs **before** the secret lookup, so a malformed body against an unknown secret returns 400, not 404. | `mtg:mtglib/proxy_api.go:565-570` (parse, 400) precedes `:572` (`SetExternalAddresses`, 404) |
| PUT response | The **merged AddressReport**, not an acknowledgement. One round trip therefore gives both the push and the read-back — **subject to the gating row below**. | `mtg:mtglib/proxy_api.go:578-579` |
| **Address fields are gated on `track-client-ips`** | `ConnectedIPs`, `ExternalIPs` and `ExternalExpiresAt` are filled in **only** inside `if p.trackClientIPs {`. `Name`, `IPLimit` and `ActiveIPs` are never gated, because they are counts, not addresses. | `mtg:mtglib/address_limit.go:368-384`, the branch at `:373` |

That last row is the one that decides Q2. `track-client-ips` is **off by default**. With it off, every GET and every PUT on external-ips returns:

```json
{ "name": "alice", "ip_limit": 3, "active_ips": 2 }
```

and nothing else. There is no `external_ips` array to diff against, and no `external_expires_at` to check convergence with. **Q2's change-detection strategies (options C and D) are therefore impossible unless Q7 resolves to "on".** Any option in Q2 other than A is a vote in Q7.

**GET and PUT response shape** is `AddressReport` (`mtg:mtglib/address_limit.go:307-330`), which is **not** the same shape as `SecretInfo`. With `track-client-ips` on:

```json
{
  "name": "alice",
  "ip_limit": 3,
  "active_ips": 2,
  "connected_ips": ["198.51.100.7", "2001:db8:1111:2222:aaaa:bbbb:cccc:dddd"],
  "external_ips": ["203.0.113.10", "2001:db8:aaaa:bbbb::1"],
  "external_expires_at": "2026-09-06T12:34:56Z"
}
```

**DELETE response:** `{"status":"ok"}` (`mtg:mtglib/proxy_api.go:592`). Note that DELETE is implemented as an empty set at the default TTL (`mtg:mtglib/proxy_api.go:586`), not as a distinct operation.

The read-back of `external_ips` is **not a byte-for-byte echo of what was pushed, but neither is it collapsed to the /64.** The exact rule, and getting either half wrong makes a change-detection diff loop forever:

- Addresses are **deduplicated** at /64, because `setExternal` keys the map on `keyOfAddr(addr)` (`mtg:mtglib/address_limit.go:180`). Two addresses in the same /64 collapse to one entry, **last one in the pushed slice winning**.
- The surviving entry reads back as the **full canonicalized address**, not the prefix, because `setExternal` stores `text: addr.String()` and `externalAddresses` returns `entry.text` (`mtg:mtglib/address_limit.go:180`, `:206`). The struct's own comment says so: the text is kept so "a report reads back as it was written" (`:92-93`).
- Order is not preserved; the returned slice is `sort.Strings`-sorted (`mtg:mtglib/address_limit.go:213`).

So a caller diffing pushed against returned must **dedup its own candidate set by /64 before pushing** (or it will see one entry come back where it sent two) and then **compare full-address strings** (not prefixes). Applying a /64 collapse to the *returned* strings is the mistake: they are never in that form.

### 2.4 Quota as a ceiling

Through v1.7.0 quota was an admission gate: an over-quota user could not open a new connection but an open connection ran on. **Since v1.8.0 quota is a hard data-path ceiling.** The counting connection cuts at the exact byte the quota is crossed, mid-stream, and a per-user `quota_cutoffs` counter increments. A background sweep closes any stream belonging to a user who has since gone over: `denySweepInterval = 5 * time.Second` at `mtg:mtglib/proxy_secrets.go:369`, started at `mtg:mtglib/proxy.go:272`, exercised by `mtg:mtglib/maintenance_test.go:72`.

**Config surface.** A size string, parsed by `config.TypeBytes.Set` (`mtg:internal/config/type_bytes.go:18-32`) via the field declaration `Quota TypeBytes` at `mtg:internal/config/config.go:23`. `Set` upper-cases the input and rewrites `IB` back to `iB` (`:12`, `:19`), then hands it to `units.ParseBase2Bytes`.

**The accepted suffix set is narrower than it looks, and the bare single letters are not in it.** `ParseBase2Bytes` tries `bytesUnitMap = MakeUnitMap("iB", "B", 1024)` and falls back to `oldBytesUnitMap = MakeUnitMap("B", "B", 1024)` (`units/bytes.go:24-25`). `MakeUnitMap` builds `{shortSuffix: 1, "M"+suffix, "G"+suffix, "T"+suffix, "P"+suffix, "E"+suffix}` plus `"K"+suffix` for a base-2 scale (`units/si.go:16-49`). The two maps are therefore `{B, KiB, MiB, GiB, TiB, PiB, EiB}` and `{B, KB, MB, GB, TB, PB, EB}`. `ParseUnit` looks the whole suffix up in the map, so:

- Accepted: `B`, `KB`/`KiB`, `MB`/`MiB`, `GB`/`GiB`, `TB`/`TiB`, `PB`/`PiB`, `EB`/`EiB`, in any case, all **base-2** — `GB` means GiB.
- **Rejected:** bare `K`, `M`, `G`, `T`. `"10G"` fails with `unknown unit G`.

**A live asymmetry the panel depends on:** the **config file** parser rejects a bare integer with no unit, because `TypeBytes.Set` goes straight to `ParseBase2Bytes` with no integer branch; the **API** accepts one, because `mtglib.ParseQuotaBytes` tries `strconv.ParseInt` first (`mtg:mtglib/secret_limits.go:139-146`). The panel renders `strconv.FormatInt(bytes, 10) + "B"` (`internal/mtproto/manager.go:850-852`), for example `"1073741824B"`, which satisfies both because `B` is `shortSuffix` in both maps. That the `B` suffix parses is verified by reading the maps; it is **UNVERIFIED by any mtg test**. The fork's wiki says the opposite about the file: its per-user limits table describes `quota` as "a human size ("10GB", "500MiB") or a bare integer byte count" (`mtg:docs/wiki/Limits-and-Quotas.md`), which is the API's grammar, not the file's, since `TypeBytes.Set` has no integer branch. Fork ask #10 covers the row as well as the test.

**Counter durability.** `quota_used` survives a secret-set swap. The evidence is narrow: `TestSwapPreservesUsage` (`mtg:mtglib/proxy_limits_test.go:172-189`) makes exactly one call, `p.ReplaceSecrets(SecretConfig{…})` at `:182-185`, and asserts the counter still reads 400 afterwards. It does **not** drive the HTTP handler and does **not** reload a file. What it proves is that the shared `ReplaceSecrets` → `swapSecretConfigLocked` path preserves the counter — and both `PUT /secrets` (`mtg:mtglib/proxy_api.go:742`) and the config reload funnel through that same function, so the property does hold on both surfaces. Say it that way rather than claiming two covered paths.

`bytes_in` and `bytes_out` are **not** persisted across restarts and **stop advancing** once the ceiling cuts, so they are not a substitute for panel-side accounting.

**No usage state file.** The panel deliberately does not emit `usage-state-file`, and a test pins that absence (`internal/mtproto/manager_test.go:225`). The sidecar's `quota_used` therefore **zeroes on every restart**, including a structural-fingerprint restart the panel itself triggers.

**Reset.** `POST /secrets/{name}/reset-quota` zeroes `quota_used`. The panel already calls it on a client traffic reset (`internal/mtproto/manager.go:647-688`). A 404 means either "unknown client" or "old binary", and the panel treats both as success (`internal/mtproto/manager.go:685`).

**Authentication.** The management API is unauthenticated when `api-token` is empty; the panel always mints a token (`internal/mtproto/manager.go:865-871`), so this is not a live exposure.

### 2.5 Expiry

**Config surface.** Either RFC3339 (`"2026-12-31T23:59:59Z"`) or a bare date `"2026-12-31"`, which means UTC midnight at the start of that day.

**Comparison is exclusive:** a user is expired once `now >= expires`, implemented as `!time.Now().Before(lim.ExpiresAt)` at `mtg:mtglib/proxy_secrets.go:298`. The zero time means never (`mtg:mtglib/secret_limits.go:48-50`).

**v1.8.0 change.** Expiry is no longer admission-only. The same five-second sweep that enforces the quota ceiling (`mtg:mtglib/proxy_secrets.go:369`, `mtg:mtglib/proxy.go:272`) closes live streams of a user whose clock has passed, so an expired client drops within five seconds instead of surviving until it reconnects.

The panel already renders this correctly (`internal/mtproto/manager.go:854-856`, UTC RFC3339). No panel change is required for expiry itself; only the behaviour description in user-facing documentation changes. Remember the write/read skew from §2's table: the panel writes `expires` and any read-back parser must look for `expires_at`.

### 2.6 GET /secrets/{name}

**Added in v1.5.0** (`a02122b`), so it is answered by every sidecar from v1.5.0 on — but *not* by the v1.3.0 binary shipping today. Returns a **bare `SecretInfo` object, not wrapped** in any envelope.

The full field set, transcribed from `mtg:mtglib/proxy_api.go:21-80` rather than from an abridged example, because a decoder written from a partial example silently drops fields:

| Field | JSON key | Always present? |
| --- | --- | --- |
| Name | `name` | yes |
| Secret | `secret` | yes |
| Host | `host` | **yes** |
| AdTag | `ad_tag` | omitempty |
| EffectiveAdTag | `effective_ad_tag` | omitempty |
| Quota | `quota` | omitempty |
| QuotaUsed | `quota_used` | omitempty |
| QuotaRemaining | `quota_remaining` | omitempty, `*int64` |
| QuotaReset | `quota_reset` | omitempty |
| ExpiresAt | `expires_at` | omitempty, `*time.Time` |
| Disabled | `disabled` | omitempty — **`false` is absent, not `"disabled": false`** |
| SpeedLimit | `speed_limit` | omitempty |
| IPLimit | `ip_limit` | omitempty |
| ActiveIPs | `active_ips` | omitempty |
| Mode | `mode` | **yes** — `"ee"`, `"dd"` or `"plain"` |
| Unserved | `unserved` | omitempty |
| Connections | `connections` | **yes** — not gated |
| LastSeen | `last_seen` | **yes**, `*time.Time`, may be `null` — not gated |
| ConnectedIPs | `connected_ips` | omitempty **and gated on `track-client-ips`** |
| LastIP | `last_ip` | omitempty **and gated** |
| ExternalIPs | `external_ips` | omitempty **and gated** |
| ExternalExpiresAt | `external_expires_at` | omitempty **and gated** |

A representative response for a fully governed, currently connected client on a proxy with `track-client-ips` on:

```json
{
  "name": "alice",
  "secret": "7gAAAAAAAAAAAAAAAAAAAABkdW1teS5jb20",
  "host": "dummy.com",
  "ad_tag": "0123456789abcdef0123456789abcdef",
  "effective_ad_tag": "0123456789abcdef0123456789abcdef",
  "quota": 1073741824,
  "quota_used": 12345678,
  "quota_remaining": 1061395946,
  "quota_reset": "monthly",
  "expires_at": "2026-12-31T23:59:59Z",
  "speed_limit": 1250000,
  "ip_limit": 3,
  "active_ips": 2,
  "mode": "ee",
  "connections": 2,
  "last_seen": "2026-09-06T12:34:50Z",
  "connected_ips": ["198.51.100.7"],
  "last_ip": "198.51.100.7",
  "external_ips": ["203.0.113.10"],
  "external_expires_at": "2026-09-06T12:34:56Z"
}
```

Traps for the implementer:

- `secret` comes back **base64 RawURL encoded**, not the hex string the panel stores and renders. Do not compare it against `SecretEntry.Secret` without decoding.
- `expires_at`, not `expires`. See §2's table.
- `quota_used` is omitted when it is zero **and** when no quota is configured. Absence is not zero-usage evidence.
- `disabled` never appears as `false`.
- 404 body is `{"error":"secret not found: \"x\""}`.
- `speed_limit`, `ip_limit` and `active_ips` are present here. `throttled_ms`, `ip_limit_refused` and `quota_cutoffs` are **not**; those live only on `/stats`.

For completeness, the per-user object on `GET /stats` is a different struct again — `UserStatsJSON` at `mtg:mtglib/proxy_stats.go:589-652`. It carries `connections`, `bytes_in`, `bytes_out`, `last_seen`, `quota_used`, `quota`, `quota_remaining`, `quota_reset`, `expires_at`, `disabled`, `quota_cutoffs`, `ad_tag_skipped`, `ad_tag_failed`, `speed_limit`, `throttled_ms`, `ip_limit`, `active_ips`, `ip_limit_refused`, `connected_ips`, `last_ip`, `external_ips`, `external_expires_at`. **`last_seen` is one timestamp per secret, not per address** — it is stored by `UpdateLastSeen(name)` on the secret's counter (`mtg:mtglib/proxy_stats.go:204-206`), and `connected_ips` is a flat `[]string` with no timestamps at all. Section 5's Unit 4 and section 6's Q9 both turn on that.

### 2.7 Feature flags

`GET /stats` carries `$.features`, a sorted array declared a public contract at `mtg:mtglib/proxy_stats.go:461-466`. `GET /secrets/{name}` does **not** carry features.

The exact advertised sets, read from each tag's `supportedFeatures` (`mtg:mtglib/proxy_stats.go:523-536` at HEAD; `git show <tag>:mtglib/proxy_stats.go` for the rest):

| Feature | v1.3.0 (7) | v1.6.0 (9) | v1.7.0 and v1.8.0 (10) |
| --- | --- | --- | --- |
| `client-ips` | yes | yes | yes |
| `faketls` | yes | yes | yes |
| `ip-limits` | no | no | **yes** |
| `listeners` | yes | yes | yes |
| `quota-reset` | yes | yes | yes |
| `secret-modes` | yes | yes | yes |
| `secrets-api` | yes | yes | yes |
| `speed-limits` | no | **yes** | yes |
| `web-relay` | yes | yes | yes |
| `web-relay-watch` | no | yes | yes |

The three sampled columns do not show where `web-relay-watch` entered: it was added by `14fbf10` in **v1.4.0**, not v1.6.0, so a v1.4.0 or v1.4.1 sidecar advertises eight. That matters only if a deployment is pinned to one of those two tags; it changes nothing for the panel, which gates on `speed-limits` and `ip-limits` alone.

Three consequences the implementer must absorb:

1. **v1.3.0, the binary shipping today, advertises seven features and lacks exactly `speed-limits`, `ip-limits` and `web-relay-watch`.** Only the first two are strings the panel needs to test.
2. **There is no feature string for `quota`, `expires`, `disabled` or ad-tags.** `quota-reset` names the reset endpoint, not the quota key. So the panel cannot gate quota or expiry on `features`, and it does not need to: both work on v1.3.0 and both are already shipping.
3. **There is no feature string for the v1.8.0 quota-ceiling and expiry-sweep semantics either, and none for `GET /secrets/{name}`.** A panel that wants to distinguish "quota blocks new connections" from "quota cuts mid-stream", or that wants to know whether the per-secret read-back route exists, must gate on the version string from `--version`, not on `features`, or not gate at all.

---

## 3. The strictness trap

### 3.1 The two surfaces do not fail alike

**Config file, unknown key: always accepted; whether it is *reported* depends on the version.** This decides what §8.4's VM check should assert.

- **At HEAD (v1.8.0) only:** the parser runs a second, strict decode purely to name what the lenient one dropped, and stores the result — `conf.UnknownKeys = unknownKeys(rawData)` at `mtg:internal/config/parse.go:185`, with the strict probe at `:191-200`. `warnUnknownKeys` (`mtg:internal/cli/run_proxy.go:325-333`, called at `:352` and `:533`) logs each one at Warning on startup, and `checkUnknownKeys` (`mtg:internal/cli/doctor.go:167-171`) lists them.
- **At v1.7.0 and earlier, including the v1.3.0 binary that actually ships:** there is no `UnknownKeys` field, no strict pass and no warning. `git show v1.3.0:internal/config/parse.go` and `git show v1.7.0:internal/config/parse.go` both call plain `toml.Unmarshal` and stop. All three of `UnknownKeys`, `warnUnknownKeys` and `checkUnknownKeys` arrive in commit `e58027c`.

So an old binary handed `speed = "1250000"` **starts fine and ignores the key in total silence**. Nothing is logged. Do not build a VM check, an operator-facing message, or a support runbook step on a warning that only the newest binary emits.

The config file's genuinely **fatal** cases, all of which abort startup and also abort `POST /reload`:

| Fatal case | Evidence |
| --- | --- |
| A known key with a bad value, for example `speed` below 64 kbit/s | `mtg:mtglib/secret_limits.go:238-239` |
| `ip-limit` negative, or quoted as a string | `mtg:internal/config/type_ip_limit_test.go:60-73` |
| `quota` as a bare integer with no unit, or with a bare `K`/`M`/`G`/`T` | `mtg:internal/config/type_bytes.go:18-25` plus the units maps |
| A `[secret-limits.<name>]` naming a secret absent from `[secrets]` | `mtg:internal/config/config.go:369`, a `validateSecretLimits()` failure |
| A repeated table header | TOML itself |

**API, unknown field: a 400.** The `PUT /secrets` decoder sets `DisallowUnknownFields` (`mtg:mtglib/proxy_api.go:926`). An old binary handed `"speed"` in the JSON body rejects the **entire request**. Worse, validation is all-or-nothing across the whole set: one bad `speed` anywhere aborts the push before anything is applied (`mtg:mtglib/proxy_api.go:719-724`).

### 3.2 The concrete failure mode in this panel

`applySecrets` treats any non-200 as failure (`internal/mtproto/manager.go:903`, `return resp.StatusCode == http.StatusOK`), and the caller falls through from a failed hot-apply to a **full process restart** (`internal/mtproto/manager.go:424-431`).

So, on a v1.3.0 sidecar, if the panel unconditionally adds `speed` to the PUT body:

1. Every client edit on that inbound produces a 400.
2. Every client edit therefore restarts the mtg-proxy process.
3. **Every connected MTProto user on that inbound is dropped on every client edit**, silently, with only a log line to show for it.

That is the trap. **The TOML path is safe to widen unconditionally. The PUT path is not.**

### 3.3 How the panel can learn capabilities, and when it cannot

Today the panel already stores the feature list but never acts on it. `Manager.Features(id)` returns a clone under `m.mu` (`internal/mtproto/manager.go:933-941`) and has no production consumer. The features are stored **only** by `CollectTraffic` (`internal/mtproto/manager.go:571-575`, guarded by an identity check so a restart in between cannot stamp the old binary's capabilities onto the new one). `Health()` and `billFinalLocked` discard the features they see. `Installed()` (`internal/cores/internal/mtproto/versions.go:52-57`, delegating to `binaryVersion` at `:59-71` whose `exec.CommandContext(ctx, path, "--version")` is at `:62`) is UI-only, one process spawn per call.

**How long the unknown-features window actually is.** `CollectTraffic` is driven by the traffic job, and that job runs at `cadenceXrayTraffic = "@every 5s"` (`internal/web/web.go:346`), registered at `internal/web/web.go:392` inside a goroutine that first sleeps five seconds (`:390-393`), and reaching the manager through `collectCoreTraffic()` at `internal/web/job/xray_traffic_job.go:83`. So: roughly **five seconds per tick, after a one-off five-second delay before the job exists at all**. The ten-second figure belongs to `cadenceCorePolicy` (`internal/web/web.go:355`) and `cadenceCoreSupervise` (`:349`), which are different jobs. Strategy C's cost/benefit must be argued against 5s, not 10s — which makes it a weaker case than it first looks.

Candidate strategies:

| Strategy | How | Cost | Verdict |
| --- | --- | --- | --- |
| **A. Gate the PUT on `Manager.Features`, fall through to restart when unknown** | Before building `secretsPayload`, read the cached features. Omit `speed`/`ip_limit` when the feature is absent **or unknown**, but when unknown also force the restart path. | Correct on a new binary. In the unknown-features window (up to 5 s after a sidecar restart, up to 10 s only for sidecars started during panel boot) it drops connections. | Rejected: turns a cold start into a guaranteed disconnect. |
| **B. Gate the PUT, omit the field when unknown, never force a restart** | Same read, but unknown means "omit and carry on". | Never drops connections. **The loss is worse than "the limit is not set yet":** see the box below. | Acceptable with eyes open, and it is the residual behaviour under C. |
| **C. Probe `/stats` right after `proc.Start`** | An HTTP GET to `/stats` populating features before any hot-apply is possible. | See the locking box below — this is not the one-line change it reads as. Combine with B for the residual failure case. | **Recommended in shape**, but only with the placement and retry budget settled; see Q3. |
| **D. Gate on `Installed()` version string instead of features** | Compare semver. | An exec per call, and version does not always equal capability for a locally built binary. | Rejected as the primary gate; useful only for the v1.8.0 ceiling semantics and for `GET /secrets/{name}`, neither of which has a feature string. |
| **E. Always emit the TOML keys, unconditionally** | Widen `renderConfig` with no gate. | Silently ignored on an old binary (§3.1). The config is the source of truth on every restart. | **Recommended**, no gate needed on this surface. |

> **What strategy B actually costs.** The tempting justification, "API-set limits are in memory only, so the limit is merely late", understates the mechanism. `PUT /secrets` is a **whole-set replace**: `handlePutSecrets` (`mtg:mtglib/proxy_api.go:686-747`) builds a brand-new `SecretConfig` from the request body alone and hands it to `ReplaceSecrets` → `swapSecretConfigLocked`. Nothing merges with what is already loaded. So omitting `speed` does not leave a previously-set value alone; it **wipes the limit the config file itself set at startup**, and the client runs unthrottled until the next structural restart re-reads the file. On a fresh v1.6.0+ sidecar whose config carries `speed`, the first hot-apply inside the features-unknown window silently un-limits every client on that inbound. That is a real regression window, not a delayed feature.

> **Where strategy C's probe may run.** Not where it reads naturally. `Manager.Reconcile` takes `m.scrapeMu` and then `m.mu` and holds **both** across the whole `ensureLocked` loop (`internal/mtproto/manager.go:491-495`, two deferred unlocks). `ensureLocked` starts the process at `internal/mtproto/manager.go:453`. A synchronous probe placed there therefore blocks `Features()`, `RoutedTags()`, `CollectTraffic()`, `Health()` and `Remove()` for its full duration, multiplied by every instance started in that pass. And a just-started sidecar will not answer the first request, so a single shot is not enough — it needs a bounded retry. The probe must run **outside** the dual-mutex hold: either after `Reconcile` releases both, or asynchronously with the features cache left empty until it lands. The asynchronous form is strategy B with extra steps, which is the honest way to describe it. The placement and the retry budget are part of Q3, not settled here.

The recommended combination remains **C plus B for the PUT, and E for the TOML**, but C is contingent on Q3 resolving where the probe runs. What that combination buys: the rendered config always carries the truth, a new binary picks limits up hot, an old binary accepts the widened file silently and ignores it, and nobody's connection is dropped by a capability mismatch.

---

## 4. Where the panel stands today

### 4.1 What the engine renders and pushes

`SecretEntry` is the panel's per-client carrier into the mtg engine (`internal/mtproto/manager.go:35-41`). It has five fields and **neither speed nor ip-limit**:

```go
type SecretEntry struct {
	Name        string
	Secret      string
	AdTag       string
	QuotaBytes  int64
	ExpiresUnix int64
}
```

`renderConfig` (`internal/mtproto/manager.go:698-777`) writes sections in a fixed order. The `[secret-limits.*]` loop comes **first**, at `:761-772`, and `[secrets]` is the **final** section, written at `:773`. The function's own doc comment states the constraint — "`[secrets]` must be the final section so trailing keys are not swallowed by another table" (`:692`) — and `internal/mtproto/manager_test.go:222` pins it:

```go
if strings.LastIndex(cfg, "[secrets]") < strings.Index(cfg, "[secret-limits.\"alice\"]") {
	t.Fatalf("[secret-limits] must precede the final [secrets] section:\n%s", cfg)
}
```

**Any new key Unit 1 adds must therefore stay above `[secrets]`, inside the existing limits loop.** A key written after it lands inside the `[secrets]` table and the config will not parse.

The emission guard at `:762-764` skips a client entirely unless it has a quota or an expiry:

```go
for _, e := range secrets {
	if e.QuotaBytes <= 0 && e.ExpiresUnix <= 0 {
		continue
	}
	fmt.Fprintf(&b, "\n[secret-limits.%q]\n", e.Name)
	if e.QuotaBytes > 0 {
		fmt.Fprintf(&b, "quota = %q\n", quotaString(e.QuotaBytes))
	}
	if e.ExpiresUnix > 0 {
		fmt.Fprintf(&b, "expires = %q\n", expiresString(e.ExpiresUnix))
	}
}
b.WriteString("\n[secrets]\n")
```

That guard is the first thing that must widen: a client with only a speed cap or only an address limit currently gets no `[secret-limits]` table at all.

Two fingerprints decide restart versus hot-apply. `StructuralFingerprint` (`internal/mtproto/manager.go:101-119`) joins fourteen values with `|` and covers listener, fronting, throttling, Xray routing, public IPs and the web backend. It does **not** cover `api-bind-to` or `api-token`, which are rendered but not fingerprinted. `SecretsFingerprint` (`internal/mtproto/manager.go:126-133`) is the hot-apply trigger:

```go
pairs = append(pairs, fmt.Sprintf("%s=%s;tag=%s;q=%d;exp=%d", e.Name, e.Secret, e.AdTag, e.QuotaBytes, e.ExpiresUnix))
slices.Sort(pairs)
return strings.Join(pairs, "|")
```

**This is the silent-failure trap of the whole feature.** A new field added to `SecretEntry` and to `renderConfig` but not to this string produces a panel where changing a client's speed cap changes nothing at runtime, because the fingerprint is unchanged and neither a hot-apply nor a restart fires.

One asymmetry worth knowing: `dedupSecrets` (`internal/mtproto/manager.go:782` onward) is last-wins, `renderConfig` consumes the deduped slice, and `SecretsFingerprint` consumes the raw `inst.Secrets`.

The hot-apply body is a **map keyed by client name**, with four flat fields per entry (`internal/mtproto/manager.go:824-848`):

```go
type secretPutEntry struct {
	Secret  string `json:"secret"`
	AdTag   string `json:"ad_tag,omitempty"`
	Quota   string `json:"quota,omitempty"`
	Expires string `json:"expires,omitempty"`
}

type secretsPutBody struct {
	Secrets map[string]secretPutEntry `json:"secrets"`
}
```

`applySecrets` PUTs it to `http://127.0.0.1:<port>/secrets` with a 3s timeout (`internal/mtproto/manager.go:886-904`).

Scrape shapes: `statsUser` is connections, bytes_in, bytes_out (`internal/mtproto/manager.go:808-812`); `mtgStats` is started_at, features, users (`internal/mtproto/manager.go:816-822`). Neither reads `connected_ips`, `last_seen`, `speed_limit`, `ip_limit`, `active_ips` or the refusal counters. And nothing on `managed` (`internal/mtproto/manager.go:152-172`) can hold a per-user address list even if `statsUser` decoded one: the struct has `features`, `counter`, `ring` and process bookkeeping, and `billReadings` (`internal/mtproto/manager.go:586` onward) returns only byte deltas. Widening `statsUser` alone parses the addresses and throws them away.

### 4.2 What maps a panel client into a secret

The live path is `toEngine` (`internal/cores/internal/mtproto/convert.go:24-58`; the file is 58 lines long in total). It reads the credentials `CredSecret` and `CredAdTag`, maps `u.QuotaBytes > 0` and `u.ExpiryUnixMilli / 1000`, and returns `serve = len(out.Secrets) > 0`. **It never reads `u.LimitIP`,** and there is no speed input on the user record it could read: `core.User` (`internal/core/core.go:97-106`) carries `Email`, `Enable`, `QuotaBytes`, `ExpiryUnixMilli`, `LimitIP` and `Credentials`, and no rate field at all. The `engine.SecretEntry` composite literal that a new field belongs in is at `:44-48`.

`InstanceFromInbound` is **not** in this file and is **not** dead. It lives in the engine package at `internal/mtproto/manager.go:229-262` (doc comment from `:229`, declaration at `:234`), and it maps an inbound's settings blob straight into an `Instance` — a parallel path to `toEngine`, reading the same six client fields off the anonymous `Clients` struct at `internal/mtproto/manager.go:282-289` (`email`, `secret`, `adTag`, `enable`, `totalGB`, `expiryTime`). Five test files call it: `internal/mtproto/manager_test.go:10`, `internal/mtproto/usable_secret_test.go:82`, `internal/mtproto/web_backend_test.go:159-160`, and — importantly — `internal/web/service/inbound_mtproto_apply_test.go:45` and `internal/web/service/mtproto_reset_keeps_running_test.go:37`, which use it to prime the manager before exercising an update. The accurate claim is that it has **no non-test production caller**, which is weaker and has different consequences. Section 5's Unit 3 and section 10's item 14 both follow from that.

**Test coverage of `toEngine` is partial, not absent.** `toEngine` is driven directly by `TestUsersComeFromTheContractNotTheSettingsBlob` (`internal/cores/internal/mtproto/driver_test.go:362`) and by a serve/error table test (`:446`). The true and narrower finding, which grep confirms, is that **no test pins the limit mapping**: `QuotaBytes`, `ExpiryUnixMilli` and `LimitIP` appear in no `_test.go` file under `internal/cores/internal/mtproto/`. The quota and expiry mapping that has been shipping for months is unpinned, and Unit 3 is where that gets fixed.

### 4.3 What the mtproto core declares

`internal/cores/internal/mtproto/core.go:22` sets `Kind core.Kind = "mtproto"`. `Describe()` (`internal/cores/internal/mtproto/core.go:57-71`) declares `UserHotAdd`, `PerUserStats`, `QuotaPushdown` and `OnlineUsers` as `core.Yes()`, and `ShareLink` as `core.No()`. mtproto is the **only** core declaring `QuotaPushdown`.

The package contains **no** `Sessions`, no `CutSessions`, no `AdmitLogin`, no `EnforceRates` and no `ShapingSelector`. It therefore binds to none of `SessionReporter`, `SessionCutter`, `LoginAdmitter`, `RateEnforcer` or `ShapingHost` in `internal/core/bind.go`.

`Deps` (`internal/cores/internal/mtproto/core.go:38-46`) carries exactly two fields — `HTTPClient func(timeout time.Duration) *http.Client` and `Platform string` — and is filled once, by value, at the single registration site: `reg.Register(mtproto.New(mtproto.Deps{HTTPClient: deps.HTTPClient, Platform: releasePlatform()}))` at `internal/cores/cores.go:262`. That shape matters for Q7; a plain `bool` added there is read once at boot and never again.

### 4.4 How the panel classifies mtproto for IP limits and speed

`ipLimitEnforcementOf` (`internal/web/service/core.go:80-89`) returns one of `"login"`, `"cut"` or `"firewall"`, and both `"login"` and `"cut"` require the core to have bound `SessionCutter`. But the switch is only ever reached for a core that bound `SessionReporter` — `coreViews` calls it inside `if bound.Sessions != nil` (`internal/web/service/core.go:124-127`). mtproto binds neither, so **mtproto is absent from the panel's session-kind and ip-limit-enforcement tables entirely**. It is not classified as `"firewall"`; it is simply not present, which is why the frontend treats it as an unsupported kind rather than as a firewall-mode kind.

On the frontend, the two behaviours differ and both matter:

- The **speed** surface only drives an Alerts panel. It disables nothing; a tier can be assigned to an mtproto client today and it silently does nothing.
- The **IP-limit** field disables only when **every** kind attached to the inbound is unsupported. On a mixed inbound set it stays enabled.

**The frontend fixtures will not notice any of this changing.** It is tempting to list `frontend/src/test/ip-limit-enforcement.test.ts:48-55` and `:105-117` and `frontend/src/test/ip-limit-unsupported.test.ts:36-46` as tests that will go red when the core starts declaring capabilities. They will not. All of them are hand-written `CoreView` object literals passed straight into the function under test — `const MTPROTO: CoreView = { ...base, id: 'mtproto', kinds: ['mtproto'], sessionKinds: [], ipLimitEnforcement: undefined }` at `ip-limit-enforcement.test.ts:47-53`, and the same shape at `ip-limit-unsupported.test.ts:36-46`. Both files import `CoreView` from `@/generated/zod` as a **type only**; neither reads the Go registry, `frontend/src/generated/examples.ts`, or anything else that moves when `Describe()` or `Bind` change. Declaring `SessionReporter` changes what the live `CoreViews()` returns (`internal/web/service/core.go:124-127`), not what a TypeScript literal contains.

The only real fallout in those files is a comment that stops being true: "The point of the whole thing: it serves a kind and reports no sessions" at `frontend/src/test/ip-limit-unsupported.test.ts:41`, and the prose block at `:5-18` of the same file, which names mtg as the reason the guard exists. If the fixtures are to keep meaning something after mtproto reports sessions, the ask is to add a **new** fixture for a still-sessionless kind, not to edit these four assertions.

### 4.5 The conformance rig

`internal/cores/internal/mtproto/driver_test.go:290-300` builds the `coretest` Rig with `NewCore`, `Instance`, `Starts`, `Alive`, `FeedTraffic`, `RestartSource` and `ServedUsers`. It has **no `FeedSession` hook**. Declaring `SessionReporter` without adding one fails the suite's `sessions/verifiable` case (`internal/core/coretest/suite.go:586-588`).

The rig's fake binary answers `/stats` from a file the rig writes, and that file is **narrower than the real payload in three ways that both Unit 2 and Unit 4 depend on**. `writeStats` (`internal/cores/internal/mtproto/driver_test.go:185-196`) marshals only `{started_at, users}` — **no `features` array** — and `fakeUser` (`:133-137`) carries only `connections`, `bytes_in`, `bytes_out` — **no `connected_ips`, no `last_seen`, no `speed_limit`**. `r.feed` (`:213-219`) writes the file and waits for the API port; it does not itself drive `CollectTraffic`.

`TestMtprotoConformsToTheContract` (`:303-306`) calls `requirePosixHost(t)`, so the entire conformance run **skips on Windows** and only proves out on Linux.

---

## 5. The gap as a work breakdown

Ordered so each unit is as independently landable as the code allows. Two of the seven are **not** independent, and saying so is the point of this table: Unit 1's `SecretEntry.SpeedBytesPerSecond` has no producer until Unit 6 supplies one, and Unit 6 cannot supply one without deciding where the merge happens (Q8). Unit 5 is a proposal, not a unit, until Q10 says it belongs here.

| # | Unit | Ships alone | Needs new mtg release | Needs a design decision first |
| --- | --- | --- | --- | --- |
| 1 | Engine plumbing: carry ip-limit (and, subject to Q8, speed) through `SecretEntry`, `renderConfig`, `SecretsFingerprint`, `secretsPayload` | The ip-limit half, yes. **The speed half is coupled to Unit 6** — see Q8. | No | **Q8** (speed carrier), Q4 (units), Q3 (gating). **And Q1:** the ip-limit half is itself the sidecar-side second layer that Q1's recommendation defers, so under Q1 = A it waits, or ships with emission gated off |
| 2 | Capability gate on the PUT path, with a features probe at start | Yes | No | Q3 (probe placement and retry budget) |
| 3 | `toEngine` reads `u.LimitIP`, and the first tests of the limit mapping | Yes | No (renders only) | **Q1**: under the recommended option A this row IS the second layer A defers. It lands only if Q1 resolves to B or C, or ships with emission gated off; the limit-mapping tests for quota and expiry land regardless |
| 4 | `SessionReporter` on the mtproto core, via `track-client-ips` and `connected_ips` | Yes | **No**, v1.3.0 already carries both | Q1, Q7, **Q9** |
| 5 | external-ips heartbeat, a new capability interface | No, depends on 1 and 4 | Yes (v1.7.0) | Q2, Q6, **Q10 (does this belong in this document at all)** |
| 6 | `RateEnforcer` on the mtproto core | No — **1 and 6 land together** | Yes (v1.6.0) | **Q8**, Q4 |
| 7 | Documentation, generated-schema refresh | Yes | No | none |

A separate, smaller commit falls out of Unit 6 and should land **before** any of the seven: see the i18n note under Unit 6.

### Unit 1: engine plumbing

**What changes.**

| File and anchor | Change |
| --- | --- |
| `internal/mtproto/manager.go:35-41` | `SecretEntry` gains `IPLimit int`, and — **only if Q8 keeps it here** — `SpeedBytesPerSecond int64` |
| `internal/mtproto/manager.go:762-764` | Widen the skip guard to `e.QuotaBytes <= 0 && e.ExpiresUnix <= 0 && e.SpeedBytesPerSecond <= 0 && e.IPLimit <= 0` |
| `internal/mtproto/manager.go:765-772` | After the existing two keys, **and still inside the loop that ends at `:772`, above the `[secrets]` write at `:773`**, emit `speed = "<n>"` (a quoted string) and `ip-limit = <n>` (a **bare** integer, never quoted) |
| `internal/mtproto/manager.go:126-133` | Extend the fingerprint pair format with `;spd=%d;ipl=%d` |
| `internal/mtproto/manager.go:824-829` | `secretPutEntry` gains `Speed string \`json:"speed,omitempty"\`` and `IPLimit int \`json:"ip_limit,omitempty"\`` as **flat siblings**, matching `mtg:mtglib/proxy_api.go:677-679` where `limitFields` is embedded, not nested. `omitempty` on the int makes "unset" and "gated off" the same wire shape, which is exactly what unit 2 needs. |
| `internal/mtproto/manager.go:835-848` | `secretsPayload` populates them subject to the unit 2 gate |
| new helper next to `internal/mtproto/manager.go:850-856` | `speedString(bytesPerSecond int64) string` returning `strconv.FormatInt(bytesPerSecond, 10)`. The caller has already divided the contract's bits per second by 8 and applied the 64 kbit/s floor inside the core (Q4); this helper formats and nothing else. |

**Verbatim rendered TOML the implementer should aim for** — note the position, above the final `[secrets]`:

```toml
[secret-ad-tags]
"alice" = "0123456789abcdef0123456789abcdef"

[secret-limits."alice"]
quota = "1073741824B"
expires = "2026-12-31T23:59:59Z"
speed = "1250000"
ip-limit = 3

[secrets]
"alice" = "ee00000000000000000000000000000064756d6d792e636f6d"
```

**Verbatim PUT body the implementer should aim for** (new binary, both features present). The top-level key is `secrets`, its value is an object **keyed by client name**, and every limit is a flat sibling of `secret` and `ad_tag`. This shape is confirmed against both sides: panel `secretsPutBody` at `internal/mtproto/manager.go:831-833` and mtg `putSecretsRequest` at `mtg:mtglib/proxy_api.go:681-684` with the embedded `limitFields` at `:675-679`.

```json
{
  "secrets": {
    "alice": {
      "secret": "ee00000000000000000000000000000064756d6d792e636f6d",
      "ad_tag": "0123456789abcdef0123456789abcdef",
      "quota": "1073741824B",
      "expires": "2026-12-31T23:59:59Z",
      "speed": "1250000",
      "ip_limit": 3
    },
    "bob": {
      "secret": "ee11111111111111111111111111111164756d6d792e636f6d",
      "ip_limit": 2
    }
  }
}
```

Three notes on the envelope. There is **no `name` field**; the name is the map key. mtg's request struct carries a top-level `ad_tag` default (`mtg:mtglib/proxy_api.go:683`) that the panel does not send and should not start sending, since per-client ad-tags are already carried per entry. And the request key is `expires`, while every read-back spells it `expires_at` (§2).

**Acceptance test.** Extend `TestRenderConfigSecretLimits`, `TestSecretsPayloadLimits` and `TestFingerprintSplit`. The cross-repo half is fork ask #12: `mtg:internal/config/panel_contract_test.go` pins the rendered shape from the sidecar's side and must gain the new keys in the same stretch of work, in the other repository. The red-proof is free: the fingerprint literal at `internal/mtproto/manager_test.go:305` (`"alice=ee11;tag=;q=0;exp=0|bob=ee22;tag=;q=0;exp=0"`) goes red the moment the format string changes, and that failure **is** the proof the new fields participate in the hot-apply decision. Keep the section-order assertion at `internal/mtproto/manager_test.go:222` green.

**What could break.** Nothing at runtime while no caller populates the new fields — which is exactly the state Unit 1 leaves `SpeedBytesPerSecond` in, and why Q8 has to be answered before Unit 6 lands rather than after. The other risk is entirely in getting the fingerprint format wrong and not noticing, which is why the literal test is load-bearing.

### Unit 2: the capability gate

**What changes.** A features read in the payload builder, and a `/stats` probe placed per Q3 so the features are known before the first possible hot-apply. `Manager.Features(id)` (`internal/mtproto/manager.go:933-941`) gets its first production consumer. Only three strings ever need testing: `speed-limits`, `ip-limits` and, if unit 4 wants it, `client-ips`.

| File and anchor | Change |
| --- | --- |
| `internal/mtproto/manager.go:835-848` | `secretsPayload` consults the cached features before emitting `speed` / `ip_limit` |
| `internal/mtproto/manager.go:403-470` | the probe, placed **outside** the `scrapeMu`/`mu` hold per §3.3 and Q3 |
| `internal/cores/internal/mtproto/driver_test.go:185-196` | **the rig's `writeStats` must emit a `features` array.** Today it emits only `{started_at, users}`, so with the gate in place every driver-level test sees an empty feature set and the manager emits neither key — silently, and the unit test below would not catch it. Add `features` to the marshalled struct and a rig knob to set it. |
| `internal/cores/internal/mtproto/driver_test.go:78-90` | **The fake binary decodes the PUT body only far enough to list secret names and discards the rest** (`var body struct { Secrets map[string]json.RawMessage }`, then `recordServed(names)` and a 200), so the driver-level assertion below has nothing to read. Have the fake write the last PUT body to a file named by a new environment variable, following the `MTG_FAKE_STATS` / `MTG_FAKE_SERVED` pattern at `:70` and `:180`, and read it back in the test. |

**Acceptance test.** A table test over three feature sets (`{}`, `{"speed-limits"}`, `{"speed-limits","ip-limits"}`) asserting the exact JSON body for each. Red-proof by removing the gate and watching the empty-feature case emit `speed`. That is a pure unit test of `secretsPayload` and will **not** catch a rig that publishes no features, which is why the rig row above is not optional: once the rig can publish features, add a driver-level assertion that a hot-apply against a feature-complete fake carries `speed` and `ip_limit`, and one against a feature-empty fake carries neither and still returns 200.

**What could break.** The probe must not block process start on a slow or dead API, and must not be taken while `Reconcile` holds both mutexes (§3.3). Bound it with the same 3s timeout `applySecrets` uses and treat a failure as "features unknown", which by strategy B means "omit and carry on" — with the whole-set-replace cost §3.3 spells out.

### Unit 3: the mapping

**What changes.** `internal/cores/internal/mtproto/convert.go:44-48`, the `engine.SecretEntry` composite literal, gains `IPLimit: u.LimitIP`. (Not `:51-54`: those lines are the closing brace of the `if u.QuotaBytes > 0` block and the `if u.ExpiryUnixMilli > 0` block, and an edit there produces a conditional assignment.) The speed value has no home on the user record — `core.User` at `internal/core/core.go:97-106` has no rate field — and arrives instead through the rate-enforcer path in unit 6, so **do not** invent a user field for it here.

**`InstanceFromInbound` should be left alone.** Both "bring it into line" and "delete it" fail the fix-size rule, and deleting it is more expensive than it looks:

- Deleting it removes the only `model.MTProto` reference in the package (`internal/mtproto/manager.go:235`), taking that file's dispatch count 1 → 0. That means moving **three** ratchet numbers, not two: `dispatchBudget` at `internal/arch/dispatch_ratchet_test.go:56` to `0`, and `dispatchTotal` at `:66` from 44 to 43.
- And then a **third** site fails: the `frozen sites are still budgeted` subtest at `internal/arch/dispatch_ratchet_test.go:195-201` errors for any file in `frozenDispatch` whose budget is zero, and `internal/mtproto/manager.go` is listed at `:74` with the rationale "a core naming its own kind, which stays correct after the refactor". Deleting the function means deleting that entry and arguing against a rationale the guard already states.
- It is also not dead. Two service tests use it as their seed-and-prime helper (`internal/web/service/inbound_mtproto_apply_test.go:45`, `internal/web/service/mtproto_reset_keeps_running_test.go:37`) and three engine tests exercise it directly. Deleting it means rewriting all five.
- "Bringing it in line" is no cheaper: it would need a `limitIp` field on the anonymous `Clients` struct at `internal/mtproto/manager.go:282-289`, for a function with no production caller.

The right answer is to do neither, and to record in the commit body that the divergence is known and deliberate: `toEngine` is the production path and carries `IPLimit`; `InstanceFromInbound` is a test-priming helper and does not.

**Acceptance test.** A new adapter test for `toEngine` covering the quota, expiry **and** ip-limit mapping. `toEngine` itself is already exercised (`internal/cores/internal/mtproto/driver_test.go:362`, `:446`), but no test in the package mentions `QuotaBytes`, `ExpiryUnixMilli` or `LimitIP` — so this is the first test of the limit mapping specifically, and that absence is itself a finding. Red-proof by reverting the `IPLimit` line.

**What could break.** Nothing structural, given the function is left alone. If Q10 or a later change reverses that, the three ratchet sites above move together in one commit.

### Unit 4: SessionReporter

This is the unit that makes panel-side IP limiting real for mtproto, and it works against the binary shipping today (§1.4).

**What changes.**

| File and anchor | Change |
| --- | --- |
| `internal/mtproto/manager.go:698-777` | Emit `track-client-ips` in `renderConfig`, as a top-level scalar above the first `[section]` header |
| `internal/mtproto/manager.go:101-119` | Add it to `StructuralFingerprint`, because toggling it must restart — **unless Q7 resolves to option A**, in which case the value never varies and does not belong in the fingerprint at all |
| `internal/mtproto/manager.go:808-812` | `statsUser` gains `ConnectedIPs []string \`json:"connected_ips"\`` and `LastSeen *time.Time \`json:"last_seen"\`` |
| `internal/mtproto/manager.go:163-165` | **`managed` gains a field beside `features`** holding the last scrape's per-user addresses and last-seen. Without it the widened `statsUser` parses the addresses and discards them: `CollectTraffic` stores only `features` (`:571-575`) and `billReadings` (`:586`) returns only byte deltas. |
| `internal/mtproto/manager.go:571-575` | `CollectTraffic` populates that field under the same identity check that guards `features` |
| `internal/mtproto/manager.go:933-941` | A **whole-manager** accessor beside `Features` that walks every running process under `m.mu` and returns a clone keyed by inbound id and email. `Features(id)` is per inbound, but `Sessions(ctx)` takes no instance (`internal/core/caps.go:442-443`) and answers for the whole core (`internal/web/service/core.go:114-115`), so copying `Features` literally produces an accessor the core cannot call |
| `internal/cores/internal/mtproto/core.go` | Implement `Sessions`, served from the cached scrape (see the timing box). The mapping into `core.Session` (`internal/core/caps.go:416-430`): one `Session` per (users-map key, `connected_ips` entry) across every process; `Email` is the users-map key, which is the client email (`internal/mtproto/manager.go:30-32`); `Source` is `netip.ParseAddr(entry)`, an unparsable entry skipped; `Local` is nil, the honest L7 answer, and `checkSessionLocal` skips when it is empty (`internal/core/coretest/suite.go:632-634`); `StartedUnixMilli` is 0; `LastSeenUnixMilli` is Q9's answer; a user with `connections > 0` and no `connected_ips` yields no `Session` at all |
| `internal/cores/internal/mtproto/driver_test.go:290-300` | Add the `FeedSession` hook to the Rig |
| `internal/cores/internal/mtproto/driver_test.go:133-137`, `:185-196` | **Widen the rig's fake stats.** `fakeUser` gains `connected_ips` and `last_seen`; `writeStats` marshals them. Until it does, `FeedSession` has nothing to write into and the suite's session cases are unverifiable. |
| `internal/web/service/core.go:32-40` | The `SessionKinds` doc comment names mtg as the no-sessions example; reword |
| `internal/core/caps.go:435-437` | Same sentence in `SessionReporter`'s own doc comment |
| `frontend/src/test/ip-limit-unsupported.test.ts:5-18`, `:41` | The prose block and the inline comment name mtg as the reason the guard exists. Reword them; **do not change the four assertions** — they are hand-written literals and do not move (§4.4). |

> **The timing constraint and the conformance rig disagree, and one of them has to give.** The panel side wants `Sessions` to serve from the cached last scrape and never issue a blocking HTTP call: `CorePolicyJob` calls it every ten seconds with `context.Background()`, so there is no deadline, and `CollectTraffic` holds `scrapeMu` across its own HTTP requests (`internal/mtproto/manager.go:541-543`). But `checkSessions` (`internal/core/coretest/suite.go:583-621`) calls `rig.FeedSession(...)` at `:602` and then `subjectSession` → `bound.Sessions.Sessions(ctx)` at `:707-708` **back to back**, with no traffic pass in between, and demands `LastSeenUnixMilli` come back byte-exact — twice, with the second value advanced (`:606-618`). Nothing between the two refreshes any cache. The existing `FeedTraffic` hook does not solve this by analogy: `r.feed` (`internal/cores/internal/mtproto/driver_test.go:213-219`) writes the stats file and waits for readiness, and the *traffic* read path scrapes on demand. So either the mtproto rig's `FeedSession` writes the stats file **and then drives `Manager.CollectTraffic()`** to refresh the cache before returning, or `Sessions()` scrapes on demand and the no-blocking-HTTP constraint is dropped. Pick one and state it in the commit. The first is cheaper and keeps the production constraint; it makes the rig, not production code, responsible for the refresh.

> **The same tension applies to Unit 6.** `checkRates` (`internal/core/coretest/suite.go:541-577`) calls `EnforcedRates` immediately after `EnforceRates`. If `EnforcedRates` gates on `speed_limit` from the last scrape (Q4 option A), the cache is up to five seconds stale and the rig's fake `/stats` emits no `speed_limit` at all. Resolve both units the same way.

**Generated-artifact fallout.** The `internal/web/service/core.go` doc comment is mirrored into `frontend/src/generated/schemas.ts:1868` and into both `openapi.json` copies. Rewording it requires `make gen` plus the unenforced `docs/public/openapi.json` copy and `pnpm gen:api`. No `StructAllow` edit is needed — see §9.5.

**Acceptance test.** The conformance suite's `sessions/verifiable` case, plus a manager-level test that a scrape without `track-client-ips` yields counts and no addresses. Red-proof by declaring `SessionReporter` without the `FeedSession` hook and watching `internal/core/coretest/suite.go:586-588` fail.

**What could break.**

- `track-client-ips` is a **structural** key, so toggling it restarts the process and drops connections. It is also a privacy change: `last_ip` outlives the connection (`mtg:mtglib/proxy_stats.go:637-640`). See Q7.
- **With `track-client-ips` off, `connected_ips` is absent**, so the core has no `Source` for any session. Whatever `Sessions()` then returns — an empty slice, or sessions with an invalid `Source` — the client never reaches a verdict: `ObserveSessions` skips any session with `!session.Source.IsValid()` at `internal/web/service/policy.go:489-491`, and a client absent from `scan.ByEmail` is never evaluated. **The IP limit silently stops applying.** Which of the two shapes `Sessions()` returns must be stated, and the operator-facing consequence belongs in Q7's copy.
- **Every session for one client carries the same timestamp**, because mtg keeps one `last_seen` per secret (`mtg:mtglib/proxy_stats.go:204-206`) and `connected_ips` has no per-address timestamps (`mtg:mtglib/address_limit.go:326`, `mtg:mtglib/proxy_stats.go:647`). `ObserveSessions` feeds that value straight into `policy.Observation.LastSeenUnixMilli` (`internal/web/service/policy.go:492-501`), and the contract at `internal/core/caps.go:424-426` treats a frozen value as a dead connection (the suite's failure text at `internal/core/coretest/suite.go:608` calls it "what the ban dedup compares"). A device that went away minutes ago reads as freshly seen for as long as any other device of the same client is active. That is a semantic choice, not a bug to route around silently — see Q9.
- The IPv6 over-count from §2.2 lands here too: `connected_ips` is deduplicated per full address, so a privacy-extensions IPv6 client presents as several sessions to `policy.Decide` while counting as one place to the sidecar.

### Unit 5: external-ips

**This unit is new architecture, it may not belong in this document at all (Q10), and it must be agreed before it is written.** There is no legal home for the push as the tree stands:

- `internal/web/service/policy.go` may not name a core; `internal/arch/policy_naming_test.go` enforces that.
- Cores may not import `internal/policy`; `TestCoresDoNotImportPolicy` (`internal/arch/imports_test.go:155`) enforces that.
- A new cron job is not available; `perCoreJobBudget` (`internal/arch/job_growth_test.go:26`) caps them.

The only clean shape is a new optional capability interface in `internal/core/caps.go`, a matching field on `core.Bound`, and exactly one assertion in `internal/core/bind.go` (`internal/arch/capability_assertions_test.go` permits capability assertions only there). Per CLAUDE.md, that is architecture and needs agreement first, so **say so in the pull request rather than shipping it next to the rest**.

Two things to weigh before agreeing, both of which feed Q10:

1. **It is a second enforcement layer for a limit Q1's recommendation already enforces.** Q1 option A puts `SessionReporter` plus the existing nft ban set in charge, and that path already works cross-node. Unit 5 adds a capability interface, a `core.Bound` field, a `bind.go` assertion, a `*Remote` whole-set method and node-sync wiring, in order to globalise the sidecar's *own* `ip-limit` — a layer that Q1 option C admits disagrees with the panel's on IPv6 units, in both the counting (§2.2) and the reporting (§2.2, `connected_ips`).
2. **The read-back the design leans on may not exist.** §2.3's gating row: `external_ips` and `external_expires_at` come back only when `track-client-ips` is on. With it off, the PUT's merged report is `{name, ip_limit, active_ips}` and there is nothing to diff. Unit 5 is therefore downstream of Q7 as well as Q2.

One implementation gift, if it goes ahead: the PUT returns the merged `AddressReport` (`mtg:mtglib/proxy_api.go:578-579`), so a push and its read-back are one round trip. Use it to verify convergence rather than following with a GET — **but only under Q7-on**.

**What could break.** Every push moves a whole set with one TTL (`mtg:mtglib/address_limit.go:176-181`). A cross-node design that pushes from more than one place will have the last writer win and the others silently discarded — see Q6.

### Unit 6: RateEnforcer

**What changes.** `EnforcesRates(kind) == Kind`, an `EnforceRates` implementation, and an `EnforcedRates` read-back. `ConvergeRates` (`internal/web/service/policy.go:380`) already calls this every ten seconds from `CorePolicyJob` (`internal/web/job/core_policy.go:57`), and `PolicyService.EnforcedFor` already reads it back, so no job or service wiring is needed.

**What calls it, with what, and what it must do synchronously.** The signature is `EnforceRates(ctx context.Context, inst Instance, limits map[string]RateLimit) error` (`internal/core/caps.go:398`). `ConvergeRates` builds `limits` keyed by email **only for clients whose `policy.Evaluate` pair is non-zero** (`internal/web/service/policy.go:426-438`; an absent email means unlimited) and calls it once per `DesiredInstances` instance every ten seconds, changed or not, including with an empty map. The conformance suite calls `EnforceRates` and then `EnforcedRates` **back to back** with no rig hook between them, then `rates/absent-is-unlimited`, then an empty-map call demanding `len(held) == 0` (`internal/core/coretest/suite.go:553-577`), and the fake binary's `/stats` is a file nobody rewrites between those calls (`internal/cores/internal/mtproto/driver_test.go:93-99`). Three obligations follow. `EnforceRates` must diff the incoming map against the manager-side table and, **only on a change**, call `m.Ensure(inst)` (`internal/mtproto/manager.go:355-362`, into `ensureLocked`) synchronously, so the limit is applied before the call returns; calling `Ensure` unconditionally is a whole-set PUT per inbound every ten seconds. `EnforcedRates` must either scrape on demand or drop Q4's liveness gate, because the cached `speed_limit` cannot be fresh inside the suite. And the fake binary must reflect a PUT's `speed` into the `speed_limit` it serves on `/stats`, or the read-back can never be non-empty under test.

| File and anchor | Change |
| --- | --- |
| `internal/cores/internal/mtproto/core.go` | `EnforcesRates`, `EnforceRates`, `EnforcedRates` |
| wherever Q8 puts it | the manager-side rate table, and the point at which it is merged into `engine.Instance.Secrets` **before** `StructuralFingerprint`/`SecretsFingerprint` are computed at `internal/mtproto/manager.go:404-405` |
| `internal/mtproto/manager.go:808-812` | `statsUser` gains `SpeedLimit int64 \`json:"speed_limit"\``, so the read-back has a liveness signal to gate on |
| `internal/mtproto/manager.go:163-165`, `:571-575`, `:933-941` | the per-inbound cached `speed_limit`, populated by `CollectTraffic` and read through an accessor mirroring `Features` |
| `internal/cores/internal/mtproto/driver_test.go:133-137`, `:185-196` | the rig's `fakeUser` gains `speed_limit` and `writeStats` marshals it, or `checkRates` fails on a fake that reports none |

**The conformance tension.** `internal/core/coretest/suite.go:553-564`, the `rates/reads-back` case, demands that a pushed `{UpBps: 1_000_000, DownBps: 2_000_000}` read back **exactly**, at `:563-565`. mtg holds one combined number and exposes it only as `speed_limit`. See Q4; and note the immediate-read-back timing problem in Unit 4's second box, which applies here identically.

**i18n: land the reword separately and land it first.** Four keys carry kernel-shaping wording — `internal/web/translation/en-US.json:1195` ("Speed limits are enforced in the kernel, and only for these protocols"), `:1196`, `:1206` ("Held by the kernel") and `:1207`. These are **already wrong today**, before mtproto exists as a rate kind: `shapingForKinds` folds `rateKinds` into the shapeable set at `frontend/src/lib/cores/client-shaping.ts:26` and `shapeableKinds` does the same at `:45`; SSH is already a `RateEnforcer` (`internal/cores/internal/ssh/core.go:126`) and is already surfaced through those same alerts, with the fixture at `frontend/src/test/client-limits.test.tsx:33` setting `rateKinds: ['ssh']`. Bundling a pre-existing wording bug into a feature unit is the thing CLAUDE.md's "refactors are separate from feature work" rule forbids. **Move the reword into its own `fix(i18n)` commit that can land before any of units 1-7.** Keep the two constraints: both `en-US.json` and `fa-IR.json` change in the same commit, and every key stays referenced from `frontend/src` or Go, or `frontend/src/test/i18n-dead-keys.test.ts` fails both ways.

### Unit 7: documentation

`docs/multi-core-architecture.md:2885` still records both capabilities as work the fork has yet to do — "MTProto is the user's fork's to do, both the speed cap and the device cap". Correct it. Remember the fourth, unenforced step for any endpoint or schema change: copy `frontend/public/openapi.json` to `docs/public/openapi.json`, then run `pnpm gen:api` in `docs/`. `docs/lib/xray/` holds a third independent implementation of link and subscription generation, but nothing here changes share-link output, so it is untouched.

---

## 6. Design decisions the user must make

### Q1. Which IP-limit enforcement mode should mtproto declare?

`ipLimitEnforcementOf` (`internal/web/service/core.go:80-89`) can return `"login"`, `"cut"` or `"firewall"`, and it is only consulted for a core that bound `SessionReporter` (`internal/web/service/core.go:124-127`). Both `"login"` and `"cut"` additionally require `SessionCutter`. mtg-proxy has **no** cut-by-source endpoint and **no** synchronous admit hook, so with `SessionReporter` alone the only declarable mode is `"firewall"`.

| Option | Consequence |
| --- | --- |
| **A. `"firewall"`** via `SessionReporter` only | The panel sees the sessions, `policy.Decide` runs, and the nft ban set in `internal/banset` does the enforcing. Works cross-node with the existing ban push. **But** `"firewall"` mode disables the UI field on a host with no usable nft table, and mtg's IPv6 /64 unit disagrees with the panel's per-address `Decide`. |
| **B. Push `ip-limit` into the sidecar and declare nothing** | Enforcement happens inside mtg, per process, with correct MTProto semantics and a real refusal counter. But it is **per inbound**, invisible to `CoreView`, and the panel's UI has no way to express it. |
| **C. Both A and B** | Two independent layers with **two different unit mismatches, not one.** The sidecar counts an IPv6 /64 as one place (`mtg:mtglib/address_limit.go:87`) while `connected_ips` — the array the panel's own layer counts from — deduplicates on the full address (`mtg:mtglib/address_limit.go:402`). So for a privacy-extensions IPv6 client the panel over-counts relative to the sidecar *and* relative to reality, and a user can be refused by the sidecar for reasons the panel cannot explain, or banned by the panel while the sidecar was content. |
| **D. Ask the fork for a cut endpoint, then declare `"cut"`** | The clean answer. Needs fork work (see section 7, ask #3). |

**Recommendation: A now, D next.** Land `SessionReporter` and declare `"firewall"`, which needs no new release and reuses the existing cross-node ban machinery. Push `ip-limit` into the sidecar only as an explicitly labelled second layer once the panel's own layer is proven, and only after the /64 mismatch — both halves of it — is settled.

### Q2. Should the panel push external IPs every tick, and at what cost?

The union endpoint is what makes the sidecar's address limit global rather than per process.

**This question is downstream of Q7.** Options C and D both need to diff the pushed set against what the sidecar reports, and `external_ips` / `external_expires_at` are returned **only when `track-client-ips` is on** (`mtg:mtglib/address_limit.go:373-381`). With Q7 resolved to "off" or "off by default", only options A and B are reachable at all.

| Option | Cost | Consequence | Needs Q7 on? |
| --- | --- | --- | --- |
| **A. Never push** | Zero | The sidecar limit is per inbound, per node. Fail-open, which is the current behaviour. | No |
| **B. Push every CorePolicyJob tick (10s) with ttl 30s** | One PUT per secret per inbound per tick. On 500 clients across 3 inbounds that is 1500 loopback PUTs every 10 seconds. | Correct within 10s. Each request is cheap and loopback-local, but the cardinality grows with client count and it is one round trip **per secret**, not per inbound. | No |
| **C. Push only for clients whose external set changed, ttl 3x the tick** | Amortized near zero in steady state | Needs the caller's own candidate set deduplicated by /64 **before** pushing (`mtg:mtglib/address_limit.go:176-181`), then compared as full address strings (`:206`) — see §2.3. Loses the set on a sidecar restart until the next change, so it needs a periodic full resync. | **Yes** |
| **D. Push on change plus a full resync every N ticks** | Low | Correct and bounded. | **Yes** |

**Recommendation: D if Q7 resolves on; A otherwise.** With D, set the TTL to three times the resync interval so a missed tick does not open the limit, capped at the sidecar's one-hour maximum (`mtg:mtglib/address_limit.go:53`). Do not adopt B; the per-secret cardinality is the problem, not the per-request cost. Whichever is chosen, the diff **must** dedup by /64 before pushing and compare full addresses on read-back, and should use the merged report the PUT already returns rather than a separate GET.

### Q3. What should the panel do against an old binary: hide the field, or record it but leave it unenforced? And where does the features probe run?

Two coupled sub-decisions.

**(a) The UI behaviour.**

| Option | Consequence |
| --- | --- |
| **A. Hide the field in the UI when the running binary lacks the feature** | Honest. But the feature list is per process and per inbound while the UI is per client, so "which binary" is ambiguous on a mixed deployment, and it flickers during the unknown window. |
| **B. Record the value, render it into TOML always, gate only the PUT** | The value is stored, survives, and starts working the moment the binary is upgraded and the process restarts. On an old binary the widened TOML is accepted **silently** — no warning, no error (§3.1). |
| **C. Record it and surface a warning badge when any attached inbound's sidecar lacks the feature** | B plus honesty, at the cost of plumbing the feature list up to the API and the UI. |

**Recommendation: B now, C if the mixed-version case becomes real.** B is the smallest change that is never wrong, and it matches the strictness asymmetry in section 3: the config file forgives, the API does not. Note that B's residual cost is not "the limit is late" but "the next hot-apply in a features-unknown window clears a limit the config file set", because `PUT /secrets` is a whole-set replace (`mtg:mtglib/proxy_api.go:686-747`) — see the box in §3.3.

**(b) Where the probe runs, and how hard it tries.** §3.3 establishes that it cannot go where it reads naturally: `Reconcile` holds `scrapeMu` and `mu` across the whole `ensureLocked` loop (`internal/mtproto/manager.go:491-495`), and a probe after `proc.Start()` (`:453`) blocks `Features`, `RoutedTags`, `CollectTraffic`, `Health` and `Remove` for its duration times the number of instances started. A just-started sidecar also will not answer the first request. So:

| Option | Consequence |
| --- | --- |
| **i. After `Reconcile` releases both mutexes, synchronous, N retries over a bounded window** | Closes the unknown window for real. Adds a post-pass to `Reconcile`'s caller. Needs an explicit retry budget: how many, over how long. |
| **ii. Asynchronous, features left empty until it lands** | No lock contention, no added latency. But it *is* strategy B with extra steps: the window still exists, just shorter than 5-10s. |
| **iii. Do not probe; rely on the 5s traffic tick** | Nothing new to build. Window is up to 5 s after a sidecar restart, and up to 10 s only for sidecars started during panel boot, since the one-off 5 s delay at `internal/web/web.go:390-393` runs once at panel start, not per sidecar. |

Given the window is 5s per tick and not 10s, option iii is a more defensible default than it looked. Whichever is picked, **state it in the commit** — a probe of unstated placement is the kind of thing that lands inside a mutex by accident.

### Q4. How do panel speed tiers map to the sidecar's unit?

The panel's tier ladder carries a separate up and down rate. mtg carries **one combined bucket** and reads it back as a single `speed_limit`. `coretest`'s `rates/reads-back` (`internal/core/coretest/suite.go:553-565`) requires an exact round trip of the pushed pair.

Two facts frame every option. The ladder is in **bits per second** (`internal/core/caps.go:382-384`) and mtg's bare `speed` is **bytes per second** (`mtg:mtglib/secret_limits.go:171`), so the pushed value is the chosen bit rate **divided by 8** and the read-back is multiplied by 8, as the SSH core already does (`internal/cores/internal/ssh/server.go:564`, `:540`). And a pair with both directions zero never arrives, since `ConvergeRates` skips it (`internal/web/service/policy.go:430-434`), but **one** direction can be zero and means unlimited in that direction, so every "min" below is the minimum over the **non-zero** directions; a literal `min(up, down)` would push 0, that is no limit at all, for a client whose plan caps one direction.

| Option | Pushed value | Read back | Conformance |
| --- | --- | --- | --- |
| **A. Push the min of the non-zero directions ÷ 8, read back the stored pushed pair, gated on `speed_limit` being non-zero on the sidecar** | min(non-zero) / 8 | the stored pair | **Passes** — but on a technicality; see below. |
| **B. Push `up + down`** | the sum | | Wrong semantically: mtg's bucket is already shared across directions (`mtg:mtglib/counting_conn.go:113`, `:135`), so summing doubles the real allowance. |
| **C. Push the same, read back `{combined×8, combined×8}`** | min(non-zero) / 8 | `{combined×8, combined×8}` | **Fails conformance.** |
| **D. Ask the fork for separate up and down keys** | the true pair | the true pair | Passes honestly, needs fork work. |

**Recommendation: A, stated plainly for what it is.** Do not justify it as "the shape SSH already uses"; that is backwards. SSH's `enforcedRates` (`internal/cores/internal/ssh/server.go:520-532`) prefers the **live limiter** and falls through to the stored table only when no live limiter exists yet: `if pair, live := s.limiters[email]; live { l = core.RateLimit{UpBps: bpsOf(pair.up), DownBps: bpsOf(pair.down)} }` at `:524-527`. SSH reads the live object precisely so a stored-but-unenforced number cannot read as enforced. Option A does the opposite: it reports the **stored** pair with only a *presence* check on the live one, because mtg's single combined `speed_limit` makes an exact live read-back of an up/down pair impossible. That is weaker than SSH's, and it is the direction the suite's own comment warns about — "Read back rather than trusted … a core that stores the numbers and throttles nothing looks the same from the push" (`internal/core/coretest/suite.go:536-539`). Keep A if the pragmatics win, but say so in the commit rather than claiming a precedent that runs the other way.

Emit the value as a **bare integer byte count in a quoted string**, `speed = "1250000"`, never a unit-bearing string: the unit grammar has three documented traps (`5MBps` is bits, `10Kb` is base-2 bytes, and there is no tera unit at all) and a bare byte count sidesteps all three.

**The floor has no panel boundary to live at.** The ladder is `policy.Limits`, shared by every `RateEnforcer`, and `ConvergeRates` forwards whatever `policy.Evaluate` yields with no per-core validation (`internal/web/service/policy.go:426-438`); a tier below 64 kbit/s is perfectly legal for SSH. mtg **refuses** anything under `MinSpeedBytesPerSecond`, 8000 bytes per second, which is 64 000 bits per second in contract units (`mtg:mtglib/secret_limits.go:165`, `:238-239`). If such a value is ever emitted: `applySecrets` gets a 400, §3.2's fall-through restarts the process, and the restarted process reads a TOML carrying the same `speed`, which §3.1 lists as fatal at startup. One tier value takes the whole inbound offline, on every reconcile, until the tier changes. So the clamp-or-skip lives **inside the mtproto core**, in `EnforceRates` or in `secretsPayload`/`renderConfig`: a pair below 64 000 bit/s is raised to the floor, or omitted and reported as unenforced, and is never emitted. Add a `manager_test` row for a sub-floor value.

### Q5. Should quota and expiry rendering change for the new ceiling semantics?

The real question is not the rendering format, which is already correct, but **what number to render**.

The panel renders the client's **total** quota (`internal/mtproto/manager.go:850-852`), and it emits no `usage-state-file` (pinned absent at `internal/mtproto/manager_test.go:225`), so **the sidecar's `quota_used` zeroes on every restart**. Since v1.8.0 that means a restart hands the client a fresh full ceiling.

| Option | Consequence |
| --- | --- |
| **A. Keep rendering total** | A restart resets the sidecar ceiling. The panel's own accounting is still authoritative and still cuts the client off, so the leak is bounded by the panel's traffic-job interval (5s), not unbounded. Fingerprint stays stable. |
| **B. Render remaining at render time** | Correct after a restart. **But** the remaining value changes on every traffic tick, so `SecretsFingerprint` (`internal/mtproto/manager.go:126-133`) moves every tick, so the panel hot-applies every tick, forever. |
| **C. Render total, and emit `usage-state-file`** | Correct across restarts with a stable fingerprint. Adds a file the panel must own, place and clean up, and reverses a decision a test currently pins. |

**Recommendation: A now, C as a follow-up.** B is disqualified by the fingerprint churn — and note that B is the same failure mode Q8 describes for speed, arriving by a different door.

### Q6. Who owns the push in a cross-node deployment?

Core capability calls run against the **local** registry only. Whole-set pushes such as `PushBans` and `PushPlanAssignments` are bolted onto `*Remote` and driven by the node traffic sync job. `Remote.CutSessions` is a node-side no-op.

**Option A is two hops, not one.** It does not simply mirror `PushBans`, because `PushBans` never reaches a sidecar: `Remote.PushBans(ctx, masterGuid, rows []model.ClientBan)` (`internal/web/runtime/remote.go:878-884`) POSTs to `panel/api/policies/pushBans` on the **node's panel** over mTLS, driven from `internal/web/job/node_traffic_sync_job.go:529`, and the node's panel then applies the rows locally. For external-ips the node panel would then have to reach its own mtg sidecars — and `TestTheWebLayerReachesPortedEnginesThroughTheRegistry` (`internal/arch/imports_test.go:116-139`) forbids any file under `internal/web/` from importing `internal/mtproto`. So the node-side apply has to go back out through the registry, using the same new capability interface Unit 5 proposes.

| Option | Consequence |
| --- | --- |
| **A. Master computes the union and pushes to each node, which applies it locally through the registry** | **Two hops and two new seams:** (1) a new `*Remote` whole-set method carrying the union to the node's panel, mirroring `PushBans`; (2) a node-side call from `internal/web/` into the new capability on `core.Bound`, because the direct import is fenced. One writer, no last-writer-wins race, but the cost is roughly double what a one-hop reading suggests. |
| **B. Each node pushes its own locally observed addresses** | No master change, no new `*Remote` method — but then it is not a union, it is what each node already knows locally, which is the fail-open status quo. |
| **C. Master pushes, nodes also push locally** | Last writer wins on a whole-set replace (`mtg:mtglib/address_limit.go:176-181`). Non-deterministic. Rejected. |

**Recommendation: A, re-costed.** It is the established pattern and it keeps a single writer with one TTL owner. But it is the same piece of new architecture flagged in Unit 5, it needs the same agreement, and it is a second argument for Q10.

### Q7. Should `track-client-ips` be on by default, and how does the setting reach the core?

Three things are bundled here: privacy, a restart, and — new — a plumbing route that does not exist yet.

- **Privacy:** `last_ip` **outlives the connection** (`mtg:mtglib/proxy_stats.go:637-640`), so the panel would be storing a client's last-seen address where it previously stored only counts.
- **Operational:** it is a **structural** config key, so flipping it restarts the process and drops every connection on that inbound.
- **Functional:** with it **off**, `connected_ips` is absent, so mtproto sessions have no `Source`, so `ObserveSessions` drops them (`internal/web/service/policy.go:489-491`) and **the IP limit silently does nothing** — the same failure the whole of Unit 4 exists to remove. Off is not "degraded to counts-only"; for IP limiting it is off entirely.

| Option | Consequence |
| --- | --- |
| **A. Always on** | `SessionReporter` always works. One forced restart at upgrade. Always retains addresses. **And** the value never varies, so it must *not* go into `StructuralFingerprint` — Unit 4's table assumes it does, which is only right under B or C. |
| **B. On only when the inbound has an IP limit configured** | Minimal retention. But the flag then toggles with a client edit, and each toggle is a connection-dropping restart. |
| **C. A panel-wide setting, default on** | One restart when the operator flips it, explicit consent, and the IP limit stops applying (visibly, one hopes) when off. |

**Recommendation: C — but it is not free, and the cost was not previously stated.** Option C needs a plumbing route the tree does not have:

- `core.Instance` (`internal/core/core.go:77-88`) carries `ID/Kind/Tag/Listen/Port/Enable/Settings/StreamSettings/Sniffing/Users` and nothing panel-wide, so `toEngine` cannot see a global setting.
- The core cannot read it directly either: `TestCoreEnginesDoNotImportTheWebLayer` (`internal/arch/imports_test.go:80-108`) fails any file under `internal/mtproto/` or `internal/cores/` that imports `github.com/Arman2122/p-ui/internal/web`, which is where `SettingService` lives.
- The cheap route is a new field on `Deps` (`internal/cores/internal/mtproto/core.go:38-46`), set at the single registration site `internal/cores/cores.go:262`. **But `Deps` is a value struct filled once at boot**, so a plain `bool` there would only be read at startup and a toggle would need a panel restart to take effect. It must be a `func() bool`, following the `HTTPClient func(...) *http.Client` pattern already in that struct, if the setting is to be live.
- Either way the setting itself needs an `entity.AllSetting` field, a `defaultValueMap` entry (`internal/web/service/setting.go:44`), `make gen`, and a label plus help text in **both** `internal/web/translation/en-US.json` and `fa-IR.json`. It also needs a typed getter on `SettingService` for the `func() bool` to call, following the existing `GetXxx()` methods in that file, and a form control on the settings page under `frontend/src/pages/settings/`. None of that appears in Unit 4's change table or Unit 7, and it should.

Option A avoids all of that plumbing and is genuinely cheaper; what it costs is the operator's choice about retention. That trade — one panel-wide setting and its plumbing versus no choice at all — is the decision.

### Q8. Where does a per-client speed value actually live, and what stops the two writers clobbering each other?

**This is a blocking design question, not an implementation detail.** As units 1 and 6 are written above, they give speed two independent carriers on two independent ten-second loops, and the two overwrite each other:

- Unit 1 makes `SecretEntry.SpeedBytesPerSecond` the carrier and folds it into `SecretsFingerprint`.
- Unit 6 says `EnforceRates` "writes into a manager-side rate table". Nothing merges that table into the `engine.Instance` the supervise pass builds.
- There is a **third writer**, per client edit rather than per tick: `AddUser`, `RemoveUser` and `apply` on the core (`internal/cores/internal/mtproto/core.go:188-211`) reach `Manager.Ensure` (`internal/mtproto/manager.go:355-362`), which builds the instance through the same speed-less `toEngine` and funnels into the same `ensureLocked`.
- `CoreSuperviseJob` (`cadenceCoreSupervise = "@every 10s"`, `internal/web/web.go:349`) calls `bound.Supervise.Reconcile` (`internal/web/job/core_supervise.go:58`) with instances from `DesiredInstances`. The mtproto core runs them through `toEngine` (`internal/cores/internal/mtproto/convert.go:24-58`), which has **no speed input at all**, so every entry carries `SpeedBytesPerSecond == 0`.
- `ensureLocked` (`internal/mtproto/manager.go:403-418`) fingerprints that zero and PUTs a body with no `speed`. Per §3.3, that is a whole-set replace: the limit is **cleared**.
- Ten seconds later `CorePolicyJob` (`cadenceCorePolicy = "@every 10s"`, `internal/web/web.go:355`) runs `ConvergeRates` (`internal/web/service/policy.go:380`) and sets it again — a **fresh bucket with a full burst allowance**, twelve times a minute, forever.

§2.1 already says clearing the field destroys the bucket and that "rapid set, clear, set is therefore an exploitable pattern". This wiring *is* that pattern, on a timer.

| Option | Shape | Consequence |
| --- | --- | --- |
| **A. One table, merged before fingerprinting** | `EnforceRates` writes a manager-side table keyed `(inboundID, email)`. `ensureLocked` overlays it onto `inst.Secrets` **before** computing `StructuralFingerprint`/`SecretsFingerprint` at `internal/mtproto/manager.go:404-405`, so `renderConfig` and `secretsPayload` both see the same value on both passes. | `SecretEntry.SpeedBytesPerSecond` stays (Unit 1), the table is the single source of truth, and neither loop clobbers the other. All three entry points, `Reconcile` every ten seconds, `Ensure` on every client edit and `ConvergeRates` every ten seconds, see the overlay because it sits in `ensureLocked` and nowhere higher. **Units 1 and 6 become one dependency chain and stop being independently landable.** |
| **B. Speed gets its own apply path** | Drop `SpeedBytesPerSecond` from `SecretEntry` (`internal/mtproto/manager.go:35-41`) and from the Unit 1 fingerprint change. `EnforceRates` PUTs speed on its own, and `Reconcile` never touches the field. | Smaller diff to the supervise path. **But** a `PUT /secrets` is a whole-set replace, so a speed-only PUT would drop everyone's quota and expiry, and a supervise PUT would drop everyone's speed. This only works if the two never both PUT, which means the whole-set semantics have to be worked around — likely by making the rate path write the config file and reload, or by the fork accepting a merge verb. |
| **C. Speed lives on `core.User`** | Add a rate field to `core.User` (`internal/core/core.go:97-106`) and let `toEngine` read it, the way `LimitIP` will. | Conceptually cleanest and kills the two-writer problem outright. **But** it is a change to the shared core contract affecting every core, and the panel's tier ladder is evaluated by `PolicyService` from committed usage, not stored per client — so `DesiredInstances` would have to run that evaluation, which is a layering inversion. |

**Recommendation: A.** It fits the seams that exist, it costs one overlay call inside `ensureLocked`, and it makes the coupling explicit rather than accidental. Whichever is chosen, **Unit 1's "ships alone, no behaviour change" claim must be corrected in the same breath**: the ip-limit half ships alone; the speed half does not.

### Q9. What should a session's `LastSeenUnixMilli` be, given mtg keeps one timestamp per secret?

mtg stores last-seen on the **secret's** counter — `UpdateLastSeen(name)` at `mtg:mtglib/proxy_stats.go:204-206` — and `connected_ips` is a flat `[]string` with no per-address timestamps (`mtg:mtglib/address_limit.go:326`, `mtg:mtglib/proxy_stats.go:647`). There is no per-device liveness in the payload at all. So the mtproto core cannot answer the question `core.Session.LastSeenUnixMilli` asks, and the panel has to be told which lie to prefer.

The contract defines it at `internal/core/caps.go:424-426`: "a frozen value is a dead connection, not a reconnect. Zero means it cannot say." The phrase "the value is what the ban dedup compares" is the conformance suite's failure message (`internal/core/coretest/suite.go:608`), not the contract's wording. `ObserveSessions` copies the value into `policy.Observation.LastSeenUnixMilli` at `internal/web/service/policy.go:492-501`.

| Option | Consequence |
| --- | --- |
| **A. Every session for a client carries the client-level `last_seen`** | A device that went away minutes ago reads as freshly seen for as long as *any* other device of that client is active. Ban dedup then never expires a stale address, so an address is re-banned or held banned on the strength of a different device's activity. A second cost: mtg refreshes `last_seen` **only at admission** (`mtg:mtglib/proxy_serve.go:193` is the only production `UpdateLastSeen` call, into `mtg:mtglib/proxy_stats.go:205`), so one long-lived connection reads as frozen, which the contract defines as dead. |
| **B. Report `0` and let the panel stamp `now`** | `ObserveSessions` already does exactly this for a core that cannot say: `if seen <= 0 { seen = nowMilli }` at `internal/web/service/policy.go:493-497`, with the comment "the core is reporting the connection as live by reporting it at all". Honest about what mtg knows, **but it fails the conformance suite as it stands**: `sessions/lastseen-advances` feeds a timestamp and demands it back byte-exact, then advanced (`internal/core/coretest/suite.go:602-618`), and a core reporting 0 fails both checks. B therefore costs a change to the suite's contract, not nothing. |
| **C. Ask the fork for a per-address last-seen** | The only option that makes the value mean what the contract says. Fork work; see section 7. |

**Recommendation: A's shape is what the suite forces today, so land A unless the user agrees to change the suite for B; C is the honest ask.** It is the user's call because every option is a lie of a different shape: A makes a long-lived single connection look dead and keeps a departed device alive on a sibling's activity, B changes a contract every other core meets, and C waits on the fork. Whichever is chosen, `StartedUnixMilli` should be `0`: mtg reports no per-address open time, and the panel counts from when it first saw the address (`internal/core/caps.go:428-429`).

### Q10. Does Unit 5 belong in this document at all?

Recorded as a decision because sections 5 and 6 currently treat "it needs agreement before it is written" as the whole of the question, when the prior question is whether to write it.

The case for cutting it and recording it as a separate proposal:

- Q1's recommendation is option A, which enforces the IP limit through `SessionReporter` plus the existing nft ban set. That already works cross-node, through machinery that already exists.
- Unit 5 exists to globalise the sidecar's *second*, redundant layer — the one Q1 option C says disagrees with the panel's on IPv6, in both the counting unit and the reporting unit.
- Its cost is a new capability interface in `internal/core/caps.go`, a new field on `core.Bound`, a new assertion in `internal/core/bind.go`, a new `*Remote` whole-set method, node-sync wiring, **and** (per Q6) a second hop through the registry on the node side.
- Its central mechanism — diffing pushed against reported — does not work at all unless Q7 resolves to "on".
- CLAUDE.md: "a small bug does not earn new columns, jobs, abstractions, config knobs or helper layers."

The case for keeping it: the sidecar's own refusal is cheaper and more precise than an nft ban (it never opens the connection, and it is correct for MTProto's connection model), and a per-process limit on a multi-node deployment is close to useless without the union.

**No recommendation.** This one is genuinely the user's, and it determines whether §5's unit table has six rows or seven.

---

## 7. Asks of the mtg-proxy fork

Kept deliberately separate from panel work. None of these blocks the panel units above except where noted.

| # | Ask | Why | Suggested shape |
| --- | --- | --- | --- |
| 1 | **Publish releases for v1.4.0 through v1.8.0** | Blocks everything. Both panel download paths resolve a published release, not a tag. | Dispatch `mtg:.github/workflows/go.yml` against each tag ref; the `refs/tags/v*` gate, the `PKG` naming (`:99`) and the checksums naming (`:184`) are unchanged from v1.3.0 and already match what the panel expects (§1.2). |
| 2 | **Clear the account-wide anonymous 404** on `Arman2122` (§1.3) | Blocks everything, independently of #1, and blocks the panel's own installer, updater and self-update too. | Check `github.com/Arman2122` in a logged-in browser for a flag banner and open a GitHub Support ticket; then confirm `curl -sI https://github.com/Arman2122/mtg-proxy/releases/latest` answers 302 from a host with no GitHub credentials. |
| 3 | **A cut-by-source endpoint**, for example `POST /secrets/{name}/kick` | Unlocks `SessionCutter`, which unlocks the `"cut"` and `"login"` IP-limit modes (Q1 option D). Today the panel can only refuse future connections or use nft. | `{"ips":["203.0.113.10"]}`, closing every stream of that secret from those places, returning the count closed. |
| 4 | **Separate up and down speed keys**, or an explicit statement that combined is the contract | `coretest` demands an exact round trip of an up/down pair (Q4). Option A works around it with a stored value gated on a liveness signal, which is weaker than what the suite is asking for. | `speed-up` and `speed-down` alongside `speed`, with `speed` remaining the combined form. |
| 5 | **A feature string for the v1.8.0 ceiling and sweep semantics, and one for the per-secret read-back route** | There is neither, so the panel cannot distinguish "quota gates admission" from "quota cuts mid-stream", nor learn whether `GET /secrets/{name}` (v1.5.0+) exists, without parsing a version string. | Add `quota-ceiling` and `secret-read` to `supportedFeatures` (`mtg:mtglib/proxy_stats.go:525-536`). |
| 6 | **Allow clearing a deleted secret's external set** | All three external-ips verbs 404 on an unknown name (`mtg:mtglib/address_limit.go:292-294`), so a stale external set is unreachable until the secret is re-added. | Make `DELETE` succeed for an unknown name, or purge the external set when a secret leaves the set. |
| 7 | **Guarantee that `speed_limit` is present and non-zero if and only if a speed is configured** | The panel wants to confirm its push landed before reporting `EnforcedRates` (Q4 option A). A zero is currently ambiguous between unset and old-binary-ignored. | Documentation, or a distinguishing null. |
| 8 | **A per-address last-seen, or an explicit statement that `last_seen` is per secret** | `core.Session.LastSeenUnixMilli` is what the panel's ban dedup compares (`internal/core/coretest/suite.go:608`; the contract at `internal/core/caps.go:424-426`), and mtg has one timestamp per secret (`mtg:mtglib/proxy_stats.go:204-206`), refreshed only at admission (`mtg:mtglib/proxy_serve.go:193`), with none per address. Q9 is a choice between wrong answers only because of this. | A parallel map on `UserStatsJSON`: `connected_ips_last_seen`, address → timestamp, refreshed on traffic and gated exactly as `connected_ips` is. |
| 9 | **Correct the fork's own comment and wiki claim that the ip-limit refusal log omits `client-ip`** | It does carry it, bound on every stream logger at construction (`mtg:mtglib/stream_context.go:191-193`). The stale claim will mislead anyone building alerting on it. | Documentation only. |
| 10 | **A test for the `B` size suffix** in the config parser, and a corrected wiki row | The panel renders `"1073741824B"` on every quota today. It parses by inspection of `MakeUnitMap`'s `shortSuffix` entry, but nothing pins it. And the wiki's per-user limits table (`mtg:docs/wiki/Limits-and-Quotas.md`) says `quota` in the file accepts "a bare integer byte count", which is true of the API and false of the config file (§2.4). | One table row in the size-parse test, and the wiki row corrected. |
| 11 | **Consider making `connected_ips` deduplicate at the same unit the limit counts at**, or say plainly that it does not | The limit counts an IPv6 /64 as one place (`mtg:mtglib/address_limit.go:87`); `connected_ips` deduplicates on the full address (`:402`). Any panel counting devices from the array disagrees with the sidecar's own refusal for exactly the client the /64 mask exists to protect. | Either a second array at the counted unit, or one sentence in the wiki. |
| 12 | **Extend `mtg:internal/config/panel_contract_test.go`** with every key the panel starts emitting | That test pins the exact shape the panel renders so the two repositories notice drift; today it carries `[secrets]`, one `[[listener]]` and `[throttle]`, but no `[secret-limits]` table, no `speed`, no `ip-limit` and no `track-client-ips`. | Add the quoted-name form `[secret-limits."alice"]` with `quota`, `expires`, `speed` and `ip-limit`, plus `track-client-ips = true` at top level, and assert `Validate()` still passes. This also closes unverified items 1 and 2 in §10. |

A related question, whether `speed` is accepted over `POST /secrets` as well as `PUT /secrets`, is closed by inspection; see §10.

---

## 8. Verification plan

### 8.1 What already covers this

| Test | Covers | Gate |
| --- | --- | --- |
| `TestRenderConfigSecretLimits` (`internal/mtproto/manager_test.go`) | quota and expiry TOML emission, that a client with no limit gets no table, **and the section order** (`:222`) | none |
| `internal/mtproto/manager_test.go:225` | that `usage-state-file` is absent | none |
| `TestSecretsPayloadLimits` (`internal/mtproto/manager_test.go:230`) | the PUT body shape, including `"1073741824B"` | none |
| `TestFingerprintSplit`, and the literal at `internal/mtproto/manager_test.go:305` | which fields trigger restart versus hot-apply | none |
| `TestInstanceFromInbound` (`internal/mtproto/manager_test.go:10`), `usable_secret_test.go:82`, `web_backend_test.go:159` | the settings-blob mapping path | none |
| `TestUsersComeFromTheContractNotTheSettingsBlob` (`internal/cores/internal/mtproto/driver_test.go:362`) and the serve/error table at `:446` | that `toEngine` runs and returns the right `serve`/`err` — **but not the limit mapping**: `QuotaBytes`, `ExpiryUnixMilli` and `LimitIP` appear in no test file in that package | none |
| `TestMtprotoConformsToTheContract` (`internal/cores/internal/mtproto/driver_test.go:303`) | the whole core contract | **`requirePosixHost`, skips on Windows** |
| `mtg:mtglib/proxy_limits_test.go:172-189` (`TestSwapPreservesUsage`) | that `ReplaceSecrets` preserves `quota_used`. **One call, `p.ReplaceSecrets` at `:182-185`** — not the HTTP handler and not a file reload. It covers the shared path both surfaces funnel through, which is the useful claim; it does not cover two paths. | mtg repo |
| `mtg:mtglib/address_limit_test.go:19,50,129,192,251,309,354,387,429,531` | key normalisation, the ledger, external-address parsing, cross-entry counting, exactly-one-under-contention, reported addresses, fail-closed, swap without dropping streams, the API path, the reload path | mtg repo |
| `mtg:mtglib/speed_limit_test.go:137,158-170` | that the bucket is shared across both directions | mtg repo |
| `mtg:mtglib/maintenance_test.go:72` | the 5s deny sweep closes a live stream | mtg repo |
| `mtg:internal/config/type_ip_limit_test.go:60-73` | that a negative or quoted `ip-limit` is fatal at parse time | mtg repo |
| `internal/arch/dispatch_ratchet_test.go` | the dispatch budget (`:56`), the total (`:66`), the frozen list (`:74`) and that frozen sites stay budgeted (`:195`) | none |
| `internal/arch/imports_test.go:80,116,155` | that cores do not import the web layer, the web layer does not import `internal/mtproto`, and cores do not import `internal/policy` | none |
| `internal/web/routes_contract_test.go` `TestRouteRegistryContract` | endpoint registry parity | **`PUI_DB_DSN`, skips without it** |
| `frontend/src/test/ip-limit-enforcement.test.ts`, `ip-limit-unsupported.test.ts` | the frontend classification logic, driven by **hand-written `CoreView` literals** — they do not observe the Go registry and will not move when the core declares new capabilities | none |
| `frontend/src/test/client-limits.test.tsx:33` | the already-shaped-by-a-rate-kind case (SSH), which is why the four kernel-worded i18n keys are already inaccurate | none |
| `frontend/src/test/i18n-dead-keys.test.ts` | every locale key exists in both files and is referenced | none |

**The gap in coverage is narrower and more specific than "nothing covers `toEngine`":** the function runs in two tests, but nothing anywhere pins that a client's quota, expiry or IP limit reaches the engine. Unit 3 must add that test.

### 8.2 What needs writing

| Unit | New test | Red-proof |
| --- | --- | --- |
| 1 | Table over five clients (quota only, expiry only, speed only, ip-limit only, all four) asserting the exact rendered TOML block, **and that it still precedes `[secrets]`** | Revert the widened guard; the speed-only and ip-limit-only rows lose their table |
| 1 | Fingerprint test asserting speed and ip-limit changes move the string | Revert the format-string change; `internal/mtproto/manager_test.go:305` goes red on its own |
| 2 | Table over three feature sets asserting the exact PUT body | Remove the gate; the empty-feature case emits `speed` |
| 2 | A driver-level test, once the rig publishes `features`, that a feature-complete fake receives `speed`/`ip_limit` and a feature-empty one receives neither and still returns 200 | Revert the rig's `features` emission; the feature-complete case sees an empty set and stops emitting |
| 3 | `toEngine` mapping test for quota, expiry and ip-limit | Revert the `LimitIP` line; the ip-limit assertion fails |
| 4 | Conformance `sessions/verifiable`, plus a manager-level test that a scrape without `track-client-ips` yields counts and no addresses, plus a test that the `managed` address cache survives a scrape and is cloned by the accessor | Declare `SessionReporter` without `FeedSession`; `internal/core/coretest/suite.go:586-588` fails |
| 6 | Conformance `rates/reads-back` | Return `{combined, combined}` instead of the stored pair; `internal/core/coretest/suite.go:563-565` fails |
| 8 (Q8) | A test that two consecutive passes — a `Reconcile` and a `ConvergeRates` — produce the **same** PUT body for a client with a speed limit | Remove the merge from `ensureLocked`; the second body loses `speed` |

Every one of these must be red-proofed the way CLAUDE.md requires: write the test, revert the fix, watch it fail, restore the fix. A test that passes either way certifies nothing.

Any new test in `internal/cores/internal/mtproto/` that calls `newRig(t)` starts with `requirePosixHost(t)` (`internal/cores/internal/mtproto/posix_only_test.go:21-26`; `TestAddUserIsAppliedWithoutRestarting` at `driver_test.go:322-323` is the precedent), or it fails on the Windows development host instead of skipping.

### 8.3 What runs without a real binary

The mtproto rig runs a **fake** binary, and that fake is narrower than the real payload in three places (§4.5). Everything in units 1, 2, 3 and the shape of 4 is provable with `make test-go` and `npm test` on any host **once the rig's `writeStats` and `fakeUser` are widened to carry `features`, `connected_ips`, `last_seen` and `speed_limit`** — until then, the gate in Unit 2 and the sessions in Unit 4 are unverifiable at the driver level. Two further exceptions:

- The conformance suite skips on Windows via `requirePosixHost`. **Run it on Linux.**
- `TestRouteRegistryContract` and every controller and service DB test skip without `PUI_DB_DSN`. A green local run **proves nothing** for the endpoint registry.

Per-unit invocations, runnable anywhere unless marked:

```
go test ./internal/mtproto/ -run 'TestRenderConfigSecretLimits|TestSecretsPayloadLimits|TestFingerprintSplit' -count=1
go test ./internal/cores/internal/mtproto/ -run 'TestMtprotoConformsToTheContract|TestAddUserIsAppliedWithoutRestarting' -count=1   # Linux only
go test ./internal/arch/ -count=1
cd frontend && npx vitest run src/test/i18n-dead-keys.test.ts src/test/ip-limit-unsupported.test.ts src/test/ip-limit-enforcement.test.ts
```

The whole tree, with the flags CI and `make test-go` actually pass (`.github/workflows/ci.yml:113`, `Makefile:52`). Go's ten-minute default timeout only bites a bare invocation that omits `-timeout`; the scale tests are excluded the way CI excludes them:

```
make dist-stub
PUI_DB_DSN='postgres://...' go test ./... -shuffle=on -count=1 -timeout 30m -skip 'Scale$'
```

### 8.4 What needs the real binary on the Linux VM

The test VM is `ssh penhoon`, an Ubuntu 24.04 box; the sync and setup commands are in §9, rule 19. These are the things a fake binary cannot prove:

1. That `speed = "1250000"` in the rendered config is accepted by a real v1.8.0 binary and actually paces a transfer. Measure with a large download through the proxy and compare against the configured rate.
2. That an **old** binary handed the widened TOML **starts and runs, with no error**, rather than failing `validateSecretLimits()`. Assert exactly that and nothing more: on v1.3.0 the unknown keys are ignored **silently** (§3.1) — there is no `UnknownKeys` field, no `warnUnknownKeys`, no warning to look for. A check that greps for two warning lines will fail against the binary that actually ships. On a v1.8.0 binary, the same file should produce exactly two Warning lines, and that is a separate assertion against a separate binary.
3. That `ip-limit = 3` refuses the fourth distinct place and that the refusal appears as `ip_limit_refused` on `/stats`, with `client-ip` present in the log line.
4. That an IPv6 client's second address in the same /64 does **not** consume a second place — and, in the same run, that **both** addresses appear separately in `connected_ips`. That single observation is the concrete form of both halves of the panel-versus-sidecar unit mismatch in Q1 and §2.2.
5. That a `PUT /secrets` carrying `speed` against a v1.3.0 binary returns 400, and that the panel's fall-through therefore restarts the process. This is the failure mode unit 2 exists to prevent; prove it exists before proving the gate removes it.
6. That a `PUT /secrets` **omitting** `speed` against a v1.6.0+ binary whose config file set one clears the limit — the whole-set-replace cost in §3.3's box, which is the reason strategy B is not free.
7. That the external-ips PUT does not disturb connections; that its returned report carries **full** IPv6 addresses, not /64 prefixes; and that with `track-client-ips` off it carries no addresses at all.
8. Which binary version the VM and the production panel are actually running today. **UNVERIFIED**; check before drawing conclusions from either box.

### 8.5 The full local gate

```
make verify
```

runs gen-check, lint, typecheck, test, build and build-storybook. `gen-check` already runs `make gen` and fails on a dirty `frontend/src/generated` or `frontend/public/openapi.json`. It is the **fast** gate, not all of CI; when this work touches the parser or DB paths, also run `make race` and `make vulncheck` locally.

---

## 9. House rules the implementer must follow

These are not advisory. Several are enforced by tests that only run in CI, and one is enforced by nothing at all.

1. **Fix size must match bug size.** Find the root cause, then make the smallest change that removes it. Unit 5 is the only unit here that genuinely needs new architecture, and per this rule it must be **agreed before it is written** — Q10 asks whether it should be written at all — never shipped unasked alongside the rest. The same rule is why Unit 3 leaves `InstanceFromInbound` alone and why Unit 6's i18n reword becomes its own commit.
2. **Comments in committed Go and TypeScript: two lines maximum per block.** Make the name carry the meaning; rename rather than annotate. Spend the two lines on a why a name cannot hold. Build and generate directives are exempt.
3. **Every state-changing inbound or client operation dispatches through `runtime.Runtime`** (`internal/web/runtime/`). Never straight to `internal/xray/api.go`, never from a controller, never from a cron job. A direct call passes every local test and silently breaks every multi-node deployment.
4. **A new `g.POST` or `g.GET` in `internal/web/controller/` requires a matching entry in `frontend/src/pages/api-docs/endpoints.ts`, then `make gen`.** `TestRouteRegistryContract` pins it both ways, and that test **skips without `PUI_DB_DSN`**, so a green local run does not clear this. Nothing in units 1-7 as scoped adds a route; Q7 option C's setting rides existing setting endpoints.
5. **Response examples come from Go struct `example:` tags via `tools/openapigen`.** Never hand-write one. A new struct must be added to openapigen's `StructAllow` allowlist in `tools/openapigen/main.go` or it is silently omitted and `build-openapi.mjs` then fails on the missing schema. **`CoreView` is already in that allowlist (`tools/openapigen/main.go:108`)**, so a new field on it needs only `make gen` plus rule 6 — no allowlist edit.
6. **The fourth step nothing checks:** for a new or renamed endpoint, or any schema change, copy `frontend/public/openapi.json` to `docs/public/openapi.json`, then run `pnpm gen:api` in `docs/`. `docs-ci.yml` fires only on `docs/**`.
7. **A new English i18n key goes in every locale JSON in `internal/web/translation/`** (two files, `en-US` and `fa-IR`) **and** must be referenced from `frontend/src` or Go in the **same commit**. `frontend/src/test/i18n-dead-keys.test.ts` fails both ways, and it is a frontend test, so run `npm test`, not just `make test-go`.
8. **No database migration is required by any of units 1-7, and none should be written.** Speed arrives through `RateEnforcer` and a manager-side table (Q8); `LimitIP` is already on `core.User` (`internal/core/core.go:102-104`) and already persisted; Q7 option C's flag is a `Setting` key/value row reached through `defaultValueMap` (`internal/web/service/setting.go:44`), not a column. Consequently the frozen-columns guard (`internal/arch/client_columns_test.go`) is not in play either: no new per-client credential and no new column is added. If a later change does touch `internal/database/model`, CLAUDE.md requires a migration in `internal/database/db.go`.
9. **Do not edit `frontend/src/generated/`.** Regenerate it.
10. **Capability type assertions belong only in `internal/core/bind.go`.** An arch guard enforces it. `Bind`'s mutual-exclusion rules are **reported** by `DeclaredMatchesImplemented` and **enforced only by the coretest suite**, not by `Registry.Register`, so a violation shows up in conformance rather than at registration.
11. **`ClientRecord` columns are frozen** by an arch guard. Any new per-client credential belongs in `client_credentials`, not a new column.
12. **`internal/web/service/policy.go` may not name a core** (`internal/arch/policy_naming_test.go`); cores may not import `internal/policy` (`internal/arch/imports_test.go:155`); cores may not import `internal/web` (`:80`); and `internal/web` may not import `internal/mtproto` (`:116`). All four are compile-time facts, and the last two together are what make Q6 two hops and Q7-C need a `Deps` field.
13. **No new cron job**; `perCoreJobBudget` (`internal/arch/job_growth_test.go:26`) caps them. Reuse `CorePolicyJob`.
14. **Stdlib `testing` only.** Table-driven, `t.Run` subtests, `t.Helper()` on helpers. Assert the exact value, the typed error or the emitted string, never merely `err != nil`. Prefer real dependencies over mocks.
15. **A test must fail without its fix.** Write it, revert the fix, watch it go red, restore.
16. **Conventional commits**: `type(area): short imperative summary`, then a body explaining the why. Types in use: `fix`, `feat`, `chore`, `refactor`, `perf`, `docs`, `style`. **No attribution trailer of any kind.**
17. **Land on `main`.** This is a solo repository with no branch protection; a pull request only makes work.
18. **`make verify` must pass before the work is done**, and the diff must be focused, with refactors separate from feature work.
19. **Verify on Linux, never on Windows.** The conformance suite skips on Windows, the whole tree needs `ulimit -n 65536` on the VM, and nothing measured on a Windows host counts. The test VM is `ssh penhoon`; sync the tree with `git ls-files -z -c -o --exclude-standard | tar --null -T - -czf - | ssh penhoon 'rm -rf /root/src && mkdir -p /root/src && tar -xzf - -C /root/src'`, then `export PATH=$PATH:/usr/local/go/bin:/root/go/bin; . ~/.pui-test-dsn; ulimit -n 65536; make dist-stub` there.
20. **Stage by path, never `git add -A`; commit on `main`; do not push.** Local `main` is 62 commits ahead of `origin/main` with the user's own work beneath; pushing is the user's call, asked each time.
21. **Nothing in this document changes mtg-proxy.** Section 7 is a list of asks; the user maintains that repository and decides what lands there.

---

## 10. Open questions and unverified points

Everything below is either not established by evidence or is a decision no reading of either repository can settle.

**UNVERIFIED, factual:**

1. That `"1073741824B"`, the exact string the panel renders today for every quota, parses in the mtg **config file**. It parses by inspection: `TypeBytes.Set` upper-cases and hands off to `units.ParseBase2Bytes`, and `B` is `shortSuffix` in both unit maps (`units/si.go:17`). No mtg test exercises the `B` suffix. Fork ask #10 exists to close this.
2. That the quoted table header form `[secret-limits."alice"]`, which `internal/mtproto/manager.go:765` emits via `%q`, is accepted. No mtg test uses the quoted form; every mtg fixture uses a bare key. This has evidently been working in production, but it is not pinned.
3. That the speed limiter is enforced on the **plain** (non-FakeTLS) door. The mtg door test excludes plain mode.
4. Which mtg-proxy binary version the test VM (`ssh penhoon`) and the production panel are actually running right now.
5. The cause of the account-wide anonymous 404 on `Arman2122` (§1.3), reproduced from two networks against a passing control. The symptom is characterized; the cause is GitHub's to confirm.
6. Whether the `/stats` probe of §3.3 / Q3 option i can reach a just-started sidecar within a bounded retry budget on a loaded box. The retry count and window are unmeasured.

**Closed by inspection, recorded so nobody re-opens them:**

- *Is `speed` accepted over `POST /secrets` as well as `PUT /secrets`?* **Yes, by inspection of the shared struct.** `postSecretRequest` (`mtg:mtglib/proxy_api.go:756-759`) embeds the same `limitFields` as `secretEntry` does inside `putSecretsRequest` (`:675-684`), and both call the same `limitFields.parse()` (`:640-673`). The single-secret upsert therefore accepts every key the whole-set replace does. The panel does not use `POST /secrets` today and has no reason to start; the answer matters only so it is not re-investigated.
- *Do the mtg release asset names match what the panel downloads?* **Yes**, on both the archive and the checksums file — §1.2 shows the strings on both sides.
- *Does v1.3.0 carry `connected_ips` and `track-client-ips`?* **Yes**, verified against the tag itself — §1.4. Unit 4's "ships against today's binary" claim holds.

**Open, requiring a decision (all ten are section 6's questions, and none should be answered silently):**

7. Q1: the enforcement mode mtproto declares, and whether the sidecar's own `ip-limit` runs as a second layer given the two IPv6 unit mismatches.
8. Q2: the external-ips push cadence and TTL — reachable only in its cheap forms unless Q7 resolves on.
9. Q3: old-binary UI behaviour, **and** where the features probe runs and how many times it retries.
10. Q4: the up-and-down to combined mapping, and whether to ask the fork for separate keys.
11. Q5: total versus remaining quota, and whether to adopt `usage-state-file`.
12. Q6: cross-node ownership of the push, now costed as two hops and two seams.
13. Q7: the `track-client-ips` default, **and** the plumbing route and its cost, including whether `Deps` gains a `bool` or a `func() bool`.
14. Q8: where the per-client speed value lives, and what stops the supervise pass and the policy pass overwriting each other every ten seconds. **Blocking for units 1 and 6.**
15. Q9: what `LastSeenUnixMilli` should be for a core whose source keeps one timestamp per secret.
16. Q10: whether Unit 5 belongs in this document at all.

**Open, structural:**

17. Whether `InstanceFromInbound` (`internal/mtproto/manager.go:229-262`) should be corrected, deleted, or — as §5 Unit 3 recommends — left exactly as it is. Deleting it moves **three** ratchet sites: `dispatchBudget` at `internal/arch/dispatch_ratchet_test.go:56` (1 → 0), `dispatchTotal` at `:66` (44 → 43), and the `frozenDispatch` entry at `:74`, whose removal must be argued against the rationale already written there — plus it fails the `frozen sites are still budgeted` subtest at `:195` until that entry goes. It would also mean rewriting five test files, two of them under `internal/web/service/`. A separate question the ratchet does not answer: **is a budget of 1 a maximum or an exact count?** If a maximum, a frozen site whose count legitimately drops to zero has no expressible state.
18. Whether the frontend fixtures that use mtproto as the canonical unsupported kind need any change at all. **They will not go red** — all four assertions are hand-written `CoreView` literals with type-only imports (§4.4). The real fallout is a stale comment at `frontend/src/test/ip-limit-unsupported.test.ts:41` and the prose block at `:5-18`. If those fixtures are to keep meaning something, the ask is to add a **new** fixture for a still-sessionless kind, not to edit these.
19. Whether the IPv6 /64 unit mismatch between mtg and the panel is acceptable, or whether the panel's own accounting should adopt /64 for consistency. Note this is **two** mismatches, not one: the enforcement unit (`mtg:mtglib/address_limit.go:87`, /64) and the reporting unit (`:402`, full address) disagree with each other inside mtg before either meets the panel's per-address `policy.Decide`. Changing the panel side is a change to shared policy behaviour affecting every core, not just mtproto, and is therefore out of scope for this work but must be recorded. Fork ask #11 is the cheaper half.
20. Whether the conformance suite's insistence on an immediate, byte-exact read-back — `checkSessions` at `internal/core/coretest/suite.go:602-618` and `checkRates` at `:553-565` — is a contract every core must meet, or a rig affordance that a scrape-backed core is entitled to satisfy by having its rig drive the scrape. Unit 4 and Unit 6 both hit it, and both should be resolved the same way.