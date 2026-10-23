#!/usr/bin/env bash
set -euo pipefail
cd /tmp/penhoon-node-v2-20261008
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64/bin:/root/go/bin:/usr/local/bin:$PATH
export GOMAXPROCS=2
. /root/.pui-test-dsn
test -n "${PUI_DB_DSN:-}"
selector='^TestRoleValidationRejectsUnknownAndNodePanels$|^TestFleetStartupRejectsIncompleteServicesBeforeDatabase$|^TestFleetRoleRequiresEveryOwnedHandler$|^TestCombinedRoleUsesFleetDispatcherWithoutLocalCore$|^TestRolePreflightDoesNotOpenDatabase$|^TestDurableEnrollmentRequestRetainsIdentityAfterLostResponse$|^TestFleetPeriodicReset|^TestFleetNative|^TestNativeBindingAllocator|^TestNativeAddressPoolExhaustion|^TestFleetNetworkPreviewRejectsMissingNativeAddressPool$|^TestFleetAdmissionRateParticipantLimitRejectsCompleteSnapshotPostgres$'
log=scratchpad/node-v2/native-role-gate-linux.log
if go run ./tools/nodecore -core xray -go test -- -race -count=1 -timeout=10m -run "$selector" . ./internal/config ./internal/web ./internal/controlplane ./internal/web/service ./internal/cores/nodeconfig >"$log" 2>&1; then
    tail -n 30 "$log"
else
    tail -n 100 "$log"
    exit 1
fi
