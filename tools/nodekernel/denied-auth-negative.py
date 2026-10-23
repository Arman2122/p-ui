import json
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="denied-auth-", dir=root / "scratchpad/node-v2"))
modules = {}
cases = [
    ("ssh-hook", None, "internal/cores/internal/ssh/server.go", "if s.demand != nil {", "if false && s.demand != nil {", "TestAuthenticatedSSHDemandNeverAuthorizesTheDeniedAttempt", "SSH demand was not restricted"),
    ("ssh-password", None, "internal/cores/internal/ssh/server.go", "!ok || subtle.ConstantTimeCompare([]byte(cred.password), password) != 1", "!ok || subtle.ConstantTimeCompare([]byte(cred.password), password) != 1 && false", "TestAuthenticatedSSHDemandNeverAuthorizesTheDeniedAttempt", "SSH demand was not restricted"),
    ("ssh-throttle", None, "internal/cores/internal/ssh/server.go", " || errors.Is(err, errAuthorizationDenied)", "", "TestAuthenticatedSSHDemandNeverAuthorizesTheDeniedAttempt", "SSH demand was not restricted"),
    ("old-proof", None, "internal/node/admission/admission.go", " || descriptor != expected", "", "TestAuthenticatedSSHOldProofCannotAdoptRotatedCredentialGeneration", "old credential proof adopted the replacement descriptor"),
    ("singbox-hook", "singbox", "core-runtimes/singbox/runtime.go", "Demand: admission.FromContext(ctx)", "Demand: admission.FromContext(context.Background())", "TestAuthenticatedSingboxDemandCannotAdmitDeniedStreams", "authenticated denied stream produced no demand"),
    ("singbox-receipt", "singbox", "core-runtimes/singbox/tracker.go", "_ = c.Close()\n\t\treturn &deniedConn{Conn: c, err: err}", "return c", "TestAuthenticatedSingboxDemandCannotAdmitDeniedStreams", "demand receipt forwarded traffic"),
    ("singbox-packet", "singbox", "core-runtimes/singbox/tracker.go", "_ = c.Close()\n\t\treturn &packetConn{PacketConn: c, err: err}", "return c", "TestAuthenticatedSingboxPacketDemandCannotForward", "demand receipt released UDP payload"),
    ("mtproto-hook", "mtproto", "core-runtimes/mtproto/managed.go", "newWithDemand(options, lookup, admission.FromContext(ctx), inbound.ID)", "newWithDemand(options, lookup, admission.FromContext(context.Background()), inbound.ID)", "TestAuthenticatedMTProtoAndWEBDemandCannotForward", "authenticated dd attempt produced no demand"),
    ("mtproto-receipt", "mtproto", "core-runtimes/mtproto/runtime.go", "\t\t\treturn nil, limits.ErrBlocked\n\t\t}\n\t\tif err := account.Check()", "\t\t\treturn connection, nil\n\t\t}\n\t\tif err := account.Check()", "TestAuthenticatedMTProtoAndWEBDemandCannotForward", "demand receipt forwarded MTProto bytes"),
    ("mixed-entry", "mtproto", "core-runtimes/mtproto/managed.go", " || modes.Has(mtglib.SecretModeFakeTLS) && (modes.Has(mtglib.SecretModeDD) || modes.Has(mtglib.SecretModePlain))", "", "TestMTProtoDemandRejectsMixedListenerEntryModes", "unservable mixed entry modes were accepted"),
]
try:
    for name, family, relative, before, after, test, failure in cases:
        module = None
        if family:
            if family not in modules:
                prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", family, "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
                module = pathlib.Path(prepared.stdout.strip()).resolve()
                if module.parent.parent != (root / ".cache/node-core").resolve() or not module.parent.name.startswith(family + "-"):
                    raise RuntimeError("unexpected prepared core directory")
                modules[family] = module
            module = modules[family]
        source = root / relative
        text = source.read_text()
        if text.count(before) != 1:
            raise RuntimeError(f"demand overlay context changed: {name}")
        replacement, overlay = output / f"{name}.go", output / f"{name}.json"
        replacement.write_text(text.replace(before, after))
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
        command = ["go", "test", "-race", "-count=1", "-timeout=45s", "-overlay", str(overlay), "-run", f"^{test}$"]
        if module:
            command += ["-modfile", str(module), "."]
        else:
            command += ["./internal/cores/internal/ssh"]
        result = subprocess.run(command, cwd=root / "core-runtimes" / family if family else root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        (output / f"{name}.log").write_text(result.stdout)
        if result.returncode == 0 or failure not in result.stdout:
            raise RuntimeError(f"demand regression did not detect removed guard: {name}\n{result.stdout[-6000:]}")
        print(f"denied-auth negative control reproduced: {name}", flush=True)
finally:
    for module in modules.values():
        shutil.rmtree(module.parent)
print(f"Authenticated demand qualified with ten negative controls: {output}")
