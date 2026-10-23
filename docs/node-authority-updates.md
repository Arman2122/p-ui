# Incremental stream authority

An authenticated client identity can be published before it has forwarding
authority. The master commits the billing decision, credit and shared rate
allocation before delivering an initial grant. An activation response alone
never permits traffic. Subsequent rate changes use the same delivery channel;
credit-only top-ups keep their existing separate RPC.

`AuthorityDeliveryService` fetches and acknowledges batches of at most 128
updates. Each batch binds node, installation, agent journal, runtime, worker
journal, credit journal, applied configuration revision and the exact private
runtime projection hash. Each entry additionally binds grant, client, term,
credential generation, placement revision and the complete canonical set of
published descriptors for that client. Idle responses retain the same base
revision and hash as the request.

The worker pins its agent journal only after a valid full deployment is received;
validation cannot establish the binding. The binding cannot be replaced and is
required for both authority updates and credit top-ups. Desired and applied
configuration must match before updates are accepted. Unconfigured runtimes are
not polled. The agent caches projection hashes by applied revision and master
payload digest, avoiding full configuration decoding on each delivery poll.

`Policy.Revision` remains the full configuration revision. `AuthorityRevision`
matches the committed rate-share revision, and `CreditRevision` tracks cumulative
issued credit independently. Grant rate metadata is persisted with the policy.
Within one base, term, expiry, quota mode and descriptor scope remain immutable.
A disabled grant cannot be revived. An enabled grant with both directions blocked
may receive a later rate allocation. Replays preserve newer credit, counters and
rate-limiter state; changing a rate never creates a fresh burst. A newer full
snapshot cannot regress managed authority or credit, or change its quota mode.

Validated authority intent is committed to the worker journal before governor
application. Only actual application receives an acknowledgement. After a crash,
matching intent is merged into the same private base before reconciliation, so
an authentication-only base does not remove a durably authorized grant. A new
base never imports old intent. Client-to-grant indexing prevents replacing a
grant within the same base. Old intent is pruned after a newer full application,
without reclaiming governor reservations or changing measured usage.

Stream adapters use this receiver. Native incremental authority is explicitly
refused until its adapter is qualified. Native full-snapshot observations still
report their persisted authority revision. Observed revisions are diagnostic
metadata: reporting them does not settle rate allocations, reclaim credit, or
turn reservations into traffic. Master restore reconciliation remains a separate
operational gate.

Tests exercise initial grants without core rebuild, rate debt, permanent stop,
newer-credit retention, durable intent before application, restart replay,
changed-base retirement, full-snapshot rollback refusal, lost acknowledgements,
partial runtime availability and sparse rate-only reporting. Isolated overlays
in `tools/nodekernel/authority-update-negative.py` remove individual safeguards
and require their regressions to return. These checks are not a fleet-scale or
all-core performance qualification.
