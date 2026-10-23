#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import sys
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped tests cannot qualify client creation.")
root = pathlib.Path.cwd().resolve()
work = root / "scratchpad/node-v2"
cases = [
    ("binding-flow", "fleet_source_create.go", ', FlowOverride: client.Flow', '',
     "TestFleetSourceCreatePreservesRequestedFlowForExplicitBindingPostgres", "explicit binding discarded requested flow"),
    ("expanded-credentials", "fleet_source_credentials.go", 'if len(row.Value) > remaining {', 'if len(row.Value) > remaining && false {',
     "TestFleetSourceCreateBoundsExpandedCredentialsBeforePersistencePostgres", "expanded credentials were stored from a small request"),
    ("actor-precondition", "fleet_source_create_api.go", 'if actor == nil || request.ExpectedActorID <= 0 || request.ExpectedActorID != actor.Id {', 'if actor == nil || request.ExpectedActorID != 0 && request.ExpectedActorID != actor.Id {',
     "TestFleetCreateRouteDispatchesAtomicOperationAndPrivateReceiptPostgres", "owned API accepted a request without its actor precondition"),
    ("unqualified-owner", "fleet_source_claims.go", 'if unqualified {', 'if unqualified && false {',
     "TestFleetSourceCreateRefusesUnqualifiedInheritedAndRetainedOwnersPostgres", "owner was invisible to credential reservation"),
    ("explicit-plan-override", "fleet_source_create_plan.go", 'if overrides.Quota != nil && *overrides.Quota {', 'if overrides.Quota != nil && *overrides.Quota && false {',
     "TestFleetSourceCreateAppliesExplicitPlanOverridesAtomicallyPostgres", "explicit managed fields were lost"),
    ("disabled-source", "fleet_source_create.go", 'Update("enable", client.Enable)', 'Update("enable", true)',
     "TestFleetSourceCreateAtomicDisabledReplayPostgres", "disabled creation changed source flags"),
    ("changed-retry", "fleet_source_create.go", '!bytes.Equal(operation.Digest, digest) ||', 'false && !bytes.Equal(operation.Digest, digest) ||',
     "TestFleetSourceCreateAtomicDisabledReplayPostgres", "changed retry accepted"),
    ("subscription-identity", "fleet_source_create.go", 'if exists {', 'if exists && false {',
     "TestFleetSourceCreateConcurrentRetriesAndSubscriptionIdentityPostgres", "same subscription created"),
    ("reserved-owner", "fleet_source_claims.go", 'if stored.ClientID != client.ID {', 'if stored.ClientID != client.ID && false {',
     "TestFleetSourceCreateClaimsCanonicalIdentityAndDisabledOwnersPostgres", "canonical lookup alias escaped"),
    ("required-claim", "fleet_source_claims.go", 'if count != int64(len(batch)) {', 'if count != int64(len(batch)) && false {',
     "TestFleetSourceCreateRequiresClaimsBeforeNewInheritedPlacementPostgres", "unprepared inherited authentication identity became admissible"),
    ("binding-projection", "client_credential.go", 'setClientCredential(c, name, value)', 'if name == "secret" { setClientCredential(c, name, value) }',
     "TestFleetSourceCredentialPreparationProjectsIncompatibleCipherKeysPerBinding", "binding credential did not reach node user projection"),
    ("foreign-binding", "fleet_source_credentials.go", 'if len(byID) != 0 {', 'if len(byID) != 0 && false {',
     "TestFleetSourceCredentialPreparationRefusesSilentRotationAndPreservesExplicitOverrides", "out-of-scope binding accepted"),
]
for name, filename, before, after, selection, expected in cases:
    if sys.argv[1:] and name not in sys.argv[1:]:
        continue
    source = root / "internal/web/service" / filename
    data = source.read_text()
    if data.count(before) != 1:
        raise SystemExit("Creation mutation changed: " + name)
    directory = pathlib.Path(tempfile.mkdtemp(prefix="source-create-negative-" + name + "-", dir=work))
    replacement = directory / filename
    replacement.write_text(data.replace(before, after, 1))
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    package = "./internal/web/controller" if selection.startswith("TestFleetCreateRoute") else "./internal/web/service"
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-overlay", str(overlay), "-count=1", "-timeout=3m", "-run", "^" + selection + "$", package], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / "result.log").write_bytes(result.stdout)
    if result.returncode == 0 or expected.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Creation negative failed to reproduce " + name + "; inspect " + str(directory / "result.log"))
    print("Creation safeguard reproduced: " + name, flush=True)
