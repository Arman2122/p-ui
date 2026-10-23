# Owned group accounting

Each new service-created term records an immutable `FleetTermGroup` row in the same transaction as the term. A nullable group ID explicitly means ungrouped; an absent row means unknown attribution. Activation, renewal, manual traffic reset, and scheduled reset use this boundary. Replaying an existing transition does not infer missing historical membership from the current client name. Foreign keys retain the term and stable group identity.

Group totals combine confirmed directional usage from every pinned term, including closed terms and late reports, with the initial activation opening once. Current normalized membership determines client counts, not historical usage ownership. The existing group display baseline is subtracted once. Owned client rollover does not subtract its counters from that baseline because its ledger history remains present. Legacy-only clients keep their existing counter and baseline behavior.

Daily group history uses reported term usage days and the same immutable group identity. It excludes opening balances, reservations, and legacy samples for owned clients. Missing usage, conflicting budget/group provenance, or an unrepresented historical term requires reconciliation instead of becoming a zero or being assigned to the current group.

The first finite group budget uses a group-scoped version of this authoritative opening calculation. Existing budget `used` and `reserved` balances are unchanged. A positive provisional legacy seed without a term/group pin cannot initialize a new owned pool. Fresh created identities are the supported path; this adds no legacy adoption or migration mechanism.

Creating a finite parent after grants were already issued without that parent remains refused, including after a stop acknowledgment and client-term rollover. Late old reports and outstanding old credit cannot be covered by merely snapshotting confirmed usage into a new pool. Live conversion from an unlimited group needs an explicit drain/reparent transition outside this slice.

A fresh version-1 source has known-zero opening only when it has no current term, no pending term, and no term history. Read projection, usage filters, and first-activation billing use that proof and ignore unrelated legacy counters. Losing a current pointer while historical terms remain is an error, not a fresh account.

Owned group resets remain refused, including when no current normalized members remain but term provenance or a parent budget survives. A real shared-pool reset still needs its own drain and applied-stop boundary. Group membership and source edits remain separate work and must establish the correct new-term boundary before they are enabled.

The node usage protocol currently identifies a grant, client, term, and runtime meter. It does not carry exact inbound attribution, and one grant can serve multiple inbound descriptors. Clearing legacy inbound columns therefore cannot qualify an owned inbound display-reset callback. That callback remains gated on a real attributable observation/projection path and must never reset client billing terms.

The first local PostgreSQL gate passed group rollover, late closed-term reports, legacy group regressions, fresh reads and first funding, and explicit network approval preservation. The combined affected service race gate passed in 235.956 seconds and the database selector in 8.462 seconds. The final quota-conversion and scheduler/manual-draft race gate passed in 7.677 seconds. Eighteen group removed-safeguard controls reproduced their expected failures, including actual erroneous funding when the unknown-opening, provisional-seed, or prior-unparented-grant guards were removed. This is not a fleet-scale or Linux runtime claim.
