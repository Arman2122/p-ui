#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
test -f "$root/internal/cores/noderuntime/native/openvpn_linux.go"
mkdir -p "$root/scratchpad/node-v2"
output=$(mktemp -d "$root/scratchpad/node-v2/openvpn.XXXXXX")
go test -race -count=1 -run '^TestNode' ./internal/cores/internal/openvpn
unshare --net --mount --propagation private env PENHOON_NATIVE_OPENVPN_TEST=1 \
  go test -race -count=1 -v -run '^TestNativeOpenVPNIsolated$' ./internal/cores/noderuntime/native
unshare --net --mount --propagation private env PENHOON_NATIVE_OPENVPN_TEST=1 PENHOON_OPENVPN_TCP=1 \
  go test -race -count=1 -v -run '^TestNativeOpenVPNIsolated$' ./internal/cores/noderuntime/native
python3 - "$root" "$output" <<'PY'
import json
import pathlib
import sys
root, output = map(pathlib.Path, sys.argv[1:])
cases = [
    ('allocation', 'internal/cores/internal/openvpn/node_auth.go', 'if !a.used[address] {', 'if true {'),
    ('learn', 'internal/cores/internal/openvpn/node_learn_linux.go', 'if address == ip.String() {', 'if address == ip.String() || true {'),
    ('quota', 'internal/node/native/nft_linux.go',
     'quota name %q counter name %q return\\n", table, n, dir, n, n+"_"+dir)',
     'counter name %q return\\n", table, n, dir, n+"_"+dir)'),
    ('rate', 'internal/node/native/shape_linux.go', 'bytesPerSecond := rate.BitsPerSecond / 8', 'bytesPerSecond := rate.BitsPerSecond / 4'),
]
for name, filename, before, after in cases:
    source = root / filename
    text = source.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f'negative overlay context changed: {name}')
    replacement = output / f'{name}.go'
    replacement.write_text(text.replace(before, after))
    (output / f'{name}.json').write_text(json.dumps({'Replace': {str(source): str(replacement)}}))
PY
if go test -race -count=1 -overlay "$output/allocation.json" \
  -run '^TestNodeManagementReservesBeforeEstablishment$' ./internal/cores/internal/openvpn > "$output/allocation.log" 2>&1; then
  echo 'OpenVPN address-allocation regression did not detect a duplicate reservation.' >&2
  exit 1
fi
grep -q 'management got' "$output/allocation.log"
for variant in learn quota rate; do
  if unshare --net --mount --propagation private env PENHOON_NATIVE_OPENVPN_TEST=1 \
    go test -race -count=1 -overlay "$output/$variant.json" -run '^TestNativeOpenVPNIsolated$' \
    ./internal/cores/noderuntime/native > "$output/$variant.log" 2>&1; then
    echo "OpenVPN $variant regression did not fail with protection removed." >&2
    exit 1
  fi
done
grep -q 'learn-address ownership 10.238.0.4' "$output/learn.log"
grep -q 'OpenVPN exceeded its shared credit' "$output/quota.log"
grep -q 'shared OpenVPN rate exceeded' "$output/rate.log"
echo "OpenVPN qualification passed, including four negative controls: $output"
