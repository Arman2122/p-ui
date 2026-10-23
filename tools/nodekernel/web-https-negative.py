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
output = pathlib.Path(tempfile.mkdtemp(prefix="web-https-", dir=root / "scratchpad/node-v2"))
cases = [
    ("public-port", module.parent / "source/internal/webrelay/origin.go", "page.NewWithPort(host, opts.PublicPort)", "page.New(host)", "TestDirectWEBTLSRotationPreservesNativeAndCarriers", "WEB bootstrap or CSP lost configured public authority"),
    ("rotation", runtime / "managed.go", 'web.ServerCert, web.ServerKey = "", ""', "web.ServerCert, web.ServerKey = structure.WEB.ServerCert, structure.WEB.ServerKey", "TestDirectWEBTLSRotationPreservesNativeAndCarriers", "certificate rotation restarted native or WEB sessions"),
    ("forwarded", runtime / "web.go", "address, err := netip.ParseAddrPort(request.RemoteAddr)", 'remote := request.RemoteAddr\n\tif value := request.Header.Get("X-Forwarded-For"); value != "" { remote = net.JoinHostPort(value,"40123") }\n\taddress, err := netip.ParseAddrPort(remote)', "TestDirectWEBTLSUsesSocketAddressDespiteForwardedHeaders", "direct WEB trusted forwarded IP headers"),
    ("proxy-prefix", runtime / "web.go", "served = tls.NewListener(web.listener,", "served = tls.NewListener(served,", "TestDirectWEBTLSRejectsProxyPrefix", "direct TLS accepted a PROXY-protocol prefix"),
    ("quota", runtime / "runtime.go", "return mtglib.NewProxy(options)", "options.GovernStream = nil\n\treturn mtglib.NewProxy(options)", "TestDirectWEBTLSAndNativeShareQuotaAndRate/quota", "HTTPS bypassed native quota"),
    ("cleanup", runtime / "web_transport.go", "for connection := range l.connections {", "for connection := range map[*trackedConnection]bool{} {", "TestDirectWEBTLSRotationPreservesNativeAndCarriers", "direct HTTPS shutdown left a carrier alive"),
    ("hostname", root / "internal/node/configuration/mtproto_tls.go", "leaf.VerifyHostname(w.Host) != nil", "leaf.VerifyHostname(w.Host) != nil && false", "TestDirectWEBTLSRotationPreservesNativeAndCarriers", "mismatched hostname certificate accepted"),
]
try:
    for name, source, before, after, test, failure in cases:
        text = source.read_text()
        if text.count(before) != 1:
            raise RuntimeError(f"HTTPS overlay context changed: {name}")
        replacement, overlay = output / f"{name}.go", output / f"{name}.json"
        replacement.write_text(text.replace(before, after))
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
        command = ["unshare", "--net", "--mount", "--propagation", "private", "env", "PENHOON_WEB_HTTPS_PUBLIC=1", "go", "test", "-race", "-count=1", "-timeout=1m", "-modfile", str(module), "-overlay", str(overlay), "-run", f"^{test}$", "."]
        result = subprocess.run(command, cwd=runtime, env=os.environ, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        (output / f"{name}.log").write_text(result.stdout)
        if result.returncode == 0 or failure not in result.stdout:
            raise RuntimeError(f"HTTPS protection not detected when removed: {name}\n{result.stdout}")
        print(f"HTTPS negative control reproduced: {name}", flush=True)
finally:
    shutil.rmtree(module.parent)
print(f"Direct HTTPS WEB qualified with seven negative controls: {output}")
