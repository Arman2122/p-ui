#!/usr/bin/env python3
"""Verify native network transaction and ownership regression sensitivity."""
import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path.cwd()
output = pathlib.Path(tempfile.mkdtemp(prefix="native-network-", dir=root / "scratchpad/node-v2"))
cases = [
    ("active", "network.go", "if record.Lease != nil || record.SelectorsPending {", "if false && (record.Lease != nil || record.SelectorsPending) {", "TestNetworkBindingRequiresQuiescenceAndPersistsPendingRecovery", "active authority allowed network rebinding", False),
    ("pending", "network.go", "state.Pending = &target", "state.Current = &target", "TestNetworkBindingRequiresQuiescenceAndPersistsPendingRecovery", "pending network allowed forwarding funding", False),
    ("neighbor", "network_linux.go", "!bytes.Equal(neighbor.HardwareAddr, in.Attrs().HardwareAddr)", "(!bytes.Equal(neighbor.HardwareAddr, in.Attrs().HardwareAddr) && false)", "TestNativeBindingRecoveryIsolated", "preflight accepted conflicting next-hop neighbor", True),
]
for name, filename, before, after, test, failure, kernel in cases:
    source = root / "internal/node/native" / filename
    text = source.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f"network overlay context changed: {name}")
    replacement, overlay = output / filename, output / f"{name}.json"
    replacement.write_text(text.replace(before, after))
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    command = ["go", "test", "-race", "-count=1", "-overlay", str(overlay), "-run", f"^{test}$", "./internal/node/native"]
    if kernel:
        command = ["unshare", "--net", "--mount", "--propagation", "private", "env", "PENHOON_NATIVE_BINDING_RECOVERY_TEST=1", *command]
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (output / f"{name}.log").write_text(result.stdout)
    if result.returncode == 0 or failure not in result.stdout:
        raise RuntimeError(f"network regression did not detect removed {name} protection: {result.stdout}")
print(f"Native network protections detected by three negative controls: {output}")
