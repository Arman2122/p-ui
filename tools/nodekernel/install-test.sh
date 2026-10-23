#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
mkdir -p "$root/scratchpad/node-v2"
work=$(mktemp -d "$root/scratchpad/node-v2/install-verification.XXXXXX")
mkdir "$work/release"
for binary in penhoon-agent penhoon-xray penhoon-ssh penhoon-singbox penhoon-mtproto penhoon-native penhoon-release; do
  printf 'signed test artifact: %s\n' "$binary" > "$work/release/$binary"
done
go run ./tools/noderelease keygen --key "$work/publisher.key" > "$work/publisher.pub"
go run ./tools/noderelease sign --key "$work/publisher.key" --dir "$work/release" --release test-build --platform linux-amd64 --sequence 1
python3 deploy/node/install-node.py --release-dir "$work/release" --trusted-key "$work/publisher.pub" --dry-run > "$work/plan.json"
python3 - "$work/plan.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as stream:
    plan = json.load(stream)
assert plan["release"] == "test-build" and plan["platform"] == "linux-amd64", plan
assert plan["runtimes"] == ["xray", "ssh", "singbox", "mtproto"], plan
PY
for format in 1 2 4; do
  go run ./tools/noderelease sign --key "$work/publisher.key" --dir "$work/release" --release test-build --platform linux-amd64 --sequence 2 --state-format "$format"
  if python3 deploy/node/install-node.py --release-dir "$work/release" --trusted-key "$work/publisher.pub" --dry-run > "$work/format-$format.log" 2>&1; then
    echo "Unsupported state format $format accepted by bootstrap." >&2
    exit 1
  fi
  grep -q 'unsupported durable state format' "$work/format-$format.log"
done
go run ./tools/noderelease sign --key "$work/publisher.key" --dir "$work/release" --release test-build --platform linux-amd64 --sequence 3
printf 'tampered' >> "$work/release/penhoon-ssh"
if python3 deploy/node/install-node.py --release-dir "$work/release" --trusted-key "$work/publisher.pub" --dry-run > "$work/rejected.log" 2>&1; then
  echo 'Installer accepted a changed signed asset.' >&2
  exit 1
fi
grep -q 'asset size changed' "$work/rejected.log"
echo "Installer signature and asset verification passed without installing services: $work"
