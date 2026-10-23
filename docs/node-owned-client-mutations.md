# Owned client mutations

This is the implementation contract for replacing ordinary client writes in master/combined roles. Atomic administrator creation, its runtime capability, and metadata-only receipt endpoints are implemented and pass the current PostgreSQL API tests. Browser integration is in progress. The production factory remains closed until the remaining adapters and callers are complete.

## Creation boundary

The current `ClientService.create` loops through legacy inbound writes, so a later failure can follow earlier committed clients. `CreateWithPlan` separately stores the plan/client before that loop and compensates after failure. Those paths are not suitable for owned creation. The existing browser also submits external links separately and may regenerate credentials on retry; owned requests must replace that behavior.

An owned create dispatches through `runtime.SourceCreateRuntime` with a current administrator, an operation UUID, and the shared `entity.ClientCreatePayload`. Plan overrides and external-link inputs live in `entity` to avoid a runtime/service import cycle. The result contains the immutable logical client UUID, normalized client ID/revision, pending state, and node-derived credential requirements. It contains no credentials.

The public owned route also requires `expectedActorId` to match the authenticated administrator before dispatch or receipt lookup. Capabilities and results identify the actual actor. A stale browser cache or another tab's login therefore cannot silently attach a new operation to a different actor's recovery list. Existing receipt envelopes provide the authoritative actor when an earlier candidate result did not include the response field.

The operation receipt, normalized client, plan assignment/overrides, explicit template memberships, credential bindings, external links, owned fleet identity, and placement reevaluation must commit together. The operation never starts a billing term, reserves quota/rates, edits legacy inbound client JSON, calls a local core, sets a local restart flag, or interprets node silence as application. Placement processing later produces the authorized graph and fresh authenticated demand activates/funds service.

Concurrent duplicate requests reuse one actor-bound receipt. After an operation commits, changed input under the same operation UUID conflicts. Existing identities are not adopted by matching email or subscription ID. Owned creates serialize subscription identity checks with a transaction-scoped advisory lock. Every future owned writer of subscription identity must use the same lock. A disabled new client remains disabled despite GORM's default-true source column. Legacy creation refuses fleet dispatchers and fleet-only request fields before any source or plan write.

Inherited plan/group templates are computed from current policy; creation stores only explicitly submitted memberships. Removing an inherited membership therefore cannot leave a permanent explicit attachment behind. A managed plan's allow-set and reseller restrictions still constrain the effective set. Standalone clients have no unresolved placeholder plan assignment.

## Credentials

`SourceCredentialDeclarer` is a separate, explicitly bound core capability. There is no fallback to the legacy credential minter: OpenVPN's old minter executes a daemon to wrap a client key. Source minting is pure and returns additions only. It refuses incompatible nonempty supplied values instead of silently rotating them.

One fleet client remains the accounting identity across protocols and targets. Explicit common credentials apply to every relevant binding unless an explicit per-binding value overrides them. Automatically generated protocol-specific credentials may differ where requirements are incompatible; they must be persisted per binding and projected consistently into node configuration, subscriptions, exports, and the editor. A mixed SS2022 128/256-bit selection cannot repeatedly overwrite one shared password.

OpenVPN wrapping tied to a node's server key is node-derived work. Missing material is reported as pending. Preserving supplied wrapped bytes does not prove their compatibility with the eventual node/server key. Neither pure source validation nor an empty pending-field list proves forwarding readiness. Native admission and credential-resource qualification remain separate release gates.

Authentication identity claims are unique per template and reserve the canonical key the pinned core actually looks up, rather than the spelling of the submitted secret. Disabled clients retain their claims. Claims also remain reserved across old authorizations; release/rotation needs an explicit safe retirement workflow and is not implemented here. Existing unqualified attached owners prevent new creation on that template. This is a fresh-system path, not a legacy identity migration.

Qualification includes explicit memberships, inherited plan/group bindings and retained physical placement references, including an orphaned normalized source. Three actual PostgreSQL regressions reproduced invisible inherited/retained ownership before this correction. The expanded creation/API race suite passed in 29.228/3.523 seconds. The conservative owner query still requires the ongoing scale audit; these correctness tests are not a million-client throughput claim.

Newly created clients have credential-claim version 1. Materialization verifies the current effective credentials against their retained claims in bounded queries. A later inherited template without prepared claims remains pending rather than receiving authority. Version 0 is retained only for provisional pre-cutover fixtures and existing candidate data; it does not qualify production cutover or claim a safe migration. Unknown versions fail closed.

## Callers and retries

Owned creation uses one request for source, plan, attachments, bindings, and external links. The frontend must not perform a second external-link write or regenerate credentials under a retained operation UUID. It may retain the exact secret-bearing request in memory; browser persistence may retain only a nonsecret operation locator. Reload recovery needs an authenticated receipt/status read, not credentials in session storage.

Process capability must come from the actual owned runtime. The browser's `PUI_ROLE` is the signed-in user's admin/reseller role, and control-listener readiness does not establish business-operation support. Bulk/import need stable per-item operation identities. Reseller and LDAP creation require their own budget/origin authorization, without pretending to be an administrator or adopting an existing manual identity.

Edit inputs must be read together with the matching source revision in a coherent snapshot. A separately fetched newer revision cannot authorize overwriting fields from an older read. Credential, membership, limit, and deletion changes retain durable stop obligations and historical grants until the appropriate applied proof; they cannot delete the normalized identity first and strand its stop worker.

## Required qualification

The expanded real-PostgreSQL race suite passed: service 15.328 seconds, controller 4.075 seconds, and live route registry 3.595 seconds. It covers atomic plan/link/claim/receipt rollback, disabled creation, concurrent duplicate operations, changed retries, serialized subscription uniqueness, canonical credential collisions, caller rollback, scoped cipher credential projection, unprepared inherited templates, early refusal of legacy writes, current administrator checks, actor-scoped receipt recovery, metadata-only responses, and auditing only new committed operations. Eight deliberate safeguard-removal controls reproduce their intended failures; a foreign-binding fixture was corrected after its control exposed an unrelated earlier rejection. These are local Windows-hosted PostgreSQL/portable-Go results, not Linux runtime qualification. The browser gate remains in progress.

The OpenAPI generator now distinguishes actual anonymous untagged embeddings from named fields. A real JSON wire regression failed before the fix for both named `Client` and tagged anonymous fields. All generator tests pass; generated request types keep `client` nested instead of silently flattening it.

The subsequent group-creation rollback and OpenVPN host-generation trap passed under PostgreSQL/race in 4.980 seconds. Requested group identity is created in the same transaction before plan locks; inherited memberships still remain derived. OpenVPN creation persists login credentials and reports its wrapped client key as node-derived, without writing or invoking master host resources. Architecture checks passed in 5.513 seconds, and controller/runtime/generator lint reported zero issues.

A new-source integration test also passes with the existing shared-rate test under PostgreSQL/race in 8.023 seconds. It creates the source through the new transaction, compiles and explicitly publishes configurations for two nodes and two inbound descriptors per node, observes APPLIED, and activates only on fresh demand. Both placements share one term and quota. A 100 Mbps allocation splits to 50 Mbps per node only after the first node acknowledges its reduction. Two-node usage and repeated identical reports produce exactly 400 uploaded/600 downloaded bytes and reduce the shared outstanding reservation once. Node receipts are simulated in this master-side integration test; it does not prove live Linux forwarding throughput.

Tests must drive real PostgreSQL rollback, concurrent duplicate operations, changed-request conflicts, disabled creation, applied-plan/group rights, scoped credential projection, and atomic external links. Trap executors must prove headless creation invokes no master-local binary or host-key generator. Supplied credentials must survive unchanged; invalid bindings must leave no client, plan assignment, receipt, or fanout. Browser tests must cover uncertain responses without storing secrets or silently issuing a different request. Full factory activation waits for all supported cores and the remaining owned callers.
