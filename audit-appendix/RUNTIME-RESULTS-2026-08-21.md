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

## 11. F085 — correction: there is no ENODEV, and the suggested swallow would have hidden F095

The finding's second half asks for a missing device to be made "genuinely
non-fatal (swallow the ENODEV/link-not-found case)" so `applyParams` matches its
own doc comment. That cannot be done as written.

Measured against the loaded module, from the panel's own `awg` package:

```go
err := ConfigureParams("awg-does-not-exist", Params{Jc: 4, Jmin: 40, Jmax: 70})

err = awg: the module refused the configuration: netlink receive: invalid argument
errors.Is(err, unix.ENODEV) = false
errors.Is(err, unix.ENOENT) = false
errors.Is(err, unix.EPERM)  = false
```

A missing device answers **EINVAL** — byte for byte the errno the module returns
for a parameter set it refuses (§10). Nothing at this seam can tell the two
apart, so a swallow keyed on the error would have swallowed F095's refusal as
well: exactly the signal F266 exists to put in front of the operator, discarded
one layer below it.

What the comment was really protecting was the reconcile stopping on it, and
that is fixed directly — the pass collects instead of early-returning. The false
claim is removed rather than made true by guesswork.

If a genuinely quiet "device not there yet" is wanted later, it has to be a
**presence check** (ask the plane whether the link exists) rather than an errno
test. That costs one extra netlink round trip per inbound per 10s pass, and it
is only worth paying once F266 puts these errors in front of an operator, where
a spurious "obfuscation failed" for an inbound still being created becomes
noise. Deliberately deferred to that slice, not forgotten.

---

## 12. F098 — what a CLEARED header is, measured

Option (i) needed grounding before it could be encoded, because "cleared" has
three candidate meanings and only one of them is a thing the module accepts.

On a throwaway `awgh0` device:

```
1. awg set awgh0 h1 10 h2 20 h3 30 h4 40      ACCEPTED
   awg show awgh0  ->  h1: 10  h2: 20  h3: 30  h4: 40

2. awg set awgh0 h1 1 h2 2 h3 3 h4 4          ACCEPTED
   awg show awgh0  ->  (no header lines at all)

3. awg set awgh0 h1 0 h2 0 h3 0 h4 0          EINVAL
   awg show awgh0  ->  (no header lines)
```

Line 2 is the answer and it is stronger than "1/2/3/4 happens to work":
awg-tools **stops reporting the headers entirely** once they are set to
WireGuard's own message types. The tooling's own definition of "this device has
no custom headers" is exactly the values option (i) writes, so the panel and
`awg show` agree about what a cleared inbound looks like.

Zero is not available as a clearing value (line 3), for the reason already
recorded in §6: `netlink.c` validates the four as ranges and four zeros collide.

### The consequence the substitution creates

`Params.Validate` skipped a zero header on the reasoning that unset headers
cannot collide. Once cleared means 1/2/3/4, they can: `H1 = 1-5` beside three
cleared siblings is refused by the module and accepted by a check that reads
only the struct fields. So the overlap check now runs over the substituted set —
the same lesson as F095, that the module validates the MERGED device state and
the panel has to validate what it is really about to send.

---

## 13. F351 — the finding is right, and its fix was wrong in two directions

Recorded next to §2, which is the same shape: a correct finding whose suggested
remedy would not have held.

### The scope is half the bug

The finding names `internal/web/service/email/subscriber.go` and stops there.
`internal/web/service/tgbot/tgbot_event.go:36` carries the **identical**
exemption, byte for byte, against its own package-level limiter:

```go
if e.Type != eventbus.EventLoginAttempt {
    if !tgEventLimiter.Allow(e.Type, e.Source) {
        return
    }
}
```

Both ship enabled by default — `setting.go:155-156` puts `login.attempt` in the
default value of *both* `tgEnabledEvents` and `smtpEnabledEvents`. So the email
fix alone leaves the Telegram path flooding, and that path is the worse of the
two: `SendMsgToTgbotAdmins` fans out to every admin, so the volume is multiplied,
and exceeding the bot's own rate limit gets it throttled — which silently stops
`xray.crash` and `node.down` reaching anyone at all. Denial of monitoring rather
than an inbox nuisance.

### The suggested key would have hidden the one event that matters

The finding proposes `s.limiter.Allow(e.Type, "")` — a constant source, so a
rotating attacker cannot mint a fresh bucket. That fixes the flood and creates a
smaller problem: with one bucket for the whole event type, the attacker's
failures and the operator's own successful sign-in share it. An admin logging in
during an attack is inside the cooldown the attacker is holding open, and the one
login notification an operator actually wants is the one that gets dropped.

`LoginEventData.Status` is already normalised to exactly `"success"` or `"fail"`
by the publisher (`tgbot_report.go:175-178`), so keying on it gives a closed
two-value space that nothing outside the panel can influence, and keeps the two
outcomes in separate buckets. That is what landed.

### Why `e.Source` cannot be the key

Worth stating explicitly, because it looks like a reasonable default: `Source` is
`attempt.IP`, and `getRemoteIp` honours `X-Real-IP` / `X-Forwarded-For` when the
peer is a trusted proxy. Behind a reverse proxy the bucket is chosen by a request
header, so one attacker rotates it without rotating anything else.

---

## 14. F063 — the leak mechanism, measured, and what "fails closed" has to mean

The finding says a marked outbound whose fwmark rule was never installed
egresses with the server's own address. Confirmed directly on the box with
`ip route get`, which asks the kernel the same question the socket asks:

```
the server's own egress
  1.1.1.1 via 172.31.1.1 dev eth0 src 91.107.151.123

marked, with its rule installed  (a realisable uplink)
  1.1.1.1 dev puxprobe table 30099 src 10.77.99.1 mark 0x4d2

the SAME marked packet, rule removed  (Fill failed, the manager zeroed it)
  1.1.1.1 via 172.31.1.1 dev eth0 src 91.107.151.123 mark 0x4d2
```

The third line is the first line with a mark on it. A mark nothing matches does
not fail, does not warn and does not drop — it falls through to the main table,
and the packet leaves as if the uplink had never been configured. Every client
on the rules pointing at that exit gets the server's identity, while the panel
shows the rule as fine and its pattern chip as "marked".

### Why both layers had to change

`EgressService.validate` returned before reading `Settings` for anything that is
not a front, so an uplink's own configuration was never checked at save.
`RoutingService.Exits` then kept the row regardless, because
`wireguard.Core.ExitHandle` is unconditional — it answers with a device name and
a nil error without reading settings at all. So the planner emitted the marked
outbound. Meanwhile `Manager.plan` catches the driver's `Fill` error, **zeroes
the fill** and carries on, so `fill.Marked` is false and the rule is never
installed.

Three components each behaving reasonably in isolation, composing into a silent
identity leak. Fixing one of them would have left the other path open: the save
check does nothing for the rows already stored, and the plan-time check does
nothing to stop an operator creating the next one.

Both now ask the driver's own `Fill`, which is the only thing that reads the
settings and therefore the only thing that can tell a saveable row from one that
cannot be realised.

---

## 15. F370 — paging, proved against a real limit-enforcing directory

The finding's own test note said only a real directory would settle it. One was
stood up on the box: OpenLDAP with 1200 seeded users and a bind subject to a
500-entry limit.

The first attempt **failed to prove anything**, and the reason matters. Stock
OpenLDAP applies `sizelimit` to the whole result set regardless of paging, so a
paged search hit the same wall as an unpaged one:

```
WITH the fix (SearchWithPaging)   -> 0 users, Size Limit Exceeded
WITHOUT the fix (plain Search)    -> 0 users, Size Limit Exceeded
```

That is not this finding's directory. Active Directory's `MaxPageSize` is a
limit **per page**, which is why paging is the documented way past it. Modelling
that shape in OpenLDAP takes one `olcLimits` line — a hard total limit for
unpaged searches, an unlimited total for paged ones:

```
olcLimits: dn.exact="cn=reader,dc=nodomain"
           size.soft=500 size.hard=500 size.pr=500 size.prtotal=unlimited
```

Against that directory, through the panel's own `FetchVlessFlags`:

```
WITH the fix (SearchWithPaging)   -> 1200 users, err=<nil>
WITHOUT the fix (plain Search)    ->    0 users, LDAP Result Code 4 "Size Limit Exceeded"
```

Zero, not five hundred. The error aborts the whole fetch, so the sync applies no
flag, creates no client and deletes none — which is the finding's claim, now
measured rather than reasoned.

### The directory class this does NOT fix, recorded because it is real

The first attempt is a finding of its own shape: **a directory that caps the
total result set rather than the page size is not helped by paging at all.**
Stock OpenLDAP with `olcSizeLimit: 500` and no per-DN limits is exactly that,
and on one of those the sync still fails completely after this fix.

There is nothing the panel can do about it from the client side — the fix for
that operator is a directory-side limit for the panel's bind DN. Worth a
sentence in the changelog rather than letting them read "LDAP paging fixed" and
conclude their case was covered.

---

## 16. F281 — a v7a binary really does SIGILL on an armv5 board

The audit warns "do not silently map arm→one variant: a v7a binary will SIGILL
on an armv5 board". Verified rather than trusted, because that warning is the
whole reason the cheap fix is not available.

`qemu-user-static` on the box, against the two real published assets from
Xray v26.3.27:

```
                     ARMv5 CPU (arm926)                ARMv7 CPU (cortex-a15)
Xray-linux-arm32-v5  Xray 26.3.27 ... linux/arm        Xray 26.3.27 ... linux/arm
Xray-linux-arm32-v7a uncaught target signal 4          Xray 26.3.27 ... linux/arm
                     (Illegal instruction) - core dumped
```

So the asymmetry is real and it is one-way: the v5 build runs on both, the v7a
build runs only on v7. Shipping v7a to every `GOARCH=arm` host would brick
armv5 and armv6 boxes at the first exec — worse than today's 404, which at
least leaves the old binary in place.

### The check that does NOT work, recorded because it is the obvious one

`readelf -A` prints **nothing** for either binary — Go does not emit the ARM
build-attributes section, so `Tag_CPU_arch` is unavailable and `file` cannot tell
the two apart either. Executing under an emulated CPU is the only test that
distinguishes them, which is why the verification plan is qemu rather than
inspection.

### What this makes checkable

The fix stamps the sub-arch at build time. Its correctness is therefore testable
end to end without an ARM board: build the panel for each ARM platform, ask it
which asset name it would download, and confirm that asset runs under the
matching emulated CPU and fails under a lower one. The table above is the
reference answer.

---

## 17. F281 — the fix verified end to end, without an ARM board

Steps 1 and 2 of the plan, on top of §16's SIGILL measurement.

**The stamped binary computes the right name.** Cross-built for each ARM level
with the link-time stamp, then executed under `qemu-user-static`:

```
armv7   computes arm32-v7a   want arm32-v7a   OK
armv6   computes arm32-v6    want arm32-v6    OK
armv5   computes arm32-v5    want arm32-v5    OK
amd64   computes 64          want 64          OK
```

**Every name it computes is a real published asset**, and the one the old code
produced is not:

```
Xray-linux-64.zip          200      Xray-linux-arm32-v6.zip    200
Xray-linux-arm64-v8a.zip   200      Xray-linux-arm32-v5.zip    200
Xray-linux-arm32-v7a.zip   200      Xray-linux-32.zip          200
Xray-linux-s390x.zip       200      Xray-linux-arm.zip         404
```

That 404 is what every armv5, armv6 and armv7 install has been getting, 100% of
the time, since the switch was written over values Go does not produce.

So the chain is closed without hardware: release.yml stamps the platform, the
binary resolves it to a name, the name resolves to an asset, and §16 says which
asset each board can actually execute.

---

## 18. F136 — the finding names the browser; the same hole was in Go

F136 is filed against `frontend/src/lib/xray/inbound-link.ts` and reads as a
frontend-only defect: the browser has no concept of a Host row, so QR codes and
Export links hand out the origin. Verifying that turned up a second instance
the audit does not mention, on the server side of the same feature.

`LinkProvider.LinksForClient` — behind `GET /panel/api/clients/links/:email`,
a documented REST endpoint with no frontend consumer — called `GetLink`
directly with no `hostEndpoints` lookup:

```go
svc := p.build(host)
svc.projectThroughFallbackMaster(inbound)
return splitLinkLines(svc.GetLink(inbound, email))
```

`inboundLinks`, three functions away in the same package and reached through
`/inbounds/allLinks` and every subscription, does the lookup and branches on it.
So the panel rendered the same client two different ways depending on the door.

Measured on a live PostgreSQL (`internal/sub`, seeded inbound at
`203.0.113.5:4451` with one host `a.cdn.com:2096`), with the fix reverted:

```
want:  vless://...@a.cdn.com:2096?host=a.host&path=%2Fa&sni=a.sni&type=ws#...
got:   vless://...@203.0.113.5:4451?host=base.host&path=%2Fbase&sni=base.sni&type=ws#...
```

Not merely the wrong address — the wrong host header, path and SNI too, i.e.
the fronting is absent rather than misaddressed. A two-host inbound returned
one link instead of two.

**Correction to F136:** its *What* section is accurate about the frontend but
its scope is understated. The Go renderer had the same defect on one endpoint,
and that half is fixed independently of the browser work.
---

## 19. H2 — the two probes, measured against a live daemon

H2 says `fail2ban-client -h` proves only that a Python binary exists. Measured
on the VM (Ubuntu 24.04, fail2ban 1.0.2-3ubuntu0.1), in the two states that
matter:

```
                              -h        status p-ui-ipl
jail file deleted              0             255
daemon stopped                 0             255
jail present, daemon up        0               0
```

Deleting the jail file is not hypothetical: it is what the panel's own menu
option 1 ("Only remove IP Limit configurations") does. On that box the old
probe returned green, `enforce` went true, the UI showed the cap enabled and
the job logged addresses over their cap — while nothing was dropped.

The daemon-stopped row explains why: `-h` never reaches the socket at all. It
is answered by the client's own argparse.

**Cost of the fix, which the §7 warning called correctly:** `status` DOES reach
the socket, so this is the panel's first synchronous dependency on fail2ban's
daemon inside `enforceIPLimits`. Both probes are now
`context.WithTimeout`-bounded at 5s and share one 30s cache, because the cron
chain is built with `SkipIfStillRunning` and a `DiscardLogger` — a hung call
would discard every later tick, silently, and freeze speed-ladder convergence
with it.

---

## 20. H8 — the PyPI step was not just risky, it shadowed the archive

H8 says the `pip install pyasynchat --break-system-packages` workaround is
unnecessary on current Ubuntu. It is worse than unnecessary. On the VM:

- the archive candidate is `1.0.2-3ubuntu0.1` — the patched build
- that package **vendors** `fail2ban/compat/asynchat.py`, and its import site
  reads `try: import asynchat / except ImportError: from ..compat import asynchat`
- it **Depends on** `python3-pyasyncore`, supplying the other module Python
  3.12 removed

With `/usr/local/lib` (pip's install root) stripped from `sys.path`,
`fail2ban.server.asyncserver` imports cleanly — `asynchat` resolving to the
vendored shim and `asyncore` to the deb. So apt already installs everything
needed, and the pip copies in `/usr/local` take precedence over the
apt-managed ones on a normal path. The workaround shadowed the distro's own
dependency with an unmanaged copy, on the one step that also requires PyPI to
be reachable.

---

## 21. H7 — the range delete, on real files

`sed -i "/[p-ui-ipl]/,/^$/d"` against a jail.conf whose next section follows
with no blank line between:

```
before                 after (old sed)        after (new awk)
# operator preamble    # operator preamble    # operator preamble
[DEFAULT]              [DEFAULT]              [DEFAULT]
bantime = 10m          bantime = 10m          bantime = 10m

[p-ui-ipl] ...         (nothing follows)      [sshd]
[sshd] ...                                    enabled = true
[recidive] ...                                port = 2222
                                              [recidive]
                                              enabled = true
```

`[sshd]` and `[recidive]` gone, on a routine update, silently. The replacement
was checked against four shapes: no blank line, blank line present (what the
old range handled correctly), `[p-ui-ipl]` last in the file, and absent
entirely.
---

## 22. The §8 checklist, on the box

IP-LIMIT-ACTUATOR-RESEARCH §8 lists eight things that cannot be settled by
reading. Box: the penhoon VM, Ubuntu 24.04.4, fail2ban 1.0.2-3ubuntu0.1,
nftables 1.0.9. Every ban used TEST-NET addresses and was unbanned afterwards.

**Coverage — this is 5 of 8 fully closed, not 8 of 8.** Read the three below as
still open before treating the checklist as discharged:

| # | State | Why |
|---|---|---|
| 1 | **partial** | Answered for fail2ban 1.0.x. Ubuntu 22.04's 0.11.2-6 is untested — no such box was available. |
| 7 | **open** | §8 scopes it to "before shipping the cut", i.e. step 11's actuator swap, which is not in this pass. It also needs a real proxy client reconnecting. |
| 8 | **partial** | Answered for the VM. The prod panel was deliberately not probed — it is not this campaign's to touch — so the panel-side allowlist question for step 11 stays open there. |

Items 2, 3, 4, 5 and 6 are closed on measurement below.

**1. Multi-IP `banip` — YES, on 1.0.2.**
`fail2ban-client set p-ui-ipl banip 203.0.113.7 203.0.113.8` returns `2`, exit
0, and both land in the banned list. *Gap:* Ubuntu 22.04's 0.11.2-6 was not
tested — no such box. The multi-IP floor is proven for 1.0.x only.

**2. Email field after a manual `banip` — EMPTY. The finding is confirmed.**

```
2026/08/25 21:32:43   BAN   [Email] =  [IP] = 203.0.113.7 banned for 1800 seconds.
```

The line appears; the field does not. `<F-USER>` is populated only by a filter
match against a log line. This is the audit-trail loss step 11 has to decide
explicitly, and it is now measured rather than predicted.

**3. Jail with an unwritten logpath — loads and STAYS loaded.**
`/var/log/p-ui/pui-ipl.log` is 0 bytes. Across `systemctl restart fail2ban`,
`status p-ui-ipl` exits 0 and `get p-ui-ipl logpath` reports the file as
monitored. So `backend = polling` is **not** needed and the no-migration
property holds.

**4. `blocktype` — REJECT, not DROP.** From the live chain:

```
-A f2b-p-ui-ipl -s 203.0.113.7/32 -p tcp -m multiport ! --dports 22,16109 -j REJECT --reject-with icmp-port-unreachable
```

**5. IPv6 — banned, not silently dropped.** `banip 2001:db8::1` returns 1 and
creates the ip6tables chain with `--reject-with icmp6-port-unreachable`. The
worry is refuted.

**6. `nft destroy` on old nft — the finding is confirmed, and now fixed.**
The jammy `nftables 1.0.2` binary was extracted from its .deb and run on the
VM beside the system 1.0.9:

```
nft 1.0.2:  destroy table inet p-ui-nat  -> exit 1
            /dev/stdin:1:9-13: Error: syntax error, unexpected table, expecting string
nft 1.0.9:  destroy table inet p-ui-nat  -> exit 0
```

Not a runtime failure — the parser has never heard of `destroy`. Ubuntu 22.04
(1.0.2) and Debian 12 (1.0.6) are both below the 1.0.8 that introduced it, so
on two of five supported targets the masquerade table was never torn down.

The replacement is `add table` + `delete table`, applied for real against the
kernel on both binaries, with the table present and absent, using a scratch
table name so the panel's own was never touched:

```
                              1.0.2   1.0.9
table absent, add+delete       0       0
table absent, run again        0       0
table present, add+delete      0       0
leftover afterwards           none    none
```

**7. Ban/reconnect/re-ban cycle — NOT measured, and not required here.**
§8 scopes it to "before shipping the cut", i.e. step 11's actuator swap, which
is not in this pass. It also needs a real proxy client reconnecting, which this
box cannot stage. It stays open, attached to step 11.

**8. Operator `ignoreip` — none on the VM.**
No `/etc/fail2ban/jail.local` exists and no `ignoreip` appears in `jail.d/`.
The prod panel was **deliberately not probed**: it is not this campaign's to
touch. So this answers one of the two boxes §8 names, and the panel-side
allowlist question for step 11 stays open on the other.
---

## Refuted on re-read

- **F234** — see §9. Not reproducible on the pinned core, and its cited code path was replaced by f1c7071d.
