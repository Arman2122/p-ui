import argparse
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify source reads.")
root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="source-read-negative-", dir=root / "scratchpad/node-v2"))
source = root / "internal/web/service/fleet_source_read.go"
content = source.read_text()
cases = [
    ("repeatable-snapshot", "Isolation: sql.LevelRepeatableRead", "Isolation: sql.LevelReadCommitted",
     "TestFleetSourceReadRevisionAndFieldsShareSnapshotPostgres", "source read mixed old revision and newer source"),
    ("retained-binding-scope", 'binding.Scope = "retained"', 'binding.Scope = "effective"',
     "TestFleetSourceReadPreservesCommonOverridesAndRetainedScopePostgres", "source read flattened per-binding credentials/flow or relabeled retained scope"),
    ("unknown-membership", 'result.DependencyDiagnostic = "SOURCE_MEMBERSHIP_UNRESOLVED"', 'result.EffectiveScopeKnown = true',
     "TestFleetSourceReadInheritanceAndUnknownScopePostgres", "unresolved source appeared as known empty membership"),
    ("fresh-actor", "if err := requireFleetAdmin(tx, actor); err != nil {", "if err := error(nil); err != nil {",
     "TestFleetSourceReadRejectsActorAndDatabaseDriftPostgres", "stale actor read source credentials"),
    ("post-read-lifecycle", "\tif err == nil {\n\t\tvar current *sql.DB", "\tif false {\n\t\tvar current *sql.DB",
     "TestFleetSourceReadRejectsActorAndDatabaseDriftPostgres", "read released source after DB replacement"),
    ("pending-term", "result.Pending = result.Pending || client.PendingTermID != nil", "",
     "TestFleetSourceReadIncludesPendingTermWithoutNodeWorkPostgres", "pending term without node work appeared complete"),
    ("credential-expansion-bound", "size.Bytes > maxFleetSourceCredentialBytes", "size.Bytes < 0",
     "TestFleetSourceReadRejectsCredentialExpansionBeforeValuesPostgres", "oversize credentials were materialized or partially returned"),
]
parser = argparse.ArgumentParser()
parser.add_argument("--start", choices=[case[0] for case in cases])
arguments = parser.parse_args()
if arguments.start:
    cases = cases[next(index for index, case in enumerate(cases) if case[0] == arguments.start):]
for name, before, after, test, expected in cases:
    if content.count(before) != 1:
        raise SystemExit("Source read negative anchor changed: " + name)
    replacement = output / (name + ".go.txt")
    replacement.write_text(content.replace(before, after))
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-timeout=90s", "-overlay", str(overlay),
                             "-run", "^" + test + "$", "./internal/web/service"], cwd=root, env=dict(os.environ, GOMAXPROCS="2"), capture_output=True, text=True, timeout=240)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log or "[build failed]" in log:
        raise SystemExit("Source read negative did not reproduce: " + name + "; inspect " + str(output / (name + ".log")))
    print("Source read negative reproduced: " + name, flush=True)
print(output)
