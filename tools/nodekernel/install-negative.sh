#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
work=$(mktemp -d "$root/scratchpad/node-v2/install-negative.XXXXXX")
python3 - "$work" <<'PY'
import pathlib
import sys
source = pathlib.Path("deploy/node/install-node.py").read_text()
guard = 'if manifest["sequence"] < high["sequence"] or manifest["sequence"] == high["sequence"] and digest != high["manifest"]:'
assert source.count(guard) == 1
pathlib.Path(sys.argv[1], "sequence.py").write_text(source.replace(guard, "if False:"))
guard = 'if (base / "activation.json").exists():'
assert source.count(guard) == 1
pathlib.Path(sys.argv[1], "journal.py").write_text(source.replace(guard, 'if (base / "activation.json").exists() and ledger["pending"] is not None:'))
guard = 'for unit in [*units, "penhoon-agent.service"]:'
assert source.count(guard) == 2
pathlib.Path(sys.argv[1], "health.py").write_text(source.replace(guard, 'for unit in [units[0]]:'))
guard = 'if type(manifest["stateFormat"]) is not int or manifest["stateFormat"] != STATE_FORMAT:'
assert source.count(guard) == 1
pathlib.Path(sys.argv[1], "format.py").write_text(source.replace(guard, 'if type(manifest["stateFormat"]) is not int or not 0 < manifest["stateFormat"] < 2**32:'))
PY
for control in sequence journal health format; do
unshare --mount --propagation private bash -s -- "$root" "$work" "$control" <<'SH'
set -euo pipefail
root=$1
work=$2
control=$3
mount --bind "$work/$control.py" "$root/deploy/node/install-node.py"
suite=tools/nodekernel/install-activation-test.sh
if [[ "$control" == format ]]; then suite=tools/nodekernel/install-test.sh; fi
if bash "$suite" > "$work/$control.log" 2>&1; then
  echo "Installer tests accepted removal of $control enforcement." >&2
  exit 1
fi
case "$control" in
  sequence) grep -q 'Invalid update was accepted: release' "$work/$control.log" ;;
  journal) grep -q 'active release pointer differs from its durable record' "$work/$control.log" ;;
  health) grep -q 'An inactive agent was accepted because another runtime was active' "$work/$control.log" ;;
  format) grep -q 'Unsupported state format 1 accepted by bootstrap' "$work/$control.log" ;;
esac
echo "Negative control reproduced the installer $control regression."
SH
done
