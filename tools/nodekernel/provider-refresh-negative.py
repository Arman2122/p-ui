import argparse
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get("PUI_DB_DSN"):
    raise SystemExit("PUI_DB_DSN is required; skipped provider tests cannot qualify refresh.")
root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="provider-negative-", dir=root / "scratchpad/node-v2"))
service = "internal/web/service/"
cases = [
    ("applied-payload-integrity", service + "fleet_provider_projection.go",
     "if !bytes.Equal(payloadDigest[:], base.Deployment.Digest) {", "if false && !bytes.Equal(payloadDigest[:], base.Deployment.Digest) {",
     "TestFleetProviderCorruptAppliedPayloadCannotAuthorizeRollout", "corrupt applied payload authorized provider rollout"),
    ("authority-fence-order", service + "fleet_provider_rollout.go",
     "if err := lockFleetNode(tx, claim.NodeID, claim.InstallationID); err != nil {", "if err := error(nil); err != nil {",
     "TestFleetProviderRolloutLocksAuthorityFenceBeforeNode", "provider rollout acquired node before authority fence"),
    ("configuration-claim-cas", service + "fleet_outbound_refresh.go",
     "if !providerClaimCurrent(source, claim, s.now().UnixMilli()) {",
     "if false && !providerClaimCurrent(source, claim, s.now().UnixMilli()) {",
     "TestFleetProviderRefreshConfigurationCASRejectsLateFetch", "late fetch committed"),
    ("strict-malformed-line", service + "fleet_outbound_fetch.go",
     'if err != nil || parsed == nil || len(entries) == maxFleetProviderEntries {\n\t\t\treturn result, fleetProviderFailure{"INVALID_FEED"}\n\t\t}',
     'if err != nil || parsed == nil || len(entries) == maxFleetProviderEntries { continue }',
     "TestFleetProviderParserStrictAndStableIdentity/bad-middle", "want INVALID_FEED"),
    ("duplicate-account", service + "fleet_outbound_fetch.go",
     "if seen[parsed.Identity] {", "if false && seen[parsed.Identity] {",
     "TestFleetProviderParserStrictAndStableIdentity/duplicate-account", "want AMBIGUOUS_IDENTITY"),
    ("redirect-origin", service + "fleet_outbound_fetch.go",
     "request.URL.Scheme != previous[0].URL.Scheme || !strings.EqualFold(request.URL.Host, previous[0].URL.Host)",
     "!strings.EqualFold(request.URL.Hostname(), previous[0].URL.Hostname())",
     "TestFleetProviderFetchRedirectPortDoesNotShareIdentity", "cross-port redirect exposed"),
    ("private-address", service + "fleet_outbound_fetch.go",
     "!allowPrivate && netsafe.IsBlockedIP(resolved.IP)", "false && !allowPrivate && netsafe.IsBlockedIP(resolved.IP)",
     "TestFleetProviderFetchPrivatePolicyAndRedaction", "want PRIVATE_TARGET"),
    ("body-limit", service + "fleet_outbound_fetch.go",
     "if len(body) > maxFleetProviderBytes {", "if false && len(body) > maxFleetProviderBytes {",
     "TestFleetProviderFetchBoundsAndCancellation", "want BODY_TOO_LARGE"),
    ("first-row-serialization", service + "fleet_provider_fanout.go",
     'if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || \'.fleet-provider-rollout:\' || ?,0))", node.ID).Error; err != nil {',
     'if err := error(nil); err != nil {',
     "TestFleetProviderConcurrentFirstFanoutMergesTargets", "concurrent first fanout lost target"),
    ("late-applied-wake", service + "fleet_provider_fanout.go",
     "err := s.wakeAppliedNode(ctx, id)", "err := error(nil)",
     "TestFleetProviderLateAppliedFollowerWakesAfterFanoutCompleted", "record not found"),
    ("manual-draft-fence", service + "fleet_provider_rollout.go",
     'if state.Head.Pending || state.Head.PublishedCompilationID == nil || *state.Head.PublishedCompilationID != base.Compilation.ID ||\n\t\t\tstate.Head.CurrentRevisionID != base.Compilation.NodeRevisionID || state.Head.DesiredDefaultsID != base.Compilation.DefaultsRevisionID {',
     'if false {',
     "TestFleetProviderManualDraftDefersUntilCompatibleAppliedBase", "draft published automatically"),
    ("source-scope-fence", service + "fleet_provider_rollout.go",
     "if err != nil || !bytes.Equal(before, after) {", "if err != nil || !bytes.Equal(before, after) && false {",
     "TestFleetProviderRematerializedSourceRequiresNewApprovedBase", "rematerialized source bypassed"),
    ("stale-work-lease", service + "fleet_provider_rollout.go",
     "if !work.Pending || work.Revision != claim.Revision || work.LeaseID == nil || claim.LeaseID == nil || *work.LeaseID != *claim.LeaseID || work.LeaseUntilUnixMilli <= s.now().UnixMilli() {",
     "if false {",
     "TestFleetProviderStaleClaimCannotPublishSupersedingWork", "stale lease published superseding work"),
    ("published-is-not-applied", service + "fleet_provider_rollout.go",
     "work.PublishedRevision, work.PublishedCompilationID = int64(publication.DeploymentRevision), &publication.CompilationID",
     "work.PublishedRevision, work.PublishedCompilationID = int64(publication.DeploymentRevision), &publication.CompilationID; work.Pending = false",
     "TestFleetProviderRolloutPublishesThenWaitsForAppliedWithoutFunding", "published work="),
    ("first-demand-source-scope", service + "fleet_provider_projection.go",
     'manifest.Placements[i].GraphSHA256, manifest.Placements[i].TermID = "", ""',
     'manifest.Placements[i].GraphSHA256 = ""',
     "TestFleetProviderRolloutPublishesThenWaitsForAppliedWithoutFunding/first-demand-before-refresh", "published work="),
    ("pinned-default", service + "fleet_network_providers.go",
     'follow := binding.FollowLatest && (state.Overrides.FollowDefaults || state.Origins["base/"+base.RuntimeID] == "overridden")',
     'follow := binding.FollowLatest',
     "TestFleetProviderPinnedDefaultsDoNotFollowSharedRefresh", "pinned default received rollout"),
    ("empty-cache-hold", service + "fleet_network_providers.go",
     "if len(outbounds) == 0 {", "if false && len(outbounds) == 0 {",
     "TestFleetProviderEmptyAndChangedSourceKeepAppliedGraph/empty-cache", "unsafe dependency changed graph"),
    ("unknown-binding-scope", service + "fleet_provider_projection.go",
     "return row.ScopeKnown && row.CompilationID == base.Compilation.ID", "return row.CompilationID == base.Compilation.ID",
     "TestFleetProviderUnknownPublicationBackoffDoesNotStarveNeighbor", "unknown publication lost visible backoff"),
]
parser = argparse.ArgumentParser()
parser.add_argument("--start", help="Resume at this control without repeating completed evidence")
arguments = parser.parse_args()
if arguments.start:
    names = [case[0] for case in cases]
    if arguments.start not in names:
        raise SystemExit("Unknown provider control: " + arguments.start)
    cases = cases[names.index(arguments.start):]
environment = dict(os.environ, GOMAXPROCS="2")
for name, relative, before, after, test, expected in cases:
    source = root / relative
    content = source.read_text()
    if content.count(before) != 1:
        raise SystemExit("Provider negative anchor changed: " + name)
    replacement = output / (name + ".go.txt")
    replacement.write_text(content.replace(before, after))
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    command = ["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-timeout=90s", "-overlay", str(overlay), "-run", "^" + test + "$", "./internal/web/service"]
    result = subprocess.run(command, cwd=root, env=environment, capture_output=True, text=True, timeout=240)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if result.returncode == 0 or expected not in log or "[build failed]" in log:
        raise SystemExit("Provider negative did not reproduce: " + name + "; inspect " + str(output / (name + ".log")))
    print("Provider negative reproduced: " + name, flush=True)
print(output)
