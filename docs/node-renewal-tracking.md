# Node renewal tracking

Penhoon records renewal generations so delayed node snapshots cannot overwrite a
newer client term, and a real renewal remains recognizable when no traffic was
used. The generation is shared by every local inbound and core serving that
client.

## Stored state

| Location | Field | Meaning |
| --- | --- | --- |
| `client_traffics` | `reset_count` | Monotonic generation of committed term renewals on this panel. |
| `node_client_traffics` | `reset_count` | Last accepted lifecycle generation from this node and client. |
| `node_client_traffics` | `observed_reset_count` | Generation belonging to the stored byte counters. |
| `node_client_traffics` | `reset_after_count` | Latest observed generation whose bytes a manual reset has already cleared. |
| `node_client_traffics` | `reset_pending` | The next byte snapshot establishes a new baseline after a manual usage reset. |

The two node generations can differ while a configuration change is pending or
while a snapshot contains settings and statistics sampled on opposite sides of
a renewal. Advancing the byte baseline prevents repeated billing while retaining
the unaccepted generation lets the next consistent snapshot complete the renewal.

Generations are compared per node and email. Two nodes can have independent
renewal histories; comparing either node directly against the controller's
combined generation would discard valid renewals.

## Renewal and reset behavior

A generation advances with the committed expiry change. A client attached to
several inbounds advances once for each elapsed renewal period. A failed or
superseded renewal preserves the previous term and generation. Native quota
reset failures are retryable without recording a successful renewal.

A manual usage reset keeps the renewal history and marks node byte baselines as
pending. This prevents an old cumulative traffic report from restoring reset
usage, while a renewal occurring before the next node poll is still detectable.
A deferred renewal observed before the reset may still advance the deadline,
but cannot restore those cleared bytes or discard other nodes' new usage.
Actual client and node deletion continues to remove their baselines.

For generation-aware nodes, the controller preserves its configured quota,
renewal interval, and absolute expiry unless a newer, coherent renewal advances
the term. A delayed-start duration can still become an absolute deadline on
first connection. An unlimited term stays unlimited. A stale disable based on
older limits cannot disable a client still allowed by the controller's limits.

A pending or just-completed configuration push freezes lifecycle adoption while
traffic continues to accumulate. Inconsistent renewal generations for the same
client under sibling inbounds reject that snapshot before writes; the existing
sync job retries a fresh snapshot. A regressed node generation reinitializes the
byte baseline and preserves the controller's term until fresh renewal evidence
arrives.

## Compatibility and rollout

The existing client-traffic payload gains an optional `resetCount` field. Updated
panels persist and emit zero before their first renewal. An absent or null field
identifies a legacy sender, whose established merge behavior remains available.
Generation-based stale-term protection requires an updated sender.

Upgrade the controller before its nodes so it can begin observing their renewal
generations. The migration adds and backfills the controller generation without
changing existing usage, deadlines, client credentials, or memberships. Node
baseline generations remain unknown until a generation-aware snapshot arrives.

This uses the existing traffic writer, node sync job, and subscription/API
contracts. Durable delivery of resets to an offline node is a separate concern;
this change does not introduce a reset retry queue.
