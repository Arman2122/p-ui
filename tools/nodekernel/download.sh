#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
test -f "$root/internal/node/native/download_linux.go"
mkdir -p "$root/scratchpad/node-v2"
output=$(mktemp -d "$root/scratchpad/node-v2/native-download.XXXXXX")
queue_test() {
  unshare --net --mount --propagation private env PENHOON_NATIVE_QUEUE_TEST=1 "$@" \
    go test -race -count=1 -v -run '^TestNativeQueuedStopIsolated$' ./internal/node/native
}
unshare --net --mount --propagation private env PENHOON_NATIVE_ROUTE_TEST=1 \
  go test -race -count=1 -v -run '^TestNativeDownloadOwnershipIsolated$' ./internal/node/native
queue_test PENHOON_NATIVE_ROUTE_DRIFT=1
queue_test PENHOON_NATIVE_QUEUE_REDUCE=1
queue_test PENHOON_NATIVE_QUEUE_EXPIRY=1 PENHOON_NATIVE_QUEUE_IPV6=1
queue_test PENHOON_NATIVE_QUEUE_EXPIRY=1 PENHOON_NATIVE_CLOCK_SHIFT=1
queue_test PENHOON_NATIVE_GUARD_LOSS=1
python3 - "$root" "$output" <<'PY'
import json
import pathlib
import sys
root, output = map(pathlib.Path, sys.argv[1:])
source = root / "internal/node/native/nft_linux.go"
cases = [
    ("purge", "if err := p.purgeQueues(lease); err != nil {", "if err := error(nil); err != nil {"),
    ("clock", "remaining := lease.ExpiresUnixMilli - now.UnixMilli()", "remaining := lease.ExpiresUnixMilli - time.Now().UnixMilli()"),
    ("quarantine", "if owned {", "if owned && false {"),
    ("bypass", "add rule inet %s %s oifname @managed_ifaces drop\\n", "add rule inet %s %s oifname @managed_ifaces accept\\n"),
]
text = source.read_text()
for name, before, after in cases:
    if text.count(before) != 1:
        raise RuntimeError(f"native download overlay context changed: {name}")
    replacement = output / f"{name}.go"
    replacement.write_text(text.replace(before, after))
    (output / f"{name}.json").write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
PY
for variant in purge clock quarantine bypass; do
  flags=()
  case "$variant" in
    purge) flags+=(PENHOON_NATIVE_QUEUE_REDUCE=1) ;;
    clock) flags+=(PENHOON_NATIVE_QUEUE_EXPIRY=1 PENHOON_NATIVE_CLOCK_SHIFT=1) ;;
    quarantine) flags+=(PENHOON_NATIVE_GUARD_LOSS=1) ;;
    bypass) flags+=(PENHOON_NATIVE_ROUTE_DRIFT=1) ;;
  esac
  if unshare --net --mount --propagation private env PENHOON_NATIVE_QUEUE_TEST=1 "${flags[@]}" \
    go test -race -count=1 -overlay "$output/$variant.json" -run '^TestNativeQueuedStopIsolated$' \
    ./internal/node/native > "$output/$variant.log" 2>&1; then
    echo "Native download protection not detected when removed: $variant" >&2
    exit 1
  fi
  if [[ "$variant" == bypass ]]; then
    grep -q 'native route drift bypassed download gate' "$output/$variant.log"
  else
    grep -q 'queued downlink survived disabled apply' "$output/$variant.log"
  fi
done
echo "Native download qualification passed with four negative controls: $output"
