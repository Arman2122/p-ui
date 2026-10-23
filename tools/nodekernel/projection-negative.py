import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
source = root / "internal/node/agent/local.go"
original = source.read_text()
output = pathlib.Path(tempfile.mkdtemp(prefix="projection-negative-", dir=root / "scratchpad/node-v2"))
for name, before, after, expected in [
    ("private-projection", 'projected[i], journals[i], manifest', 'deployment, journals[i], manifest', "private worker journal contains another runtime"),
    ("master-digest", 'if !bytes.Equal(masterDigest[:], deployment.PayloadSha256) {', 'if false && !bytes.Equal(masterDigest[:], deployment.PayloadSha256) {', "projection accepted an invalid master digest"),
    ("authority-scope", 'AuthorityGeneration: deployment.AuthorityGeneration', 'AuthorityGeneration: 1', "projected hash or authority scope changed"),
]:
    if original.count(before) != 1:
        raise SystemExit("projection negative anchor changed: " + name)
    changed, overlay = output / (name + ".go"), output / (name + ".json")
    changed.write_text(original.replace(before, after))
    overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
    result = subprocess.run(["go", "test", "-trimpath", "-race", "-count=1", "-timeout=30s", "-overlay", str(overlay), "-run", "^TestRuntimeProjectionKeepsOtherCoreSecretsOutOfPrivateJournals$", "./internal/node/agent"], cwd=root, capture_output=True, text=True, timeout=90)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log:
        raise SystemExit("projection negative did not reproduce: " + name + "\n" + log)
    print("projection negative reproduced: " + name, flush=True)
print(output)
