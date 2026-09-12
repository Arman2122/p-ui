# Penhoon design completion

Reference: Claude Design project `08e0778b-d3df-4f8e-bf0a-6e998e6c783b`.

The earlier shell, page headers and statistics strips are foundations, not completed screens.
Completion means matching the screen composition and responsive behavior, preserving existing
operations, and checking the complete route in a browser. Backend-dependent controls must have
real persistence, validation and authorization before being reported as working.

## Remaining acceptance checklist

- [x] Recover all reference screens and imports from the user's ZIP export (42 verified files).
- [ ] Clients: two-line rows, status, plan, usage, speed, expiry and all existing actions.
- [x] Groups: card grid with existing membership and usage controls.
- [x] Cores: rows and detail view with instances, logs, versions and restart operations.
- [ ] Plans: catalogue, limits, lifecycle settings, inbound assignments and client assignment.
- [ ] Inbounds, Nodes, Hosts: full content and forms, not only headers.
- [x] Outbounds and Routing: independent route compositions preserving existing editors.
- [ ] Settings and Core Configs: complete sections and controls, with current subscription/security options preserved.
- [ ] API: discoverable endpoint explorer and token management.
- [ ] Notifications: persisted per-user read state, activity/audit log and 30-day/10,000-row retention.
- [ ] Resellers: real roles, ownership and server-side scoped access, caps and dashboard.
- [ ] Integrated desktop/mobile/light/dark verification; failed requests must stay visibly failed.
- [ ] Build, generation, lint, tests and test-VM deployment after verification.

## Reference handling

The user's ZIP was verified and extracted under the Git-ignored
`scratchpad/design-import/export`. A SHA-256 manifest records all 42 files.
The workspace ZIP was deleted after extraction. Neither it nor its extracted contents belongs in a commit.

## Implemented in the current frontend pass

Clients now combines identity, presence, attachments and comments into compact rows, retaining
enable toggles, usage, remaining quota, speed, expiry, row actions, selection and bulk operations.
Plans has a searchable catalogue and details backed by the existing policy API. The legacy
`/policies` route remains usable alongside `/plans`.

Nodes has a card/table switch, health/resource cards, search and the existing management actions.
Transitive nodes stay read-only and cannot request history using the synthetic node id zero.
Hosts and Inbounds use the shared table appearance; inbound identity includes its listener port.
The inbound editor has a responsive section rail while preserving protocol-specific fields.

Settings exposes certificates, tokens, backup, accounting, time, LDAP and device limits directly,
in addition to general, notifications, security and subscription sections. The existing token and
backup services are reused. API documentation now has a searchable endpoint rail and the existing
OpenAPI-based request runner. CSRF headers are attached only to requests to the current origin.

## Still requires implementation (not complete)

The Plans catalogue does not yet implement the design's lifecycle wizard, ownership, assignment
statistics or archive/renewal actions. The Clients table still needs plan metadata and historical
traffic data for its plan/trend columns. Group pooled quotas and the reference's additional node
telemetry need server support. Reseller accounts and their permission boundary are not implemented.
Activity is still session-local; durable audit history, read state, retention and computed alerts
remain backend work. Backup scheduling and the complete per-core configuration screens also remain.

These gaps must not be represented by sample values, successful mock responses or working controls
until their backend behavior is implemented and tested.
