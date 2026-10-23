import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
frontend = root / "frontend"
output = pathlib.Path(tempfile.mkdtemp(prefix="fleet-network-ui-", dir=root / "scratchpad/node-v2"))
cases = [
    ("editor-installation", "src/pages/nodes/FleetNetwork.tsx", "editing.spec, editing.installationId", "editing.spec", "components", "src/test/fleet-network.test.tsx", "does not retarget an open node editor", "installationId"),
    ("asset-installation", "src/pages/nodes/FleetNetworkAssets.tsx", "state={{ ...state, installationId: upload.installationId }}", "state={state}", "components", "src/test/fleet-network.test.tsx", "keeps asset upload bound", "installationId"),
    ("publish-idempotency", "src/pages/nodes/FleetNetworkPreview.tsx", "operationID.current ??= RandomUtil.randomUUID();", "operationID.current = RandomUtil.randomUUID();", "components", "src/test/fleet-network.test.tsx", "reuses the publication operation id", "operationId"),
    ("stale-preview", "src/pages/nodes/FleetNetwork.tsx", "${state.installationId}:${state.revision.id}:${state.defaults.id}:${defaults.data?.revision.id}:${previewVersion}", "fixed-preview", "components", "src/test/fleet-network.test.tsx", "invalidates a displayed preview", "Ready for publication"),
    ("authority", "src/pages/nodes/FleetNetworkPreview.tsx", "preview?.ready === true && pending.every(identity => ['dormant', 'authority_required'].includes(identity.state) && hasAuthenticationScope(identity))", "preview?.ready === true", "components", "src/test/fleet-network.test.tsx", "inactive$", "Client authorization is pending"),
    ("auth-only-scope", "src/pages/nodes/FleetNetworkPreview.tsx", " && hasAuthenticationScope(identity)", "", "components", "src/test/fleet-network.test.tsx", "blocks auth-only publication", "Client authorization is pending"),
    ("listen-inheritance", "src/schemas/forms/fleet-network.ts", "if (value.overrideListen) inbound.listen", "if (true) inbound.listen", "unit", "src/test/fleet-network-forms.test.ts", "distinguishes an explicitly empty listen", "listen"),
]
for name, relative, before, after, project, test, pattern, failure in cases:
    source = frontend / relative
    original = source.read_bytes()
    text = original.decode("utf-8")
    if text.count(before) != 1:
        raise RuntimeError(f"UI negative context changed: {name}")
    changed = text.replace(before, after).encode("utf-8")
    try:
        source.write_bytes(changed)
        result = subprocess.run(["node", str(frontend / "node_modules/vitest/vitest.mjs"), "run", "--project", project, test, "-t", pattern], cwd=frontend, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding="utf-8", errors="replace", timeout=60)
        (output / f"{name}.log").write_text(result.stdout, encoding="utf-8")
        if result.returncode == 0 or failure not in result.stdout:
            raise RuntimeError(f"UI test did not detect removed guard: {name}\n{result.stdout[-4000:]}")
    finally:
        if source.read_bytes() != changed:
            raise RuntimeError(f"concurrent edit detected; refusing to overwrite {relative}")
        source.write_bytes(original)
    print(f"UI negative control reproduced: {name}", flush=True)
print(f"Fleet networking qualified with seven removed-guard controls: {output}")
