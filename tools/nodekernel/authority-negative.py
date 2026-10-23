import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("internal/node/state/runtime_journal.go", "previous != nil && !bytes.Equal(previous, encoded)", "previous != nil && bytes.Equal(previous, []byte(\"impossible\"))", "./internal/node/state", "TestRuntimeJournalPairRejectsCompleteWorkerStateReplacement", "replacement journals accepted"),
    ("internal/node/governor/authority.go", "BurnedHighWater: snapshot.PreviouslyReserved", "BurnedHighWater: 0", "./internal/node/governor", "TestAuthorityDistinguishesReservedAdmittedBurnedAndMeasured", "restart authority misrepresented"),
    ("internal/controlplane/journal_registry.go", "func RequireRuntimeJournalTx(tx *gorm.DB, nodeID, installationID, runtimeID string) error {", "func RequireRuntimeJournalTx(tx *gorm.DB, nodeID, installationID, runtimeID string) error {\n return nil", "./internal/controlplane", "TestFundingRequiresPriorRuntimeJournalBindingPostgres", "unbound funding="),
    ("internal/controlplane/authority_report.go", "if report.Sequence == uint64(journal.Sequence) {", "if report.Sequence == uint64(journal.Sequence) {\n if err := tx.Model(&journal).Update(\"received_unix_milli\", time.Now().UnixMilli()).Error; err != nil { return err }", "./internal/controlplane", "TestAuthorityReportIsIdempotentScopedAndNeverBillablePostgres", "replay refreshed evidence"),
    ("internal/node/worker/authority.go", "request.Header.Get(\"X-Pui-State-Journal\") == journal.StateJournalId && request.Header.Get(\"X-Pui-Credit-Journal\") == journal.CreditJournalId", "request.Header.Get(\"X-Pui-State-Journal\") != \"\" && journal.CreditJournalId != \"\"", "./internal/node/agent", "TestWorkerJournalReplacementCannotReceiveDeployment/between_identity_and_apply", "replacement received deployment"),
]
for relative, old, new, package, test, failure in cases:
    source = root / relative
    content = source.read_text(encoding="utf-8")
    if content.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + relative)
    with tempfile.TemporaryDirectory(prefix="authority-negative-", dir=root / "scratchpad/node-v2") as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(content.replace(old, new), encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
        result = subprocess.run(["go", "test", "-count=1", "-timeout=2m", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True, timeout=180)
        output = result.stdout + result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
        print("negative control reproduced: " + test, flush=True)
