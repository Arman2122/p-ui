# Owned fleet subscription projection proposal

Status: architecture approved, implementation in progress, 19 October 2026. The pure renderer checkpoint is being qualified; the projection and HTTP/UI integration are not complete. The master factory and native public capability gates remain closed.

## Existing evidence and gaps

`FleetNetworkCompilation` freezes the manifest and server runtime definitions. `FleetNetworkPublication` relates a compilation to a deployment revision. `FleetPublishedRuntime`, `FleetPublishedClientAdmission` and `FleetPublishedAdmissionBinding` index the digest-verified deployment's descriptor scope; `PublishedAuthorityScopeTx` reads those records. An admission-only graph legitimately has no `BaseGrant`. `FleetNode.AppliedRevision` identifies acknowledged configuration, while `FleetRuntimeJournal.Ready` is a separate, transient observation.

These records do not contain a complete client profile. The WireGuard runtime needs a peer public key, but the subscriber needs its own private key and the allocated address. The OpenVPN compiler verifies a supplied wrapped client key, but its runtime client representation retains the login and addresses, not the wrapped key. The exact compilation input must therefore contribute client share material before it is discarded. A current `ClientRecord`, lowest inbound credential, embedded legacy peer, or current plan/group join cannot reconstruct historical applied credentials safely.

Existing `cores.ClientShare` / `core.LinkRenderer` implementations cover sing-box links and SSH/native files. Xray and MTProto link encoders still live in `internal/sub/service.go`; JSON and Clash also contain format-specific paths. Existing native file renderers have legacy defaults, including WireGuard's fallback address, which are not evidence of an allocated fleet binding.

Legacy `Host` rows are mutable and keyed by numeric template ID, without node installation or publication scope. Neither they nor the old `Node` table provide a safe fleet advertised endpoint. A subscription server URL and a node's control-plane peer address are not data-plane endpoint evidence.

## Proposed data additions

These are proposed additions, not existing models.

1. `FleetCompiledClientShare`: immutable rows keyed by compilation, client UUID, runtime ID, inbound-instance UUID and endpoint ID. Each row records the stable share UUID, placement/credential scope pins, core/protocol, payload digest, renderer version, and either the canonical client deliverables or a bounded diagnostic. Payloads are internal fields (`json:"-"`). A client-leading index supports subscription reads without scanning all clients on a node; a compilation-leading key supports atomic publication verification and pruning.
2. `FleetPublishedShareSelection`: one row per node UUID, installation UUID and deployment revision, selecting an exact publication operation, compilation and share-set digest, also bound to the deployment digest. It is a publication selector, not authority or an ACK. Missing selection is unknown and never invokes legacy reconstruction.
3. A nullable share-set digest/version on `FleetNetworkCompilation`. The digest covers sorted row identities, scope pins, diagnostics and payload hashes. Existing compilations without the new evidence remain unknown; there is no source-based backfill.

The selector is needed because `PublishConfiguration` deduplicates identical runtime bundles. An explicitly approved endpoint-only edit can create another compilation/publication on the same deployment revision. Selecting an arbitrary publication would be ambiguous, while replacing immutable share rows would erase the previous evidence. A fresh publication changes the selector under the existing node lock; exact operation replay returns its receipt without rewinding a newer selector. No additional node wire field or durable agent state version is needed.

Public endpoint metadata belongs in the already versioned `FleetInboundNetwork`, rather than a new mutable endpoint table. The initial bounded shape is a stable endpoint ID, explicit DNS name/IP, and optional explicitly advertised port (otherwise the exact compiled listener port). The compiled listener identity and role remain pinned separately, so an advertised NAT port cannot be mistaken for the node's bind port. No endpoint is inferred from the HTTP request, management host, control stream, wildcard listen address or legacy host rows. TLS/transport parameters come from the compiled connection; arbitrary CDN security/path transformations are outside the first slice and must remain unsupported until qualified.

## Compiler and publication boundary

The compiler produces one canonical share artifact from the same effective inbound and per-binding user that produced the runtime configuration. Existing credential overrides are per template/client, not an invented per-node credential table; the same template's supplied authentication material is reused exactly where compilation accepts it. Native addresses come from the immutable native-binding pin. Public certificate/key material comes only from the exact referenced asset revision or a separately qualified authenticated node observation. Server private keys, unrelated clients, provider credentials and control-plane secrets must never enter the share artifact.

Client secrets belong in the authorized client deliverable, including a WireGuard client's own private key. Missing client private material is pending/unavailable; a profile never invents it. OpenVPN's missing node-derived wrapper remains pending. Native public gates remain closed even when static settings and an address allocation exist. No new worker key-report protocol is proposed in this slice.

Preview persists the share rows and digest alongside the compilation in the same transaction. Share availability is distinct from configuration compilation success. Publication revalidates the current compilation input as it already does, verifies the frozen share digest and exact membership, and selects that artifact only after the deployment and publication are persisted. Reads never repeat source materialization or a full runtime-payload scan for every client.

Stop publication copies only the exact prior selection after its existing frozen graph/descriptor/assets proof, rebinding the deployment digest and revision. It cannot copy fresh endpoint or source state. Provider refresh and normal source publications generate/select their own complete projection. Unknown or ambiguous provenance stays unknown. Projection retention follows desired/applied/sent deployment retention, preserving the current APPLIED selection while a newer revision is pending.

## Read and identity contract

The internal read seam is `ReadFleetClientSharesTx(ctx, tx, clientID)` after token or administrator authorization has resolved the current owned client. It returns an explicit owned/found result, entries and diagnostics, plus authoritative ledger metadata. An owned client with zero applied entries is distinct from an unowned client, so an empty or failed fleet result cannot fall through to legacy links.

The query joins client-indexed immutable rows to the exact current node installation, its `AppliedRevision`, the publication selector and indexed runtime/descriptor digests. Current mutable source credentials and template values are not read for connection construction. A desired-but-unapplied credential or port edit leaves the old APPLIED profile intact. Removal is reflected only by the appropriate applied omission; deleted ownership/token or a retired installation cannot authorize an old file URL.

Admission-only APPLIED descriptors are valid share membership before first-use funding. Requiring a grant or spendable credit to emit a profile would prevent the first authenticated connection that requests activation. Share availability therefore does not mean forwarding readiness, current liveness, quota availability or a settled rate share. Those are separate statuses derived from current authoritative observations/ledger. Paused or exhausted status must never be inferred from absent legacy counters.

The stable file/share UUID is derived from client, node, installation, runtime, inbound-instance and endpoint identities. It excludes compilation, deployment revision and credential generation, so a normal update preserves the URL while an installation replacement does not redirect an old URL into a new installation. Numeric inbound IDs remain template metadata and are not repurposed as fleet node or file identities.

Reads are bounded, cancellable and use one consistent read-only snapshot. SQL resolves only the requested client's records, with batched scope verification and no per-entry source queries. Capacity exhaustion is a typed failure before a partial subscription is emitted. Initial capacity limits and index plans need explicit qualification; the design alone does not establish million-client throughput. One logical client's ledger is counted once regardless of how many node entries it has.

## Rendering and delivery scope

The shared primitive remains `core.LinkRenderer` / `cores.ClientShare`. The bounded prerequisite is extracting the existing Xray and MTProto connection encoders into pure helpers consumed by their core adapters and existing legacy wrappers. The service layer gains no protocol switch and no second encoder. Existing pure URI-to-Xray-outbound and Clash conversion helpers consume the canonical share where the target format supports it; unsupported formats remain explicitly unsupported. Round-trip fixtures must prove that the canonical share preserves the approved transport, TLS, credentials and endpoint before replacing a legacy format path.

Raw subscriptions, JSON/Clash, panel links/QR and file downloads consume the same selected artifact. Owned panel views stop deriving WireGuard files from `InboundOption` or a numeric attached-inbound list. The sub token authorizes only that client's entries. A new opaque fleet file route is distinct from the legacy numeric file route; all bodies and API metadata remain `no-store`. Subscriber delivery URLs may still use the configured subscription origin, but that origin never becomes a node connection endpoint.

Existing external client links remain a separately labeled source supplied by the operator; they are not fleet node evidence. Their current authorization and fetch policy must not be broadened while introducing the fleet branch.

## Proposed implementation ownership

- Pure sharing: `internal/core`, `internal/cores` facade/adapters, a pure extracted helper package as necessary, and existing share/round-trip fixtures. No core lifecycle or native authority changes.
- Persistence: one new model file with the two proposed tables, a narrow migration/index addition in `internal/database/db.go`, compilation share digest fields, and dedicated fleet share compiler/publication/read files in `internal/web/service`.
- Publication hooks: narrow additions to fleet preview/publish and Stop provenance; preserve all existing authority validation, lock ordering and exact retry semantics. No quota writes, new authority or synthesized ACKs.
- Delivery: an owned branch in `internal/sub` raw/JSON/Clash/file/page entry points and the registered panel link provider. New fleet file/share DTO/routes only after coordination with the source-read/API owner, followed by normal generated schema/OpenAPI/docs updates.
- Frontend: generated share response consumers in client info, saved-links and QR/file controls, replacing client-side reconstruction only for owned entries. Ordinary flows stay covered by current fixtures.

Implementation proceeds in reviewed checkpoints: pure renderer conformance; immutable projection/publication/read tests; then the HTTP and UI consumers. Endpoint-bearing management specifications must use a new version that older master parsers reject; no node wire or state version change is needed for those endpoints.

## Required regressions

- One logical client on two APPLIED nodes with different approved ports/settings, plus distinct per-template credential overrides; all formats and panel/file outputs agree per entry without flattening those overrides.
- Inherited-only plan/group bindings without `ClientInbound`, including two templates with incompatible generated credentials.
- Admission-only APPLIED graph with no term/grant can emit a profile; desired-only, missing endpoint, missing projection and foreign installation cannot.
- Pending source credential, port and endpoint edits preserve the old applied profile until the corresponding approved selection becomes effective.
- Identical runtime publication with a new endpoint-only compilation selects exactly that metadata; replay of an older publication cannot rewind it.
- Stop carry-forward preserves unrelated artifacts and cannot read future endpoint/source values; omitted applied bindings remove their files.
- Stable per-node/inbound/endpoint file IDs across revision changes, with cross-client and replaced-installation requests refused.
- Native missing key/address/wrapper stays pending and never falls back to embedded peers or a fabricated address; no server private material reaches public payloads.
- Read-only snapshot consistency, bounded indexed client lookup, no N+1 source/payload scans, honest capacity errors and one ledger aggregate across multiple entries.
- Removed-safeguard controls for APPLIED gating, actor/token scope, immutable credentials, endpoint provenance, duplicate-base selection and native pending behavior.
