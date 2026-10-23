# Penhoon node control plane v2 plan

Status: implementation in progress, started 8 October 2026. Product decisions below are confirmed. Enforcement feasibility, performance, and production guarantees remain subject to the implementation and qualification gates. See node-control-plane-v2-progress.md for implemented scope and verification evidence.

The recommended design is a master-owned control plane and a dedicated headless node agent, developed in this repository and released as separate installable products. Every node runs its own complete networking configuration and uses its own configured outbounds. Nodes establish authenticated management connections to the master, retain durable local operating state, and enforce their assigned policies while the master is unavailable. Existing multi-core adapters remain the foundation for execution.

## Confirmed requirements

The operator selected the following requirements during planning:

- Up to 1,000 nodes and 1 million registered clients.
- Nodes continue serving existing clients during a master outage, subject to locally assigned limits.
- A simple single-master installation first, with an explicit path to high availability.
- All currently supported cores are in scope, introduced through staged implementation and qualification.
- Direct outbound node-to-master TLS connections are sufficient initially.
- Each node owns its networking execution, including inbounds, outbounds, DNS, routing, egress, and local enforcement. Remote nodes may inherit outbound definitions, but execute their own connections and never use the master's running outbound merely because it manages them.
- One logical client identity and one shared traffic allowance span all assigned nodes and inbounds, including simultaneous connections.
- A client's configured speed limit is aggregate across all its connections and nodes. A 100 Mbps limit is not independently multiplied by the node or inbound count.
- Client placement on new nodes is automatic through master-owned plan/group rules rather than copying every client to every node.
- The master manages and synchronizes the complete assigned node configuration and accounting, rather than only inbound/client CRUD.
- Nodes automatically inherit the master's default node configuration, with explicit, professionally managed node-specific overrides when needed.
- Offline quota policy is strict: when a node exhausts that client's locally reserved authority, it stops that client's service until more authority becomes available.
- Both master-only and master-plus-local-node installations are first-class deployment modes. A second server is optional.
- The master's node-control listening port and advertised connection endpoint must be configurable independently of the panel UI and client-facing ports.
- No node web UI. Installation, enrollment, configuration, diagnostics, updates, and retirement are controlled through the master.
- The project is unreleased. Replace the old node mode completely: fresh enrollment only, with no old-node support, migration, import, transport fallback, or mixed legacy fleet.
- Prove enforcement feasibility first, then use minimal, pinned, maintained core patches where necessary, with upstream tracking and compatibility tests.
- During master outages, continue only already active authorized terms within their limits. First-use activation and automatic renewal wait for the master; no future term is activated autonomously offline.

One million registered clients is a cardinality target, not a definition of simultaneous sessions or client-to-node assignments. The capacity envelope below makes those additional assumptions visible. A production capacity claim must name its workload and hardware.

## Findings in the current implementation

These are architectural observations, not a claim that every mechanism below is currently defective.

| Current mechanism | Evidence in this repository | Consequence for the replacement |
| --- | --- | --- |
| Nodes are independently installed panels; remote actions call their administrative HTTP API | internal/web/runtime/remote.go, especially do, UpdatePanel, FetchTrafficSnapshot | Give nodes their own protocol and package, independent of browser routes and panel settings |
| Remote operations use tags, local integer IDs, aliases, and cached mappings | remote.go: resolveRemoteID, AdoptInboundAlias; internal/web/service/inbound_node.go: ReconcileNode | Assign immutable control-plane resource IDs and remove old-node alias/adoption compatibility |
| A configuration-dirty flag coordinates writes with snapshots that can also supply client lifecycle state | internal/database/model/model.go: Node; inbound_node.go: setRemoteTrafficLocked | Separate desired configuration, observed runtime state, and accounting events |
| Traffic, presence, IP attribution, plans, and bans move through several polling and push paths | internal/web/job/node_traffic_sync_job.go | Give each traffic class independent durability, prioritization, and backpressure rules |
| Traffic synchronization has eight concurrent workers and combines reconciliation with per-node collection | node_traffic_sync_job.go: nodeTrafficSyncConcurrency, syncOne | Use persistent sessions and bounded per-node workers; one slow node must not delay fleet progress |
| Node heartbeat has a separate bounded polling loop, and node hierarchies report descendants | internal/web/job/node_heartbeat_job.go; internal/web/service/node_tree.go | Use direct node identities and independent connectivity/runtime health dimensions |
| Runtime mixes local execution with remote panel operations, including Xray-specific restart methods | internal/web/runtime/runtime.go | Introduce a durable deployment contract rather than treating a remote machine as a local function call |
| Core instances already avoid panel/database types, and adapters are compiler-fenced | internal/core/core.go; internal/cores/cores.go | Preserve these boundaries and remove remaining panel dependency callbacks through explicit node-local providers |
| Xray base configuration and provider outbounds are assembled from panel settings; routing/egress paths contain local-node assumptions | internal/cores/cores.go: XrayBaseConfig and HTTPClient dependencies; internal/web/service/xray.go: GetXrayBaseConfig; internal/web/service/routing.go; internal/web/service/egress.go | Extract node-scoped configuration assembly and resource references; replacing inbound RPCs alone does not synchronize a whole node |
| Some core daemons already run under systemd independently of the panel | internal/supervisor/systemd.go | Extend and verify that model rather than inventing another process supervisor |
| Shared accounting intentionally avoids guessing unknown usage; Counter baselines are in memory | internal/core/counter.go | Add durable collection boundaries without inventing usage after an unrecoverable measurement gap |
| Renewal generations exist, but offline reset delivery is explicitly outside their scope | docs/node-renewal-tracking.md | Carry forward the proven renewal semantics and add durable delivery in the new protocol |

Preserve unrelated working-tree changes and useful accounting, reset, authentication, quota, and concurrency regression scenarios. Tests that exist only to preserve the removed node protocol must be retired or replaced deliberately with tests of the new contract. A fresh node architecture does not authorize resetting the entire checkout or discarding unrelated panel improvements.

## System boundaries and ownership

The master owns clients, plans, reseller/group budgets, desired configuration, placement, credentials policy, releases, and audit history. A node owns the observation and execution of its assigned resources. Observations never become an implicit configuration edit.

A node belongs to exactly one control-plane identity at a time. Multiple master processes may later serve that identity. Independent panels do not concurrently manage the same node. Organizational groups and regions are metadata, not trees of panels that rewrite each other's state.

The same client may have simultaneous assignments on several nodes and inbounds, all consuming one shared allowance. Client identity, protocol credential identity, inbound identity, and accounting producer identity are separate concepts. A shared logical client does not require identical credential formats across different protocols. UUIDs are stable identifiers; email, display name, inbound tag, and IP address are attributes. Identities created in the new system remain stable across ordinary edits, reassignment, restarts, and upgrades unless explicitly rotated.

Each node executes its own client data path: client to that node's inbound, through that node's routing policy, to that node's selected outbound or egress. Client traffic does not traverse the master merely because the master manages it. When the master machine also hosts a local node, only traffic explicitly assigned to that local node uses its networking resources.

Two different meanings of outbound must stay separate: a network outbound is a proxy/egress selected for client traffic; an outbound-initiated control connection is simply the node dialing the master's management endpoint. Establishing the latter never selects the master's client-traffic outbound.

~~~mermaid
flowchart TB
  UI["Master panel and public API"] --> CP["Control plane modules"]
  CP <--> PG[("PostgreSQL: desired state, operations, budgets, usage")]
  A["Node A agent"] -->|"Management mTLS on configured port"| CP
  B["Node B agent"] -->|"Management mTLS on configured port"| CP
  A -->|"Durable usage reports"| CP
  B -->|"Durable usage reports"| CP
  A --> RA["Node A cores and routing"]
  B --> RB["Node B cores and routing"]
  U["Same logical client"] --> IA["Node A inbound"]
  U --> IB["Node B inbound"]
  IA --> RA --> OA["Node A own outbound / egress"]
  IB --> RB --> OB["Node B own outbound / egress"]
  OA --> NET["Internet or configured destination"]
  OB --> NET
  L["Optional local node on master host"] -->|"Same management contract"| CP
~~~

### Complete node configuration and synchronization

The master stores desired state for every node. It compiles an immutable, node-scoped deployment graph with all dependencies required to execute that state. A node stores a durable materialized copy of its own graph and enforces it locally; it does not need other nodes' secrets or the master's entire administrative database.

| Resource | Master responsibility | Node responsibility and reported state |
| --- | --- | --- |
| Inbounds and client attachments | Assign stable identities, listeners, protocol settings, and eligible clients | Bind local listeners, install credentials, report actual listeners and readiness |
| Outbounds and providers | Specify each node's direct, proxy, tunnel, or provider-backed outbounds and credential references | Execute those connections from that node; report reachability and provider/core errors |
| Routing, DNS, and balancing | Resolve the node's routing graph, resolver settings, outbound references, and supported balancing policy | Apply and enforce the complete local graph; report its applied revision and validation errors |
| Kernel egress and host networking | Assign routes, interfaces, tunnel parameters, owned firewall/shaping resources, and dependency order | Reconcile only owned local resources; report missing modules, conflicts, and drift |
| Clients, limits, and lifecycle | Own shared identity, plan, allowance epoch, expiry policy, assignments, and grant allocation | Maintain local credential/policy materializations, assigned credits, and enforcement state |
| Certificates, keys, and assets | Authorize and distribute only required secret versions, certificate policy, and verified geodata/artifact references | Resolve local paths, protect secrets, report effective versions, expiries, and asset hashes |
| Core versions and settings | Select supported versions and per-node runtime configuration | Validate, supervise, update, and report actual version/configuration/readiness |
| Usage and presence | Commit scoped reports into shared client totals and per-node/per-inbound views | Measure locally, journal accounting, and report fresh observations with producer identity |

All resource references are scoped by node and stable resource ID. Repeated human-readable tags on different nodes do not imply a shared object. A routing rule on node A cannot accidentally resolve a same-named outbound on node B or on the master's local node. An intentional remote proxy endpoint is represented as an explicit outbound configuration.

Extract the existing panel-dependent base-config/provider inputs into node-scoped configuration assembly. Preserve qualified routing, egress, and core mechanisms while removing assumptions that only locally stored inbounds participate. Certificate file paths and interface names are resolved on the target node, never copied as paths assumed to exist on every host.

Synchronize desired configuration downward and applied/runtime/accounting observations upward. All managed domains need explicit revision and coverage state, so the master can distinguish missing configuration, an invalid graph, a pending change, a healthy old revision, and a fully converged node. Periodic inventory/hash reconciliation repairs missed notifications; bounded snapshots recover missing delta history.

Local manual changes to managed resources are reported as drift. The agent restores the master-authorized configuration or reports a blocked unsafe conflict. It does not silently promote local edits into master policy. Unmanaged host resources remain outside this ownership boundary. A complete synchronization contract does not mean an unreachable node can update instantly; the master must expose the last observation time and pending work.

### Automatic defaults and explicit node overrides

The master exposes a Default node configuration using the same complete networking editors available for an individual node. New nodes inherit this configuration automatically. A centrally defined outbound is a reusable definition: each inheriting node constructs its own local outbound and establishes its own network connections. It does not tunnel through the master's running outbound.

Resolve effective node configuration from a validated default revision, explicit node-specific overrides, node-local bindings, and assigned workloads. Required fleet security/accounting invariants are outside the override mechanism. The master stores and displays both the source settings and each node's resolved effective graph.

An operator editing a setting chooses its scope visibly: fleet default or this node. Default edits automatically target inheriting nodes through versioned, validated rollout; overridden settings retain their node-specific value. The master-local node participates in the same inheritance mechanism. Editing its override does not silently edit fleet defaults.

For each overridable setting or resource, show Inherited or Overridden, the source revision, the effective value, and a Reset to default action with a diff. Inheritance is explicit metadata: missing, empty, zero, and reset-to-default must not be conflated. A reset removes the override without regenerating client credentials or silently clearing unrelated settings.

Use schema-defined override operations and stable resource IDs. Ordered routing rules and similar lists must use an explicit full override or validated ID-addressed operations with defined ordering; arbitrary deep JSON merging is not a configuration contract. Validate dependencies, deleted references, port conflicts, and core capability changes against the entire affected resolved graph before publishing.

Defaults cannot copy machine identity or assume shared physical resources. Mint node identity and host keys locally; bind certificate references, interface names, addresses, and secrets explicitly to the target node. Initialize defaults through the new configuration model, classifying reusable values separately from host-bound values. Node bindings are filled automatically where safe and surfaced as required setup when ambiguous.

Each published deployment pins the exact default/override/provider/asset revisions used to compile it. Automatic inheritance means following newly approved default revisions through the rollout controller, not dereferencing mutable settings during application. The master shows affected nodes, override conflicts, pending nodes, and failure reasons. A temporary rollout hold is an operational state; it must not silently become a permanent configuration fork.

Provider refreshes produce versioned inputs. Provider definitions and allowed credentials may be inherited, but node-local probes validate the resulting reachability. A failed configured egress follows its declared containment policy; it must not silently escape through a direct route.

Provider and tunnel identities need an explicit shareability contract. Reusable server parameters do not mean the same enrolled peer/private key or account session is valid concurrently from every node. Declare whether credentials can be shared, provision node-specific upstream identities where supported, and surface missing bindings before deployment. Inheritance must not silently create provider-session or tunnel-peer conflicts.

Stable global resource IDs require durable mappings to node-local bounded identifiers such as kernel routing-table IDs, marks, and device names. Do not hash into an unchecked shared range or reuse identifiers while old resource generations can still be referenced.

### Client placement through plans and groups

The master evaluates plan/group placement rules into stable client-to-node/inbound assignments. A new enrolled node receives matching clients after its labels, core capabilities, required bindings, and configuration have been validated. It does not receive all one million clients merely because it joined the fleet.

Define one deterministic rule combination policy and expose its explanation in the UI. The proposed policy forms eligible targets from plan/group placement rules and intersects them with explicit restrictions, node capability, inbound eligibility, and reseller grants; explicit restrictions win. Repeated membership in several matching groups yields one assignment per client/target, not extra credentials, budgets, or charges. Node-reported metadata cannot expand its own authorized client set.

Placement changes are desired-state revisions with per-node operation results. Removing an assignment revokes its local access and settles or conservatively retains outstanding usage/credit; it does not erase the client's shared billing history. Moving an assignment does not assume an unreachable old node has stopped spending. Heartbeat failure alone must not silently clone a client's full quota or rate authority onto replacement nodes.

Subscription rendering uses centrally managed assignments and node-specific advertised endpoints. Preserve subscription identities created by the new system across normal assignment changes, apply explicit visibility policy, and distinguish pending deployment from confirmed working configuration. No address or certificate path is inferred from the master's own host merely because a node inherits a template.

### Required invariants

1. Only committed master intent may change a managed resource.
2. Receiving or persisting a message does not mean the resource was successfully applied.
3. Repeating a delivery cannot repeat a billable reset or grant additional quota.
4. Older revisions cannot overwrite newer revisions; a rollback is a new authorized revision.
5. A node receives only its assigned client records, secrets, and budget authority.
6. Losing contact with the master does not clear working configuration or recreate counters.
7. An incomplete inventory is never interpreted as a list of resources to delete.
8. Billing data is never silently discarded to preserve telemetry freshness.
9. A control-plane restore cannot silently reset the fleet's authority or billing history.
10. No component promises a guarantee stronger than its core's measurement and enforcement capabilities.

## Repository and deployment structure

Keep one repository initially. Protocol changes, core behavior, installer/update behavior, and compatibility tests for future new-system releases need to evolve together. A separate repository now would add release coordination and encourage duplicated domain logic.

Create separately built and packaged master and node products. Proposed boundaries:

| Boundary | Responsibility |
| --- | --- |
| Existing master entry point | Admin/API, subscriptions, business policies, PostgreSQL |
| cmd/penhoon-node | Headless agent, local CLI, and narrowly selected runtime modes |
| cmd/penhoon-hostd | Privileged local operations through a restricted Unix socket |
| api/node/v1 | Versioned protobuf definitions, generated clients, compatibility fixtures |
| internal/controlplane | Enrollment, desired state, sessions, reconciliation scheduling, operations |
| internal/agent | Local durable state, connection management, apply journal, collectors |
| internal/accounting | Shared identifiers and explicit accounting contracts; master and node implementations remain distinct |
| internal/core and internal/cores | Existing core contracts, adapters, and conformance tests |

Names above are proposed, not existing paths. Prefer a single Go module initially. Compiler/import guards must prohibit agent dependencies on Gin, panel controllers, browser assets, GORM repositories, or a PostgreSQL connection.

Master storage remains PostgreSQL. Nodes require no database server and no PostgreSQL credentials. Proposed agent-local persistence is bbolt for a bounded journal and materialized state, subject to crash and throughput qualification. It offers an embedded pure-Go transactional store; its single-writer model requires short, batched transactions. It does not replace the master's PostgreSQL database. [bbolt documentation](https://github.com/etcd-io/bbolt)

Agent, host helper, and cores have independent restart boundaries. Embedded SSH and RADIUS functionality must move into appropriate separately supervised node runtime processes so replacing the network agent does not terminate their sessions or authentication service.

Support master-only, node-only, and master-plus-local-node installation roles from the first complete vertical slice. A master-plus-local-node installation provisions the same agent, identity, complete node configuration, quota handling, and accounting path on the master host. Its agent connects through a loopback-reachable authenticated endpoint; it has no privileged in-process shortcut around the protocol. All three roles use the new architecture from their first installation.

### Technology choices and alternatives

| Decision | Recommendation and reason | Alternative and when to reconsider |
| --- | --- | --- |
| Implementation language | Retain Go, the existing supported toolchain, and static Linux builds; share qualified core code | A new language adds a second runtime/toolchain and adapter boundary without resolving distributed-state problems |
| Control transport | gRPC/protobuf provides typed bidirectional streams and an independently versioned machine contract | No old panel-API transport fallback; any future alternative transport requires a demonstrated deployment need and the same new protocol semantics |
| Delivery infrastructure | PostgreSQL outbox and session workers in a modular master | An external broker becomes appropriate if measured ingestion, retention, or independent-consumer needs exceed this design |
| Agent persistence | Qualify bbolt with bounded transactions, compaction, and crash tests | Reconsider the embedded engine if measured local write rate or recovery latency fails; do not substitute an ad hoc journal |
| Master data access | Keep existing GORM domain repositories; use explicit reviewed SQL/batch ingestion for the new hot paths as needed | A repository-wide ORM rewrite is not a dependency of the node architecture |
| Service layout | Separately packaged master and node, with privileged host operations locally isolated | Separate network microservices only where scaling or security measurements require them |
| Configuration model | Versioned desired state plus durable explicit operations | Replaying unversioned imperative CRUD or exchanging full panel database rows retains the current ambiguity |
| Runtime supervision | Reuse and qualify systemd integration and existing core adapters | Container orchestration is unnecessary for the supported bare-metal/VPS installation model |

## Communication protocol

Use grpc-go with Protocol Buffers over TLS 1.3 and per-node mutual authentication. Each node initiates management connections to a dedicated control endpoint with an operator-configurable TCP port. Port 443 is a convenient suggested default, not a protocol requirement. No inbound node administration port is required; configured client-facing protocol ports still need to be reachable.

The panel and node endpoint can share a host but should have separate listeners or hostnames. The control/accounting gRPC listener requires a valid node client certificate at the TLS handshake. Initial enrollment uses a separate ticket-authenticated HTTPS handler, mounted on the master's HTTPS surface or a dedicated bootstrap listener; it exposes no control/accounting RPCs. A load balancer must pass through node TLS or terminate it at a deliberately trusted identity gateway. Ordinary forwarding headers must not be accepted as proof of node identity.

### Control endpoint and network independence

Configure the master's bind address/port separately from the endpoint advertised to nodes. For example, an operator may expose control.example.net:7443 while keeping the panel UI on another port, or map an external port through NAT/load balancing to a different listener. Enrollment material includes the exact authorized endpoint and trust information. Validate allowed port ranges, address families, listener conflicts, and certificate names before installation or a change.

The agent uses the node host's direct management path by default. It must not inherit the master's outbound proxy settings or automatically route its management connection through an inbound/outbound graph that it needs the master to repair. Any future node-specific management proxy is a separate, explicitly configured dependency with a tested recovery path. Node-originated downloads and provider probes likewise use explicitly defined node-side paths.

Owned policy-routing, tunnel, DNS, and firewall changes must preserve the established management path. Preflight and runtime checks cover accidental interception, route loops, DNS dependency cycles, and listener collisions. Security validation still applies when an operator selects a nonstandard port; changing ports never enables insecure TLS.

An endpoint change is a staged operation: add and verify the new listener/path, advertise it with an authenticated configuration revision, let nodes prove reconnection, and then retire the previous endpoint under a visible rollout policy. Offline nodes that have never received the replacement need the old reachable endpoint or an explicit recovery procedure. A UI save cannot safely remove their only known path.

The co-located agent uses the same identity and revision protocol over a loopback-reachable endpoint. Network-bind details may differ from the externally advertised endpoint while retaining explicit certificate verification.

Use two independently bounded channels:

- Control: identity/session negotiation, desired revisions, operation acknowledgments, connectivity heartbeat, runtime conditions, and urgent policy changes.
- Usage: resumable accounting batches with durable acknowledgments.

Use separate underlying connections where necessary to prevent usage backlog from exhausting the control connection's flow-control window. Diagnostic logs are opt-in, capped, and lower priority. Large artifacts travel through verified HTTPS downloads rather than occupying the control stream.

gRPC is the transport, not the durability system. Its documented stream writes may still be buffered, and a broken connection can lose unsent data. Application journals, acknowledgment cursors, and replay are therefore part of our protocol. [Flow control](https://grpc.io/docs/guides/flow-control/), [keepalive](https://grpc.io/docs/guides/keepalive/)

### Session and message contract

The first authenticated handshake supplies the node UUID, installation incarnation, protocol versions, supported capabilities, locally durable applied revision, and usage cursors. Identity is bound to the certificate and stored node record, not trusted from a payload.

Each change carries its control-plane identity, node/resource identity, resource revision, operation ID when applicable, and an authority/session generation. Apply requests also carry dependency and expected-base information. Wall-clock timestamps are diagnostics and policy deadlines, never the ordering mechanism.

The node presents its persisted cursors after reconnect. The master supplies the missing changes or a complete immutable snapshot if retained deltas no longer cover the gap. Snapshot chunks have a manifest, fixed generation, explicit completeness, bounded decoded size, and hashes over specified canonical bytes. Partial snapshots cannot activate.

Deletes are explicit tombstones. Tombstones are compacted only after acknowledgment or an explicit minimum-generation rule that forces stale agents to bootstrap from a full snapshot.

Use exponential reconnect backoff with jitter, rate limits, bounded queues, maximum decoded message sizes, slow-consumer handling, and negotiated compression. Invalid configuration gets a stable rejection reason and waits for a new revision; it must not create an endless rapid retry loop.

The first release accepts only the new node protocol. Future compatibility is independent of the panel release number: support N and N-1 released generations of this new protocol within a published window. This does not include the removed panel-to-panel protocol. Reserve protobuf field numbers on removal, gate features by declared capabilities, and run breaking-change checks in CI. [Protobuf evolution](https://protobuf.dev/programming-guides/proto3/), [Buf checks](https://buf.build/docs/breaking/)

A ten-second application heartbeat and suspicion after roughly three missed reports are initial tuning proposals. They express control reachability, not proof that VPN traffic has stopped. Transport keepalive follows the transport/server limits and is not set aggressively merely to update a dashboard.

Relays are outside the first release. If later required, an opaque transport relay may forward end-to-end authenticated traffic. It must not become a second configuration or billing authority.

## Desired configuration and operations

Configuration follows a reconciliation model:

1. Validate the admin request, authorization, core capability, budget, and optimistic resource revision.
2. Commit domain changes, the new desired revision, and its durable delivery record in one PostgreSQL transaction.
3. Return an operation ID and target list to the UI. A committed save means accepted for delivery.
4. The node durably records the received revision.
5. It validates dependencies, credentials, owned ports, routes, disk capacity, and core-specific configuration before activation.
6. It plans the actual impact: no-op, hot update, or restart of the affected runtime.
7. It applies changes under the node's single execution ownership for that resource group.
8. It reads back observable runtime state, durably records the result, and reports success or failure.
9. The master updates observed state and the UI operation timeline.

Expose at least desired revision, received revision, applied revision, effective hash, last validation error, and runtime readiness. A fleet operation can be partially applied; one unreachable node is not concealed inside a global success response.

This borrows the useful revision and acknowledgment ideas of xDS without adopting Envoy's resource types or pretending that acknowledgment proves complete runtime health. [xDS protocol](https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol.html)

Hot changes are preferred when the adapter supports them. Restart-required changes show their affected inbounds and expected connection impact before execution. Full fleet atomicity is not promised: resources are grouped by actual shared-daemon/network dependencies, and results are reported per group.

Last-known-good recovery must not undo a security revocation, restore a deleted credential, or reset usage. On failure, preserve safe previous configuration where possible; otherwise quarantine the affected resource and report degraded status. Other independent resources continue.

### Operations that cannot be collapsed into configuration

| Operation | Required semantics |
| --- | --- |
| Set desired enabled state | Convergent state; newer revision supersedes older revision |
| Reset a client's allowance | Unique billing epoch transition with a durable per-node application boundary |
| Change quota credit | Unique grant ID and monotonically increasing grant version |
| Restart a runtime | Durable attempt record; after an uncertain response, inspect actual runtime generation before retrying |
| Kick a session | Stable session identity and expiry; if the session is gone, complete harmlessly |
| Upgrade a binary | Pinned artifact identity, verified staging, activation record, health gate, rollback boundary |
| Retire a node | Stop/withdraw workloads and reconcile final usage, then revoke enrollment and certificate authority |

Operations use at-least-once delivery with idempotent outcomes where the actuator supports them. No blanket exactly-once claim is made for external processes. An interrupted non-idempotent action may need state inspection or an explicit uncertain outcome.

The new admin API and its UI use asynchronous operation state directly. An accepted operation is not reported as applied. Remove the old node-operation routes and update the route registry, generated schemas/OpenAPI, frontend calls, and documentation together; no legacy synchronous-operation adapter is required.

## Accounting and offline enforcement

This is the central correctness problem. Retaining a connection stream does not solve accounting, and sharing the last known fleet total does not prevent every disconnected node spending the same allowance.

### One client across several nodes and inbounds

One master client UUID and one billing epoch own the shared allowance. Each real usage meter has an explicit producer identity and declared scope. A producer includes its node installation, local meter/runtime identity and incarnation, client, and billing epoch, with durable sequence/checkpoint information. An inbound attachment is not automatically a distinct meter.

The master adds consumption from independent producers even when they belong to the same client. It deduplicates repeated checkpoints from the same producer. It must neither discard node B's real traffic because node A reported the same client nor add several inbound views that mirror one underlying counter.

Every core adapter declares its authoritative billing meter family: a client aggregate across that runtime, independently measured client/inbound counters, or unavailable attribution. Choose one non-overlapping family for billing. If Xray reports one aggregate for a client attached to two inbounds, that aggregate is recorded once. When distinct per-inbound meters exist, their contributions can be added and displayed separately. Unsupported per-inbound attribution remains visibly unknown; it must not be manufactured by evenly splitting an aggregate.

Client upload/download bytes are normalized from the client's perspective and stored as integers. Inbound, outbound, bridge, and host-interface totals are separate operational statistics and are not added again to the client's bill. Preserve the existing core-specific byte-accounting contract and qualify how protocol overhead is represented; do not claim all native meters measure identical packet boundaries.

Known local core bridges nominate one billable service boundary and classify the other readings as diagnostics. Any supported cross-node/transit topology must declare and qualify its billing boundary before it is enabled in the new system. Similar byte counts, client names, IP addresses, or timestamps are not evidence that two independent observations describe the same bytes. An opaque chain without trustworthy attribution cannot promise automatic single-charge deduplication.

The master-local node is another distinct producer under the same client. It receives no direct-database shortcut that could duplicate or bypass the remote accounting contract.

### Worked example with two independent node connections

All quantities in this example are GiB, where one GiB equals 2^30 bytes. Client C has one 20 GiB allowance, is attached to an inbound on node A and an inbound on node B, and uses both at the same time. A's traffic exits through A's configured outbound; B's traffic exits through B's configured outbound.

| Event | A outstanding authority | B outstanding authority | Master unallocated authority | Acknowledged usage |
| --- | ---: | ---: | ---: | ---: |
| Reserve 8 GiB for A and 8 GiB for B | 8 | 8 | 4 | 0 |
| A reports 1 GiB up + 5 GiB down; B reports 1 GiB up + 2 GiB down | 2 | 5 | 4 | 9 |
| Refill A with the remaining 4 GiB of free authority | 6 | 5 | 0 | 9 |
| Both disconnect, consume their assigned remainders, reconnect, and settle | 0 | 0 | 0 | 20 |

After the second row, the client's combined reported traffic is 2 GiB uploaded plus 7 GiB downloaded, totaling 9 GiB. The accounting identity is acknowledged consumption plus outstanding authority plus unallocated authority equals 20 GiB. Reserved capacity is not displayed as already measured usage.

If A's report is committed but its acknowledgment is lost, replay adds zero. B's independent 3 GiB still counts. If A's 6 GiB meter is mirrored in a second inbound view, A still contributes 6 GiB, not 12 GiB. Outbound and host-interface readings do not increase that bill.

Alternatively, after the refill, A might consume all its 6 GiB while B consumes only 2 of its 5 GiB. Total eventual reported use is then 17 GiB, with 3 GiB still reserved on B. Under the confirmed strict offline policy, A stops serving that client while disconnected. Once connected, an acknowledged reduction of B's unused authority can transfer it to A. A fixed equal split is only an illustration, not the online allocation algorithm.

### Durable usage

The node collects core-native observations, normalizes them through the common counter model, and transactionally persists cumulative attributable usage and its collection checkpoint. The shared counter implementation must gain a durable prepare/commit or checkpoint boundary; do not build a second independent delta engine beside it.

Use separate identifiers for:

- Client and assignment identity.
- Node installation incarnation.
- Core/process or meter incarnation.
- Billing term/epoch.
- Producer batch sequence and acknowledged cursor.

Agent restarts do not create a new billing epoch. Client renames do not reset identity. Daemon counter resets do not reset the customer's term.

The node retains unacknowledged accounting. The master acknowledges only after PostgreSQL durably commits either the accounting transaction or a replayable ingestion batch. If using an ingestion journal, distinguish the durable receipt cursor from the applied/accounted cursor; budget decisions must not treat unprojected consumption as free capacity.

A replay updates each producer's accepted cumulative values monotonically or rejects inconsistent sequences. Deduplication and the corresponding ledger/projection update are transactional. Old terms remain reconcilable without contaminating the new term.

Usage can be coalesced into cumulative checkpoints for the same producer, client, and epoch, preserving the final amount and required time buckets. Different billing epochs cannot be merged. This bounds outage storage without pretending every short-interval graph sample must be retained forever.

The core may lose traffic between its last readable measurement and a crash. That is different from delivery loss. Record an accounting-quality gap instead of inventing a charge. Native quota enforcement, conservative headroom, and capability restrictions determine which strict guarantees each core can support.

### Offline quota allocations

The master reserves a bounded amount of spend authority for each node. The node enforces that authority across the local cores serving the client.

For one billing scope:

**Acknowledged consumption + all outstanding reserved spend authority must not exceed the authorized allowance.**

Outstanding authority includes credit that may already have been consumed but has not yet been reported. When consumption is acknowledged, the same amount moves from reserved authority to acknowledged consumption. Retrying a grant never creates extra credit.

For example, if 20 GB remains, a master may allocate 8 GB to node A, 8 GB to node B, and retain 4 GB for future allocation. During an outage, A and B can keep serving within their own limits. A cannot borrow B's unused allocation while disconnected.

Return or transfer unused credit only after the former node durably reduces its local authority and reports the settlement. A timeout is not proof that credit was unused. Even expiry of a spending lease does not reveal how much was consumed before expiry. Lost-node credit stays conservatively reserved until reconciled or resolved through an audited adjustment.

This is an escrow-style allocation design. The literature on bounded counters supports the general separation of distributed spending rights; the proposed implementation uses a central transactional allocator rather than adding a CRDT database. [Numeric invariants research](https://arxiv.org/abs/1503.09052)

Allocate credit adaptively from observed demand, with a ceiling on stranded/offline credit. Quota exhaustion can stop a client on one node while another node still holds unused credit. This is the explicit availability cost of preserving the global bound while disconnected.

For online nodes, request refill before a low-water threshold derived from measured consumption and refill latency. Use a bounded urgent path for the refill request and any required durable usage settlement, independent of ordinary 15-second batch reporting and log backlogs. The master allocates free authority transactionally, favors active demand fairly, and can transfer unused authority only after the donor has durably reduced it. Qualification includes high-bandwidth clients and near-exhaustion behavior; an available global allowance should not routinely stall traffic because a periodic dashboard update has not run.

A node grant covers the client's combined local inbounds and cores. Either one local governor enforces it, or the node durably divides it into non-overlapping runtime subgrants. Giving an 8 GiB node grant independently to two 8 GiB inbound limiters would authorize 16 GiB and is prohibited. Adding/removing an inbound or changing a core must preserve both consumption and outstanding subgrants.

Strict enforcement also requires bounded local actuation. Polling-only cores can overshoot between samples unless there is an enforceable traffic-rate bound and reserved safety margin. Publish enforcement classes per core: native hard limit, bounded sampled enforcement, or best effort. Never advertise exact global quota enforcement across unsupported combinations.

Master disconnection and local enforcer failure are different faults. Because core runtimes can outlive the agent, a sampling interval plus rate headroom does not bound cumulative overspend if the collector/controller hangs indefinitely. Strict profiles require a data-path quota gate, an independent local cutoff mechanism with a qualified deadline under the stated fault model, or equivalent enforcement that survives loss of the network agent. Without that protection, bounded sampled enforcement is only a healthy-agent property and cannot satisfy the requested strict outage contract. Test stopped processes, scheduler stalls, collector failure, lost policy rules, and surviving daemon traffic explicitly.

Before making a quota slice spendable by a runtime, persist its reservation and actuator generation. If a crash loses the final counter for that slice, the unknown portion remains reserved until it can be reconciled; it is not issued again on reboot. Separate uncertain reserved capacity from measured customer usage: withholding an unproven refund is not permission to fabricate a traffic charge. The same rule applies to local allocation across multiple cores. A durable network-delivery journal alone cannot close this measurement-to-enforcement crash window.

Enforce group and reseller budget constraints in the grant allocator as well as client budgets. A consumed byte contributes once to the client and to each relevant accounting scope, without becoming multiple client charges. Reserve locks in deterministic order and qualify heavily shared groups for contention. Locally share or safely partition assigned credit across cores.

A quota reduction below already consumed or outstanding authority cannot retroactively make existing grants disappear. Treat the lower desired limit as a pending tightening, stop new allocations, and obtain durable reductions or stop acknowledgments before declaring the lower limit effective. Disconnected-node reservations remain visible. The UI must distinguish the requested limit from unresolved authority issued under an earlier authorized revision.

### Aggregate bandwidth and usage-based speed tiers

The operator selected a shared speed limit across all nodes and connections. Existing separate upload/download settings retain their meanings, but each configured directional limit applies across the client's complete assignment set. A 100 Mbps configured direction means 100,000,000 bits/second combined, not 100 Mbps per inbound or per node.

Model rate authority separately from quota credit. The master allocates non-overlapping rate shares to active nodes such that their effective aggregate allowance stays within the configured rate. Each node enforces its share across all local inbounds and cores, or partitions it into non-overlapping runtime shares. Examples include 60 Mbps on A plus 40 Mbps on B, or 100 Mbps on A when A is the sole active holder. These shares are an implementation detail; the customer sees one shared plan.

Redistribute rate toward demand while online. Increasing B's share based on capacity taken from A requires proof that A's former allowance has been durably reduced, or a qualified expiry mechanism that prevents overlap. Lost acknowledgments, agent restart, and uncertain actuator state cannot authorize two active copies of the same rate share. During a partition each node retains only its authorized local share; it cannot assume that idle or unreachable nodes surrendered theirs. Tightening the global rate has the same explicit pending-convergence limitation as reducing a quota.

Define the actual enforcement envelope, including scheduling windows, packetization, and the sum of permitted limiter bursts. For each configured direction, the target token-bucket bound is admitted bytes over an interval no greater than rate_bits_per_second times interval_seconds divided by eight, plus the global burst allowance, with specified measurement and queue/packet effects. A 100 Mbps rate is 12,500,000 bytes/second before that burst allowance. Different cores' instantaneous graphs will fluctuate; the claimed bound must name its measurement layer and windows. Do not enable an unimplemented burst product feature merely because a limiter has internal buffering. Qualify every adapter's actual per-client rate actuator, including live sessions and multiple inbounds; dividing a number in the master is not enforcement.

Usage-based speed ladders must preserve their total-used threshold semantics, not merely bound a separate pool of fast bytes. For a first-10-GiB fast tier, allocating 5 GiB fast rights to A and B and then letting A use later-tier bytes while B still spends fast rights would allow some fast traffic after the first 10 GiB of total use. A fast-byte bucket alone is therefore insufficient.

The proposed strict design authorizes spending in global usage phases. All currently spendable byte rights count against the active phase's remaining interval, including traffic served at a temporarily lower local rate. For each finite phase, consumed phase bytes plus outstanding phase authority must not exceed its authorized byte budget. Later-phase grants remain dormant until the master establishes the prior phase's accounting boundary and durably settles/revokes all higher-rate authority that could still be spent. Agents cannot activate a later phase merely because their own fast share ran out.

This preserves ordering by potentially pausing an offline node at a phase boundary while unused earlier-phase authority remains elsewhere, even when the account's overall quota is not exhausted. The master must show the blocked phase and redistribute authority promptly while connected. A product preference for uninterrupted slower offline service would require a separately specified conservative threshold policy; it must not be silently implemented as independent fast/slow byte buckets. Tier transitions never add another traffic charge, reset total usage, or let an old policy revision restore consumed tier authority.

Rate allocation updates, quota refills, and ordinary accounting reports have different scheduling requirements. A 15-second usage-report interval is not the bandwidth governor. Benchmark allocation responsiveness, fairness, tier transitions, and aggregate overshoot under simultaneous high-speed connections, with independently bounded control traffic at the target fleet scale.

All current cores remain in scope, and the requested aggregate rate feature requires demonstrated actuator support across them. Any core that cannot identify and limit an individual client's combined live traffic needs a qualified mechanism before the requested all-core feature is considered complete. An unsupported capability must not silently become a per-inbound limit or a successful no-op, and reducing the agreed product scope requires an explicit decision.

### Known rate-enforcement work required by the current code

The current Xray adapter deliberately exposes no ShapingHost because the pinned integration has no per-user limiter or reliable kernel selector for multiplexed users (internal/cores/internal/xray/core.go). The current sing-box TUIC/AnyTLS adapter likewise lacks a declared per-client rate actuator. The global-rate requirement therefore includes actuator engineering, not just a controller allocation change.

| Current core family | Existing actuator scope | Required aggregation qualification |
| --- | --- | --- |
| Xray | No declared per-client rate actuator in the current integration | Implement and qualify identity-aware limiting in the actual traffic path |
| sing-box TUIC/AnyTLS | No declared per-client rate actuator in the current adapter | Implement and qualify identity-aware rate and strict quota enforcement |
| SSH | Client token buckets within one server/inbound | Bound the client's aggregate across several SSH inbounds and other cores |
| MTProto | Client limiter within one sidecar/inbound | Subdivide across sidecars, account for minimum rates, and preserve burst state |
| WireGuard/AmneziaWG | Client inner-IP selectors on each interface | Bound multiple interfaces; reject or solve selectors the current implementation cannot safely attribute |
| OpenVPN | Client tunnel-address selectors on an interface | Bound multiple inbounds and prevent forwarding before shaping is active |
| OpenConnect/ocserv | Separate shaping devices for individual sessions | Aggregate or safely partition across sessions as well as inbounds |

Record actuator scope, minimum/granularity, burst semantics, supported address families, admission ordering, update behavior, and actual-state read-back in the capability contract. A boolean supports-speed flag cannot prove this aggregate policy. The current policy path applies evaluated limits per device or inbound (internal/web/service/policy.go), so retaining that loop unchanged would multiply the configured global limit.

Before committing to a mechanism, build feasibility experiments that demonstrate client-identity-aware enforcement for these cores. Prefer a supported native control API if one can satisfy the contract. The operator has approved minimal maintained core patches when feasibility proves they are necessary: pin the exact source, retain a small reviewable patch set, track upstream changes, and run protocol/security/performance regressions. Evaluate throughput, licensing, and maintenance cost explicitly. Port/IP-based shaping over multiplexed shared sockets is not a substitute for per-client identity. A substantially different architecture such as a new proxy layer still requires its own technical decision; the accepted patch policy is not a mandate for an unrestricted core rewrite.

Existing primitives also need stricter semantics. internal/core/caps.go defines a zero RateLimit as unlimited, and missing entries can mean unlimited. The v2 contract therefore needs explicit unlimited, limited, and blocked states: an exhausted or unassigned rate share must never be encoded as zero or as an omitted entry that permits unlimited traffic. Read-back must confirm what the actuator enforces.

MTProto currently raises positive limits below 64,000 bits/second to its daemon minimum (internal/mtproto/manager.go: sidecarRate). The allocator must account for actual granularity, minimum rates, and rounding, or the actuator must be changed. Splitting a small global limit across many targets must not create a larger effective limit through independent upward rounding. Repeated updates/restarts must not replenish unaccounted limiter bursts.

Per-inbound and per-device limiters must be combined or partitioned correctly across a client's simultaneous inbounds, devices, and sessions. Qualify both upload and download, current and newly opened sessions, hot updates, restarts, and private/core bridge paths. Record measured enforcement support in the capability matrix rather than relying only on interface declarations.

Install the assigned policy before a new connection/interface may forward traffic. A periodic discovery job must not leave an unlimited admission window. Rule loss, interface recreation, unknown selectors, or failed actuation need a conservative, observable response. Strict quota exhaustion must affect existing traffic as well as new authentication; merely removing credentials is insufficient if live streams continue. Qualify client-scoped interruption without silently restarting unrelated workloads. A one-process-per-client workaround requires explicit capacity and operational evidence before consideration.

### Resets, renewals, activation, and revocation

A reset creates a new term or explicit allowance epoch; it does not zero lifetime measurement history. The node records the exact transition boundary. Late old-epoch usage remains attributable to the old epoch.

An offline node continues its already assigned epoch and receives the new one on reconnect. The UI shows the reset as pending for that node. A master cannot retroactively make an unreachable node apply a reset at an exact wall-clock instant.

The master is the sole authority for first-use activation and renewal. While it is unreachable, a node may serve only a term already active and authorized locally, until its expiry or any stricter quota/rate/tier/revocation bound. A renewed term is not assumed to exist because the previous term expired. Preauthorized future offline terms and autonomous node renewals are outside the first-release contract.

First-use activation is one transactional transition per client lifecycle generation. Define first use as the first successful master authorization of an authenticated activation attempt, not an inferred globally earliest payload byte. A connection requiring activation waits for the master's committed decision and durable local authorization before forwarding billable service traffic. The master records one start, expiry, billing epoch, and eligibility decision with its initial credit reservations; simultaneous requests from several nodes converge on that same result. A lost reply retrieves the already committed term rather than restarting its duration. Every supported protocol needs a qualified admission boundary, including protocols without a conventional login exchange.

During an outage, an unactivated client's connection receives a retryable unavailable/pending result and is not served on speculative credit. A durable observation may explain the attempted activation, but a stale queued login must not start a paid duration days later merely because the node reconnects. Require fresh demand or an explicit authorized action after connectivity returns. Renewal requests and due-term observations are likewise deduplicated by client and term generation and revalidated against current plan, owner, budget, suspension, and policy before committing a new term. The master UI shows pending activation/renewal separately from an active term.

Automatic renewal creates at most one successor per prior term/schedule slot. Commit the lifecycle change, eligibility/budget effects, and credit reservations together. An expired or exhausted current term stops until the node receives its valid successor. Expiring a request deadline does not erase a renewal or activation that already committed; resolve its existing outcome instead of issuing a second transition.

Do not replay missed renewal periods to accumulate allowances or duplicate charges. For duration-based products, the proposed catch-up rule starts one eligible successor at the successful master renewal authorization time. Calendar products use the current defined calendar window, retain its end boundary, and record actual authorization separately; skipped windows receive no automatic accumulated credit. First-release calendar semantics must define timezone and boundary behavior explicitly. Never record service as provided during an interval when no node was authorized to provide it.

On receiving a successor, the node journals the previous epoch's final readable measurements and the transition boundary before activating the new term. Late old usage stays in the old epoch. Unsettled old client/group/reseller reservations remain reserved until reconciled; creating a new client term cannot make uncertain parent-budget consumption disappear. An explicit byte-allowance reset is distinct from a duration renewal and does not restart expiry or first-use activation unless the master operation explicitly requests that lifecycle change.

Revocation reaches connected nodes promptly and remains durably pending for disconnected nodes. Immediate revocation and indefinite disconnected service cannot both be guaranteed. Nodes enforce already received expiries, revocations, and credit exhaustion. Optional short authorization leases can narrow revocation delay for particular products, with an explicit availability tradeoff.

Global simultaneous device/IP limits have the same coordination issue. Local enforcement remains available; strict global admission needs assigned admission rights or a live authority. Do not silently label replicated IP observations as a strict global device count.

Clock health is part of enforcement. Use monotonic elapsed time within a running process and retain a trusted-time high-water mark across restarts. A backward wall-clock step must not extend a term or resurrect an expired authorization. Excessive skew or an unverifiable reboot clock produces an explicit degraded state and a conservative policy for affected expiring grants, rather than an automatic allowance extension.

### Exhaustion and storage failure

At disk-pressure thresholds, drop/coarsen diagnostic metrics first, compact accounted journals second, and alert. Never discard unacknowledged billing amounts.

If required durable enforcement/accounting state cannot be recorded, prevent new credit activation and place affected metered service into its documented safe mode. Unlimited or independently enforceable workloads need not be stopped merely because an unrelated telemetry queue is full.

On reboot without the master, only intact persisted configuration and spend authority may be resumed. Corrupt or unexpectedly rolled-back state must not silently reconstruct the full original allowance.

## Node security and installation

### Identity and enrollment

The master generates a short-lived, single-use enrollment ticket scoped to one fleet/node slot and permitted enrollment policy. The node generates its own private key and requests a certificate. Consuming the ticket and binding the node identity is transactional; retrying a lost response is allowed only for the same enrollment request/key.

Trust the master through normal public TLS or an explicitly authenticated CA/public-key fingerprint supplied during installation. No insecure verification mode is part of v2. Tickets must not grant panel administration access and must be excluded from normal logs and process arguments.

After enrollment, use short-lived per-node certificates with automated rotation, overlap, and explicit revocation. Bind application authorization to the node record on every session. Bound session age and actively disconnect revoked sessions; certificate expiry alone does not terminate an already established connection.

If a node's certificate expires during a long outage, it keeps its authorized local service policy but cannot bypass TLS to reconnect. An authenticated re-enrollment/recovery procedure restores control access. Private keys never need to be copied to the master.

Use standard Go X.509/TLS libraries and a reviewed CA lifecycle. SPIFFE-compatible identities are useful, but an external SPIRE cluster is not a prerequisite for the simple installation. SPIRE's enrollment/rotation model is a reference, not an additional mandatory daemon. [SPIRE configuration](https://spiffe.io/docs/latest/deploying/configuring/)

Separate node identity certificates from client-facing TLS certificates. Manage client-facing certificate issuance/renewal as its own resource, including what happens if the master is unavailable near certificate expiry.

### Privilege boundary

Run the network-facing agent without root. A local helper exposes narrowly typed operations over a permission-restricted Unix socket with peer-identity checks.

The helper manages only Penhoon-owned service units, artifacts, routes, firewall chains, interfaces, and directories. It must validate ownership, normalized paths, symlink handling, resource bounds, allowed binaries, and permitted configuration hooks. No general shell execution endpoint, unrestricted file write, arbitrary systemd unit, or arbitrary downloaded executable is allowed.

Core daemons run with the minimum capabilities they actually require. Reuse the existing supervisor backend where its ownership contract fits, and extend its hardening tests. Systemd offers transient-unit controls through its D-Bus interface, but support must be checked on each supported distribution baseline. [Systemd transient settings](https://systemd.io/TRANSIENT-SETTINGS/)

Treat an agent or node compromise as authority over that node's assigned clients only. A compromised master can change fleet policy; separate software-release signing keys from the online master so it cannot use an update action to authorize arbitrary binaries.

Validate reported resource IDs against the node's assignment history, enforce numeric and decoded-size limits, and reject conflicting producer sequences. A node's report cannot modify another node's records or the desired policy. Authentication establishes who reported usage; it does not prove that a malicious host measured it honestly. Core management endpoints remain local and access-restricted rather than becoming additional public administration surfaces.

Ordinary software-only identity cannot defeat a malicious root user cloning an offline machine and its state. State the trust assumption; stronger anti-cloning guarantees require hardware-backed identity or online coordination.

### Operator workflow

1. In the master, choose Add node, region/labels, allowed core set, and update policy.
2. Receive a versioned installation method and one-time enrollment material.
3. Install one node package through an authenticated package/bootstrap channel; provide enrollment through protected input or a root-readable enrollment file.
4. Preflight OS, architecture, clock, disk, required kernel features, and outbound connectivity.
5. Install only selected core dependencies and managed services.
6. Enroll, publish capabilities, download assigned state, and report validation/application progress in the master.
7. All subsequent routine work takes place in the master.

The initial bootstrap is part of the trust chain. A script fetched from a compromised endpoint cannot establish trust merely by verifying a key delivered by the same compromised script. Document the authenticated package/signing-key bootstrap explicitly.

The node CLI provides status, doctor, inspect-applied-state, redacted support export, recovery enrollment, and uninstall. It is a local operational tool, not a second policy editor.

Uninstall and retirement are different operations. Graceful retirement drains supported workloads, stops remaining workloads with explicit impact, settles usage, and then revokes identity. Force-retiring an unreachable node leaves unresolved consumption and pending revocations visible.

## Updates and rollback

Pin agent, helper, and core versions per rollout policy. Stage artifacts, verify signatures and digests, check protocol/config compatibility, activate a canary, observe health and accounting continuity, then expand the wave.

Use a reviewed TUF-compatible update client and trusted root metadata to handle signed metadata, key rotation, expiry, and rollback protection. A checksum alone is insufficient. An intentional operational rollback must remain authorized by current trusted metadata rather than disabling anti-rollback checks. [TUF security model](https://theupdateframework.io/docs/security/)

Retain the previous compatible binary/configuration and a durable activation journal. Rollback must preserve spent credits, billing epochs, revocations, and operation deduplication. Local storage schema migrations need an expand/contract strategy or an explicitly declared rollback boundary.

The agent can update without restarting independent core units. A core upgrade may terminate sessions; the master reports the actual impact, supports maintenance windows, and never advertises zero downtime for a daemon without that capability.

Provide manual, canary, staged, and pinned update modes. Do not automatically upgrade the entire fleet merely because an upstream release exists.

## Master persistence and high availability

Start with one master deployment and PostgreSQL, using modules in one process. Do not require Kubernetes, Redis, Kafka, NATS, etcd, or a service mesh for basic operation.

New conceptual persistence includes node identities and enrollments, desired revisions/manifests, per-node delivery state, operations and attempts, usage producer cursors/batches, billing epochs and grants, and rollout records. Exact tables follow the protocol model and migration review; do not replicate the panel schema onto nodes.

Persist intent and outbox records in the same transaction. Background workers claim bounded work from PostgreSQL, with retry deadlines and per-node fairness. LISTEN/NOTIFY may reduce wakeup latency, but durable rows and periodic reconciliation determine correctness. Notifications are not a replayable delivery ledger. [PostgreSQL NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)

Later HA deployments use several master processes behind a compatible load balancer and an HA PostgreSQL service. Separate modules can become independently scalable workers without changing the node protocol.

Exactly one active owner drives a node's control session at a time. Ownership changes advance a database-backed fencing generation; the node durably rejects older authority generations after observing a newer one. Each new mutation still requires current database authorization. Old workers stop sending when they lose ownership or database access.

Fencing does not cancel an action already applied or in flight. Durable operation identity, committed resource revisions, and non-overlapping quota grants remain necessary. A distributed lease alone is not sufficient protection for an external actuator. [etcd discussion of fencing](https://etcd.io/docs/v3.5/learning/why/)

HA is not complete until API/session state, schedulers, background jobs, CA signing, notifications, subscription reads, migrations, and backups have been audited for singleton or local-memory assumptions. HA PostgreSQL also needs one fenced write authority; application replicas do not repair database split brain.

### Restore and disaster recovery

Back up desired state, identities, trust material under separate access control, operation records, usage cursors, and budget reservations. Exercise restoration with active and disconnected nodes.

Restoring an older master database must enter a recovery mode before issuing quota or configuration. Reconcile node high-water marks and reservations, establish a new authorized control-plane generation, and quarantine conflicts. An older backup cannot safely assume that previously granted money or bandwidth was never spent.

A rebuilt node receives a new installation incarnation. Importing old state is a controlled recovery action, not automatic reuse of a spendable journal. Lost node disks can leave unreported usage; settle conservatively without fabricating customer traffic.

Publish separate RPO/RTO for the single-master and HA profiles after restore drills. No zero-data-loss promise follows merely from using PostgreSQL.

## Capacity and performance qualification

Initial qualification proposal:

| Dimension | Proposed benchmark envelope |
| --- | --- |
| Enrolled and concurrently connected agents | 1,000 |
| Registered client identities | 1,000,000 |
| Client-to-node assignments | 5,000,000 initially; explicitly test skew |
| Baseline concurrently active assignments | 100,000 |
| Stress active assignments | 1,000,000 |
| Hot-node case | 100,000 assigned clients on one agent, subject to actual core limits |
| Normal usage reporting interval | 15 seconds with adaptive batching; enforcement samples are independent |
| Reconnect storm | All 1,000 agents after a master outage |
| Outage qualification | Seven days, with enough reserved credit and durable storage; expiries still apply |

These are test inputs, not measured capacity. If every one of a million clients is assigned to all 1,000 nodes, the system has a billion assignments and needs a different capacity assessment.

Illustrative accounting load: 100,000 changed producer/client records every 15 seconds is about 6,667 records/second. At an assumed 128 encoded bytes each, that is about 0.85 MB/second before TLS, indexes, and storage overhead. At one million changed records it is about 66,667 records/second and 8.53 MB/second. Retaining every such raw record indefinitely would create roughly 74 GB/day or 737 GB/day respectively before database overhead; retention and aggregation are essential.

Benchmark multiple active producers per client and skewed groups/resellers, not only evenly distributed users. Native daemon capacity and aggregate VPN bandwidth are separate from control-plane capacity.

Use delta delivery, bounded snapshots, batch ingestion, indexed assignment lookup, bounded read models, and server-side UI pagination. Do not send million-row arrays to browsers or re-render the whole fleet on every heartbeat. Do not fan out every client's state to every node.

PostgreSQL is the initial source of truth and durable work ledger. Qualify batching, partitioning, indexes, checkpoint lag, vacuum, disk growth, and backup/restore before adding another datastore. Introduce an external broker or analytics store only if measured requirements justify its operational cost; keep accounting authority explicit if that happens.

### Proposed service objectives

These are release acceptance targets to validate on documented hardware and network conditions, not current product guarantees.

- For a bounded hot-applicable change of up to 100 clients, at the baseline workload and at most 100 ms node RTT: committed-to-applied p95 under 3 seconds and p99 under 10 seconds.
- Mark stale control connectivity within 30 to 45 seconds without equating it to proven data-plane failure.
- Agent-only restart preserves independent daemon sessions and acknowledged accounting.
- Every accepted operation has a durable pending, completed, rejected, superseded, or uncertain outcome.
- No duplicate charge for any replay of durably recorded usage within the defined producer/epoch contract.
- Offline service continues through the outage test while assigned credit, expiry, local health, and durable storage permit it.
- Reconnect recovery has bounded memory, bounded per-node queues, and fair progress; final recovery-time targets follow the measured backlog envelope.
- Per-core quota overshoot, aggregate rate/burst bounds, and crash measurement loss are measured, published, and tested rather than hidden behind a generic fleet guarantee.

## Master UX and observability

The node page should answer: Is it reachable? Is its runtime serving? Is its configuration current? Is accounting current? What is blocking progress?

Expose independent conditions for control connectivity, configuration convergence, core readiness, accounting freshness, disk pressure, certificate status, and update state. Show desired/applied versions, last contact, last successful reconciliation, pending operations, and a concrete diagnostic cause.

For one client, show one shared allowance, acknowledged upload/download, per-node contributions, aggregate rate policy, and actual per-node enforcement state. Show per-inbound contributions only where the meter supports them. Separate measured consumption, unreported/uncertain reservations, allocatable balance, and recent local spendable-credit observations. Add freshness to usage and speed displays; a disconnected node means the fleet total may be incomplete, not that its traffic became zero. Operators can inspect allocation details without forcing customers to manage node shares.

For configuration, show inherited/default sources, explicit overrides, resolved node settings, applied revisions, drift, and pending changes across all networking domains. A green agent connection must not conceal a failed outbound, stale routing graph, unapplied rate limit, or missing usage acknowledgment.

Add operation timelines with request/actor identity and per-node progress. Record authorization, change hashes, artifact versions, and results without logging secrets. Audit history includes enrollment, rotation, grants, resets, maintenance, recovery, and retirement.

Instrument OpenTelemetry traces and bounded-cardinality metrics; provide Prometheus-compatible export. Nodes can send telemetry through their outbound channel rather than opening a monitoring port. Client IDs belong in scoped diagnostic/accounting storage, not a million-series metric label. [OpenTelemetry concepts](https://opentelemetry.io/docs/concepts/)

Logs and support bundles are explicitly requested, size/time bounded, redacted, and access-controlled. Fleet-wide log streaming is not enabled by default.

## Fresh implementation and removal of old node support

The project has not been released. The operator explicitly chose a fresh replacement with complete removal of the old node mode. There is no production migration requirement, no legacy import, no shadow/adoption mode, no transport adapter for old nodes, and no mixed old/new fleet. The first new release enrolls only the new headless agents.

Remove the obsolete node subsystem as part of the replacement, including:

- Panel-to-panel runtime transport and calls to another panel's administrative API.
- Old node address/base-path/API-token/TLS-mode settings and their frontend forms; the new endpoint and enrollment model replace them.
- Old heartbeat/traffic synchronization jobs, dirty flags, snapshot-driven lifecycle adoption, tag/alias mapping, and descendant-panel hierarchy management.
- Old node-operation routes, controller/service paths, frontend requests, and obsolete API examples.
- Legacy-only schemas, tables/columns where superseded, fixtures, and compatibility tests.
- Installer/update options, CLI paths, documentation, and UI entries that still select or describe the old node mode.

Review resource ownership before removal: shared core/routing/egress engines, useful counter invariants, master business features, local data-plane bridges, and unrelated working changes remain in scope to preserve. Rewrite relevant regression scenarios against the new contract rather than preserving an obsolete API solely to keep its tests passing.

Use fresh installation environments and disposable databases for implementation qualification. Old development data is not automatically imported or adopted. Removing support is not an instruction to erase arbitrary existing directories or databases. Master schema changes still follow repository migration conventions, including explicit changes in internal/database/db.go; schema evolution is separate from supporting old node installations.

Update the controller route registry and regenerate schemas/OpenAPI whenever endpoints are added or removed. Synchronize the docs OpenAPI copy and generated API reference, remove dead locale keys appropriately, and retain English/Persian parity. Architecture guards should fail if the new agent imports the web/database service layer or the master falls back to old node HTTP synchronization.

The first-release gate proves that all installation roles use the new node contract and that the old node mode cannot be selected or reached. Later signed upgrades, rollbacks between compatible new-system releases, endpoint changes, workload reassignment, HA ownership transfer, and backup restoration retain the safety rules elsewhere in this plan. They do not reintroduce old-node compatibility.

## Implementation phases and gates

| Phase | Deliverables | Exit gate |
| --- | --- | --- |
| 0. Specify and measure | Removal inventory; threat model; full node graph/inheritance and placement model; accounting, rate, master-only lifecycle, storage, and new-protocol contracts; per-core enforcement feasibility | Quota/rate/activation/reset/recovery transitions have model-test scenarios; enforcement approach is demonstrated and any minimal core patches satisfy the approved maintenance policy |
| 1. Protocol and persistence foundation | Protobuf contracts; compatibility CI; master intent/outbox model; agent journal; enrollment design implemented in isolation | Crash/replay and invalid-identity tests pass without invoking a real core |
| 2. Headless vertical slice | All three installation roles; agent/helper; Xray deployment; complete inherited and overridden networking graph; configured control endpoint; diagnostics | Master-only, node-only, and combined installs work; node-local outbound/DNS/routing differs as configured; nondefault ports, offline reboot, and failed customer-route recovery pass |
| 3. Accounting and policy | Durable meter identity; replay; shared quota and rate allocation; tier rights; billing epochs; placement; group/reseller scopes | Two-node/multi-inbound traffic is additive exactly once; quota, bandwidth, and tier authority cannot multiply under retries, partitions, restarts, or reassignment |
| 4. All-core execution | Qualified Xray rate mechanism; MTProto, WireGuard, AmneziaWG, SSH, sing-box TUIC/AnyTLS, OpenVPN, OpenConnect, and local RADIUS integration | Real-daemon tests demonstrate independent node networking, aggregate client limits, actual actuator read-back, and supported OS/architecture behavior for every core |
| 5. Fleet operations | Asynchronous master UX, diagnostics, signed updates, canaries, rollback, drain/retire, secret rotation | Interrupted update and bad-config drills preserve isolation and produce accurate operation state |
| 6. Fresh deployment and old-mode removal | Complete removal of old node transport/jobs/routes/settings/UI; new-schema setup; all-role installers; end-to-end master integration | Fresh master-only, node-only, and combined deployments pass; no old node mode, import, adoption, or transport fallback remains reachable |
| 7. Production qualification | Load/soak/failure tests, security review, installer matrix, backup/restore drills, operator runbooks | Published measured capacity, SLO results, unresolved limits, and rollback evidence |
| 8. HA qualification | Qualify multiple new-system master processes, shared services, and HA PostgreSQL | Owner failover and disaster recovery pass without duplicate operations, lifecycle transitions, or grants |

All current cores are required for the complete first v2 release. The Xray vertical slice is an engineering milestone, not permission to silently drop the other cores.

Critical path: ownership and shared accounting/rate semantics, missing core-rate/admission actuator feasibility, full node-graph extraction, durable offline enforcement, all-core parity, then fresh-deployment and production qualification. Protocol selection alone is a small part of the work.

Do not attach a confident delivery date before the vertical slice and accounting experiments measure the hardest unknowns. Estimate by these gates and maintain a visible risk register.

## Required validation matrix

- Run one client simultaneously on two nodes with separate inbounds and separate outbound/DNS/routing configurations; verify combined traffic and rate against independent measurements.
- Repeat one producer's report, mirror one meter into several inbound views, and report a second independent producer; only independent consumption increases the bill.
- Run multiple inbounds/cores and multiple sessions on one node; confirm neither quota grants nor rate shares multiply by attachment/session count.
- Verify the exact 20 GiB worked example, lost acknowledgments, urgent high-bandwidth refill, safe credit/rate transfer, near-exhaustion, and strict offline stopping of the affected client only.
- Test shared speed-tier boundaries, including A attempting later-phase traffic while B still holds fast-phase rights; verify global phase ordering and visible conservative pauses. Test zero-versus-unlimited semantics, minimum-rate rounding, burst behavior, unknown actual enforcement, and aggregate upload/download across all cores.
- Inherit fleet defaults, override selected node settings, change defaults again, and reset overrides; verify provenance, dependency validation, and independent runtime connections.
- Add a node matching plan/group placement rules and confirm deduplicated assignments, reseller restrictions, stable identities, and no automatic fleet-wide client replication.
- Use a nondefault control port, separate bind/advertised ports, IPv4/IPv6 paths, and endpoint migration with one offline node.
- Break a client-side outbound/route/DNS configuration while confirming that the management path remains recoverable and no unrelated host network state is flushed.
- Stop master-host traffic cores and verify remote nodes continue using their own egress; restart the master service and verify the optional local agent follows the same accounting/recovery contract.
- Kill the agent before and after each journal commit, core action, and acknowledgment.
- Drop, duplicate, delay, reorder, and replay messages across reconnects and software versions.
- Restart the master after committing intent but before delivery; restart a worker after node application but before recording its acknowledgment.
- Partition a node for hours/days; reboot it while offline; exhaust credit; expire a term; restore connectivity with a large backlog. Separately stop or hang the local collector/agent while cores continue forwarding and prove the strict cutoff remains effective.
- Race two nodes to activate one dormant client; lose committed activation/renewal replies; confirm one term and one set of valid reservations.
- Attempt first use offline and replay the stale observation after reconnect, revocation, deletion, or a plan/credential change; confirm it cannot activate without current authority and fresh demand.
- Disconnect past expiry with unused credit and through several calendar periods; confirm no autonomous successor, accumulated allowance, or duplicated renewal charge.
- Exercise explicit byte reset separately from duration renewal, plus deletion/recreation, rename, and owner/group transfer around late old-epoch usage and unresolved reservations.
- Fail a core during collection, reset native counters, remove/re-add peers, lose a counter sample, and verify known versus unknown measurement boundaries.
- Fill disks, fail fsync, corrupt staged state, and restore older local/master backups. Confirm no silent reset of spend authority.
- Rotate CA and node certificates with partial fleet connectivity; reject wrong-node identities, replayed enrollments, expired credentials, and revoked sessions.
- Inject invalid configurations, route/firewall conflicts, missing kernel features, bad certificate pairs, unsupported capabilities, and malicious artifact metadata.
- Kill processes during each new-system install, upgrade, reassignment, and restore phase; test downgrade authorization and incompatible local-state schemas.
- Test old/new master ownership overlap, stale sessions, delayed database responses, worker pauses, and HA database failover.
- Saturate one node and one large group while proving the rest of the fleet makes progress.
- Run 1,000-agent reconnect storms and the stated one-million-client/assignment load matrix.
- Run real-daemon client traffic through each supported core; mocks do not establish session continuity, quota enforcement, or process adoption.
- Verify master UI/API owner and reseller scopes, server-side pagination, operation histories, redaction, English/Persian messaging, and generated API documentation.
- Run existing architecture guards, regression suites, generation checks, lint, race tests, fuzzing, and new protocol compatibility/model tests.
- Run supported Debian/Ubuntu installation, repeat-install, upgrade, recovery, and uninstall drills without modifying unrelated host networking.
- Prove old-node routes, UI/settings, startup roles, jobs, and transport fallback are absent; the new enrollment endpoint must not adopt or accept old panel-node credentials.

Use state-machine/property tests for accounting and ownership. For the highest-risk grant/reset/recovery algorithms, a small executable model or bounded formal model is preferable to hundreds of examples that assume favorable event ordering. Preserve the repository rule that a regression test must fail when its corresponding protection is removed.

## Selected design and implementation entry conditions

Product decisions are settled for this plan. The following design direction is the implementation baseline; feasibility experiments and qualification gates must establish its technical claims before release. No further approval of minimal necessary maintained core patches is required once their stated feasibility conditions are met. A substantially different architecture or a reduction of the agreed all-core behavior remains a new scope decision.

- One control-plane owner per node; master-managed UUID identity; no ongoing bidirectional panel adoption.
- Same repository, separately packaged headless node, no database server on nodes.
- Node-initiated gRPC/mTLS with durable application-level replay and separate usage traffic.
- PostgreSQL on the master and a qualified embedded transactional node journal.
- Complete per-node networking graphs with inherited defaults, explicit overrides, scoped dependencies, and rule-based client placement.
- Independent node data paths, a configurable management endpoint/port, and first-class combined master/local-node installation.
- Bounded offline quota allocations, capability-specific enforcement, and visible pending reset/revocation status.
- Global aggregate speed and tier rights, with actual per-core rate mechanisms qualified before claiming the requested all-core behavior.
- Master-only first-use activation and automatic renewal; offline nodes continue current authorized terms only, with no future-term schedule activation.
- Privilege-separated host operations and independent core process lifetimes.
- Signed staged upgrades and explicit rollback boundaries.
- Fresh node enrollment and complete removal of old node support, with no migration, import, adoption, or mixed legacy fleet.
- Feasibility-first minimal core patches, pinned source and signed builds, upstream tracking, and compatibility/security/performance regressions.
- HA-ready identities and fencing from the start, while operational HA arrives only after its own qualification.

The most important acceptance criterion is explainability under failure: an operator must be able to see what was requested, what was durably accepted, what actually happened on each node, what can still happen while disconnected, and what requires recovery.
