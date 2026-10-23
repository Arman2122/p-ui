import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("internal/node/worker/worker.go", "w.restoreFailed.Store(true)", "w.restoreFailed.Store(false)\n\t\t\tw.available.Store(true)", "./internal/node/worker", "TestRejectedRestoreKeepsControlAvailable", "failed restore advertised ready"),
    ("internal/controlplane/observations.go", "if uint64(node.AuthorityGeneration) != generation {", "if false {", "./internal/controlplane", "TestObservationsAreFencedAndCannotAcknowledgeDeployment", "superseded session observation"),
    ("internal/web/service/fleet.go", "observed.RuntimeGeneration == node.AuthorityGeneration && ", "", "./internal/web/service", "TestFleetStatusSeparatesConnectivityFromCurrentRuntimeObservations", "want connected/rejected/stale"),
]
for relative, old, new, package, test, failure in cases:
    source = root / relative
    text = source.read_text(encoding="utf-8")
    if text.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + relative)
    with tempfile.TemporaryDirectory(prefix="penhoon-observation-negative-") as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(text.replace(old, new), encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
        result = subprocess.run(["go", "test", "-count=1", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True)
        output = result.stdout + result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
        print("negative control reproduced: " + test)
