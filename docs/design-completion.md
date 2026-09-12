# Penhoon design implementation

Reference: Claude Design project `08e0778b-d3df-4f8e-bf0a-6e998e6c783b`,
`Penhoon Panel.html` and its imported screens, components and styles.

## Implemented behavior

- The shell, navigation, page headers, cards, tables and forms use the exported design. The shell supplies the Ant Design theme to every route. Phone layouts contain wide tables and keep section navigation scrollable.
- The dashboard uses real CPU, memory, swap, disk, core, network and socket data. Window selection requests the corresponding history. Missing observations remain missing; stopped cores do not display an uptime. Client counts and retained activity use their APIs.
- Clients include plan metadata and recorded traffic history. Plan assignment is available during creation, editing and bulk creation, with explicit override handling. Existing credential, attachment, subscription, traffic and bulk operations remain available.
- Plans support quota, duration, IP limits, inbound sets, speed tiers, lifecycle settings, archive, duplication, assignment statistics, members and reapplication. Applied settings are preserved until requested reapplication; template changes do not silently overwrite client overrides.
- Groups support pooled quotas and explicit inbound membership. Membership changes use runtime dispatch and retry failed convergence.
- Nodes include fleet status, resource history and served-inbound telemetry. Transitive nodes remain read-only. Inbounds, Hosts, Outbounds, Routing and Cores retain their operational editors and actions in the new layout.
- Core Configs includes Xray's template editor and other cores' persisted per-inbound settings, supported validation, editing, download and restart controls.
- Settings includes subscription, listener/session, certificates, authentication, tokens, notifications, traffic accounting, calendars, LDAP, device limits and backup operations. Default-setting reads do not require a running Xray process.
- Activity persists per-user read markers, owner visibility and cursor pagination, with 30-day retention and a maximum of 10,000 events. Successful mutations and system events feed the dashboard and notification menu. Current alerts are computed separately. Account changes and database restores invalidate stale frontend read state.
- Resellers have roles, client ownership, granted inbounds, budgets, expiry, suspension, private plans, tokens and account settings. Administrator routes and the global WebSocket are denied to reseller identities. OpenAPI and the explorer expose their permitted operations.
- Reseller budgets count actual traffic. Resetting or deleting a client never refunds spent traffic. Replicated traffic is not counted twice; ownership transfers count future consumption, and stale snapshots cannot conceal new usage. Allocation history records committed quota reservations once and excludes failed edits and unchanged retries.
- Backups support manual snapshots, schedules, retention, download, file upload and restore, durable operation history, optional Telegram delivery, and request-only passphrases for authenticated encrypted archives. Restore authenticates encrypted archives before modifying PostgreSQL.
- API metadata and generated request/response schemas cover the added operations. The documentation site uses the same OpenAPI document.

## Capability boundaries

- Rate shaping and quota continuation depend on core capabilities; unsupported combinations are rejected. Burst controls remain disabled because the runtime has no burst capability.
- Reseller plans cannot enable first-use activation or automatic renewal. These require owner-budget and account-term enforcement that the current reseller lifecycle does not expose.
- Automatic encrypted backups require an external key provider. No request passphrase is retained for unattended jobs; scheduled snapshots are explicitly unencrypted. Manual encrypted snapshots and restores are supported.
- Database archives include managed configuration and stored history. External certificate files, core binaries and other host files need a filesystem backup. Restore retains the existing owner-only plaintext safety dump, disclosed in the restore flow.
- Client trends contain locally recorded counter changes, including managed-node traffic merges. They do not manufacture history from cross-master snapshots.

## Verification

Integration checks cover TypeScript, ESLint, frontend component and Chromium
Storybook tests, Go lint, generated schemas, and Linux tests using isolated
PostgreSQL databases. The encrypted-backup test performs a real PostgreSQL restore
inside a private temporary mount namespace. Ownership, quota resets, replicated
traffic, lock ordering, authentication, export scope and activity read state have
regression coverage, including negative runs without their fixes.

Browser review uses a separate, unprivileged Linux panel with a private PostgreSQL
schema and disabled test inbounds. It exercises administrator and reseller screens,
real plan creation, notification persistence, role-restricted API discovery, light
and dark themes, and phone layouts. No sample response is substituted into the
running application.

## Reference handling

The export was verified and extracted into the Git-ignored
`scratchpad/design-import/export`; its manifest covers all 42 files. The original
workspace ZIP was deleted after extraction. Neither the ZIP nor the extracted
reference files belong in a commit.
