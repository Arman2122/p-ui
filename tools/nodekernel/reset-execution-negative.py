#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify reset execution.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
cases = [
    ("placement-independent-policy", "internal/web/service/fleet_reset_execution.go",
     "snapshot, err := fleetBillingSnapshotTx(tx, *client.PanelClientID)",
     '''snapshot, err := func() (fleetClientSnapshot, error) {
        var placement model.FleetPlacement
        if err := tx.Where("client_id = ?", client.ID).Order("id").First(&placement).Error; err != nil { return fleetClientSnapshot{}, err }
        ids, err := placementInboundIDs(tx, placement.ID)
        if err != nil { return fleetClientSnapshot{}, err }
        return fleetSnapshot(tx, *client.PanelClientID, ids, placement.CoreID)
     }()''',
     "TestFleetScheduledResetUsesClientPolicyAfterAllInboundAttachmentsAreRemovedPostgres", "detached placement prevented client-owned cadence="),
    ("actual-stop", "internal/controlplane/client_drain.go",
     "if err := requireStoppedGrants(tx, previous); err != nil {",
     "if err := requireStoppedGrants(tx, previous); err != nil && false {",
     "TestFleetScheduledResetResumesAcrossMissedCyclesAndRequiresEveryAppliedStopPostgres", "reset completed before every actual applied stop="),
    ("atomic-stop-intent", "internal/web/service/fleet_reset_execution.go",
     "db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {",
     "func(apply func(*gorm.DB) error) error { return apply(db.WithContext(ctx)) }(func(tx *gorm.DB) error {",
     "TestFleetScheduledResetRollbackAndSourceDriftPreservePendingWorkPostgres", "failed stop queue left previous term draining="),
    ("due-selection-cas", "internal/web/service/fleet_reset_execution.go",
     "if !sameFleetResetWork(schedule, expected) {", "if !sameFleetResetWork(schedule, expected) && false {",
     "TestFleetScheduledResetResumesAcrossMissedCyclesAndRequiresEveryAppliedStopPostgres", "stale due selection created another reset="),
    ("retired-grant-pointers", "internal/web/service/fleet_reset_execution.go",
     '"current_grant_id": nil, "current_grant_term_id": nil',
     '"current_grant_id": placement.CurrentGrantID, "current_grant_term_id": placement.CurrentGrantTermID',
     "TestFleetScheduledResetResumesAcrossMissedCyclesAndRequiresEveryAppliedStopPostgres", "reset left old authority attached="),
    ("credential-fence", "internal/web/service/fleet_reset_execution.go",
     "!bytes.Equal(snapshot.CredentialDigest, client.CredentialDigest)",
     "(!bytes.Equal(snapshot.CredentialDigest, client.CredentialDigest) && false)",
     "TestFleetScheduledResetRollbackAndSourceDriftPreservePendingWorkPostgres", "changed credentials completed pending reset="),
    ("retry-selection-cas", "internal/web/service/fleet_reset_scheduler.go",
     '''Where("client_id = ? AND revision = ? AND next_due_unix_milli = ? AND ready_unix_milli = ? AND pending_operation_id IS NOT DISTINCT FROM ? AND bound_term_id IS NOT DISTINCT FROM ?",
			expected.ClientID, expected.Revision, expected.NextDueUnixMilli, expected.ReadyUnixMilli, expected.PendingOperationID, expected.BoundTermID).''',
     'Where("client_id = ?", expected.ClientID).',
     "TestFleetResetRetryCannotOverwriteNewPolicyAndDisablePreservesCadencePostgres", "stale reset retry overwrote new policy="),
]
for name, filename, before, after, selection, expected in cases:
    if sys.argv[1:] and name not in sys.argv[1:]:
        continue
    source = root / filename
    data = source.read_text()
    if data.count(before) != 1:
        raise SystemExit("Reset execution mutation changed: " + name)
    directory = pathlib.Path(tempfile.mkdtemp(prefix="reset-execution-negative-" + name + "-", dir=work))
    replacement = directory / source.name
    replacement.write_text(data.replace(before, after, 1))
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-overlay", str(overlay), "-count=1", "-timeout=3m", "-run", "^" + selection + "$", "./internal/web/service"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or expected.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Reset execution negative failed to reproduce " + name + "; inspect " + str(directory / "result.log"))
    print("Reset execution safeguard reproduced: " + name, flush=True)
