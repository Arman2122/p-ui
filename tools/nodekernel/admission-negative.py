import json
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache/node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("xray-"):
    raise SystemExit("unexpected prepared module location")
cases = [
    ("internal/node/admission/admission.go", " || descriptor != expected", "", "./internal/node/admission", "TestOldAuthenticationProofCannotAdoptReplacementCredentialGeneration", "old credential relabeled as current proof="),
    ("internal/node/admission/admission.go", " || len(b.pending) >= MaxPending", "", "./internal/node/admission", "TestDemandCapacityAndUnknownTimeFailClosed", "overflow authentication retained"),
    ("internal/node/admission/admission.go", " || result.ClientId != pending.request.ClientId", "", "./internal/node/admission", "TestAuthenticatedDemandIsEphemeralScopedAndNeverCredit", "foreign receipt="),
    ("internal/node/admission/admission.go", "pending.delivered = true", "pending.delivered = false", "./internal/node/admission", "TestAuthenticatedDemandIsEphemeralScopedAndNeverCredit", "delivered demand replay="),
    ("internal/node/trustedtime/clock.go", "saved := c.request.Load()", "saved := c.saved.Load()", "./internal/node/trustedtime", "TestFreshRequestClockNeverReusesExpirationHighWater", "lower request bound="),
    ("internal/node/agent/admission.go", "request.RuntimeId != worker.endpoint.ID", "request.RuntimeId == \"\"", "./internal/node/agent", "TestLocalActivationRequiresAppliedScopeAndCannotMintCredit", "runtime scope accepted"),
    ("internal/node/agent/admission.go", "request.DeploymentRevision != cursors.Applied", "request.DeploymentRevision == 0", "./internal/node/agent", "TestLocalActivationRequiresAppliedScopeAndCannotMintCredit", "unapplied revision accepted"),
    ("internal/cores/internal/xray/enforcement/dispatcher.go", "_ = d.admission(ctx, client, inbound.Tag)", "_ = ctx.Err()", "./internal/cores/noderuntime/xray", "TestOnlyAuthenticatedDeniedXrayAttemptsRequestAuthority", "auth-only attempt calls=0"),
]
try:
    for relative, old, new, package, test, failure in cases:
        source = root / relative
        content = source.read_text(encoding="utf-8")
        expected = 2 if relative.endswith("dispatcher.go") else 1
        if content.count(old) != expected:
            raise SystemExit("negative control anchor changed: " + relative)
        with tempfile.TemporaryDirectory(prefix="admission-negative-", dir=cache) as directory:
            changed = pathlib.Path(directory) / source.name
            changed.write_text(content.replace(old, new), encoding="utf-8")
            overlay = pathlib.Path(directory) / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
            result = subprocess.run(["go", "test", "-trimpath", "-p=1", "-count=1", "-timeout=60s", "-modfile", str(module), "-overlay", str(overlay), "-run", "^"+test+"$", package], cwd=root, capture_output=True, text=True)
            output = result.stdout+result.stderr
            if result.returncode == 0 or failure not in output:
                raise SystemExit("negative control did not fail as expected: " + test + "\n" + output)
            print("negative control reproduced: " + test, flush=True)
    source = root / "internal/controlplane/service.go"
    content = source.read_text(encoding="utf-8")
    start = content.index("\t\t\tcase *nodev1.NodeMessage_Activation:")
    end = content.index("\n\t\t\tdefault:", start)
    content = content[:start] + """\t\t\tcase *nodev1.NodeMessage_Activation:
                result := s.activate(ctx, p, generation, message.Activation)
                if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Activation{Activation: result}}); err != nil { return err }
""" + content[end:]
    content = content.replace("\tactivationSlots := make(chan struct{}, 4)\n", "")
    with tempfile.TemporaryDirectory(prefix="admission-negative-", dir=cache) as directory:
        changed = pathlib.Path(directory) / "service.go"
        changed.write_text(content, encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
        result = subprocess.run(["go", "test", "-trimpath", "-p=1", "-count=1", "-timeout=60s", "-modfile", str(module), "-overlay", str(overlay), "-run", "^TestSlowActivationDoesNotBlockConfigurationDelivery$", "./internal/controlplane"], cwd=root, capture_output=True, text=True)
        output = result.stdout+result.stderr
        if result.returncode == 0 or "activation blocked deployment=" not in output:
            raise SystemExit("control-stream negative did not fail as expected\n"+output)
        print("negative control reproduced: TestSlowActivationDoesNotBlockConfigurationDelivery", flush=True)
finally:
    shutil.rmtree(module.parent)
