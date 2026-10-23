#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
test -f "$root/internal/cores/noderuntime/native/runtime_linux.go"
mkdir -p "$root/scratchpad/node-v2"
output=$(mktemp -d "$root/scratchpad/node-v2/native-runtime.XXXXXX")
go test -race -count=1 ./internal/cores/noderuntime/native ./internal/node/worker
unshare --net --mount --propagation private env PENHOON_NATIVE_RUNTIME_TEST=1 \
  go test -race -count=1 -v -run '^TestNativeRuntimeWireGuardIsolated$' ./internal/cores/noderuntime/native
python3 - "$root" "$output" <<'PY'
import json
import pathlib
import sys
root, output = map(pathlib.Path, sys.argv[1:])
source = root / "internal/cores/noderuntime/native/runtime_linux.go"
before = "return r.control.Reconcile(ctx, assignments)"
after = "for i := range assignments { assignments[i].Grant.Policy.Enabled = true }; " + before
text = source.read_text()
if text.count(before) != 1:
    raise RuntimeError("native runtime overlay context changed")
replacement = output / "runtime.go"
replacement.write_text(text.replace(before, after))
(output / "overlay.json").write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
PY
if unshare --net --mount --propagation private env PENHOON_NATIVE_RUNTIME_TEST=1 \
  go test -race -count=1 -overlay "$output/overlay.json" -run '^TestNativeRuntimeWireGuardIsolated$' \
  ./internal/cores/noderuntime/native > "$output/negative.log" 2>&1; then
  echo 'Native runtime did not detect ignored client revocation.' >&2
  exit 1
fi
grep -q 'unauthorized client connected' "$output/negative.log"
echo "Native runtime qualification passed, including revocation negative control: $output"
