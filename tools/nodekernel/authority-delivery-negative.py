import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"], cwd=root, check=True, capture_output=True, text=True)
module = pathlib.Path(prepared.stdout.strip()).resolve()
cache = (root / ".cache/node-core").resolve()
if module.parent.parent != cache or not module.parent.name.startswith("xray-"):
    raise SystemExit("unexpected prepared module location")
source = root / "internal/controlplane/authority_delivery.go"
cases = [
    ("if sameAuthorityScope(previous, batch) {\n\t\t\t\tincoming.AcknowledgedRevision", "if previous.GrantID != \"\" {\n\t\t\t\tincoming.AcknowledgedRevision", "TestFleetAuthorityNewBaseRequiresNewReceipt", "authority delivery="),
    ("if !allowed[row.GrantID] {", "if !allowed[row.GrantID] && row.GrantID == \"\" {", "TestFleetAuthoritySourceFencePreservesReservedQuotaAndAllowsStop", "disabled source still received authority="),
    (" || !bytes.Equal(scope.PayloadSHA256, request.BasePayloadSha256)", "", "TestFleetInitialAuthorityOutboxDoesNotRepublishConfiguration", "foreign projected base="),
    ("err = tx.Where(\"id = ? AND client_id = ?\", row.TermID, row.ClientID).First(term).Error", "term, err = managedAuthority(tx, row.ClientID, row.TermID, false)", "TestFleetAuthorityAcknowledgmentDoesNotLockMutableClient", "authority acknowledgment waited for a mutable client lock:"),
    ("if old.CreditRevision != update.CreditRevision || old.CreditCeiling != update.CreditCeiling {", "if old.CreditRevision > update.CreditRevision || old.CreditCeiling > update.CreditCeiling {", "TestFleetIncrementalAuthorityRefillsThroughCreditChannel", "quota-only change silently accepted by authority channel="),
]
if "--same-base-only" in sys.argv:
    cases = []
try:
    for old, new, test, failure in cases:
        content = source.read_text(encoding="utf-8")
        if content.count(old) != 1:
            raise SystemExit("negative control anchor changed: " + test)
        with tempfile.TemporaryDirectory(prefix="authority-delivery-negative-", dir=cache) as directory:
            changed = pathlib.Path(directory) / source.name
            changed.write_text(content.replace(old,new),encoding="utf-8")
            overlay = pathlib.Path(directory) / "overlay.json"
            overlay.write_text(json.dumps({"Replace":{str(source):str(changed)}}))
            result = subprocess.run(["go","test","-trimpath","-p=1","-count=1","-timeout=90s","-modfile",str(module),"-overlay",str(overlay),"-run","^"+test+"$","./internal/web/service"],cwd=root,capture_output=True,text=True)
            output = result.stdout+result.stderr
            if result.returncode == 0 or failure not in output:
                raise SystemExit("negative control did not fail as expected: "+test+"\n"+output)
            print("negative control reproduced: "+test,flush=True)
    content = source.read_text(encoding="utf-8")
    old = "if foundOther.RowsAffected != 0 {"
    if content.count(old) != 1:
        raise SystemExit("same-base identity guard changed")
    model = root / "internal/database/model/fleet_authority_update.go"
    model_content = model.read_text(encoding="utf-8")
    if model_content.count(";uniqueIndex:idx_fleet_authority_client_base") != 4:
        raise SystemExit("same-base uniqueness constraint changed")
    with tempfile.TemporaryDirectory(prefix="authority-delivery-negative-", dir=cache) as directory:
        changed = pathlib.Path(directory) / source.name
        changed.write_text(content.replace(old,"if foundOther.RowsAffected < 0 {"),encoding="utf-8")
        changed_model = pathlib.Path(directory) / model.name
        changed_model.write_text(model_content.replace(";uniqueIndex:idx_fleet_authority_client_base",""),encoding="utf-8")
        overlay = pathlib.Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace":{str(source):str(changed),str(model):str(changed_model)}}))
        result = subprocess.run(["go","test","-trimpath","-p=1","-count=1","-timeout=90s","-modfile",str(module),"-overlay",str(overlay),"-run","^TestFleetAuthorityTermReplacementCannotPoisonOneClientBatch$","./internal/web/service"],cwd=root,capture_output=True,text=True)
        output = result.stdout+result.stderr
        if result.returncode == 0 or "same-base grant replacement entered delivery prefix=" not in output:
            raise SystemExit("same-base negative did not fail as expected\n"+output)
        print("negative control reproduced: TestFleetAuthorityTermReplacementCannotPoisonOneClientBatch",flush=True)
finally:
    shutil.rmtree(module.parent)
