#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
mkdir -p "$root/scratchpad/node-v2"
work=$(mktemp -d "$root/scratchpad/node-v2/tuf-negative.XXXXXX")
python3 - "$root" "$work" <<'PY'
import json
import pathlib
import sys
root, work = map(pathlib.Path, sys.argv[1:])
source = root / "internal/node/release/tuf.go"
data = source.read_text()
guard = 'if len(entry.Hashes) != 1 || entry.Length != asset.Size || hex.EncodeToString(entry.Hashes["sha256"]) != asset.SHA256 {'
assert data.count(guard) == 1
replacement = work / "tuf.go"
replacement.write_text(data.replace(guard, "if false {"))
(work / "overlay.json").write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
PY
if go test -count=1 -overlay "$work/overlay.json" -run 'TestTUFFetchEnforcesExpiryRollbackDelegationAndAssetBinding/artifact_set_binding' ./internal/node/release > "$work/result.log" 2>&1; then
  echo 'TUF test accepted a signed artifact from outside the selected release manifest.' >&2
  exit 1
fi
grep -q 'mismatched helper target: <nil>' "$work/result.log"
echo 'Negative control reproduced TUF artifact-set substitution without manifest binding.'
