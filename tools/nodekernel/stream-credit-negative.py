import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("internal/node/limits/account.go", "\tif a.persistence != nil {\n\t\tif err := a.persistence.StorePolicy(p);", "\tif a.persistence != nil && !CreditAdvance(a.policy, p) {\n\t\tif err := a.persistence.StorePolicy(p);", "./internal/node/governor", "TestCreditTopupSurvivesSameBaseRestoreWithoutReplay", "bad restored authority"),
    ("internal/node/governor/store.go", "grant.Policy = limits.RetainCredit(grant.Policy, a.Snapshot().Policy)", "grant.Policy = grant.Policy", "./internal/node/governor", "TestCreditTopupSurvivesSameBaseRestoreWithoutReplay", "same base restore lost durable topup"),
    ("internal/node/worker/credit.go", "cursors.Desired != cursors.Applied || ", "", "./internal/node/worker", "TestWorkerCreditUpdateFencesJournalsAndPendingConfiguration", "pending configuration allowed credit expansion"),
]
for relative, old, new, package, test, failure in cases:
    source = root / relative
    content = source.read_text(encoding="utf-8")
    if content.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + relative)
    with tempfile.TemporaryDirectory(prefix="credit-negative-", dir=root / "scratchpad/node-v2") as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(content.replace(old, new), encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
        result = subprocess.run(["go", "test", "-count=1", "-timeout=2m", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True, timeout=180)
        output = result.stdout + result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
        print("negative control reproduced: " + test, flush=True)
