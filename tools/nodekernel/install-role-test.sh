#!/usr/bin/env bash
set -euo pipefail
if [[ $(id -u) != 0 ]] || [[ $(readlink /proc/self/ns/mnt) == $(readlink /proc/1/ns/mnt) ]]; then
    echo 'Run this fixture in a private mount namespace as root.' >&2
    exit 1
fi
mount --make-rprivate /
for directory in /etc /var/lib /var/log /usr/local /run; do
    mount -t tmpfs -o mode=0755,nodev,nosuid penhoon-role-fixture "$directory"
done
mkdir -p /run/systemd/system
printf 'ID=debian\nVERSION_ID=12\n' > /etc/os-release
python3 tools/nodekernel/install-role-test.py
