import json
import os
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "mtproto", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache" / "node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("mtproto-"):
    raise SystemExit("unexpected prepared module location")

runtime = root / "core-runtimes" / "mtproto"
cases = [
    (runtime / "runtime.go", "return mtglib.NewProxy(options)", "options.GovernStream = nil\n\treturn mtglib.NewProxy(options)", "TestWEBAndNativeMTProtoShareOneQuota", ("WEB quota leaked bytes",)),
    (runtime / "runtime.go", "return mtglib.NewProxy(options)", "options.GovernStream = nil\n\treturn mtglib.NewProxy(options)", "TestWEBAndNativeMTProtoShareOneRate", ("WEB/native aggregate rate exceeded", "WEB/native upload=")),
    (runtime / "web_transport.go", "for connection := range l.connections {", "for connection := range map[*trackedConnection]bool{} {", "TestWEBListenerFailureAndShutdownCloseCarriers", ("WEB shutdown left a carrier alive",)),
    (module.parent / "source" / "mtglib" / "proxy_serve.go", "if ctx.Err() != nil || client == nil || telegram == nil {", "if false {", "TestShutdownDuringSuccessfulDialCannotRelayNilConnection", ("invalid memory address", "canceled dial entered relay")),
    (runtime / "managed.go", "existing.structure == digest && !existing.exited.Load() && (existing.web == nil || !existing.web.exited.Load())", "existing.structure == digest", "TestManagedWEBHotRotationAndDeadListenerRepair", ("same deployment acknowledged a dead WEB listener",)),
    (runtime / "managed.go", "process.web, err = startWEB(ctx, inbound, secrets, proxy, options.Network, options.Logger, stateDir, store)", "separate, createErr := New(options, lookup)\n\t\tif createErr != nil { return nil, createErr }\n\t\tprocess.web, err = startWEB(ctx, inbound, secrets, separate, options.Network, options.Logger, stateDir, store)", "TestManagedWEBAndNativeShareAddressAdmission", ("WEB bypassed shared client IP limit",)),
]
try:
    for source, old, new, test, failures in cases:
        content = source.read_text(encoding="utf-8")
        if content.count(old) != 1:
            raise SystemExit("negative control anchor changed: " + str(source))
        with tempfile.TemporaryDirectory(prefix="web-negative-", dir=cache) as directory:
            changed = pathlib.Path(directory) / source.name
            changed.write_text(content.replace(old, new), encoding="utf-8")
            overlay = pathlib.Path(directory) / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}), encoding="utf-8")
            command = ["go", "test", "-count=1", "-timeout=1m", "-modfile", str(module), "-overlay", str(overlay), "-run", "^" + test + "$", "."]
            result = subprocess.run(command, cwd=runtime, env=os.environ, capture_output=True, text=True)
            output = result.stdout + result.stderr
            if result.returncode == 0 or not any(failure in output for failure in failures):
                raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
            print("negative control reproduced: " + test, flush=True)
finally:
    shutil.rmtree(module.parent)
