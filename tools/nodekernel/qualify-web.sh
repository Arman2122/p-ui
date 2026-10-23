#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
if [[ ${PENHOON_WEB_QUALIFICATION_NS:-} != 1 ]]; then
  exec unshare --net --mount --propagation private env PENHOON_WEB_QUALIFICATION_NS=1 bash "$0"
fi
for kind in net mnt; do
  [[ $(readlink "/proc/self/ns/$kind") != $(readlink "/proc/1/ns/$kind") ]] || exit 1
done
[[ $(id -u) == 0 ]] || exit 1
ip link set lo up
export PENHOON_WEB_HTTPS_PUBLIC=1
go run ./tools/nodecore -core mtproto -race -run 'TestDirectWEB|TestWEBRoutedCover|TestWEBCoverCancellation|TestManagedWEB'
python3 tools/nodekernel/web-cover-negative.py
python3 tools/nodekernel/web-https-negative.py
go run ./tools/nodecore -core mtproto -lint
