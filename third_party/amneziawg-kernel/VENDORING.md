# AmneziaWG kernel module — vendored source

Upstream: https://github.com/amnezia-vpn/amneziawg-linux-kernel-module
Tag: **v3.1.20260812**
Commit: `46803204e7ec3b068199cd671143bec661d3fe21`
Vendored: 2026-08-18
License: GPL-2.0 (see `COPYING`)

## Why the source and not a package

The Amnezia PPA stops at Ubuntu 25.04 and covers no Debian at all, while this
panel targets Ubuntu 22.04/24.04/26.04 and Debian 12+. A kernel module cannot be
shipped prebuilt either — it is bound to the running kernel's version and
config — so the only thing that serves the whole support matrix is source plus
DKMS, rebuilt by the host on every kernel upgrade.

## Why kernel and not amneziawg-go

**Not throughput.** That was the original claim here and it did not survive
measurement. On the test box (2 vCPU Skylake, both tunnel endpoints in one host
over a veth, identical parameters jc=4 jmin=40 jmax=70, MTU 1420):

| | 1 stream | 4 streams | per %CPU |
|---|---|---|---|
| kernel module | 1105 Mbit/s | 1202 Mbit/s | ~12 |
| amneziawg-go | 1415 Mbit/s | 1396 Mbit/s | ~14 |

The userspace daemon was FASTER, and slightly cheaper per unit of CPU. The
reason is visible in the device: its TUN carries `vnet_hdr on`, so it receives
GSO super-segments up to 64 KB and amortises per-packet work across them, while
the module handles 1420-byte packets. A veth flatters that enormously. A real
NIC delivering individual frames would narrow it, and settling the question
properly needs two hosts rather than one — which nobody has run.

**Memory is the reason.** Under load each amneziawg-go process grew to ~690-730
MB RSS, and there is one process per tunnel endpoint. The panel runs one inbound
per tunnel, so five WireGuard inbounds would be ~3.5 GB of a 3.8 GB VPS spent on
daemons. The kernel module runs no process at all.

**Lifecycle is the second reason.** A userspace tunnel dies with its daemon, so
every inbound would need supervision, crash reporting and a restart ladder — the
panel already carries that for two child-process cores, and a daemon PER INBOUND
is a different order of operational cost. A kernel device persists on its own and
is driven through the same netlink seam `internal/wireguard` already uses.

So: kernel, for capacity and operational shape, not for speed. If a future
two-host benchmark shows a large real-NIC throughput gap as well, that is a
bonus rather than the justification.

## What the panel depends on

- **Link type `amneziawg`** for `ip link add … type amneziawg`.
- **Generic netlink family `amneziawg`** — declared as `WG_GENL_NAME` in
  `src/uapi/wireguard.h`. This is why `golang.zx2c4.com/wireguard/wgctrl`
  cannot drive it: that client binds the family name `wireguard` at compile
  time, and it has no vocabulary for the attributes below.
- **The 3.1 device attributes** in `enum wgdevice_attribute`: `JC`, `JMIN`,
  `JMAX`, `S1`–`S4`, `H1`–`H4`, `I1`–`I5`, `HEADER_PROTECTION_KEY`,
  `CONTENT_PADDING_ADDITION`, `REKEY_AFTER_TIME`, `REKEY_TIMEOUT`,
  `REJECT_AFTER_TIME`, `KEEPALIVE_TIMEOUT`, `MAX_HANDSHAKE_ATTEMPTS`,
  `RANDOM_TRAILERS`, `DISABLE_COOKIES`. Every one has to match on both ends of
  a tunnel, so each is part of a client's configuration, not a server-side knob.

## Updating

Re-vendor the whole tree from a tag, then update the header of this file. Do not
patch the sources in place: a local change here is invisible to upstream's
history and will be silently reverted by the next re-vendor. If a change is
genuinely needed, carry it as a patch file beside this document with the reason.

Verified on 2026-08-18 against kernel 6.8.0-137-generic (Ubuntu 24.04): builds
clean, loads, coexists with the in-tree `wireguard` module, and accepts
`ip link add … type amneziawg`.
