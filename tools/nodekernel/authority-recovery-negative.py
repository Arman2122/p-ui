#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify recovery holds.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
work.mkdir(parents=True, exist_ok=True)
report = "internal/controlplane/authority_report.go"
recovery = "internal/controlplane/authority_recovery.go"
cases = [
    ("durable-global-hold", recovery,
     'return tx.Model(&model.FleetFence{}).Where("id = 1").Updates(map[string]any{"recovery_required": true, "reason": reason, "witness_node_id": p.NodeID}).Error',
     '_ = reason; return nil', "TestAuthorityRollbackWitnessPersistsGlobalHoldWithoutBillingPostgres", "rollback witness did not persist scoped global hold"),
    ("known-credit-witness", report,
     "rowAhead := observed.CreditCeiling > uint64(grant.IssuedBytes)", "rowAhead := false",
     "TestAuthorityRollbackWitnessPersistsGlobalHoldWithoutBillingPostgres/credit$", "rollback witness="),
    ("known-rate-witness", report,
     "if observed.AuthorityRevision > uint64(share.Revision) {", "if observed.AuthorityRevision > uint64(share.Revision) && false {",
     "TestAuthorityRollbackWitnessPersistsGlobalHoldWithoutBillingPostgres/rate$", "rollback witness="),
    ("sticky-rate-witness", recovery,
     "creditAhead, rateAhead := false, validatedRateWitness", "creditAhead, rateAhead := false, false",
     "TestAuthorityRollbackRateWitnessSurvivesRevisionReusePostgres", "validated rate witness erased by numeric catch-up"),
    ("exclusive-owner-revalidation", recovery,
     "if !exists || owners[share.BudgetID] != grant.ClientID {", "if !exists || owners[share.BudgetID] != grant.ClientID && false {",
     "TestAuthorityRollbackRevalidatesOwnershipBeforeHoldPostgres/rate-owner", "changed witness scope="),
]
for name, filename, before, after, selection, expected in cases:
    data = (root / filename).read_text()
    if data.count(before) != 1:
        raise SystemExit("Recovery mutation target changed: " + name)
    directory = pathlib.Path(tempfile.mkdtemp(prefix="authority-recovery-negative-" + name + "-", dir=work))
    replacement = directory / pathlib.Path(filename).name
    replacement.write_text(data.replace(before, after, 1))
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(root / filename): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-overlay", str(overlay), "-count=1", "-run", "^" + selection, "./internal/controlplane"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or expected.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Recovery negative did not reproduce " + name + "; inspect " + str(directory / "result.log"))
    print("Recovery safeguard reproduced: " + name, flush=True)
