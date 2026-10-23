import json
import os
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
environment = dict(os.environ)
environment.pop("GOFLAGS", None)
spec = json.loads((root / "core-runtimes/xray/patches/concurrency.json").read_text())
upstream = json.loads(subprocess.check_output(["go", "list", "-m", "-json", spec["Module"]], cwd=root, env=environment))
if upstream["Version"] != spec["Version"] or upstream.get("Replace"):
    raise SystemExit("unreviewed upstream module")
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, env=environment, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache/node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("xray-"):
    raise SystemExit("unexpected prepared module location")
patched = json.loads(subprocess.check_output(["go", "list", "-modfile", str(module), "-m", "-json", spec["Module"]], cwd=root, env=environment))
source = pathlib.Path(patched["Dir"]).resolve()
if source.parent != module.parent:
    raise SystemExit("unexpected patched module location")
cases = [
    ("common/singbridge/packet.go", "^TestXrayProtocolQuotaAtAuthenticatedDispatch$/shadowsocks-2022/udp", "\nconst PenhoonPatchLevel = 5\n", "WARNING: DATA RACE"),
    ("transport/internet/hysteria/conn.go", "^TestXrayProtocolQuotaAtAuthenticatedDispatch$/hysteria2", "", "WARNING: DATA RACE"),
    ("proxy/vless/inbound/inbound.go", "^TestVLESSVisionTLS13Interoperability$/transfer", "", "fatal error: checkptr"),
    ("proxy/vless/outbound/outbound.go", "^TestVLESSVisionTLS13Interoperability$/transfer", "", "fatal error: checkptr"),
]
try:
    for relative, selection, marker, failure in cases:
        original = pathlib.Path(upstream["Dir"]) / relative
        with tempfile.TemporaryDirectory(prefix="xray-negative-", dir=cache) as directory:
            changed = pathlib.Path(directory) / original.name
            changed.write_text(original.read_text(encoding="utf-8") + marker, encoding="utf-8")
            overlay = pathlib.Path(directory) / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source / relative): str(changed)}}))
            result = subprocess.run(["go", "test", "-race", "-count=3", "-timeout=90s", "-modfile", str(module), "-overlay", str(overlay), "-run", selection, "./internal/cores/internal/xray/enforcement"], cwd=root, env=environment, capture_output=True, text=True)
            output = result.stdout+result.stderr
            if result.returncode == 0 or failure not in output or original.name not in output:
                raise SystemExit("upstream failure was not reproduced: "+relative+"\n"+output)
            print("negative control reproduced upstream failure: "+relative, flush=True)
finally:
    shutil.rmtree(module.parent)
