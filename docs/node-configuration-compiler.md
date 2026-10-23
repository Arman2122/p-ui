# Node configuration compiler

`internal/cores/nodeconfig.Compile` is a pure master-side compiler. It produces
runtime definitions, assigned client IDs, listener reservations, asset claims,
bridge requirements and explicit omissions. It does not start cores, activate
billing terms, allocate quota, choose shared rates or establish deployment
readiness.

Inputs are scoped to one node. Normalized client assignments are supplied
separately from inbound settings; stale source `clients` arrays are never an
assignment authority. Protocol credentials remain distinct from stable fleet
client IDs. Xray's node metadata binds authenticated labels to canonical IDs;
routing receives a copied canonical session identity without modifying shared
authentication objects. Source user and inbound selectors are resolved against
the selected node's graph.

Fleet defaults and node overrides must be resolved and pinned before compilation.
Overrides replace schema-defined networking values; they cannot provide grants,
client credentials, limits or billing state. Assets belong to a node and its
installation. The compiler consumes authorized material, never opens a path from
the master's inbound settings. Runtime definitions may contain the secrets needed
by that node; preview APIs must expose metadata, digests and diagnostics instead
of raw definitions or asset contents.

Publication rechecks source, credential, placement, default, override and asset
revisions. File materialization and bridge claims are requirements, not evidence
that their producers exist. The master must refuse publication until those
requirements are resolved and the selected enforcement profile is qualified.
Missing dependencies fail compilation; deliberate disabled/empty workloads have
explicit omission reasons. A failed compile must never become an empty deployment.

Current profile boundaries:

- Native WG/AWG preserve keys and explicit client addresses; OpenVPN/ocserv need
  placement-owned session-address/subnet bindings. Native networking overrides
  use the durable binding transaction described in
  [node-native-network-binding.md](node-native-network-binding.md).
- SSH preserves login credentials, requires explicit local host-key generation or
  supplied identity, and declares WSS file assets and any Xray bridge dependency.
- Xray handles assigned VMess/VLESS/Trojan/Shadowsocks/Hysteria/HTTP/mixed users,
  scoped TLS/REALITY identity, outbound dependencies and routing selectors. DNS,
  geodata, tunnel, loopback and internal bridge profiles still need qualification
  and are rejected by the current compiler.
- Sing-box handles TUIC/AnyTLS with inline node certificates. The initially
  qualified base is one plain direct outbound and its final route; advanced
  outbound/DNS/routing options are rejected instead of being forwarded unchecked.
- MTProto preserves assigned FakeTLS secrets and ad-tags, requires explicit
  trusted proxy ranges when PROXY input is enabled, and requires node-bound TLS
  assets plus explicit public bind/advertised ports for WEB HTTPS. A master WEB
  backend port or cover directory is not automatically reused on a node.

Limits are explicit: at most 4,096 inbound definitions, one million client
occurrences in compiler input, 60,000 active native client classes, a 32 MiB
configuration payload and 4,096 WEB profiles per origin. They are rejection
bounds, not proof of target fleet throughput. Positive shared IP/device limits
remain rejected until their distributed enforcement is qualified.

Tests include actual compiled VLESS forwarding and canonical routing, preserved
HTTP/mixed usernames, native/SSH configuration validation, node asset isolation,
secret projection, explicit WEB/proxy requirements and cross-core listener
conflicts. Linux race tests and compiler negative controls cover these paths.
All-client/all-core performance, complete bridge/asset activation and every
advanced networking profile remain release gates.
