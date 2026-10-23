# Headless MTProto WEB ingress

A node can serve Telegram WEB traffic directly over HTTPS through its MTProto
worker. It uses the same proxy, authenticated client identity and local quota/rate
account as that inbound's native MTProto listeners. This does not introduce a
second budget or rely on a panel UI process.

The WEB runtime contract has an explicit `bind`, canonical `host` and advertised
`publicPort`. The compiler receives those from the node's approved WEB binding;
it never reuses the master's internal WEB backend port as a public listener.
Node-authorized `serverCert` and `serverKey` PEM material selects direct TLS.
The pair must match and cover `host`; absent, partial or mismatched identity is
rejected for a public listener. The combined PEM input is bounded to 32 KiB.
Certificate issuance, renewal and trust-chain delivery remain operator/master
asset responsibilities.

Direct TLS requires TLS 1.2 or newer. The HTTP authority must match the configured
hostname and advertised port. Browser bootstrap, CSP and WebSocket origin use
that authority; capability identity continues to use the canonical hostname.
The pinned MTG patch adds the advertised port to the existing page generator.
It does not change the capability derivation or MTProto credentials.

The direct listener attributes clients from the accepted socket's remote
address. It rejects a plaintext PROXY prefix, and forwarding headers cannot
replace that address. Without a TLS pair, the existing loopback-only listener
continues to require PROXY protocol from a loopback peer. That mode requires a
separately managed trusted reverse proxy; public plaintext listeners fail
validation.

Changing only the certificate/key atomically changes the certificate used by
new handshakes. It preserves native sessions, WEB carriers and listener sockets.
Changing bind, authority, TLS mode or other structural configuration requires
the normal managed replacement. Secret rotation preserves listeners but revokes
streams authenticated with the old secret.

The pinned upstream WEB facade derives the DD WEB profile from an EE FakeTLS
secret's underlying key. The native secret stays EE. Native EE traffic and
derived DD WEB traffic are tested together through a secret rotation; no second
credential normalization is maintained by the node adapter.

Cover fetching uses the same effective node network as the native proxy,
including its configured SOCKS route. The owned HTTP transport has no
environment-proxy fallback, verifies the cover's TLS hostname, bounds dialing,
handshakes, response headers and idle connections, and carries request
cancellation through the transport's detached connection-pool dial context.
Shutdown closes its idle connections and all tracked WEB carrier sockets.

`tools/nodekernel/qualify-web.sh` runs actual traffic under the race detector in
private Linux network/mount namespaces, including public IPv4 bind, IPv6 socket
attribution, valid and invalid hostname verification, native/WEB shared quota
and rate, certificate rotation, EE/DD interoperability, routed cover and cleanup.
The associated overlay controls remove each selected protection to verify that
the corresponding regression test fails. They do not edit the dependency cache,
deploy a service or change host networking. These tests qualify this transport
path; they are not evidence of million-client throughput or deployment readiness.
