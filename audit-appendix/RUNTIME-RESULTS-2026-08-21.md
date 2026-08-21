# Runtime results — 2026-08-21

`AUDIT-2026-08-20.md` was static analysis: nothing in it was executed. This file
records what happened when the findings that needed a real Linux box were actually
run, and it is where a finding goes when a re-read or a run **refutes** it, rather
than being "fixed" into something that was never wrong.

**Box.** Ubuntu 24.04.4, kernel 6.8.0-137-generic, x86-64. Xray **26.7.28**
(`5ca6f4b`, go1.26.5) — the binary the panel ships and execs. Go 1.26.6.
`amneziawg` DKMS module loaded. PostgreSQL 16, throwaway database.

---

## 1. The core refuses to start — confirmed, six ways

Each config below was handed to the shipped binary as `xray -test -c <file>`, which
is `core.LoadConfig` + `core.New` with no listeners bound — the same load a start
performs. Every one exits **23** and serves nothing. A baseline config from the live
panel returns `Configuration OK.` in ~50ms, 31MB RSS.

| Finding | Config | What the core says |
|---|---|---|
| F014 | shadowsocks `2022-blake3-chacha20-poly1305` + one client | `shadowsocks 2022 (multi-user): only blake3-aes-*-gcm methods are supported` |
| F014 | the same method with **no** clients | `Configuration OK.` — single-user is legal, the client is what breaks it |
| F073 | two outbounds tagged `pex4` | `app/proxyman/outbound: existing tag found: pex4` |
| F002 | VLESS `decryption` + one fallback | `VLESS settings: "fallbacks" can not be used together with "decryption"` |
| F005 | REALITY, `privateKey: ""` | `Failed to build REALITY config. > empty "privateKey"` |
| F005 | REALITY, `serverNames: []` | `empty "serverNames"` |
| F005 | REALITY, `shortIds: []` | `empty "shortIds"` |
| F005 | REALITY, `shortIds: ["abc"]` | `invalid "shortIds[0]": abc` |
| F005 | REALITY with no `target` at all | `non-empty "serverNames", please use "serverName" instead` — the builder takes its *client* branch |
| F321 | TLS, `certificate: []` and `key: []` | `Failed to build TLS config. > both file and bytes are empty.` |
| F064 | routing rule with `outboundTag: ""` | `neither outboundTag nor balancerTag is specified in routing rule` |
| F052 | routing rule with `port: "abc"` | `invalid port: abc` |

F073 is the one only `core.New` catches: no config *builder* rejects a duplicate
tag, it fails at handler registration. That is why the pre-flight runs the binary
rather than the vendored builders.

---

## 2. F052 — the finding is right, its suggested fix was not

**Correction to the audit.** F052 proposes rejecting "a `network` outside
{tcp,udp,tcp+udp}, a `protocol` outside {http,tls,bittorrent,quic}" and a
`port`/`sourcePort` that is not a well-formed list of `n` or `n-m`. Probing the
pinned core's own rule parser shows it accepts all of those:

| criteria | pinned core's answer |
|---|---|
| `{"sourcePort":"90-10"}` (backwards range) | accepted |
| `{"network":"sctp"}` | accepted |
| `{"protocol":["gopher"]}` | accepted |
| `{"domain":["regexp:("]}` (unparseable regexp) | accepted |
| `{"port":"abc"}` | **rejected** — `invalid port: abc` |
| `{"ip":["not-an-ip"]}` | **rejected** — `unsupported address family` |

So the hand-written validator the audit sketches would have refused four
configurations the core is happy to load. The outage claim is real and confirmed
above; the *fix* was over-strict. What landed instead validates through
`conf.RouterConfig.Build()` — xray's own parser — which refuses exactly what the
core refuses and cannot drift from it as the core changes.

**Also found while probing:** a `geoip:`/`geosite:` token is resolved against the
`.dat` files at parse time, so with the assets missing the parser fails with a
wrapped `fs.ErrNotExist`. `geoip:private` is in the shipped default template, and
`UpdateGeofile` (F282) truncates those files in place — so a naive check would let a
failed asset download take the whole rule editor down. The landed fix treats
`fs.ErrNotExist` as "cannot judge" and accepts.

---

### F321's blast radius, measured

F321 stands exactly as written — the blank entry the TLS radio seeds is refused.
But probing the builder shows it is the **only** certificate shape refused:

| `tlsSettings` | pinned core's answer |
|---|---|
| `certificates: [{certificate: [], key: []}]` | **rejected** — `both file and bytes are empty.` |
| `certificates: [{certificateFile: <real>}]`, no key at all | accepted |
| `certificates: [{certificateFile: <real>, keyFile: ""}]` | accepted |
| no `certificates` key at all | accepted |
| `certificates: [{certificateFile: <missing path>}]` | rejected, wrapping `fs.ErrNotExist` |

The last row is why the save boundary now downgrades *file existence* only: pointing
an inbound at a path certbot has not written yet is the ordinary order of
operations, and the file can appear between the save and the restart. The restart
pre-flight is the hard gate and still refuses it, so a file that never appears
cannot become an outage.

---

## 3. F335 — PLAUSIBLE → **CONFIRMED**, and worse than the finding claims

Two real xray-core processes: a server with a VLESS/tcp inbound holding one client
and the panel's gRPC API inbound, and a client with two HTTP inbounds routed to two
VLESS outbounds — one plain, one with `mux.enabled: true, concurrency: 8`. A local
HTTP sink streams indefinitely so one transfer stays open across the call. The
removal is issued through the panel's own `XrayAPI.RemoveUser`, which the core
accepts. Identical across repeated runs.

| after `RemoveUser("vless-in", "victim@x")` | result |
|---|---|
| the transfer that was already in flight | **still flowing** — 9216 further bytes, no error, no reset |
| a **new** request over the already-warmed mux connection | **succeeds** (`"pong"`) |
| a brand-new connection presenting the same UUID | refused — `HTTP 503` |

So `RemoveUser` works exactly as designed and never touches an established
connection. The mux row is the part that makes this open-ended rather than
bounded-by-one-transfer: a revoked client whose tunnel is already up keeps
originating new requests over it for as long as it holds the connection.

Consequence for the panel: quota exhaustion, expiry and manual disable all revoke
through this call, so none of them ends access for a client that is currently
connected.

---

## 4. F226 — CONFIRMED

Driven through the panel's own `awg.ConfigureParams` against a real `amneziawg`
device (`pawgt0`, created for the test and removed after), pre-set to the values the
UI preset writes:

```
before:                jc=4 jmin=40 jmax=70 s1=20 s2=30
after clearing s1/s2:  jc=4 jmin=40 jmax=70 s1=20 s2=30
after clearing all:    jc=4 jmin=40 jmax=70 s1=20 s2=30
```

Both halves of the finding hold independently. The second row went all the way to
`client.ConfigureDevice` (the struct is not zero) and still changed nothing — that
is `encodeParams` skipping zero-valued attributes. The third row never reached the
kernel at all — that is `applyParams`' `IsZero()` early return.

Meanwhile the `.conf` the panel hands out after the same edit omits `S1`/`S2`
entirely, so device and client disagree and no client on that inbound can handshake.

---

## 5. F151 — CONFIRMED

`ls -ln` on a box installed from a release build:

```
/usr/local/p-ui/:
drwxr-xr-x 2 1001 1001     4096 bin
-rwxr-xr-x 1    0    0 66023586 p-ui
-rwxr-xr-x 1 1001 1001   109818 p-ui.sh

/usr/local/p-ui/bin/:
-rwxr-xr-x 1 1001 1001 37720190 xray-linux-amd64
-rwxr-xr-x 1 1001 1001 13668514 mtg-linux-amd64
-rw-r--r-- 1 1001 1001 17425291 geoip.dat
```

The unit runs with no `User=`, i.e. as root, and execs `bin/xray-linux-amd64`. Both
child binaries, every geo asset, the management script and the **containing
directory** are owned by uid 1001, so that uid can replace what root executes. uid
1001 does not exist on this particular box, but it is the second ordinary account on
a stock Debian/Ubuntu install. The panel's own `p-ui` binary is correctly `0:0` —
only what came out of the tarball carries the uid.

---

### The flag, measured on the box

GNU tar 1.35, extracting an archive whose files are uid 1001, as root:

| extraction | owner afterwards |
|---|---|
| `tar zxf` (what shipped) | `1001:1001` |
| `tar --no-same-owner -zxf` | `0:0` |
| `--no-same-owner` over a file that is already 1001-owned | `0:0` |

**Refinement to the finding.** `update.sh` already ends with
`chown -R root:root ${pui_folder}`, so an update repairs the tree. `install.sh`
chowns the systemd unit and the env file but never the install tree — so this is
a **fresh-install** defect that survives until the box is first updated, which
matches the audit's wording. The fix therefore adds the same sweep to
`install.sh` rather than only the extract flag, so a box installed before this
change is repaired by the next run of either script.

---

## 6. F226 — how the parameter is cleared, measured

The finding is confirmed above; this is the follow-up probe that decides the fix's
shape. Run in-package against the loaded `amneziawg` module, sending the netlink
attribute the panel's encoder currently refuses to emit.

**Does the module accept an attribute carrying zero?** Yes, and the device drops
the value:

| cleared with an explicit zero | module | device afterwards |
|---|---|---|
| `jc` alone | accepted | `jc` gone, `jmin`/`jmax` untouched |
| `jc` + `jmin` + `jmax` | accepted | all three gone |
| `s1` + `s2` | accepted | both gone, `jc` untouched |
| `s1` + `s2` + `s3` + `s4` | accepted | all four gone |
| `h1` alone | accepted | — |
| **`h1` + `h2` + `h3` + `h4` together** | **refused, EINVAL** | unchanged |

An attribute the message does not carry is still "leave it alone", so clearing one
parameter does not disturb the others — the second and third rows show `jc`
surviving an `s1`/`s2` clear.

**Why the header row refuses.** `third_party/amneziawg-kernel/src/netlink.c:865-907`
validates the MERGED state: H1 must not overlap H2, H3 or H4, and so on for each.
Sending all four as zero makes them identical, so the first check fails. Zero is
therefore not the cleared state for the headers — WireGuard's own 1/2/3/4 is, which
is what the device already holds when they were never set. That makes clearing
H1..H4 a different job from clearing the rest, and it is entangled with F098 (the
form's schema drops h1..h4 entirely), so it is out of this fix's scope.

**What read-back-and-recreate would have cost.** Not needed — explicit zeros work
for every parameter the form can clear — but measured for the record, since it was
the alternative shape. Deleting and recreating the link loses everything the device
was:

```
before: listening port 51997, jc/jmin/jmax/s1/s2 set, 1 peer, 10.77.0.1/24, UP
after:  no port, no key, no parameters, no peers, no address, DOWN
```

Every peer's session state goes with it, and the panel would have to re-add the
address and re-run its own addressing before anything could reconnect. Explicit
zeros change one attribute and touch nothing else.

---

## Refuted on re-read

*(none yet)*
