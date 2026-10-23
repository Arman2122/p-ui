# Fresh authenticated node demand

This path separates proof of an authenticated attempt from forwarding authority. A node can ask the master to activate an assigned client, but a successful activation response contains only the authoritative term. It cannot create a governor account, assign speed, reset traffic, or authorize a packet. The original denied attempt stays denied; a subsequent attempt can use separately committed quota and rate authority.

## Scope and lifetime

Each published runtime carries admission descriptors for its assigned credentials. A descriptor pins the stable client ID, credential generation, placement ID and revision, inbound instance ID, and runtime-local inbound tag. The runtime callback captures an immutable descriptor snapshot. The broker compares that snapshot with the currently applied descriptor before accepting a request; an old authentication callback cannot adopt a replacement credential generation.

The worker disarms requests before applying a new graph and arms the new descriptors only after durable successful application. Stream runtimes are closed when admission descriptors change, ensuring newly created authentication handlers receive the matching snapshot. Unchanged descriptor sets preserve the existing runtime. The immutable comparison remains necessary even when shutdown normally drains authentication handlers.

Requests live only in memory, for at most five seconds and no longer than their caller's context. A runtime admits at most 256 pending requests, coalesces concurrent attempts by client, and delivers each request once. Cancellation, graph replacement, expiry, or restart cannot turn the queue into a delayed activation log. A lost local response requires a new authenticated attempt.

Request deadlines use a separate lower bound from the latest authenticated time response, with floor-rounded boot-clock elapsed time. Authorization expiry retains its conservative upper bound and durable high-water mark. Reusing that expiry high-water mark for request deadlines could extend a stale request after a clock correction. The request clock is memory-only, so a restarted runtime synchronizes before requesting activation.

## Transport and master decision

The agent reads requests over peer-credential-protected Unix RPC, pinned to the registered runtime state and credit journals. It forwards only requests for its currently settled applied deployment, retains at most 1,024 reply routes, and starts this bridge only after the protocol handshake and authority fence. Replies return to the same runtime and journal pair. Independent local runtime failures do not erase existing forwarding authority.

The master checks the authenticated node certificate, current session, exact applied deployment descriptor, placement and credential revisions, source eligibility, and request deadline. The term decision occurs in the supplied database transaction. An existing active term is returned unchanged; first-use activation occurs only at this live master decision. Disconnected nodes cannot activate or renew a term.

Control-stream activation work has a per-node concurrency bound of four and a global bound of 32. Slow billing work does not block deployment messages or heartbeat processing. Cancellation is joined when the session ends.

## Qualification and remaining integration

The real Xray fixture verifies that invalid credentials emit no demand, valid unfunded credentials emit the canonical identity and inbound, an affirmative callback still forwards no traffic, and separately installed authority subsequently forwards with exact byte accounting. Worker/agent tests cover applied-revision and journal scope, failed application, cancellation, replay, capacity, and credential-generation replacement. The other stream adapters have corresponding authentication fixtures; runtime capability enablement follows their race and removed-guard checks.

WireGuard and AmneziaWG require a separately qualified source of authenticated peer demand. A timer, an arbitrary interface packet, or an accounting callback is not interchangeable with proof of a new authenticated attempt.

Initial quota and rate allocation remains a separate master scheduler concern. Its queue must contain an already-authorized term and expire stale demand; it must never replay first-use activation. Allocating another node must preserve every outstanding quota and rate reservation, including authority held by disconnected nodes. Incremental authority delivery needs its own applied-graph and journal fences; the existing credit-only top-up channel must retain its narrower contract.
