#!/usr/bin/env bash
set -euo pipefail
[[ "$(readlink /proc/self/ns/mnt)" != "$(readlink /proc/1/ns/mnt)" ]] || exit 90
mount -t tmpfs -o mode=0755,nodev,nosuid penhoon-private-run /run
export SYSTEMD_LOG_TARGET=console SYSTEMD_COLORS=0
echo 'Starting isolated systemd PID1; host registration and networking are disabled.'
exec nice -n 19 "$1" --directory "$2" --boot --machine "penhoon-qualification-$$" --settings=no --register=no --keep-unit --link-journal=no --resolv-conf=off --timezone=off --private-network --private-users="$3:65536" --private-users-ownership=chown --console=pipe
