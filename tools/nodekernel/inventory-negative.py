import json
import os
import pathlib
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "scratchpad" / "node-v2" / "inventory-negative"
OUTPUT.mkdir(parents=True, exist_ok=True)
AGENT = "internal/node/agent/capabilities.go"
WORKER = "internal/node/worker/capabilities.go"
CASES = [
    ("identity", AGENT,
     'snapshot.NodeID != id.NodeID || snapshot.InstallationID != id.InstallationID || snapshot.ControlPlaneID != id.ControlPlaneID || ',
     'false && (snapshot.NodeID != id.NodeID || snapshot.InstallationID != id.InstallationID || snapshot.ControlPlaneID != id.ControlPlaneID) || ',
     "./internal/node/agent", "^TestInventoryRejectsForeignMalformedAndOversizedSnapshots$/^(node|installation|control_plane)$"),
    ("journal", AGENT, ' || !proto.Equal(snapshot.Journal, journal)', '',
     "./internal/node/agent", "^TestInventoryRejectsForeignMalformedAndOversizedSnapshots$/^(runtime|state_journal|credit_journal)$"),
    ("response-bound", AGENT, ' || len(data) > inventory.MaxResponseBytes', '',
     "./internal/node/agent", "^TestInventoryRejectsForeignMalformedAndOversizedSnapshots$/^oversize$"),
    ("core", AGENT, ' || capability.CoreId != journal.CoreId', '',
     "./internal/node/agent", "^TestInventoryRejectsForeignMalformedAndOversizedSnapshots$/^core$"),
    ("family-consensus", AGENT, 'if !blocked[family] {', 'if !blocked[family] || capability != nil {',
     "./internal/node/agent", "^TestInventoryRequiresEverySameFamilyWorkerToAgree$"),
    ("worker-journal", WORKER, 'if !w.matchesJournals(request) {', 'if false {',
     "./internal/node/worker", "^TestCapabilityEndpointFencesJournalsBeforeProbingAndPreservesReadiness$"),
    ("native-closed", WORKER, ' || w.core == "native"', '',
     "./internal/node/agent", "^TestNativeInventoryStaysUnknownDespitePositiveStaticProvider$"),
    ("heartbeat-refresh", "internal/node/agent/agent.go", 'if a.inventoryChanged(ctx, hello.Capabilities) {', 'if false {',
     "./internal/node/agent", "^TestControlRefreshesHelloWhenAuthenticatedInventoryChanges$"),
]


def main():
    environment = {**os.environ, "GOMAXPROCS": "2"}
    for name, relative, before, after, package, selector in CASES:
        source = ROOT / relative
        original = source.read_text(encoding="utf-8")
        if original.count(before) != 1:
            raise RuntimeError(f"{name}: expected exactly one guard")
        with tempfile.TemporaryDirectory(prefix=f"{name}-", dir=OUTPUT) as temporary:
            directory = pathlib.Path(temporary).resolve()
            directory.relative_to(OUTPUT.resolve())
            mutated = directory / source.name
            mutated.write_text(original.replace(before, after, 1), encoding="utf-8")
            overlay = directory / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}), encoding="utf-8")
            command = ["go", "test", "-p=1", "-overlay", str(overlay), "-count=1", "-timeout=45s", "-run", selector, package]
            result = subprocess.run(command, cwd=ROOT, env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
            (OUTPUT / f"{name}.log").write_text(result.stdout, encoding="utf-8")
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or "[build failed]" in result.stdout:
                raise RuntimeError(f"{name}: removed safeguard did not fail the intended test; see its log")
            if source.read_text(encoding="utf-8") != original:
                raise RuntimeError(f"{name}: production source changed during qualification")
            print(f"PASS {name}: removed safeguard rejected", flush=True)


if __name__ == "__main__":
    main()
