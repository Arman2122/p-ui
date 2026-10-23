#!/usr/bin/env bash
set -euo pipefail
directory=$(pwd -P)/scratchpad/node-v2
[[ -d "$directory" ]] || exit 90
CGO_ENABLED=0 go build -o "$directory/htb-burst-probe" tools/nodekernel/htb-burst-probe.go
PENHOON_HTB_BURST_PROBE=1 timeout 30s unshare --net "$directory/htb-burst-probe"
