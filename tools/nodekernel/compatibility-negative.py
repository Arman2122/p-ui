import json
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="compatibility-negative-", dir=root / "scratchpad/node-v2"))
cases = [
    ("journal-format", "internal/node/state/store.go", "len(version) != 8 || binary.BigEndian.Uint64(version) != uint64(compatibility.StateFormat)", "false && (len(version) != 8 || binary.BigEndian.Uint64(version) != uint64(compatibility.StateFormat))", "./internal/node/state", "TestStoreRefusesIncompatibleFormatWithoutChangingDurableState", "incompatible state reopened"),
    ("release-format", "internal/node/release/manifest.go", " || m.StateFormat != compatibility.StateFormat", "", "./internal/node/release", "TestReleaseRejectsTamperingPlatformAndChangedAssets", "unsupported format 1 verified"),
    ("master-protocol", "internal/controlplane/service.go", "hello.ProtocolVersion != compatibility.Protocol || ", "", "./internal/controlplane", "TestAuthenticatedControlAndAccountingPostgres", "unsupported protocol 0 accepted"),
    ("agent-protocol", "internal/node/agent/agent.go", "if authority.ProtocolVersion != compatibility.Protocol {", "if false && authority.ProtocolVersion != compatibility.Protocol {", "./internal/controlplane", "TestAgentRefusesIncompatibleMasterBeforePersistingAuthorityPostgres/old", "incompatible master response accepted"),
    ("agent-negotiation", "internal/node/agent/agent.go", "if !negotiated {", "if false && !negotiated {", "./internal/controlplane", "TestAgentRefusesIncompatibleMasterBeforePersistingAuthorityPostgres/missing", "incompatible master response accepted"),
    ("trimpath-default", "tools/nodecore/goflags.go", 'return joinGoFlags(append(fields, "-trimpath"))', 'return joinGoFlags(fields)', "./tools/nodecore", "TestXrayTestTrimpathDefaultsAndExplicitOverridesReachBuilds/default", "parent flag changed"),
]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, capture_output=True, text=True, check=True, timeout=120)
modfile = pathlib.Path(prepared.stdout.strip()).resolve()
if modfile.name != "runtime.mod" or modfile.parent.parent != (root / ".cache/node-core").resolve() or not modfile.parent.name.startswith("xray-"):
    raise SystemExit("unexpected temporary Xray module path")
try:
    for name, relative, before, after, package, test, expected in cases:
        source = root / relative
        content = source.read_text()
        if content.count(before) != 1:
            raise SystemExit("compatibility negative anchor changed: " + name)
        changed, overlay = output / (name + ".go"), output / (name + ".json")
        changed.write_text(content.replace(before, after))
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
        command = ["go", "test", "-trimpath", "-race", "-count=1", "-timeout=60s", "-overlay", str(overlay), "-run", "^" + test + "$", package]
        if package == "./internal/controlplane":
            command[2:2] = ["-modfile", str(modfile)]
        result = subprocess.run(command, cwd=root, capture_output=True, text=True, timeout=300)
        log = result.stdout + result.stderr
        (output / (name + ".log")).write_text(log)
        if result.returncode == 0 or expected not in log:
            raise SystemExit("compatibility negative did not reproduce: " + name + "\n" + log)
        print("compatibility negative reproduced: " + name, flush=True)
finally:
    shutil.rmtree(modfile.parent)
print(output)
