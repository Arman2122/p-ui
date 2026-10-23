import json
import os
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="native-authority-negative-", dir=root / "scratchpad/node-v2"))
cases = [
    ("durable-intent", "internal/node/native/authority_journal.go", "record.PendingAuthority = &authorityChange{Grant: assignment.Grant, Selectors: slices.Clone(assignment.Selectors), Initial: initial[i]}", "record.PendingAuthority = nil", "./internal/node/native", "TestNativePendingAuthorityFencesOldPathsAndResumesWithoutReservation", "failed kernel update was acknowledged"),
    ("configuration-pending-fence", "internal/node/native/controller.go", "if record.PendingAuthority != nil {\n\t\t\treturn ErrBusy\n\t\t}", "if false && record.PendingAuthority != nil {\n\t\t\treturn ErrBusy\n\t\t}", "./internal/node/native", "TestNativePendingAuthorityFencesOldPathsAndResumesWithoutReservation", "authority retry reset reservation or failed to converge"),
    ("failed-amendment-block", "internal/node/native/authority_update.go", "result = errors.Join(result, c.closeFailedAuthority(*record.Lease))", "result = errors.Join(result)", "./internal/node/native", "TestNativePendingAuthorityFencesOldPathsAndResumesWithoutReservation", "failed authority lost its durable closed transition"),
    ("credit-pending-fence", "internal/node/native/credit.go", " || record.PendingAuthority != nil", "", "./internal/node/native", "TestNativePendingAuthorityFencesOldPathsAndResumesWithoutReservation", "credit bypassed pending authority"),
    ("strict-stop-proof", "internal/node/native/authority_update.go", "count, err := c.plane.Stop(ctx, lease)\n\t\tif err != nil {", "count, err := c.plane.Stop(ctx, lease)\n\t\tif err != nil && !errors.Is(err, ErrMissing) {", "./internal/node/native", "TestNativeAuthorityStopRequiresKernelProofAndCannotRevive", "missing kernel state acknowledged stop"),
    ("reservation-high-water", "internal/node/native/journal.go", "r.Reserved += bytes", "r.Reserved = bytes", "./internal/node/native", "TestNativeAuthorityCrashIntentRetainsChargedReservationAndRejectsOldBasePolicy", "recovery reused uncertain lease credit"),
    ("recovery-authority", "internal/node/native/authority_update.go", "return c.journal.commitAuthority(record.Grant.ID, record.PendingAuthority.Grant)", "return nil", "./internal/node/native", "TestNativeAuthorityCrashIntentRetainsChargedReservationAndRejectsOldBasePolicy", "recovery forgot authority or refunded uncertain reservation"),
    ("credential-proof", "internal/cores/noderuntime/native/authority_config.go", "if !known || !current[update.ClientID] {", "if !known {", "./internal/cores/noderuntime/native", "TestNativeAuthorityRequiresCurrentConfiguredCredentialProof", "unproved native identity accepted authority"),
    ("expired-replay", "internal/node/native/authority_journal.go", "if policy != record.Grant.Policy && policy.Enabled", "if policy.Enabled", "./internal/node/native", "TestNativeAuthorityExpiredReplayAcknowledgesOnlyCommittedRevision", "committed expired authority rejected exact replay"),
]
environment = dict(os.environ, GOMAXPROCS="2")
for name, relative, before, after, package, test, expected in cases:
    source = root / relative
    content = source.read_text()
    if content.count(before) != 1:
        raise SystemExit("native authority negative anchor changed: " + name)
    changed = output / (name + ".go.txt")
    changed.write_text(content.replace(before, after))
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(changed)}}))
    command = ["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-timeout=90s", "-overlay", str(overlay), "-run", "^" + test + "$", package]
    result = subprocess.run(command, cwd=root, env=environment, capture_output=True, text=True, timeout=300)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log:
        raise SystemExit("native authority negative did not reproduce: " + name + "\n" + log)
    print("native authority negative reproduced: " + name, flush=True)
print(output)
