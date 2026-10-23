import json
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="hot-auth-", dir=root / "scratchpad/node-v2"))
module = None
cases = [
    ("proof-current", "admission", "internal/node/admission/proof.go", [("return p.broker.proofs[descriptorKey(p.descriptor)] == p", "return true")], "TestHotProofCommitRetainsUnchangedAndPermanentlyRetiresReplacedCredentials", "candidate proof became current"),
    ("proof-retain", "admission", "internal/node/admission/proof.go", [("next != nil && next.descriptor == proof.descriptor", "next != nil && next.descriptor == proof.descriptor && false")], "TestHotProofCommitRetainsUnchangedAndPermanentlyRetiresReplacedCredentials", "hot prepare invalidated an unchanged identity"),
    ("worker-hot", "worker", "internal/node/worker/backend.go", [("s.engine.(AuthenticationInvalidator); ok {", "s.engine.(AuthenticationInvalidator); ok && false {")], "TestWorkerHotProofCommitFencesFailedApplyWithoutClosingUnchangedSubjects", "qualified engine was closed"),
    ("worker-commit", "worker", "internal/node/worker/worker.go", [("if err := w.admissions.Commit(deployment.Revision, binding); err != nil {\n\t\treturn err\n\t}", ""), ("if err := w.backend.Apply(admission.WithBinding(w.ctx, binding), runtime); err != nil {", "if err := w.admissions.Commit(deployment.Revision, binding); err != nil { return err }; if err := w.backend.Apply(admission.WithBinding(w.ctx, binding), runtime); err != nil {")], "TestWorkerHotProofCommitFencesFailedApplyWithoutClosingUnchangedSubjects", "candidate became current before the engine"),
    ("ssh-registration", "ssh", "internal/cores/internal/ssh/server.go", [(' || record.token != sc.Permissions.Extensions["authentication"]', "")], "TestSSHHotProofUpdatesPreserveOtherClientTCPUDPAndAccounting", "old funded password adopted the new credential proof"),
    ("ssh-revoke", "ssh", "internal/cores/internal/ssh/authentication.go", [("connection.close()", "_ = connection")], "TestSSHHotProofUpdatesPreserveOtherClientTCPUDPAndAccounting", "rekey kept the changed client's prior carrier alive"),
    ("mtproto-proof-capture", "mtproto", "@upstream/mtglib/proxy_serve.go", [("ctx.authentication = set.authentication[set.names[i]]", "ctx.authentication = nil")], "TestMTProtoOldSnapshotCannotRegisterAfterHotProofOrKeyRotation/proof", "stale authenticated snapshot was registered"),
    ("mtproto-key-current", "mtproto", "@upstream/mtglib/proxy_serve.go", [(" || key != [SecretKeyLength]byte(ctx.matchedSecretKey)", " || (key != [SecretKeyLength]byte(ctx.matchedSecretKey) && false)")], "TestMTProtoOldSnapshotCannotRegisterAfterHotProofOrKeyRotation/key", "stale authenticated snapshot was registered"),
    ("mtproto-revoke", "mtproto", "core-runtimes/mtproto/authentication.go", [("process.proxy.InvalidateAuthentication(clients)", "_ = clients; _ = process")], "TestMTProtoHotProofEditsPreserveOtherClientNativeWEBAndAccounting", "retired placement retained a funded carrier"),
    ("mtproto-key-owner", "mtproto", "core-runtimes/mtproto/managed.go", [("if keys[secret.Key] {", "if keys[secret.Key] && false {")], "TestMTProtoHotProofEditsPreserveOtherClientNativeWEBAndAccounting", "same key with another hostname can select another accounting identity"),
    ("mtproto-cancel-dial", "mtproto", "@upstream/mtglib/proxy_telegram.go", [("p.network.DialContext(ctx.ctx, addr.Network, addr.Address)", "p.network.Dial(addr.Network, addr.Address)")], "TestShutdownDuringSuccessfulDialCannotRelayNilConnection", "Telegram dial was not reached"),
]
try:
    for name, family, relative, edits, test, expected in cases:
        if family == "mtproto" and module is None:
            prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "mtproto", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
            module = pathlib.Path(prepared.stdout.strip()).resolve()
            if module.parent.parent != (root / ".cache/node-core").resolve() or not module.parent.name.startswith("mtproto-"):
                raise RuntimeError("unexpected prepared core directory")
        source = module.parent / "source" / relative.removeprefix("@upstream/") if relative.startswith("@upstream/") else root / relative
        text = source.read_text()
        for before, after in edits:
            expected_count = 2 if name == "ssh-revoke" else 1
            if text.count(before) != expected_count:
                raise RuntimeError(f"hot-auth overlay context changed: {name}")
            text = text.replace(before, after)
        replacement, overlay = output / f"{name}.go", output / f"{name}.json"
        replacement.write_text(text)
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
        command = ["go", "test", "-race", "-count=1", "-timeout=30s", "-overlay", str(overlay), "-run", f"^{test}$"]
        if family == "mtproto":
            command += ["-modfile", str(module), "."]
            cwd = root / "core-runtimes/mtproto"
        else:
            command += ["./internal/cores/internal/ssh" if family == "ssh" else "./internal/node/" + family]
            cwd = root
        result = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        (output / f"{name}.log").write_text(result.stdout)
        if result.returncode == 0 or expected not in result.stdout:
            raise RuntimeError(f"hot-auth regression missed removed guard: {name}\n{result.stdout[-6000:]}")
        print(f"hot-auth negative control reproduced: {name}", flush=True)
finally:
    if module:
        shutil.rmtree(module.parent)
print(f"Immutable hot authentication qualified with eleven negative controls: {output}")
