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

**Groundwork for F098.** When F098 gives the form back h1..h4, this is the
semantics it inherits: their cleared state is 1/2/3/4, not zero, and the panel-owned
set in internal/awg/encode.go is the one declaration that has to grow to include
them. Recorded here so that disposition does not have to rediscover it.

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

## 7. The `[LIMIT_IP]` line is the jail's input, not a log message

Recorded here because it is a design constraint, not a finding, and it binds any
future change to the IP-limit pass — slot 10 edits the jail config directly.

`p-ui.sh`'s `create_iplimit_jails` writes `filter.d/p-ui-ipl.conf` on every
installed box, whose `failregex` matches `IPLimitLogFormat` word for word, and
points a jail at the file `xray.GetIPLimitLogPath()` returns. The fail2ban daemon
reads that file on its own schedule. It does not read `PUI_ENABLE_FAIL2BAN`, it
does not ask the panel whether enforcement is on, and it keeps running after the
operator turns the panel's toggle off — nothing in the install or update path
removes the jail when the env var changes.

So **writing that line is enforcing**, in the same sense that calling
`fail2ban-client` would be. Any "always record, gate only the action" design has
to treat the file write as part of the action, or it arms enforcement on exactly
the boxes whose operator switched it off. C2/H1 hit this: un-gating the verdict
made the pass produce bans to report on a host set not to ban, and without the
gate on the write those bans would have landed on the first tick after upgrade.

The operator-facing record therefore has to live somewhere the jail cannot read:
the `inbound_client_ips` row (which is what `/clientIps` and both info modals
show) and the panel's own log. That is where it lives now.

Corollary for slot 10: if a step changes `IPLimitLogFormat`, changes the log
path, or adds a second writer, the deployed `failregex` on every existing box is
part of the blast radius — the filter is not regenerated on update.

---

## 8. F282 — a corrupt geofile DOES get past the pre-flight, and which one decides is arbitrary

Run on the VM against the panel's own pinned binary
(`/usr/local/p-ui/bin/xray-linux-amd64`, Xray 26.7.28 / 5ca6f4b), by truncating
a 19,768,301-byte `geoip.dat` and running `xray -test` over a config whose only
routing rule names one `geoip:` code.

```
                       geo rule   no geo rule
intact                 exit 0     exit 0
1 KB of 19 MB          exit 23    exit 0
zero bytes             exit 23    exit 0
half the file          exit 0     exit 0     <-- accepted
```

So the answer to "does F004 already block this" is **no, not reliably**. A
50%-truncated file is accepted, and which codes survive is decided by where they
sit in the protobuf, not by anything the operator can see:

```
code            cn  us  ir  ru  de  jp  br  za  zw
intact          0   0   0   0   0   0   0   0   0
half the file   0   23  0   23  0   0   0   23  23
90% of the file 0   23  0   0   0   0   0   23  23
```

A panel whose rules name `geoip:ir` — this project's stated audience — starts
cleanly on a half-downloaded file. One whose rules name `geoip:us` does not.

### What a surviving code contains: all of it, or nothing

`common/geodata/geodat_loader.go:81 find()` scans the file record by record: tag
byte, varint length, then the code prefix. On a match it `io.ReadFull`s the
**whole** record and `proto.Unmarshal`s that. So truncation is binary per code —
a code either loads with its complete CIDR list, or the read fails and the
config is refused. There is no silently under-populated `geoip:cn`, and so no
rule that quietly stops matching most of its traffic. That is the one piece of
good news here, and it means validation can be structural rather than semantic.

### What this settles for the fix

Shape (i) — temp file plus rename — is necessary and sufficient for the file
never to be corrupt on disk. Validate-before-swap is also warranted, because the
condition it guards ("corrupt but present, and the pre-flight lets it through")
is measured above rather than hypothesised. Structural is enough: re-run the
loader's own scan over the downloaded file before the rename and require it to
reach EOF on a record boundary.

Note the residual failure mode is not the one the finding describes. Post-F004
the core is not taken down: it keeps running, and the corruption surfaces later
as a restart the pre-flight refuses — visible, but at an unrelated time and
naming a routing rule rather than a geofile.

---

## 9. F234 — REFUTED. The rotation takes, and the path the finding cites no longer exists

Two independent reasons, either of which is sufficient.

### The runtime premise is false on the pinned core

Two xray processes on the box, both 26.7.28: a server holding a VLESS+REALITY
inbound, and two clients differing **only** in their REALITY key. Rotation was
performed with `xray api rmi` + `xray api adi` — the same HandlerService
RemoveInbound/AddInbound pair the panel's runtime uses — against a process that
never restarted (`MainPID` unchanged throughout).

```
                        client A (key A)   client B (key B)
baseline, server = A    200                000
after rmi+adi -> B      000                200
after rmi+adi -> A      200                000
after rmi+adi -> B      000                200
```

The old key is refused and the new one works, on every rotation. The claim that
"the running listener keeps authenticating against the old REALITY parameters"
does not reproduce.

*Method note, because it nearly produced the opposite answer:* the first run of
this probe left the two clients on different `serverName` values, so the clean
flip it showed was SNI, not the key. Both clients must differ in exactly one
field. The corrected run is the table above.

*Second method note:* REALITY's post-handshake path sleeps 5s
(`xtls/reality/tls.go`), and a target with a large certificate chain
(www.microsoft.com) fails the relay entirely regardless of the key —
`hs.c.conn == conn: true` with `isHandshakeComplete: false`. www.cloudflare.com,
dl.google.com, addons.mozilla.org, www.lovelive-anime.jp and swdist.apple.com
all work. A REALITY e2e that "fails" is far more likely to be the target than
the key.

### The code path the finding cites was already replaced

F234 locates the bug at "postCommitApply's DelInbound+AddInbound pair
(inbound.go:1554/1560)". f1c7071d (F227) replaced that pair with a single
`rt.UpdateInbound`. That routes through `Core.PlanChange` /
`Core.ApplyInstance`, both of which build old and new configs carrying the
**same tag** — so `exists` is true in `diffInbounds`, and the REALITY guard the
finding says never fires is now exactly the guard on the path. It returns false,
the hot apply is refused, `UpdateInbound` errors, and `needRestart` is set.

So the guard added by 892c06c8 now does its job, and F227 fixed F234 as a side
effect. No change is needed. `TestAREALITYKeyRotationIsNeverHotApplied` pins the
link, because F227's correctness now depends on this guard and nothing else
said so.

---

## 10. F095 — CONFIRMED, including the half-applied device

Run on the VM against the loaded `amneziawg` module, on a throwaway
`awgprobe0` device recreated between cases (device state MERGES, so reusing one
silently answers a different question than the one asked).

```
s1..s4 all >= 12, with key            ACCEPTED   key on device: yes
s3 unset (0), with key                EINVAL     key on device: no
s3 = 5 (non-zero, below 12), key      EINVAL     key on device: no
s3 = 12 exactly, with key             ACCEPTED   key on device: yes
s3 = 11, with key                     EINVAL     key on device: no
all four unset, with key              EINVAL     key on device: no
s3 unset, NO key                      ACCEPTED   key on device: no
s3 = 5, NO key                        ACCEPTED   key on device: no
```

So with a header-protection key set, **every** one of s1..s4 must be at least
12, and zero is refused in exactly the same way as 5 — it is not "leave it
alone". Without a key, any padding value passes. That is precisely the rule the
panel's `Validate` was missing, and the `padding.value > 0 &&` guard was the
whole of the gap.

### The refusal does not roll back what it already wrote

One call, `jc 4 jmin 40 jmax 70 s1 20 s2 30 header-protection-key <32B>`, s3/s4
left unset:

```
$ awg set awgprobe0 jc 4 jmin 40 jmax 70 s1 20 s2 30 header-protection-key /root/hpk.key
Unable to modify interface: Invalid argument

$ awg show awgprobe0
  jc: 4
  jmin: 40
  jmax: 70
  s1: 20
  s2: 30            <-- all of it landed
  (no header protection key)
```

Everything except the key is applied and stays applied. So the pre-fix failure
was not "the edit did nothing": it was a device carrying half the operator's
obfuscation and none of the header protection, while every `.conf` the panel
hands out claims a `HeaderProtectionKey` the server never installed. Header
protection encrypts the header the server parses first, so not one packet from
any client of that inbound is recognisable. Every 10s supervise pass replays the
identical call and re-creates the identical half-state.

### Correction to the finding's suggested check

The audit says to "read dmesg for `S3 must be more then 12 to use
headerProtection`". **No dmesg line is emitted at all** on this build — the
module returns EINVAL silently. The operator's only signal is `awg`'s generic
"Unable to modify interface: Invalid argument", and the panel does not even
surface that (F266). Naming the field in the panel's own refusal is therefore
the entire remedy, not a nicety.

An operator who already has a half-applied device is repaired by the next apply
once the parameters are valid, or by clearing the key; nothing needs to hunt for
them.

---

## Refuted on re-read

- **F234** — see §9. Not reproducible on the pinned core, and its cited code path was replaced by f1c7071d.
