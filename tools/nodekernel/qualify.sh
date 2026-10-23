#!/usr/bin/env bash
set -euo pipefail

if [[ $(id -u) != 0 ]]; then
  echo 'Native kernel qualification requires a root Linux test runner.' >&2
  exit 1
fi
root=$(pwd -P)
test -f "$root/internal/node/native/nft_linux.go"
mkdir -p "$root/scratchpad/node-v2"
run_dir=$(mktemp -d "$root/scratchpad/node-v2/native-qualification.XXXXXX")
mkdir "$run_dir/tmp"
export TMPDIR="$run_dir/tmp"
unset PENHOON_NATIVE_KERNEL_TEST PENHOON_NATIVE_WIREGUARD_TEST

go test -race -count=1 ./internal/node/native
for wireguard in 0 1; do
  unshare --net --mount --propagation private env PENHOON_NATIVE_KERNEL_TEST=1 \
    PENHOON_NATIVE_WIREGUARD_TEST="$wireguard" \
    go test -race -count=1 -v -run '^TestNativeKernelIsolated$' ./internal/node/native
done

python3 - "$root" "$run_dir" <<'PY'
import json
import pathlib
import sys

root, output = map(pathlib.Path, sys.argv[1:])
cases = [
    ("journal", "internal/node/native/journal.go", "r.Reserved += bytes", "r.Reserved += 0"),
    ("quota", "internal/node/native/nft_linux.go",
     'quota name %q counter name %q return\\n", table, n, dir, n, n+"_"+dir)',
     'counter name %q return\\n", table, n, dir, n+"_"+dir)'),
    ("rate", "internal/node/native/shape_linux.go", "bytesPerSecond := rate.BitsPerSecond / 8", "bytesPerSecond := rate.BitsPerSecond / 4"),
    ("replacement", "internal/node/state/runtime_journal.go", "!bytes.Equal(previous, []byte(journalID))", "!bytes.Equal(previous, []byte(journalID)) && false"),
    ("units", "internal/node/native/nft_linux.go", "lease.ExpiresUnixMilli/1000", "uint64(lease.ExpiresUnixMilli)*1000000"),
]
for name, filename, before, after in cases:
    source = root / filename
    text = source.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f"negative overlay context changed: {name}")
    replacement = output / f"{name}.go"
    replacement.write_text(text.replace(before, after))
    (output / f"{name}.json").write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
source = root / "internal/node/native/nft_linux.go"
text = source.read_text()
for before, after in [
    ("lease.ExpiresUnixMilli/1000", "max(lease.ExpiresUnixMilli/1000, time.Now().Add(time.Hour).Unix())"),
    ("remaining := lease.ExpiresUnixMilli - now.UnixMilli()", "remaining := int64(24*time.Hour/time.Millisecond)"),
]:
    if text.count(before) != 1:
        raise RuntimeError("negative expiry overlay context changed")
    text = text.replace(before, after)
replacement = output / "expiry.go"
replacement.write_text(text)
(output / "expiry.json").write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
PY

if go test -race -count=1 -overlay "$run_dir/journal.json" \
  -run '^TestNativeReservationSurvivesLostKernel$' ./internal/node/native > "$run_dir/journal-negative.log" 2>&1; then
  echo 'Durable-reservation regression did not fail with protection removed.' >&2
  exit 1
fi
grep -q 'replayed credit or class' "$run_dir/journal-negative.log"
if unshare --net --mount --propagation private env PENHOON_NATIVE_KERNEL_TEST=1 \
  PENHOON_NATIVE_WIREGUARD_TEST=1 go test -race -count=1 -overlay "$run_dir/quota.json" \
  -run '^TestNativeKernelIsolated$' ./internal/node/native > "$run_dir/quota-negative.log" 2>&1; then
  echo 'WireGuard quota regression did not fail with protection removed.' >&2
  exit 1
fi
grep -q 'combined quota' "$run_dir/quota-negative.log"
if unshare --net --mount --propagation private env PENHOON_NATIVE_KERNEL_TEST=1 \
  PENHOON_NATIVE_WIREGUARD_TEST=1 go test -race -count=1 -overlay "$run_dir/rate.json" \
  -run '^TestNativeKernelIsolated$' ./internal/node/native > "$run_dir/rate-negative.log" 2>&1; then
  echo 'Shared-rate regression did not fail with doubled rate.' >&2
  exit 1
fi
grep -q 'multiplied rate' "$run_dir/rate-negative.log"
if go test -race -count=1 -overlay "$run_dir/replacement.json" \
  -run '^TestWorkerRefusesReplacementCreditJournal$' ./internal/node/worker > "$run_dir/replacement-negative.log" 2>&1; then
  echo 'Journal-replacement regression did not fail with protection removed.' >&2
  exit 1
fi
grep -q 'accepted replacement credit journal' "$run_dir/replacement-negative.log"
if unshare --net --mount --propagation private env PENHOON_NATIVE_KERNEL_TEST=1 \
  PENHOON_NATIVE_WIREGUARD_TEST=1 go test -race -count=1 -overlay "$run_dir/expiry.json" \
  -run '^TestNativeKernelIsolated$' ./internal/node/native > "$run_dir/expiry-negative.log" 2>&1; then
  echo 'Kernel-expiry regression did not fail with the deadline extended.' >&2
  exit 1
fi
grep -q 'expired kernel authorization accepted traffic' "$run_dir/expiry-negative.log"
if unshare --net --mount --propagation private env PENHOON_NATIVE_KERNEL_TEST=1 \
  PENHOON_NATIVE_WIREGUARD_TEST=1 go test -race -count=1 -overlay "$run_dir/units.json" \
  -run '^TestNativeKernelIsolated$' ./internal/node/native > "$run_dir/units-negative.log" 2>&1; then
  echo 'Kernel deadline read-back did not catch incorrect time units.' >&2
  exit 1
fi
grep -q 'kernel expiry encoded in wrong units' "$run_dir/units-negative.log"
echo "Native qualification passed, including negative controls: $run_dir"
