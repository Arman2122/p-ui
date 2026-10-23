import argparse
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify group guards.")
root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="legacy-group-negative-", dir=root / "scratchpad/node-v2"))
cases = [
    ("source-before-parent", "fleet_legacy_group.go", [
        ('tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", batch)', 'tx.Where("id IN ?", batch)')],
     "TestFleetLegacyGroupLocksSourceBeforeParentPostgres", "legacy group parent was reached without source lock"),
    ("membership-entry", "client_groups.go", [
        ('if err := lockLegacyGroupMutationTx(db, emails, []string{group}, group); err != nil {', 'if err := error(nil); err != nil {')],
     "TestFleetLegacyGroupMutationsRefuseBeforeAnyWritePostgres/join-owned-pool", "legacy group mutation bypassed owned lifecycle"),
    ("rename-transaction", "client_groups.go", [
        ('if err := lockLegacyGroupMutationTx(db, nil, []string{oldName, newName}, ""); err != nil {', 'if err := error(nil); err != nil {')],
     "TestFleetLegacyGroupMutationsRefuseBeforeAnyWritePostgres/retained-rename", "legacy group mutation bypassed owned lifecycle"),
    ("quota-entry", "group_quota.go", [
        ('if err := lockLegacyGroupMutationTx(tx, nil, []string{name}, ""); err != nil {', 'if err := error(nil); err != nil {')],
     "TestFleetLegacyGroupMutationsRefuseBeforeAnyWritePostgres/quota", "legacy group mutation bypassed owned lifecycle"),
    ("retained-pool", "fleet_legacy_group.go", [
        ('requireLegacyPool(tx, nil, group)', 'requireLegacyPool(tx, nil, "")'),
        ('for _, group := range groups {', 'for range groups {')],
     "TestFleetLegacyGroupMutationsRefuseBeforeAnyWritePostgres/retained-quota", "legacy group mutation bypassed owned lifecycle"),
    ("inbound-neighbor", "fleet_legacy_group.go", [
        ('return requireLegacyClientLifecycle(tx, emails)', 'return nil')],
     "TestFleetLegacyInboundCommitRefusesOwnedTargetAndNeighborPostgres", "legacy inbound commit rewrote owned source or neighbor"),
    ("appearing-member", "fleet_legacy_group.go", [
        ('if err := requireLockedLegacySources(locked, current); err != nil {\n\t\treturn err\n\t}\n\tselected :=', '_ = current\n\tselected :=')],
     "TestFleetLegacyGroupRejectsMemberAppearingBeforeParentLockPostgres", "group edit acquired an unplanned source after parent lock"),
    ("quota-template-atomicity", "group_membership.go", [
        ('return db.Transaction(func(tx *gorm.DB) error {', 'if err := db.Transaction(func(tx *gorm.DB) error {'),
        ('return saveLegacyGroupQuotaTx(tx, name, quotaBytes)\n\t})', 'return nil\n\t}); err != nil {\n\t\treturn err\n\t}\n\treturn saveLegacyGroupQuotaTx(db, name, quotaBytes)')],
     "TestFleetLegacyGroupQuotaAndTemplateMetadataRollbackTogetherPostgres", "group template metadata committed before rejected quota"),
    ("deleted-parent-scope", "group_quota.go", [
        ('return tx.Where("id = ? AND name = ?", group.Id, name).First(&model.ClientGroup{}).Error', 'return nil')],
     "TestFleetLegacyGroupQuotaRejectsDeletedParentPostgres", "deleted group was not rejected before quota write"),
    ("derived-parent-serialization", "fleet_legacy_group.go", [
        ('for _, row := range observed {\n\t\tensure = append(ensure, row.Group)\n\t}', '')],
     "TestFleetLegacyDerivedGroupSerializesParentNamePostgres", "derived group did not serialize its parent name"),
]
parser = argparse.ArgumentParser()
parser.add_argument("--start", choices=[case[0] for case in cases])
arguments = parser.parse_args()
if arguments.start:
    cases = cases[next(index for index, case in enumerate(cases) if case[0] == arguments.start):]
for name, relative, replacements, test, expected in cases:
    source = root / "internal/web/service" / relative
    content = source.read_text()
    for before, after in replacements:
        if content.count(before) != 1:
            raise SystemExit("Legacy group negative anchor changed: " + name)
        content = content.replace(before, after)
    replacement = output / (name + ".go.txt")
    replacement.write_text(content)
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    selector = "^" + test.replace("/", "$/^") + "$"
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-timeout=90s", "-overlay", str(overlay),
                             "-run", selector, "./internal/web/service"], cwd=root, env=dict(os.environ, GOMAXPROCS="2"), capture_output=True, text=True, timeout=240)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log or "[build failed]" in log:
        raise SystemExit("Legacy group negative did not reproduce: " + name + "; inspect " + str(output / (name + ".log")))
    print("Legacy group negative reproduced: " + name, flush=True)
print(output)
