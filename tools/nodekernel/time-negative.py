import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("internal/node/limits/account.go", "func (a *Account) allowed(now time.Time, dir Direction, n uint64) error {\n\tif a.clock != nil {", "func (a *Account) allowed(now time.Time, dir Direction, n uint64) error {\n\tif false {", "./internal/node/trustedtime", "TestPersistedGovernorCannotReviveExpiredAuthorityAfterRestart", "expired authority revived"),
    ("internal/node/trustedtime/clock.go", "at.Boot != saved.Boot || ", "", "./internal/node/trustedtime", "TestTimeResponsesCannotReplayRegressOrSurviveAnUnknownBoot", "new boot reused old authority"),
    ("internal/node/trustedtime/clock.go", 'nonce == "" || nonce != c.nonce', 'nonce == ""', "./internal/node/trustedtime", "TestTimeResponsesCannotReplayRegressOrSurviveAnUnknownBoot", "replayed response"),
    ("internal/node/worker/worker.go", "if w.clock != nil && runtime != nil {", "if false {", "./internal/node/worker", "TestRuntimeWaitsForAuthenticatedTimeBeforeRestoringAuthority", "untrusted boot reached engine"),
    ("internal/controlplane/time.go", "return lockAuthenticatedNode(tx, principal)", "return lockNode(tx, principal.NodeID, principal.InstallationID)", "./internal/controlplane", "TestTimeAuthorityRequiresCurrentAuthenticatedNodePostgres", "superseded certificate received time authority"),
]
for relative, old, new, package, test, failure in cases:
    source = root / relative
    content = source.read_text(encoding="utf-8")
    if content.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + relative)
    with tempfile.TemporaryDirectory(prefix="penhoon-time-negative-") as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(content.replace(old, new), encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
        result = subprocess.run(["go", "test", "-count=1", "-timeout=2m", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True)
        output = result.stdout + result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
        print("negative control reproduced: " + test, flush=True)
