#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify fleet statistics.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
snapshot = "internal/web/service/fleet_stats_report.go"
report = "internal/web/service/tgbot/fleet_report.go"
archive = "internal/web/service/tgbot/backup_archive.go"
cases = [
    ("stale-observation", snapshot,
     "return timestamp > 0 && timestamp >= now.Add(-45*time.Second).UnixMilli() && timestamp <= now.Add(5*time.Second).UnixMilli()",
     "return timestamp > 0", "./internal/web/service", "TestFleetStatsDoesNotPresentStaleOrFutureObservationsAsReady", "fleet observation presentation="),
    ("unbounded-retention", snapshot,
     "if !userCandidate && (!near || !client.EnabledInbound) {", "if false && !userCandidate && (!near || !client.EnabledInbound) {",
     "./internal/web/service", "TestFleetStatsKeepsAggregateTotalsWithoutRetainingEveryClient", "fleet statistics exceed bounded report capacity"),
    ("candidate-cap", snapshot,
     "if len(result.Clients) >= fleetStatsCandidateLimit {", "if false && len(result.Clients) >= fleetStatsCandidateLimit {",
     "./internal/web/service", "TestFleetStatsKeepsAggregateTotalsWithoutRetainingEveryClient", "report silently dropped or retained unbounded recipients"),
    ("reservation-as-measured", snapshot,
     "{&result.MeasuredBytes, accounting.MeasuredUploaded}",
     "{&result.MeasuredBytes, accounting.MeasuredUploaded + func() int64 { if accounting.ReservedBytes == nil { return 0 }; return *accounting.ReservedBytes }()}",
     "./internal/web/service", "TestFleetStatsSnapshotUsesLedgerAndBatchesRecipientReadsPostgres", "fleet snapshot cardinality/query count"),
    ("snapshot-isolation", snapshot,
     "Isolation: sql.LevelRepeatableRead", "Isolation: sql.LevelReadCommitted",
     "./internal/web/service", "TestFleetStatsSnapshotDoesNotReadPartialTermStatePostgres", "read-only snapshot mixed transactions"),
    ("nested-transaction", snapshot,
     "if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {", "if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested && false {",
     "./internal/web/service", "TestFleetStatsRejectsNestedAndReplacedDatabaseHandlesPostgres", "nested transaction lost snapshot options"),
    ("unknown-not-zero", report,
     "if amount == nil {\n\t\treturn t.I18nBot(\"tgbot.messages.fleetUnknown\")", "if amount == nil {\n\t\treturn common.FormatTraffic(0)",
     "./internal/web/service/tgbot", "TestFleetStatsReportKeepsMeasuredOpeningAndUnknownSeparate", "report omitted"),
    ("admin-recipient-dedup", report,
     "&& !slices.Contains(admins, client.TelegramID)", "",
     "./internal/web/service/tgbot", "TestFleetStatsFakeDeliveryPreservesRecipientsAndBackupScope", "administrator report/backup scope ambiguous"),
    ("send-lifecycle", report,
     "if err := check(); err != nil {", "if err := error(nil); err != nil {",
     "./internal/web/service/tgbot", "TestFleetStatsStopsEmissionWhenLifecycleOrDatabaseChanges", "stale lifecycle emitted later report/backup"),
    ("archive-lifecycle", archive,
     "if check != nil {", "if check != nil && false {",
     "./internal/web/service/tgbot", "TestFleetStatsBackupRechecksDatabaseBeforeEachRecipient", "stale backup sent to later recipient"),
]
if sys.platform == "linux":
    cases.append(("dump-lifecycle", "internal/web/service/server.go",
                  "context.WithTimeout(parent, pgDumpTimeout)", "context.WithTimeout(context.Background(), pgDumpTimeout)",
                  "./internal/web/service", "TestFleetStatsDumpStopsWithLifecycle", "fleet dump outlived host lifecycle"))
for name, filename, before, after, package, selection, evidence in cases:
    source = (root / filename).read_text()
    expected = 5 if name == "send-lifecycle" else 1
    if source.count(before) != expected:
        raise SystemExit("Mutation target changed: " + name + " count=" + str(source.count(before)))
    destination = pathlib.Path(tempfile.mkdtemp(prefix="stats-negative-" + name + "-", dir=work))
    replacement = destination / pathlib.Path(filename).name
    replacement.write_text(source.replace(before, after) if name == "send-lifecycle" else source.replace(before, after, 1))
    overlay = destination / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(root / filename): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-overlay", str(overlay),
                             package, "-run", "^" + selection + "$", "-count=1", "-timeout=3m"],
                            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (destination / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or evidence.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Negative control did not detect " + name + "; inspect " + str(destination / "result.log"))
    print("Detected removed safeguard: " + name, flush=True)
