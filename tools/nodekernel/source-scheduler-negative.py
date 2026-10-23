#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify source scheduling.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
cases = [
    ("queue-index-upgrade", "internal/database/db.go",
     'if err := db.Exec("DROP INDEX IF EXISTS idx_fleet_source_work_due").Error; err != nil {',
     'if err := error(nil); err != nil {',
     "TestFleetSourceQueueIndexUpgradePreservesPendingWorkPostgres", "queue migration retained old index", "./internal/database"),
    ("enable-action-dispatch", "internal/web/service/fleet_source_scheduler.go",
     "action := ReconcileFleetSourceWorkTx", "action := StopFleetSourceWorkTx",
     "TestFleetSourceSchedulerReenablesThroughNewAppliedScope", "scheduler did not publish enabled source"),
    ("stop-action-dispatch", "internal/web/service/fleet_source_scheduler.go",
     "if expected.RequiresStop {", "if expected.RequiresStop && false {",
     "TestFleetSourceSchedulerPublishesStopAndCompletesOnlyAfterAppliedReceipt", "source scheduler failed to converge"),
    ("current-installation-page", "internal/web/service/fleet_source_mutation.go",
     "AND installation_id = (SELECT installation_id FROM fleet_nodes WHERE id = ?)",
     "AND (?::uuid IS NOT NULL)",
     "TestFleetSourceSchedulerDefersInvalidTargetWithoutStarvingRealStop", "invalid source target"),
    ("dependency-isolation", "internal/web/service/fleet_source_scheduler.go",
     "if err != nil && !deferFleetSourceError(err) {", "if err != nil {",
     "TestFleetSourceSchedulerDefersInvalidTargetWithoutStarvingRealStop", "invalid source target stopped other work"),
    ("preflight-backoff", "internal/web/service/fleet_source_scheduler.go",
     "err = DeferFleetSourceWork(retry, db, expected, delay)",
     "_ = retry; _ = expected; _ = delay; err = nil",
     "TestFleetSourceSchedulerDefersInvalidTargetWithoutStarvingRealStop", "invalid source target lost intent or did not back off"),
]
selected = set(sys.argv[1:])
if selected - {case[0] for case in cases}:
    raise SystemExit("Unknown source scheduler control: " + ", ".join(sorted(selected)))
for case in cases:
    name, filename, before, after, selection, expected, *package = case
    if selected and name not in selected:
        continue
    data = (root / filename).read_text()
    if data.count(before) != 1:
        raise SystemExit("Source scheduler mutation target changed: " + name)
    directory = pathlib.Path(tempfile.mkdtemp(prefix="source-scheduler-negative-" + name + "-", dir=work))
    replacement = directory / pathlib.Path(filename).name
    replacement.write_text(data.replace(before, after, 1))
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(root / filename): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-overlay", str(overlay), "-count=1", "-timeout=2m", "-run", "^" + selection + "$", package[0] if package else "./internal/web/service"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or expected.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Source scheduler negative did not reproduce " + name + "; inspect " + str(directory / "result.log"))
    print("Source scheduler safeguard reproduced: " + name, flush=True)
