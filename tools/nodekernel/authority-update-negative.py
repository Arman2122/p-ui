import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="authority-update-negative-", dir=root / "scratchpad/node-v2"))
cases = [
    ("intent-before-apply", "internal/node/worker/authority_update.go", [('w.store.SaveAuthorityIntents(batch)', 'error(nil)')], "./internal/node/worker", "TestAuthorityIntentCommitsBeforeGovernorAndReplaysAfterRestart", "pending authority intent was not durable"),
    ("restore-intent", "internal/node/worker/authority_update.go", [('for _, intent := range intents {', 'for _, intent := range intents[:0] {')], "./internal/node/worker", "TestAuthorityIntentCommitsBeforeGovernorAndReplaysAfterRestart", "pending authority intent was not restored"),
    ("retired-base", "internal/node/state/authority_intent.go", [('if intent.BaseConfigurationRevision != revision || !bytes.Equal(intent.BasePayloadSha256, digest) {', 'if false && (intent.BaseConfigurationRevision != revision || !bytes.Equal(intent.BasePayloadSha256, digest)) {')], "./internal/node/worker", "TestChangedBaseCannotRestorePriorAuthOnlyGrant", "changed base revived prior auth-only grant"),
    ("descriptor-set", "internal/node/worker/authority_update.go", [(' || !bytes.Equal(claim.SHA256, update.DescriptorSha256)', '')], "./internal/node/worker", "TestAuthorityReceiverFencesScopeAndRestoresAuthOnlyBase/descriptor_set", "unsafe authority scope accepted"),
    ("rate-metadata", "internal/node/governor/store.go", [('r.Grant.RateRevision = policy.AuthorityRevision', 'r.Grant.RateRevision = r.Grant.RateRevision')], "./internal/node/governor", "TestAuthorityPolicyAndRateMetadataCommitWithoutResettingCredit", "authority metadata or counters differ"),
    ("burst-debt", "internal/node/limits/account.go", [('a.buckets[dir].SetLimitAt(now, rate.Limit(float64(next.BitsPerSecond)/8))', 'a.buckets[dir] = bucket(next)')], "./internal/node/limits", "TestAuthorityUpdatesPreserveCreditDebtAndPermanentStop", "authority resize minted burst"),
    ("permanent-stop", "internal/node/limits/authority.go", [(' || !before.Enabled && after.Enabled', ''), (' || !current.Enabled && desired.Enabled', '')], "./internal/node/limits", "TestAuthorityUpdatesPreserveCreditDebtAndPermanentStop", "same-base stopped authority revived"),
    ("full-credit-baseline", "internal/node/limits/account.go", [('if (p.CreditRevision != 0 || a.policy.CreditRevision != 0) && (p.CreditCeiling < a.policy.CreditCeiling || p.CreditRevision == a.policy.CreditRevision && p.CreditCeiling != a.policy.CreditCeiling) {', 'if false {')], "./internal/node/limits", "TestFullSnapshotCannotRegressAuthorityOrIssuedCredit/same_credit_revision_ceiling", "full snapshot regressed"),
    ("quota-mode", "internal/node/limits/account.go", [('if (p.AuthorityRevision != 0 || a.policy.AuthorityRevision != 0) && p.QuotaUnlimited != a.policy.QuotaUnlimited {', 'if false {')], "./internal/node/limits", "TestManagedFullSnapshotCannotChangeQuotaMode", "managed full snapshot changed quota mode"),
    ("native-gate", "internal/node/worker/authority_update.go", [('if w.core == "native" {', 'if false && w.core == "native" {')], "./internal/node/worker", "TestNativeBackendCannotAcceptIncrementalAuthority", "unsupported backend accepted authority"),
    ("observed-revision", "internal/node/authority/validation.go", [('AuthorityRevision: grant.AuthorityRevision', 'AuthorityRevision: 0')], "./internal/node/worker", "TestRateOnlyAuthorityChangeProducesDurableObservation", "initial authority observation"),
    ("configured-only", "internal/node/agent/authority_update.go", [('worker.endpoint.Core == "native" || !configured', 'worker.endpoint.Core == "native" || !configured && false')], "./internal/node/agent", "TestAuthorityDeliverySkipsInstalledUnconfiguredRuntimes", "unconfigured runtime was polled"),
]
for name, relative, edits, package, test, expected in cases:
    source = root / relative
    content = source.read_text()
    for before, after in edits:
        if content.count(before) != 1:
            raise SystemExit("authority negative anchor changed: " + name)
        content = content.replace(before, after)
    changed, overlay = output / (name + ".go"), output / (name + ".json")
    changed.write_text(content)
    overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
    result = subprocess.run(["go", "test", "-trimpath", "-race", "-count=1", "-timeout=45s", "-overlay", str(overlay), "-run", "^" + test + "$", package], cwd=root, capture_output=True, text=True, timeout=180)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log:
        raise SystemExit("authority negative did not reproduce: " + name + "\n" + log)
    print("authority update negative reproduced: " + name, flush=True)
print(output)
