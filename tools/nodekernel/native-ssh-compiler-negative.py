#!/usr/bin/env python3
"""Check pure native/SSH compiler regressions against removed protections."""
import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path.cwd()
output = pathlib.Path(tempfile.mkdtemp(prefix="native-ssh-compiler-", dir=root / "scratchpad/node-v2"))
cases = [
    ("identity", "ssh_compile.go", "Email: client.ClientID", "Email: client.User.Email", "TestSSHCompilerPreservesLoginsAndUsesStableAccounting", "SSH login identity was coupled"),
    ("asset-scope", "renderer.go", "asset.NodeID != input.NodeID", "asset.NodeID != input.NodeID && false", "TestNativeCompilerRejectsUnresolvedAndUnsupportedState/cross-node_key", "unsafe compile"),
    ("routing", "ssh_compile.go", "if settings.RouteThroughXray || inbound.Egress.BridgeID != \"\" {", "if false && (settings.RouteThroughXray || inbound.Egress.BridgeID != \"\") {", "TestSSHCompilerRequiresApprovedBridgeAndNeverFallsBackDirect", "configured routing requirement was accepted without a bridge"),
    ("host-key", "ssh_compile.go", "if binding.GenerateLocally {", "if binding.GenerateLocally || binding.AssetID == \"\" {", "TestSSHCompilerRequiresExplicitHostIdentityAndAssets", "compiler inferred an unapproved host-key strategy"),
]
for name, filename, before, after, test, failure in cases:
    source = root / "internal/cores/nodeconfig" / filename
    text = source.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f"compiler overlay context changed: {name}")
    replacement, overlay = output / filename, output / f"{name}.json"
    replacement.write_text(text.replace(before, after))
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    command = ["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-race", "-count=1", "-overlay", str(overlay), "-run", f"^{test}$", "./internal/cores/nodeconfig"]
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (output / f"{name}.log").write_text(result.stdout)
    if result.returncode == 0 or failure not in result.stdout:
        raise RuntimeError(f"compiler regression did not detect removed {name} protection: {result.stdout}")
print(f"Native/SSH compiler protections detected by four negative controls: {output}")
