# Owned outbound provider refresh

This contract is approved for implementation. The provider adapter and rollout consumer are not yet installed in the role factory. The legacy outbound subscription job is not a substitute: it reads master-local egress/core state, mutates cache rows and requests a local restart.

## Source and immutable cache

A provider has a UUID, a revisioned private fetch configuration, a successful cache head, and durable claim/retry metadata. Each successful fetch creates an immutable cache revision containing its configuration revision/digest, canonical outbounds, stable identity-to-tag mapping, content digest and operation receipt. Fetch errors update only a bounded diagnostic code and retry eligibility; they never replace a successful cache or node graph.

Shared credentials require an explicit shared-provider declaration. Otherwise a provider is bound to one logical node and installation. Scope is immutable for that provider identity. Missing or foreign bindings are compiler errors. Provider/tunnel enrollment identities and private peer keys are not cloned across nodes. Unsupported protocols remain an explicit factory qualification gap.

The management client uses a dedicated direct HTTP transport. It does not use the panel egress proxy, environment proxy, local Xray listener, local core executable or restart flag. Requests have lifecycle cancellation, bounded time and response bytes, guarded DNS/IP dialing and redirect validation. Private destinations and insecure TLS require explicit source permission. URLs, query tokens, credentials and response bodies are excluded from diagnostic strings and test output.

The parser accepts declared supported feed formats and fails the entire refresh on a malformed non-comment entry or unqualified protocol. It cannot silently drop unknown lines. An explicit empty feed is a successful immutable empty revision, distinct from a fetch/parse failure. An empty result does not automatically expose a direct/manual fallback: affected rollout remains visibly blocked until an approved network edit resolves that dependency.

Tags are preserved by parsed identity within the provider UUID namespace. Positional fallback cannot assign an old tag to a different remote peer. Renamed remarks and reordered entries do not change an existing identity. Collisions with manual outbounds fail compilation; manual routes are never pruned automatically.

## Explicit network references

Runtime bases contain ordered provider bindings with either an exact cache revision or an explicit `FollowLatest` permission. Existing whole-base replace/reset/remove override semantics apply to the provider list too. An override does not accidentally inherit omitted provider bindings. Pinned providers remain pinned; pinned defaults freeze inherited provider inputs. A node-local override can separately grant explicit follow permission.

Provider-bearing defaults/override revisions use network specification version 2. Existing immutable version-1 records remain untouched and valid only without provider bindings. Provider-aware compilations use a new compiler marker. Older readers must reject the new specification/compiler version instead of ignoring provider metadata and producing a direct-only graph. Node protocol generation 2 and durable state format 3 do not change.

Preview resolves exact immutable cache revisions and records their IDs, revisions and digests in the compilation manifest. Compilation validates the supported outbound configuration using the pure node compiler, without querying or starting a master-local core. Publication uses the existing whole-node path, with no fresh quota allocation supplied by provider work.

## Durable refresh and rollout

The lifecycle-owned refresh job scans a bounded due set and claims work durably. Network I/O occurs after the claim transaction commits. The commit transaction rechecks the configuration digest, source revision, cache head and lease. A delayed response or an older claim cannot overwrite a newer revision. A failed transaction leaves both the cache head and rollout intent unchanged.

There is one durable provider rollout intent per logical node, with a bounded map of exact desired provider revisions. It captures the current installation, last APPLIED compilation, network/default revisions, applied base revision/hash and normalized source-scope digest. It also records a CAS revision, claim lease, retry eligibility, diagnostic and publication receipt. Repeated provider refreshes coalesce targets without lowering any revision.

The cache commit durably records a provider fanout revision and cursor. Fanout pages read a normalized projection of exact published provider bindings joined to each node's actual APPLIED deployment, then merge the per-node intent. There is no fixed follower-count ceiling: one pass advances at most 32 logical nodes and retains the cursor for the next pass. New publications also carry a pending wake bit, so a publication which becomes APPLIED after the cursor passed is reconciled without another fetch.

The refresh writer changes provider rows and fanout intent only. It neither takes target node/source locks nor writes generic network-head `Pending` as permission to publish. Projection and rollout rows have no target-node/source foreign key that could acquire node key-share locks under a provider lock. First creation of a rollout row is serialized by its own advisory key before reading/merging targets; different providers cannot overwrite each other's first target. Never-published nodes cannot be bootstrapped by refresh. Disabled nodes retain queued refresh intent while publication remains blocked.

The rollout lease transaction commits before compilation. The consumer then locks the authority fence, node, network state, all normalized source clients in their existing order, provider heads in sorted UUID order, and finally the rollout CAS. It revalidates the complete immutable baseline and approved follow bindings. Compilation/publication and the exact work receipt commit atomically; provider network I/O is never performed in that transaction.

Unrelated manual pending changes, source changes, installation changes or unresolved dependencies defer work while preserving the applied graph. A later legitimately APPLIED base may replace the captured baseline only after proving that desired equals applied, no manual draft is pending, source/default pins match that approved compilation and follow permission is still present. The rebase gets a new CAS revision/receipt. A merely desired draft cannot authorize a rebase.

Published and APPLIED are distinct states. A publication receipt does not settle node application. New refreshes can supersede work while an older deployment awaits application; the consumer waits for the real applied base before publishing the next exact target. An old lease or publication callback cannot clear newer pending work.

## Required verification

Use local HTTP/TLS fixtures and real disposable PostgreSQL schemas only. Cover private/redirect policy, DNS-safe dialing, TLS permission, cancellation, body limits, strict parsing versus explicit empty, identity/tag stability and secret-free diagnostics. Capture stale-response/config-edit/concurrent-claim races with deterministic barriers.

Database tests must prove immutable revisions, transactional cache/work insertion, exact retry receipts, inherited versus overridden/pinned targets, superseded leases, manual draft refusal, later-approved-base rebase, full source-scope revalidation, old graph retention on compile failure and distinct publication/application settlement. Include removed-guard controls. Tests must detect any master-local egress/core/restart call.

The factory remains gated until the configured protocol/format coverage and real consumer path are qualified. Source/model scaffolding, a cache-only callback, or a callback that marks generic `Pending` without consumption is not completion.

The current candidate parses line lists and base64 lists strictly and compiles VMess, VLESS, Trojan and Shadowsocks provider entries through the Xray node compiler. Hysteria2/WireGuard feed entries and other formats remain explicit qualification gaps. A feed is bounded to 8 MiB and 4,096 entries; the private identity ledger retains at most 32,768 historical connection identities and reports capacity exhaustion instead of recycling tags. The node compiler's own configuration bounds still apply. A successful empty cache is retained as an immutable revision, but automatic publication holds with `PROVIDER_EMPTY`. Scope, credentials and failed-fetch diagnostics remain separate from public result metadata.

`RunFleetProviderSchedulers` owns independent refresh, fanout/wake, and rollout loops under the supplied lifecycle context and database handle. Expected missing publication/cache scope remains pending with bounded retry eligibility; cancellation joins the loops and interrupts in-flight HTTP. The constructor and loops are not connected to the production role factory yet. Whole-node publication retains the runtime's existing structural-apply behavior; this candidate does not qualify uninterrupted sessions across outbound graph changes.

## Candidate evidence

The complete provider/network selection passed against disposable local PostgreSQL with the race detector: service 62.899 seconds and node configuration 2.010 seconds. Later focused race regressions qualified authority-fence ordering (9.714 seconds) and exact applied-payload integrity plus a funded-demand refresh (6.815 seconds). The funded refresh retains the existing grant, term expiry, credit revision and 300-byte reservation. Tests also cover 34 actual APPLIED publications across two fanout pages, concurrent first-row target merging, late APPLIED wake, frozen Stop carry-forward, cancellation and stale leases.

All 18 removed-safeguard provider controls reproduced their intended semantic assertions. Shared lint over services, control plane, node configuration and database packages completed with zero issues after formatting. The logs and individual control outputs are retained under `scratchpad/node-v2`; the local checkpoint lists the exact evidence directories. These results qualify the saved candidate paths, not production factory wiring, unsupported feed protocols/formats, uninterrupted structural updates, 1,000-node scale or release artifacts.

Refresh operation receipts are recorded only after a successful commit. A failed fetch retains the old graph and has no completed receipt; its operation ID remains reusable until success. A completed retry returns the recorded result instead of performing another fetch.
