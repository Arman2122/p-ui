#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify reset scheduling.")
root = pathlib.Path.cwd().resolve()
source = root / "internal/web/service/fleet_reset_schedule.go"
data = source.read_text()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
cases = [
    ("monthly-anchor", "min(start.Day(), last)", "min(cursor.Day(), last)",
     "TestFleetResetCadenceKeepsMonthlyAnchorAndSkipsMissedSlots", "anchored reset="),
    ("atomic-policy-receipt", "db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {",
     "func(apply func(*gorm.DB) error) error { return apply(db.WithContext(ctx)) }(func(tx *gorm.DB) error {",
     "TestFleetResetScheduleRejectsPendingTransitionAndRollsBackReceiptPostgres", "failed schedule left operation receipt="),
    ("schedule-revision", "if uint64(schedule.Revision) != request.ExpectedRevision {",
     "if uint64(schedule.Revision) != request.ExpectedRevision && false {",
     "TestFleetResetScheduleConfigurationIsIdempotentAndDoesNotResetAllowancePostgres", "stale schedule revision="),
    ("pending-transition", "schedule.PendingOperationID != nil || client.PendingTermID != nil",
     "schedule.PendingOperationID != nil",
     "TestFleetResetScheduleRejectsPendingTransitionAndRollsBackReceiptPostgres", "schedule edit erased pending transition="),
]
for name, before, after, selection, expected in cases:
    if sys.argv[1:] and name not in sys.argv[1:]:
        continue
    if data.count(before) != 1:
        raise SystemExit("Reset schedule mutation changed: " + name)
    directory = pathlib.Path(tempfile.mkdtemp(prefix="reset-schedule-negative-" + name + "-", dir=work))
    replacement = directory / source.name
    replacement.write_text(data.replace(before, after, 1))
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-overlay", str(overlay), "-count=1", "-timeout=3m", "-run", "^" + selection + "$", "./internal/web/service"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or expected.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Reset schedule negative failed to reproduce " + name + "; inspect " + str(directory / "result.log"))
    print("Reset schedule safeguard reproduced: " + name, flush=True)
