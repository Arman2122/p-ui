# Node capability inventory

Placement capability is evidence that an installed worker can accept the supported graph and enforcement contract. It does not mean a graph is applied or a client has forwarding authority. Runtime conditions, current journal observations, acknowledged publications, credit, and authenticated admission retain their separate gates.

## Producer and transport

The agent obtains capabilities from `GET /v1/capabilities` on its configured local worker sockets. The existing Unix transport authenticates both peers by UID. The request supplies the bound state and credit journal headers; the worker checks them before probing and again before replying. Its local JSON response contains the worker's node, installation and control-plane identity, exact runtime journal identity, and the existing `CoreCapability` message. The agent checks every identity against its own durable state and journal binding.

The worker delegates to the executing engine, never to an agent configuration label. Xray and sing-box run their side-effect-free validation paths, including the required patch checks, before claiming enforcement. MTProto validates its compiled configuration path; its governor and authentication interfaces are required by the compiled patched module. SSH describes its compiled engine without generating host keys. These probes do not start listeners, apply configurations, or change readiness.

Qualified protocols are `http`, `hysteria`, `mixed`, `shadowsocks`, `trojan`, `vless`, and `vmess` for Xray; `anytls` and `tuic` for sing-box; `ssh` for SSH; and `mtproto` for MTProto. All four qualified stream engines report client rate enforcement, client quota enforcement, and activation admission. The native worker explicitly returns no capability, even if a backend implements a static positive provider. Native first-use and authority qualification remain closed.

## Build identity and family resolution

`Version` is the opaque 71-character value `sha256:<64 lowercase hexadecimal digits>`. It fingerprints the running worker executable through `/proc/self/exe`, so replacing a release path cannot relabel the old process. The digest is cached only after a complete bounded read. It is a reported build observation, not host attestation or a semantic version, and must not be ordered as a release number. Non-Linux executable lookup exists only for portable tests; production worker startup still requires Linux.

`CoreCapability` has no runtime ID dimension. A family is advertised only if every currently configured worker of that family returns matching authenticated identity and exactly equal protocol, feature, and build evidence. A missing, invalid, unavailable or disagreeing sibling removes the entire family from the reported set. Arbitrary runtime IDs remain supported; no canonical worker is invented from the family name.

The master resolves that family only through current approved network bases and current-installation runtime journals. It preserves an existing matching placement runtime or requires one unambiguous base/journal choice for a new target. The current compiler requires canonical runtime IDs; an otherwise resolved alias remains explicitly pending until compiler mapping is qualified. Old journal rows without current bases cannot become targets. Capability absence leaves a new target pending; it is not evidence to retire an existing assignment or block a frozen stop operation. Node labels remain master-owned.

## Refresh and bounds

The initial authenticated inventory is included in the existing `NodeHello` after journal registration and time synchronization. On the existing ten-second heartbeat cadence, the agent probes again. A changed canonical set, including loss or recovery, ends that control stream so its normal reconnect sends a fresh Hello. Identical observations do not force reconnects. Transient graph application or readiness state does not alter static inventory.

Each response is limited to 16 KiB and strictly decoded. Worker probes have a three-second deadline; the agent bounds the complete inventory pass to five seconds with at most eight concurrent requests. Executable fingerprinting has a one-GiB read ceiling and checks cancellation between reads. Failure supplies no positive capability and never falls back to configured names or guessed dependency versions.

This adds one local read-only endpoint. It does not change the master protobuf wire, protocol version 2, or the independently versioned durable state format 3.
