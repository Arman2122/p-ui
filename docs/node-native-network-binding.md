# Native node network binding

The native runtime accepts an optional `network` object in its configuration. The master compiler can supply different values for each node:

```json
{
  "network": {
    "peerIPv4": "172.31.240.2",
    "gatewayIPv4": "172.31.240.3"
  }
}
```

These addresses support the private download shaping hop. The default peer is `169.254.234.2/32`; the default gateway is `169.254.234.3`. Omitting `network` selects these defaults. The peer address is assigned to the owned receiving veth. The gateway is a permanent neighbor on the sending veth.

Both values must be distinct, canonical IPv4 addresses from private or link-local space. The first and last `/24` of `169.254.0.0/16` are excluded. The runtime rejects overlap with host addresses or nondefault routes, native client/server addresses or pools, and resolved control/enrollment endpoints. A host route covering the default addresses requires a nonconflicting override.

A fresh node starts its control connection before creating this network. Its first configuration can therefore select an override even when the defaults conflict with the host. Configuration validation checks existing routing resources and neighbors without changing them.

## Changes and recovery

The credit journal records the current binding and a pending target. Applying a changed binding first stops forwarding and purges queued packets, while retaining issued quota reservations. It then records the target, reconciles the owned addresses, neighbors, and routes, commits the binding, and applies the inbounds. Existing HTB classes are retained.

Pending transactions cannot reserve new forwarding credit. After interruption, the runtime resumes the recorded target using the saved current and pending identities. Known partial address/neighbor changes are reconciled. Foreign or unexplained resources fail closed; an incomplete pair without ownership evidence is not adopted. Existing credit records without their durable binding also fail closed.

The veth names (`phvdownout`, `phvdownin`), IPv6 next hop (`fe80::2`), routing table (`32000`), rule priority (`30000`), and route protocol (`247`) remain reserved. Address overrides do not relocate those resources. IPv6 uses the same final authorization path and requires host IPv6 forwarding to be enabled beforehand. Host global forwarding and reverse-path filtering remain administrator-managed prerequisites.

## Client address reservations

The master compiler now assigns stable OpenVPN host addresses and ocserv session subnets from the effective per-node pool. OpenVPN uses one `/32` per client and reserves the network, server and broadcast addresses. Ocserv currently uses one `/29` per client with one concurrent session. Supported allocation pools are private IPv4 prefixes from `/16` through `/29`. WireGuard and AmneziaWG retain their explicit client addresses; those addresses, server addresses and the configured shaping hop are excluded from generated allocations.

Preview computes the assignments without reserving them. Publication revalidates the complete source manifest and commits address claims, forwarding authority and the deployment in one PostgreSQL transaction. A retry preserves the same claim. Changing a pool or its binding requires retirement rather than silently assigning an existing client another address.

An inactive native placement can leave the compiled graph only after every grant has an applied stop and zero settled rate reservation. Its address remains reserved until a later published graph omits the binding and the node acknowledges that complete graph. A subsequent publication releases the retired claim atomically. An offline node, missing stop evidence, pending configuration, remaining rate reservation or mismatched installation prevents reuse. This address lifecycle does not reclaim uncertain traffic credit.

The public-path PostgreSQL tests cover OpenVPN and ocserv publication, transaction rollback, retries, explicit-address conflicts, and retirement through an applied stop followed by a published removal. Native authenticated first-use admission and automatic publication scheduling remain separate integration work.

## Qualification evidence

The isolated Linux fixtures cover nondefault TCP/UDP NAT, routing-mark preservation, host firewall denial, dual-stack WireGuard/AmneziaWG traffic, live binding changes, helper restart, conflicting management/client addresses, IPv6 queued expiry with shifted logical time, and partial transaction recovery without quota replay. `tools/nodekernel/native-network-negative.py` removes three protections through Go compiler overlays and requires the matching regressions to fail.

The native production privilege proposal and the combined host-suspend/wall-clock-rollback/dead-helper deadline gate are tracked separately in the node implementation progress document.
