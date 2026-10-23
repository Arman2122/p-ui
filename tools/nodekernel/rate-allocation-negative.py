import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
source = root / "internal/controlplane/rate_allocation.go"
content = source.read_text(encoding="utf-8")
cases = [
    ("candidate := total", "candidate := rateTotals{}; candidate.add(current[i])", "TestSharedRateAllocatorWaitsForActualReleaseAcrossNodes", "unconfirmed reduction reused="),
    (" && s.high == 0", "", "TestRateAllocationDoesNotWrapOutstandingCapacity", "wrapped outstanding rate created capacity="),
    ("if minimum > burst {", "if minimum > burst && len(eligible) == 0 {", "TestSharedRateAllocatorPreservesPacketGranularityAndUnlimitedSemantics", "insufficient packet burst silently rounded upward="),
]
for old, new, test, failure in cases:
    if content.count(old) != 1:
        raise SystemExit("negative control anchor changed: " + test)
    with tempfile.TemporaryDirectory(prefix="rate-allocation-negative-", dir=root / "scratchpad/node-v2") as directory:
        changed = pathlib.Path(directory) / "rate_allocation.go"
        changed.write_text(content.replace(old,new),encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace":{str(source):str(changed)}}))
        result = subprocess.run(["go","test","-p=1","-count=1","-timeout=60s","-overlay",str(overlay),"-run","^"+test+"$","./internal/controlplane"],cwd=root,capture_output=True,text=True)
        output = result.stdout+result.stderr
        if result.returncode == 0 or failure not in output:
            raise SystemExit("negative control did not fail as expected: "+test+"\n"+output)
        print("negative control reproduced: "+test,flush=True)
