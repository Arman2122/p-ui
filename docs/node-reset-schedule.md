# Shared client reset scheduling

The user selected one plan/client reset schedule for the shared allowance. Inbound reset settings do not create separate allowances for the same identity. Inbound display counters are a separate concern.

## Policy and cadence

`FleetResetSchedule` belongs to the immutable fleet client UUID. A nullable override inherits the applied plan's quota reset cycle; an explicit override chooses `none`, `hourly`, `daily`, `weekly`, or `monthly`. An overridden quota does not silently inherit the plan's reset cycle. Saving this policy does not create a billing term, issue traffic credit, or reset measured usage.

Cadence uses a persisted UTC anchor. A schedule configured before activation starts with the authorized term. A later explicit policy change establishes a new anchor; saving the same policy leaves it unchanged. Monthly scheduling retains the original day, clamping short months and restoring that day in later months. A traffic reset preserves the anchor and expiry; renewal establishes the new term's anchor. A disabled client retains its schedule but is not selected for a new reset.

Billing reads the canonical client policy and credentials independently of retained placement definitions. Detaching every inbound does not discard the client's existing cadence or its old stop obligations. Actual placement publication and credit allocation continue to validate attachment and protocol rights separately.

The partial `(ready_unix_milli, client_id)` index selects bounded work pages. `next_due_unix_milli` retains the original due slot; `ready_unix_milli` is the next worker attempt. The worker is part of the control-plane lifecycle alongside credit, admission, and source reconciliation. Its current page size and deadlines are implementation bounds, not a measured fleet throughput guarantee.

## Durable execution

The due selection is checked under the normalized client, fleet client, and schedule locks. A deterministic operation receipt pins the schedule revision, original due time, predecessor term, source policy, and credential generation. Beginning the reset marks that term draining and persists real source-stop work for every placement in the same PostgreSQL transaction. Enabled authority outboxes and new admission work cannot bypass the drain.

The existing source worker sends disabled authority or a frozen native stop configuration. Transmission, node silence, and a partial set of acknowledgements do not complete a reset. Every predecessor grant needs its applied stop and settled zero rate authority. Offline or lost installations retain their reservations; unresolved authority is not converted into measured usage.

Completion activates one replacement term, projects it, clears old placement grant pointers, and queues source reconciliation atomically. The original operation remains referenced across restarts and calendar boundaries. The next due time advances from the original anchor beyond completion, skipping missed intervals instead of granting one allowance for every missed tick.

Stream placements republish an authentication-only graph before fresh authenticated demand obtains new-term credit. The old terms, grants, measurements, and unresolved reservations remain historical accounting records. Native automatic funding remains gated by its separate kernel/daemon qualification.

## Administration

Configuration administrators use these master endpoints:

| Method | Path | Effect |
| --- | --- | --- |
| GET | `/panel/api/fleet/clients/:id/reset-schedule` | Read policy, next due/attempt, and pending operation. |
| POST | `/panel/api/fleet/clients/:id/reset-schedule` | Configure the policy with operation UUID and expected schedule revision. |
| POST | `/panel/api/fleet/clients/:id/reset-schedule/cancel` | Cancel the exact pending operation with an operation UUID and expected revision. |

Mutations dispatch through the owned runtime and its current database lifecycle checks. Conflicting request replay and stale revisions fail. A source or credential change blocks automatic completion; it does not silently authorize a replacement identity or term.

Explicit cancellation keeps the original term and remaining allowance, closes the unused pending term, and skips the cancelled due interval. It preserves mandatory node stops before source reconciliation may resume service. It does not replenish credit, erase reservations, adopt changed credentials, or bypass recovery holds. Lost-response retries retain the same operation UUID and request; reads after a successful mutation show current state.

## Evidence and remaining qualification

Real PostgreSQL tests cover different inbound schedules inheriting one applied plan, UTC month anchoring, idempotent policy changes, detached clients, two simultaneous node authorities, every-node stop acknowledgements, transactional rollback, durable scheduler restart/cancellation, unchanged old reservations, and actual master-side source recompile/APPLIED/fresh-demand funding after term closure. Administrator routes verify runtime dispatch, scope, optimistic concurrency, and committed-change audit records. Four schedule and seven execution removed-safeguard controls reproduce their expected failures. The billing extraction passes a broader real-PostgreSQL source/admission/credit/materialization checkpoint in 131.204 seconds and focused reset/source race tests in 20.961 seconds.

The master client-details UI exposes this policy only for authenticated administrators and a projected fleet client UUID. It retains an actor/client-scoped exact mutation request before sending, offers explicit same-operation retries after uncertain results, and uses a fresh read to reconcile revisions. Cancellation requires an explicit explanation/confirmation. Fifteen component tests, seven Chromium light/dark accessibility stories, five i18n/schema checks, ten removed-guard controls, typecheck, lint, and the Vite build pass for this interface.

These are database/control-plane integration checks, not real Linux packet or daemon qualification. The main production factory remains gated by the other owned business operations. Full fleet load, long outages, final release gates, and inbound display-reset integration remain outstanding.
