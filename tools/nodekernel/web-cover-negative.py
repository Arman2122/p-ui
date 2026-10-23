import json
import os
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "mtproto", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache/node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("mtproto-"):
    raise SystemExit("unexpected prepared module location")
runtime = root / "core-runtimes/mtproto"
output = pathlib.Path(tempfile.mkdtemp(prefix="web-cover-", dir=root / "scratchpad/node-v2"))
source = runtime / "web.go"
cases = [
    ("routed-network", "return effectiveNetwork.DialContext(dial, protocol, address)", "return (&net.Dialer{}).DialContext(dial, protocol, address)", "TestWEBRoutedCoverUsesNodeSOCKSAndClosesIdleConnections", "cover bypassed configured node egress"),
    ("environment-proxy", "Proxy: nil,", "Proxy: http.ProxyFromEnvironment,", "TestWEBRoutedCoverUsesNodeSOCKSAndClosesIdleConnections", "cover bypassed configured node egress"),
    ("request-cancel", "request.Clone(ctx)", "request.Clone(context.WithValue(request.Context(), webCoverContext{}, ctx.Done()))", "TestWEBCoverCancellationStopsRoutedDial", "cover request cancellation did not stop routed dial"),
    ("idle-cleanup", "w.coverTransport.CloseIdleConnections()", "_ = w.coverTransport", "TestWEBRoutedCoverUsesNodeSOCKSAndClosesIdleConnections", "WEB shutdown leaked its upstream idle connection"),
]
try:
    for name, before, after, test, failure in cases:
        text = source.read_text()
        if text.count(before) != 1:
            raise RuntimeError(f"cover overlay context changed: {name}")
        replacement, overlay = output / f"{name}.go", output / f"{name}.json"
        replacement.write_text(text.replace(before, after))
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
        command = ["unshare", "--net", "--mount", "--propagation", "private", "env", "PENHOON_WEB_HTTPS_PUBLIC=1", "go", "test", "-race", "-count=1", "-timeout=1m", "-modfile", str(module), "-overlay", str(overlay), "-run", f"^{test}$", "."]
        result = subprocess.run(command, cwd=runtime, env=os.environ, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        (output / f"{name}.log").write_text(result.stdout)
        if result.returncode == 0 or failure not in result.stdout:
            raise RuntimeError(f"cover protection not detected when removed: {name}\n{result.stdout}")
        print(f"cover negative control reproduced: {name}", flush=True)
finally:
    shutil.rmtree(module.parent)
print(f"Routed WEB cover qualified with four negative controls: {output}")
