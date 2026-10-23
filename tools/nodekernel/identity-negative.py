import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("internal/controlplane/credential_authority.go", "if current.SerialNumber.Text(16) != principal.Serial {", "if current.SerialNumber.Text(16) == \"\" {", "./internal/controlplane", "TestCredentialRecoveryAndRotationPostgres", "superseded certificate renewed a new key"),
    ("internal/node/state/material.go", "previous != nil && !bytes.Equal(previous, digest)", "previous != nil && bytes.Equal(previous, []byte(\"impossible-digest\"))", "./internal/cores/noderuntime/ssh", "TestNodeLocalSSHIdentitySurvivesRestartAndRefusesSilentReplacement", "changed key was silently accepted"),
    ("internal/controlplane/cli.go", "pack, err := host.Enroll(ctx, *name)", "_ = os.WriteFile(*destination, nil, 0o600)\n\t\tpack, err := host.Enroll(ctx, *name)", "./internal/controlplane", "TestFailedEnrollmentCommandDoesNotPublishAnEmptyPackage", "failed enrollment published an output file"),
]
for relative, old, new, package, test, failure in cases:
    source = root / relative
    content = source.read_text(encoding="utf-8")
    if content.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + relative)
    with tempfile.TemporaryDirectory(prefix="penhoon-identity-negative-") as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(content.replace(old, new), encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
        result = subprocess.run(["go", "test", "-count=1", "-timeout=2m", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True)
        output = result.stdout + result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
        print("negative control reproduced: " + test, flush=True)
