import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
cases = [
    ("agent-receipt", "internal/node/agent/local.go", "if manifest != nil && response.Header.Get(configuration.AssetManifestHeader) != hex.EncodeToString(manifest) {", "if false && manifest != nil && response.Header.Get(configuration.AssetManifestHeader) != hex.EncodeToString(manifest) {", "./internal/node/agent", "TestLocalApplyRequiresExactAssetReceiptIncludingEmptyManifest", "unproven worker files were acknowledged"),
    ("master-receipt", "internal/controlplane/deployment.go", "if !bytes.Equal(result.AssetManifestSha256, configuration.PayloadAssetManifest(deployment.Payload)) {", "if false && !bytes.Equal(result.AssetManifestSha256, configuration.PayloadAssetManifest(deployment.Payload)) {", "./internal/controlplane", "TestAppliedManifestMustMatchDesiredFilesPostgres", "incorrect manifest accepted"),
    ("partial-retention", "internal/node/worker/assets.go", "keep := slices.Clone(w.activeAssets)", "keep := []string{}", "./internal/node/worker", "TestAssetCleanupRetainsPartiallyActiveConfigurationAcrossFailures", "partially active material deleted"),
    ("uncertainty-bound", "internal/node/worker/assets.go", "if len(digests) > 2*configuration.MaxRuntimeFileAssets {", "if false && len(digests) > 2*configuration.MaxRuntimeFileAssets {", "./internal/node/worker", "TestAssetUncertaintyBoundRefusesBeforeDesiredChange", "uncertain material bound"),
    ("unused-material", "internal/node/worker/assets.go", "if len(used) != len(paths) {", "if false && len(used) != len(paths) {", "./internal/node/worker", "TestSSHFileAssetsValidateBeforeActivationAndSurviveRestart/unused", "invalid candidate activated"),
    ("prevalidated-pair", "internal/cores/noderuntime/ssh/assets.go", "if _, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile); err != nil {", "if _, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile); false && err != nil {", "./internal/node/worker", "TestSSHFileAssetsValidateBeforeActivationAndSurviveRestart/mismatched_pair", "invalid candidate changed durable state"),
    ("file-content", "internal/node/configuration/assets.go", "if hex.EncodeToString(digest[:]) != asset.SHA256 {", "if false && hex.EncodeToString(digest[:]) != asset.SHA256 {", "./internal/node/configuration", "TestDecodeRejectsInvalidOrUnboundedFileAssets/hash", "accepted hash"),
    ("bundle-bound", "internal/node/configuration/config.go", "if assetBytes > MaxBundleFileBytes {", "if false && assetBytes > MaxBundleFileBytes {", "./internal/node/configuration", "TestDecodeRejectsInvalidOrUnboundedFileAssets/bundle_size", "accepted bundle size"),
]
output = pathlib.Path(tempfile.mkdtemp(prefix="asset-integration-", dir=root / "scratchpad/node-v2"))
for name, relative, before, after, package, test, failure in cases:
    if len(sys.argv) > 1 and name not in sys.argv[1:]:
        continue
    source = root / relative
    content = source.read_text()
    if content.count(before) != 1:
        raise SystemExit("asset negative anchor changed: " + name)
    changed, overlay = output / (name + ".go"), output / (name + ".json")
    changed.write_text(content.replace(before, after))
    overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
    command = ["go", "test", "-trimpath", "-count=1", "-race", "-timeout=60s", "-overlay", str(overlay), "-run", "^" + test + "$", package]
    stage = None
    if package == "./internal/controlplane":
        prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, capture_output=True, text=True, check=True, timeout=120)
        modfile = pathlib.Path(prepared.stdout.strip()).resolve()
        if modfile.name != "runtime.mod" or modfile.parent.parent != (root / ".cache/node-core").resolve() or not modfile.parent.name.startswith("xray-"):
            raise SystemExit("unexpected temporary Xray module path")
        stage = modfile.parent
        command[2:2] = ["-modfile", str(modfile)]
    try:
        result = subprocess.run(command, cwd=root, capture_output=True, text=True, timeout=300)
    finally:
        if stage is not None:
            shutil.rmtree(stage)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or failure not in log:
        raise SystemExit("asset negative did not reproduce: " + name + "\n" + log)
    print("asset integration negative reproduced: " + name, flush=True)
print(output)
