# Fleet source credentials

`cores.SourceClientCredentialAuthority(kind)` resolves the pure source capability
bound by `core.Bind`. A missing capability or unsupported source kind is refused;
there is no fallback to `CredentialDeclarer` or a local runtime.

The creation transaction calls `MintSourceClientCredentials`, merges its `Values`
into a separate copy of the supplied binding values, then calls
`ValidateSourceClientCredentials` before saving. `Values` contains additions only.
An incompatible nonempty value which legacy minting would replace returns
`core.ErrSourceCredentialConflict`, without including its secret in the error.
Unchanged explicit values still require the validation call. Inputs are not mutated.

One logical client owns its identity and allowance. The caller may generate a
shared UUID once and provide it to compatible bindings. Automatic passwords and
other incompatible credentials belong to each binding; a 16-byte SS2022 key is
not silently replaced when another binding requires 32 bytes. A supplied shared
password must satisfy each applicable target, unless the request explicitly
provides a binding-specific override. These APIs do not persist or publish anything.

Source validation follows the node credential contract: canonical nonzero Xray
UUIDs, SS2022 key size, HTTP/mixed username and password, usable nonzero FakeTLS
keys, canonical nonzero WireGuard keys with matching supplied keypairs, and native
login/password limits. The legacy VMess `security` field is not a source credential
because the current node compiler does not consume it. Native address assignment,
server assets, quotas, admission, and runtime qualification are separate gates.

WireGuard `allowedIPs` uses the client's existing comma-separated source form:
empty clears the value; nonempty input is limited to 4096 bytes and 128 prefixes,
each parsed as a network prefix. This checks syntax only. Conversion to the typed
client list preserves the prefixes; the node compiler still enforces its stricter
address, ownership, and overlap rules.

OpenVPN source minting generates only username/password. An absent `tlsCryptV2`
always appears in `NodeDerived`, including when no server material exists yet.
The source path never invokes OpenVPN, generates host keys, or creates temporary
key files. Supplied wrapped material is preserved and checked for the node
compiler's PEM structure; correspondence to the node's server key still requires
node-side validation. Neither absence from `NodeDerived` nor successful source
validation establishes forwarding readiness. Legacy minting retains its existing
daemon-backed wrapping behavior.

Focused tests exercise mint/validate/retry through the actual registry, refusal of
legacy repair, independent SS2022 binding keys, node account compilation, and an
OpenVPN host-dependency trap. `tools/nodekernel/source-credentials-negative.py`
runs removed-safeguard variants using Go overlays without changing production
source. This capability does not enable the fleet factory or any native gate.

## Authentication claims contract

`SourceClientCredentialClaims` returns a
bounded nonempty set of `{Scheme, Digest [32]byte}` after source validation.
Each digest uses a versioned domain and the canonical authentication lookup key; configuration
labels and legacy `ClientIdentity` do not substitute for these claims. Claims
must not contain raw credential material or be exposed in public responses.

| Source kind | Canonical authentication identity |
| --- | --- |
| VMess | The pinned Xray `protocol.ID.CmdKey()` bytes. |
| VLESS | The parsed UUID after pinned `vless.ProcessUUID` clears bytes 6 and 7. |
| Trojan | The SHA-224 password lookup key used by the pinned validator. |
| SS2022 | The first 16 bytes of BLAKE3-512 over the decoded exact-size client PSK. |
| Legacy Shadowsocks AEAD | The effective pinned cipher enum and `Account.AsAccount().Key`, independent of method aliases. |
| Hysteria | UUID-shaped auth clears bytes 6 and 7; other auth remains exact bytes under a distinct scheme. |
| HTTP/mixed | The exact username, including the node compiler's email fallback when absent. |
| TUIC | The parsed 16-byte UUID, independent of accepted textual spelling. |
| AnyTLS | The SHA-256 password lookup key. |
| SSH, OpenVPN, ocserv | The exact validated login username, independent of its password. |
| MTProto | The 16-byte FakeTLS secret key, independent of hostname and hex/base64 encoding. |
| WireGuard/AmneziaWG | The parsed 32-byte public key. |

Persistent ownership belongs to the creation/source transaction, scoped to the
immutable template identity and serialized with other source writers. Disabled
and pending owners still reserve identities. A source disable, deletion, or
credential edit cannot release a claim while an older authorized credential may
still serve; release requires the appropriate node drain proof. This pure
capability neither changes persistence nor opens native/factory gates.

Qualification covers actual Xray UUID validators, VMess AEAD authentication,
Trojan lookup, SS2022 authentication over an in-memory connection, and node
compiler rejection of duplicate usernames/public keys/FakeTLS keys. Shared
synthetic reference fixtures also run against pinned sing-box authenticated-user
updates and MTProto secret parsing without importing either runtime into the
master module. Root race tests, nested conformance checks and scoped lint pass;
all 12 controls in `tools/nodekernel/source-claims-negative.py` reproduce their
intended semantic failures using source overlays.
