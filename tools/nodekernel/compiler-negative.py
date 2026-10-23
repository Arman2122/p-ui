import json
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache/node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("xray-"):
    raise SystemExit("unexpected prepared module location")
cases = [
    ("internal/cores/nodeconfig/singbox_compile.go", "if err := plainSingBoxBase(base); err != nil {", "if err := plainSingBoxBase(base); err != nil && len(base) == 0 {", "./internal/cores/nodeconfig", "TestSingBoxCompilerRefusesUnqualifiedBaseConfiguration", "unqualified base accepted"),
    ("internal/cores/internal/xray/enforcement/identity.go", "copyUser.Email = client", "copyUser.Email = inbound.User.Email", "./internal/cores/nodeconfig", "TestCompiledXrayPreservesCredentialsAndCanonicalRouting", "compiled client/routing failed"),
    ("internal/cores/nodeconfig/xray_selectors.go", "selected[i] = resolved", "selected[i] = value", "./internal/cores/nodeconfig", "TestCompiledXrayPreservesCredentialsAndCanonicalRouting", "compiled client/routing failed"),
    ("internal/cores/nodeconfig/mtproto_compile.go", "if source.ProxyProtocolListener {", "if false {", "./internal/cores/nodeconfig", "TestMTProtoCompilerUsesOnlyAssignedSecretsAndExplicitProxyTrust", "implicit proxy trust accepted"),
    ("internal/node/worker/time.go", "if needsTime && !w.available.Load() {", "if !w.available.Load() && (needsTime || w.clock != nil) {", "./internal/node/worker", "TestRuntimeWaitsForAuthenticatedTimeBeforeRestoringAuthority", "time refresh retried a rejected configuration"),
]
try:
    for relative, old, new, package, test, failure in cases:
        source = root / relative
        content = source.read_text(encoding="utf-8")
        if content.count(old) != 1:
            raise SystemExit("negative control anchor changed: " + relative)
        with tempfile.TemporaryDirectory(prefix="compiler-negative-", dir=cache) as directory:
            changed = pathlib.Path(directory) / source.name
            changed.write_text(content.replace(old, new), encoding="utf-8")
            overlay = pathlib.Path(directory) / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
            result = subprocess.run(["go", "test", "-count=1", "-timeout=60s", "-modfile", str(module), "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True)
            output = result.stdout+result.stderr
            if result.returncode == 0 or failure not in output:
                raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
            print("negative control reproduced: " + test, flush=True)
finally:
    shutil.rmtree(module.parent)
