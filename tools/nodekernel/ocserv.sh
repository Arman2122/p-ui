#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
test -f "$root/internal/cores/noderuntime/native/ocserv_linux.go"
mkdir -p "$root/scratchpad/node-v2"
output=$(mktemp -d "$root/scratchpad/node-v2/ocserv.XXXXXX")
go test -race -count=1 ./internal/node/native ./internal/radius
for tcp in 0 1; do
  unshare --net --mount --propagation private env PENHOON_NATIVE_OCSERV_TEST=1 PENHOON_OCSERV_TCP="$tcp" \
    go test -race -count=1 -timeout 4m -v -run '^TestNativeOcservIsolated$' ./internal/cores/noderuntime/native
done
python3 - "$root" "$output" <<'PY'
import json
import pathlib
import sys
root, output = map(pathlib.Path, sys.argv[1:])
cases = [
    ('pending', 'internal/node/native/controller.go', '!r.SelectorsPending && slices.Equal', 'slices.Equal'),
    ('reservation', 'internal/node/native/controller.go', 'ok && r.Lease.ExpiresUnixMilli > time.Now().UnixMilli()', 'ok && false && r.Lease.ExpiresUnixMilli > time.Now().UnixMilli()'),
    ('journal', 'internal/node/native/controller.go', 'c.journal.selectors(before.Grant.ID, before.ID, union, true)', 'error(nil)'),
    ('guard', 'internal/cores/noderuntime/native/ocserv_linux.go', 'm.plane.gate.GuardDynamic(ctx, config.Prefix())', 'error(nil)'),
    ('source', 'internal/node/native/nft_linux.go',
     'fmt.Fprintf(&script, "add rule inet %s %s drop\\n", table, dir)',
     'if dir == "up" { fmt.Fprintf(&script, "add rule inet %s %s return\\n", table, dir) } else { fmt.Fprintf(&script, "add rule inet %s %s drop\\n", table, dir) }'),
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
replacements = {}
for filename in ['nft_linux.go', 'selectors_linux.go']:
    source = root / 'internal/node/native' / filename
    text = source.read_text()
    before = 'quota name %q counter name %q return\\n", table, n, dir, n, n+"_"+dir)'
    after = 'counter name %q return\\n", table, n, dir, n+"_"+dir)'
    if text.count(before) != 1:
        raise RuntimeError(f'quota negative overlay context changed: {filename}')
    replacement = output / f'quota-{filename}'
    replacement.write_text(text.replace(before, after))
    replacements[str(source)] = str(replacement)
(output / 'quota.json').write_text(json.dumps({'Replace': replacements}))
PY
for variant in pending reservation journal; do
  if go test -race -count=1 -overlay "$output/$variant.json" -run 'TestNativeDynamicSelectors|TestNativeSelector' \
    ./internal/node/native > "$output/$variant.log" 2>&1; then
    echo "Native selector $variant regression did not fail with protection removed." >&2
    exit 1
  fi
done
grep -q 'staged intent mistaken for kernel state' "$output/pending.log"
grep -q 'dynamic sessions reset or reissued credit' "$output/reservation.log"
grep -q 'recovery lost possible selector ownership' "$output/journal.log"
for variant in guard source quota rate; do
  if unshare --net --mount --propagation private env PENHOON_NATIVE_OCSERV_TEST=1 \
    go test -race -count=1 -timeout 4m -overlay "$output/$variant.json" -run '^TestNativeOcservIsolated$' \
    ./internal/cores/noderuntime/native > "$output/$variant.log" 2>&1; then
    echo "Ocserv $variant regression did not fail with protection removed." >&2
    exit 1
  fi
done
grep -q 'unguarded dynamic interface forwarded its first packet' "$output/guard.log"
grep -q 'spoofed source reached native input' "$output/source.log"
grep -q 'ocserv exceeded shared quota' "$output/quota.log"
grep -q 'ocserv shared rate exceeded' "$output/rate.log"
echo "Ocserv qualification passed, including seven negative controls: $output"
