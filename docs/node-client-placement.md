# Client placement

Placement policy is master-owned metadata, separate from network overrides and node observations. Enrollment creates no default target and copies no clients. An administrator explicitly enables a node policy, lists eligible inbound templates, and assigns labels before a rule can select that node.

Rules have an immutable plan or stable client-group ID. A client has one group. Matching rules contribute a union of targets; explicit node IDs and labels within one rule are conjunctive. Source attachments, managed plan restrictions, reseller inbound grants, node template eligibility, and client restrictions narrow that union. A rule with neither node IDs nor labels is refused. Removing a rule does not remove an explicit manual target.

Effective source templates are the union of explicit client links, current group links, and non-overridden plan membership, narrowed by a nonempty managed plan allow-set and reseller grants. Creation, placement preview, manual attach, and materialization use the same selector. Inherited templates do not become explicit client links, so removing inheritance or choosing an empty explicit override can remove them. Membership alone does not prepare credentials or authorize funding.

An unplanned, ungrouped client can use an explicitly configured default node installation. Null means unplaced. The default is considered only when the client has no explicit manual targets. An installer may choose a combined local default only after real enrollment and policy approval; enrollment itself never selects it.

## Preferences and stop requirements

The client preference transaction has an immutable operation UUID and a dedicated placement revision, independent of billing terms and the source enable revision. It runs through `runtime.SourcePlacementRuntime`, using the same current-database and real-transaction checks as the enable adapter.

- `restrict` replaces a nullable global inbound allow-set. Null inherits; an empty list denies every template.
- `detach` adds suppression for the exact logical node and template IDs. It does not remove that template from other nodes or remove normalized source attachments.
- `attach` records an explicit manual target and clears only those exact suppressions. The requested templates must already belong to the normalized client source.

Restrictions fence new allocation, refill, authenticated demand, enabled authority delivery, and normal publication immediately, including when assignment fanout has not created an intent yet. Existing quota and outstanding rates remain reserved. Removed references also queue real per-placement source stop work in the same transaction. A later reattach cannot erase that stop requirement. Frozen disabled authority remains deliverable, and only actual applied stop proof clears the barrier.

The source transaction locks the normalized client, logical fleet client, and its placements. It takes no target node lock and creates no target-node foreign-key dependency. Fanout and placement intent rows deliberately have no cross-node target foreign keys. Node workers acquire their node before the source locks; a full-node compile must acquire all normalized source rows in compiler order.

## Capability and runtime evidence

Authenticated `BeginSession` records a bounded, canonical capability inventory. Identical reordered reconnects retain the semantic digest and revision while refreshing session generation and receipt time. Changed inventory atomically queues reevaluation keyed by node and installation. Malformed inventory fails before changing the session; a fanout persistence failure rolls back inventory and session changes together. Empty inventory is valid and means unavailable for new application.

Capabilities can narrow application eligibility but cannot grant labels, add rules, retire an assignment, or release reserved authority. `applicationEligible` in the preview refers to publishing a qualified authentication baseline, not live forwarding readiness. Quota funding still requires its own fresh authenticated demand, exact applied scope, runtime journals, current term, and authority observations. Native automatic application remains blocked with an explicit funding diagnostic.

Runtime IDs are not assumed to equal core names. A target is resolved against current master-approved network bases and current-installation runtime journals. One existing matching placement runtime is preserved; otherwise exactly one matching runtime is required. Missing or ambiguous runtimes produce a diagnostic. Old journal registrations without a current master base cannot select a worker. The current compiler still requires canonical core-name runtime IDs and one runtime per core, so resolved aliases remain explicitly unqualified for automatic application until compiler mapping and resource claims are tested.

## API and implementation checkpoint

Configuration-administrator routes are under `/panel/api/fleet/placement`: node policy reads/writes, cursor-paginated rule reads/writes, standalone default reads/writes, and client preview/preference writes. Schemas and examples are generated from Go, and both panel and documentation OpenAPI artifacts are refreshed together. Mutation responses indicate pending convergence; they never promise an immediate remote disconnect.

The current checkpoint implements durable placement intents and leased fanout consumption alongside the metadata, preference, runtime dispatch, preview, and admin API layers. Fanout uses bounded client keysets and commits each client's derived intent with an exact work revision and lease check. Node application materializes a stream target, queues real source reconciliation, and waits for its applied authentication baseline. This creates no billing term or quota grant; fresh authenticated demand still starts funding.

Retirement retains the placement and its grant history, waits for actual stopped authority, publishes omission through the ordinary compiler, then waits for that graph to be applied. Reattachment preserves an outstanding stop requirement and invalidates the old descriptor revision, including an unfunded authentication-only baseline. Rule writers lock their immutable parent so a newly matching rule cannot cross the final retirement publication check.

Automatic publication requires an explicitly applied network baseline matching the current head and defaults, with no pending manual network draft. Client source pins may refresh within that approved network. Initial network authorization remains an administrator action. Stop work stays independent, and an already applied omission can settle without approving a later unrelated draft.

Applied omission can use an indexed runtime only when publication computed a complete grant-to-descriptor projection from the exact verified payload. An old or incomplete projection requires the bounded full-payload proof. The completeness flag is read acceleration and conveys no authority. Existing rows migrate to unknown rather than being trusted from absence alone.

The placement scheduler now runs in `RunFleetAuthoritySchedulers`. Its integration test observes an applied authentication baseline without creating a term or grant before demand, and verifies cancellation of the scheduler group. Ordinary source CRUD routing and the production runtime factory remain separate cutover work. The additive APIs do not enable the factory or claim fleet-scale qualification. Policy `pending` fields describe queued policy reevaluation; they are not proof that every selected node has applied its graph.

Workers retain pending work when an inherited target lacks a prepared credential or an exact reserved authentication claim. They do not mint credentials while materializing a target. A failed client's derived writes roll back independently while the parent fanout advances to later clients. Per-node retry selection excludes old installations, and canceled attempts retain their original queue time with a later retry availability.

Current bounds are 4,096 templates per source/set, 128 matching rules per client, 1,000 matching nodes per rule, and 8,000 projected node/runtime targets per client. Sentinel overflow is an explicit refusal, never a truncated successful decision. These bounds are safety limits, not throughput measurements.

## Verification

Local PostgreSQL gates cover capability reconnect stability, malformed input, fanout rollback and keyset bounds, scoped suppression, runtime transaction rollback without node-lock inversion, actual disabled authority delivery and stop acknowledgment, rule union with restriction intersection, manual preservation, and explicit standalone defaults. Later gates passed real stream application and retirement, authentication-only reattachment, indexed omission with an unrepresented base grant, unknown-projection fallback, fresh-schema migration, and concurrent rule insertion during retirement.

The scheduler integration, failed-client fairness, current capability recheck, inherited membership, and unprepared credential cases passed their real PostgreSQL tests. The focused placement/template suite passed with race instrumentation in 76.203 seconds and the migration suite in 3.900 seconds. A subsequent fence-order correction in the separate retirement publication transaction passed its deterministic lock probe and affected retirement/scheduler race tests in 9.513 seconds. These local Windows/PostgreSQL results do not establish Linux runtime or fleet-scale qualification.

Twenty-three placement removed-safeguard overlays reproduced the intended failures, including incomplete-index omission, disabled grants absent from descriptor scopes, rule insertion during retirement, authentication-only reattachment, stale installations, failed-client rollback, inherited membership restrictions, credential dependency handling, retirement fence order, manual draft preservation, and scheduler wiring. Capability-session validation has three additional qualified controls.

The later combined source/placement/group race gate passed in 235.956 seconds, with its database migration selector passing in 8.462 seconds. The final scheduler/manual-draft/group-conversion race gate passed in 7.677 seconds. These gates retain the explicit initial-network approval requirement.
