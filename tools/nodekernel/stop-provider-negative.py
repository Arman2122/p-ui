import json
import os
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "scratchpad" / "node-v2" / "stop-provider-negative"
OUTPUT.mkdir(parents=True, exist_ok=True)
SOURCE = "internal/controlplane/stop_provider_provenance.go"
CASES = [
    ("carry", "internal/controlplane/stop_provenance.go",
     '\tif err := carryStopProviderBindings(tx, principal, compilation, base, revision); err != nil {\n\t\treturn err\n\t}\n', '',
     "^TestStopConfigurationCarriesFrozenProviderBindingsPostgres$", "record not found"),
    ("known-scope", SOURCE, ' || !row.ScopeKnown', '',
     "^TestStopConfigurationRefusesUnprovenProviderBindingsPostgres$/^unknown$", "unproven provider scope accepted"),
    ("cache-pin", SOURCE, ' || hex.EncodeToString(row.ProviderDigest) != pin.SHA256', ' || false && hex.EncodeToString(row.ProviderDigest) != pin.SHA256',
     "^TestStopConfigurationRefusesUnprovenProviderBindingsPostgres$/^changed_cache$", "unproven provider scope accepted"),
    ("pending-wake", SOURCE, 'row.DeploymentRevision, row.Pending, row.ReadyUnixMilli = revision, true, 0',
     'row.DeploymentRevision, row.Pending, row.ReadyUnixMilli = revision, true, 123',
     "^TestStopConfigurationCarriesFrozenProviderBindingsPostgres$", "stop changed frozen provider scope"),
    ("stale-diagnostic", SOURCE, '\t\trow.Diagnostic = ""\n', '',
     "^TestStopConfigurationCarriesFrozenProviderBindingsPostgres$", "stop changed frozen provider scope"),
    ("own-projection", SOURCE, 'if !bytes.Equal(index.PayloadDigest, projected[:]) {',
     'if false && !bytes.Equal(index.PayloadDigest, projected[:]) {',
     "^TestStopProviderCarryRequiresFrozenGraphAndFreshProjectionPostgres$/^stale_projection$", "provider scope stale projection was carried"),
    ("frozen-graph", SOURCE, 'if !bytes.Equal(graph, after[id]) {', 'if false && !bytes.Equal(graph, after[id]) {',
     "^TestStopProviderCarryRequiresFrozenGraphAndFreshProjectionPostgres$/^changed_graph$", "provider scope changed graph was carried"),
]


def main():
    if not os.environ.get("PUI_DB_DSN"):
        raise RuntimeError("disposable PostgreSQL DSN required")
    environment = {**os.environ, "GOMAXPROCS": "2"}
    prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"],
                              cwd=ROOT, env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    module = pathlib.Path(prepared.stdout.strip()).resolve()
    module.relative_to((ROOT / ".cache" / "node-core").resolve())
    if module.name != "runtime.mod" or not module.is_file():
        raise RuntimeError("unexpected patched module")
    for name, relative, before, after, selector, failure in CASES:
        if len(sys.argv) > 1 and name not in sys.argv[1:]:
            continue
        source = ROOT / relative
        original = source.read_text(encoding="utf-8")
        if original.count(before) != 1:
            raise RuntimeError(f"{name}: expected exactly one guard")
        with tempfile.TemporaryDirectory(prefix=f"{name}-", dir=OUTPUT) as temporary:
            directory = pathlib.Path(temporary).resolve()
            directory.relative_to(OUTPUT.resolve())
            mutated = directory / source.name
            mutated.write_text(original.replace(before, after, 1), encoding="utf-8")
            overlay = directory / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}), encoding="utf-8")
            command = ["go", "test", "-p=1", "-modfile", str(module), "-overlay", str(overlay), "-count=1",
                       "-timeout=90s", "-run", selector, "./internal/controlplane"]
            result = subprocess.run(command, cwd=ROOT, env=environment, text=True,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
            (OUTPUT / f"{name}.log").write_text(result.stdout, encoding="utf-8")
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or failure not in result.stdout or "[build failed]" in result.stdout:
                raise RuntimeError(f"{name}: removed safeguard did not fail the intended assertion; see its log")
            if source.read_text(encoding="utf-8") != original:
                raise RuntimeError(f"{name}: production source changed during qualification")
            print(f"PASS {name}: removed safeguard rejected", flush=True)


if __name__ == "__main__":
    main()

