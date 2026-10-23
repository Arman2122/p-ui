#!/usr/bin/env python3
"""Verify native credit regressions detect removed journal protections."""
import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path.cwd()
output = pathlib.Path(tempfile.mkdtemp(prefix="native-credit-", dir=root / "scratchpad/node-v2"))
cases = [
    ("version", "journal.go", " || grant.Policy.CreditRevision < r.Grant.Policy.CreditRevision", "", "TestNativeRejectsCreditVersionRollbackAcrossConfiguration", "new configuration rolled durable credit backward"),
    ("restore", "credit.go", "limits.RetainCredit(grant.Policy, record.Grant.Policy)", "grant.Policy", "TestNativeCreditPreservesLiveSliceAndRestoresRaisedCeiling", "same base lost durable topup"),
]
for name, filename, before, after, test, failure in cases:
    source = root / "internal/node/native" / filename
    text = source.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f"credit overlay context changed: {name}")
    changed = text.replace(before, after)
    replacement, overlay = output / filename, output / f"{name}.json"
    replacement.write_text(changed)
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "test", "-race", "-count=1", "-overlay", str(overlay), "-run", f"^{test}$", "./internal/node/native"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (output / f"{name}.log").write_text(result.stdout)
    if result.returncode == 0 or failure not in result.stdout:
        raise RuntimeError(f"credit regression did not detect removed {name} protection: {result.stdout}")
print(f"Native credit protections detected by two negative controls: {output}")
