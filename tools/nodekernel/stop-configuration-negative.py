#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify stop publication.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
configuration = "internal/controlplane/stop_configuration.go"
composition = "internal/controlplane/stop_composition.go"
outboxes = "internal/controlplane/stop_outboxes.go"
deployment = "internal/controlplane/deployment.go"
cases = [
    ("initial-delta-omission", composition,
     "state.grants[row.GrantID] = governor.Grant{ID: row.GrantID, ClientID: row.ClientID, RateRevision: policy.AuthorityRevision, Policy: policy}",
     "if exists { state.grants[row.GrantID] = governor.Grant{ID: row.GrantID, ClientID: row.ClientID, RateRevision: policy.AuthorityRevision, Policy: policy} }",
     "TestStopConfigurationPreservesDeltaAuthorityAndFrozenGraphPostgres", "stop publication="),
    ("credit-rollback", composition,
     "grant.Policy.CreditRevision, grant.Policy.CreditCeiling = uint64(row.Revision), uint64(row.CreditCeiling)",
     "grant.Policy.CreditRevision, grant.Policy.CreditCeiling = grant.Policy.CreditRevision, grant.Policy.CreditCeiling",
     "TestStopConfigurationPreservesDeltaAuthorityAndFrozenGraphPostgres", "stop publication="),
    ("pending-source-authorization", composition,
     "if !allowed[row.GrantID] {", "if !allowed[row.GrantID] && false {",
     "TestStopConfigurationRefusesUnprovenUnrelatedAuthorityPostgres", "ambiguous unrelated authority accepted"),
    ("orphan-lock-order", composition,
     'tx.Select("id,panel_client_id,owned").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids[offset:min(offset+1024, len(ids))]).Order("id").Find(&rows)',
     'tx.Select("id,panel_client_id,owned").Where("id IN ?", ids[offset:min(offset+1024, len(ids))]).Order("id").Find(&rows)',
     "TestStopConfigurationOrdersOrphanClientsAcrossNodesPostgres", "opposite per-node grant order deadlocked orphan stop"),
    ("premature-rate-release", configuration,
     'map[string]any{"revision": share.Revision, "desired": share.Desired}',
     'map[string]any{"revision": share.Revision, "desired": share.Desired, "outstanding": share.Desired}',
     "TestStopConfigurationPreservesDeltaAuthorityAndFrozenGraphPostgres", "publication released unacknowledged rate authority"),
    ("authority-cleanup-cas", outboxes,
     'tx.Where("grant_id = ? AND node_id = ? AND installation_id = ? AND base_configuration_revision = ? AND revision = ? AND payload_sha256 = ?", row.GrantID, row.NodeID, row.InstallationID, row.BaseConfigurationRevision, row.Revision, row.PayloadSHA256)',
     'tx.Where("grant_id = ? AND node_id = ? AND installation_id = ?", row.GrantID, row.NodeID, row.InstallationID)',
     "TestAppliedStopCleanupKeepsNewerQueuedRowsPostgres", "cleanup erased newer queued authority"),
    ("credit-cleanup-cas", outboxes,
     'tx.Where("grant_id = ? AND node_id = ? AND installation_id = ? AND base_configuration_revision = ? AND revision = ? AND credit_ceiling = ?", row.GrantID, row.NodeID, row.InstallationID, row.BaseConfigurationRevision, row.Revision, row.CreditCeiling)',
     'tx.Where("grant_id = ? AND node_id = ? AND installation_id = ?", row.GrantID, row.NodeID, row.InstallationID)',
     "TestAppliedStopCleanupKeepsNewerQueuedRowsPostgres", "cleanup erased newer queued authority"),
    ("equal-revision-policy", outboxes,
     "if policy.AuthorityRevision == update.AuthorityRevision && (policy.Enabled != update.Enabled || policy.Upload != rates.Upload || policy.Download != rates.Download) {",
     "if false && policy.AuthorityRevision == update.AuthorityRevision && (policy.Enabled != update.Enabled || policy.Upload != rates.Upload || policy.Download != rates.Download) {",
     "TestAppliedStopCleanupRetainsUnprovenEqualRevisionPayloadPostgres", "unproven equal-revision policy was discarded"),
    ("applied-pool-lock-order", configuration,
     "if err := lockStopRatePools(tx, state, request.GrantIDs); err != nil {", "if err := error(nil); err != nil {",
     "TestStopConfigurationOrdersPoolsAgainstAppliedAcknowledgementPostgres", "stop and applied acknowledgement lock order diverged"),
    ("obsolete-disabled-proof", deployment,
     "if deployment.Revision == node.DesiredRevision && deployment.Revision >= node.AppliedRevision {", "if deployment.Revision >= node.AppliedRevision {",
     "TestAppliedStopProofCannotResurrectAcrossReenablePostgres", "stale stop proof count="),
    ("enabled-proof-clear", deployment,
     "if err := clearAppliedEnabledStops(tx, p, deployment.Revision, deployment.Payload); err != nil {", "if err := error(nil); err != nil {",
     "TestAppliedStopProofCannotResurrectAcrossReenablePostgres", "stale stop proof count="),
    ("provenance-omission", configuration,
     "if err := carryStopProvenance(tx, principal, request.OperationID, base.Revision, deployment.Revision); err != nil {", "if err := error(nil); err != nil {",
     "TestStopConfigurationCarriesOnlyExactBaseProvenancePostgres", "stop lost exact source pins"),
    ("foreign-provenance", "internal/controlplane/stop_provenance.go",
     "if compilation.NodeID != principal.NodeID || compilation.InstallationID != principal.InstallationID || !compilation.Ready {",
     "if false && (compilation.NodeID != principal.NodeID || compilation.InstallationID != principal.InstallationID || !compilation.Ready) {",
     "TestStopConfigurationCarriesOnlyExactBaseProvenancePostgres", "foreign or ambiguous provenance accepted"),
    ("received-is-not-applied", deployment,
     "if result.Stage == nodev1.ApplyStage_APPLY_STAGE_APPLIED {",
     "if result.Stage == nodev1.ApplyStage_APPLY_STAGE_APPLIED || result.Stage == nodev1.ApplyStage_APPLY_STAGE_RECEIVED {",
     "TestStopConfigurationPreservesDeltaAuthorityAndFrozenGraphPostgres", "outbox count=0 want=1"),
]
for name, filename, before, after, selection, evidence in cases:
    source = (root / filename).read_text()
    expected = 2 if name == "pending-source-authorization" else 1
    if source.count(before) != expected:
        raise SystemExit("Mutation target changed: " + name)
    destination = pathlib.Path(tempfile.mkdtemp(prefix="stop-negative-" + name + "-", dir=work))
    replacement = destination / pathlib.Path(filename).name
    replacement.write_text(source.replace(before, after, 1))
    overlay = destination / "overlay.json"
    replacements = {str(root / filename): str(replacement)}
    if name == "orphan-lock-order":
        pool_source = (root / configuration).read_text()
        pool_guard = "if err := lockStopRatePools(tx, state, request.GrantIDs); err != nil {"
        if pool_source.count(pool_guard) != 1:
            raise SystemExit("Orphan lock-order pool guard changed")
        pool_replacement = destination / "stop_configuration.go"
        pool_replacement.write_text(pool_source.replace(pool_guard, "if err := error(nil); err != nil {", 1))
        replacements[str(root / configuration)] = str(pool_replacement)
    overlay.write_text(json.dumps({"Replace": replacements}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-overlay", str(overlay),
                             "./internal/controlplane", "-run", "^" + selection + "$", "-count=1", "-timeout=3m"],
                            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (destination / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or evidence.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Negative control did not detect " + name + "; inspect " + str(destination / "result.log"))
    print("Detected removed safeguard: " + name)
