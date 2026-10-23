#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'Usage: bash tools/nodekernel/vm/host.sh VERIFIED_UBUNTU_IMAGE BUILT_NODE_BUNDLE' >&2
  exit 1
fi
root=$(pwd -P)
image=$(realpath -e -- "$1")
bundle=$(realpath -e -- "$2")
case "$image" in "$root"/scratchpad/node-v2/*) ;; *) echo 'Image must remain in the qualification workspace.' >&2; exit 1;; esac
case "$bundle" in "$root"/scratchpad/node-v2/*) ;; *) echo 'Bundle must remain in the qualification workspace.' >&2; exit 1;; esac
for command in qemu-system-x86_64 qemu-img mkfs.vfat mcopy timeout tar python3; do command -v "$command" >/dev/null; done
python3 - "$root" <<'PY'
import pathlib, shutil, sys
fields = dict(line.split(":", 1) for line in pathlib.Path("/proc/meminfo").read_text().splitlines())
assert int(fields["MemAvailable"].split()[0]) >= 1536*1024, "at least 1.5 GiB available host memory is required"
assert shutil.disk_usage(sys.argv[1]).free >= 6*1024**3, "at least 6 GiB free workspace space is required"
PY
printf '%s  %s\n' 2f119f50bf29fe40e73b69ae2a736e8210d8146796f036e0324315b6b3a777df "$image" | sha256sum -c -
work=$(mktemp -d "$root/scratchpad/node-v2/vm-qualification.XXXXXX")
mkdir -p "$work/payload/deploy/node"
bash tools/nodekernel/vm/fetch-prerequisites.sh "$root/scratchpad/node-v2/vm-prerequisites"
cp -a "$root/scratchpad/node-v2/vm-prerequisites" "$work/payload/prerequisites"
CGO_ENABLED=0 go build -trimpath -o "$work/payload/nodeguest" ./tools/nodeguest
GOMAXPROCS=1 GOMEMLIMIT=256MiB "$work/payload/nodeguest" prepare --bundle "$bundle" --dir "$work/payload/fixture"
cp deploy/node/install-node.py deploy/node/*.service "$work/payload/deploy/node/"
cp tools/nodekernel/vm/guest.sh "$work/payload/guest.sh"
tar -C "$work/payload" -czf "$work/payload.tar.gz" .
cat > "$work/user-data" <<'CLOUD'
#cloud-config
users: []
ssh_pwauth: false
disable_root: true
ssh_deletekeys: false
ssh_genkeytypes: []
write_files:
  - path: /run/penhoon-vm-qualification
    permissions: '0600'
    content: disposable-guest-only
runcmd:
  - [bash, -c, 'mkdir -p /mnt/qualification /opt/penhoon-qualification; mount -o ro LABEL=cidata /mnt/qualification; tar -C /opt/penhoon-qualification -xzf /mnt/qualification/payload.tar.gz; bash /opt/penhoon-qualification/guest.sh']
power_state:
  mode: poweroff
  delay: now
  timeout: 30
  condition: true
CLOUD
printf 'instance-id: penhoon-qualification-%s\nlocal-hostname: penhoon-qualification\n' "$(basename "$work")" > "$work/meta-data"
truncate -s 256M "$work/seed.img"
mkfs.vfat -n cidata "$work/seed.img" >/dev/null
mcopy -i "$work/seed.img" "$work/user-data" ::user-data
mcopy -i "$work/seed.img" "$work/meta-data" ::meta-data
mcopy -i "$work/seed.img" "$work/payload.tar.gz" ::payload.tar.gz
qemu-img create -f qcow2 -F qcow2 -b "$image" "$work/guest.qcow2" 4G >/dev/null
cp /usr/share/OVMF/OVMF_VARS_4M.fd "$work/OVMF_VARS.fd"
acceleration=(-accel tcg,thread=single,tb-size=64 -cpu max)
if [[ -r /dev/kvm && -w /dev/kvm ]]; then acceleration=(-accel kvm -cpu host); fi
echo "Running isolated systemd guest (1 vCPU, 768 MiB, 20-minute limit, ${acceleration[1]}): $work"
set +e
timeout --kill-after=10s 1200s nice -n 19 qemu-system-x86_64 -nodefaults -no-user-config -machine q35 "${acceleration[@]}" -smp 1 -m 768 -display none -monitor none -serial "file:$work/serial.log" -pidfile "$work/qemu.pid" -smbios type=1,serial=ds=nocloud -no-reboot -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd -drive "if=pflash,format=raw,file=$work/OVMF_VARS.fd" -drive "if=virtio,format=qcow2,file=$work/guest.qcow2" -drive "if=virtio,format=raw,readonly=on,file=$work/seed.img" -object rng-random,filename=/dev/urandom,id=guest-rng -device virtio-rng-pci,rng=guest-rng,max-bytes=1024,period=1000 -netdev user,id=guestnet,ipv6=off -device virtio-net-pci,netdev=guestnet > "$work/qemu.log" 2>&1
status=$?
set -e
if ((status!=0)) || ! grep -q 'PENHOON_VM_RESULT PASS ' "$work/serial.log"; then
  echo "VM qualification did not pass (QEMU exit $status); logs retained in $work" >&2
  tail -n 60 "$work/serial.log" >&2
  cat "$work/qemu.log" >&2
  exit 1
fi
grep 'PENHOON_VM_RESULT PASS ' "$work/serial.log"
echo "Disposable guest evidence and disks retained under $work"
