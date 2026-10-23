import argparse
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify network approval.")
root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="network-approval-negative-", dir=root / "scratchpad/node-v2"))
service = "internal/web/service/"
cases = [
    ("explicit-network-approval", "fleet_source_enable.go",
     "if err := requireAppliedFleetNetworkTx(tx, state); err != nil {", "if err := error(nil); err != nil {",
     "TestFleetSourceInitialNetworkRequiresExplicitApprovalPostgres", "unpublished network was implicitly approved"),
    ("held-materialization-commit", "fleet_source_enable.go",
     "return setFleetSourceApprovalHoldTx(tx, work, true)", "return ErrFleetNetworkApproval",
     "TestFleetSourceDisabledEnableCanMaterializeThenExplicitlyPublishWithoutFundingPostgres", "disabled enable published draft"),
    ("superseded-hold-clear", "fleet_source_mutation.go",
     '\n\t\t\t"diagnostic": "",', '',
     "TestFleetSourceDraftHoldDoesNotDelayStopAndCannotSurviveSupersessionPostgres", "fresh stop inherited publication hold"),
    ("applied-hold-clear", "fleet_source_work.go",
     'work.Diagnostic = ""', 'work.Diagnostic = work.Diagnostic',
     "TestFleetSourceDisabledEnableCanMaterializeThenExplicitlyPublishWithoutFundingPostgres", "actual APPLIED did not clear hold"),
    ("bounded-hold-backoff", "fleet_network_approval.go",
     'time.Now().Add(time.Second).UnixMilli()', 'time.Now().UnixMilli()',
     "TestFleetSourceDraftHoldDoesNotDelayStopAndCannotSurviveSupersessionPostgres", "draft lacked bounded pending hold"),
]
environment = dict(os.environ, GOMAXPROCS="2")
parser = argparse.ArgumentParser()
parser.add_argument("--start", choices=[case[0] for case in cases])
arguments = parser.parse_args()
if arguments.start:
    cases = cases[next(index for index, case in enumerate(cases) if case[0] == arguments.start):]
for name, relative, before, after, test, expected in cases:
    source = root / (service + relative)
    content = source.read_text()
    if content.count(before) != 1:
        raise SystemExit("Network approval negative anchor changed: " + name)
    replacement = output / (name + ".go.txt")
    replacement.write_text(content.replace(before, after))
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-timeout=90s", "-overlay", str(overlay),
                             "-run", "^" + test + "$", "./internal/web/service"], cwd=root, env=environment, capture_output=True, text=True, timeout=240)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log or "[build failed]" in log:
        raise SystemExit("Network approval negative did not reproduce: " + name + "; inspect " + str(output / (name + ".log")))
    print("Network approval negative reproduced: " + name, flush=True)
print(output)
