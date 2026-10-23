# Disposable systemd qualification guest

This harness exercises the real installed Go executables under systemd. It uses a synthetic local TLS enrollment/control service and TUF repository, not an existing panel or production account. Native networking is disabled; its unit sandbox is not changed by this test.

Run from the repository root on a Linux test host with QEMU, qemu-img, OVMF, mkfs.vfat, mcopy, Python 3 and Go available. The image and built bundle must both be under `scratchpad/node-v2`. At least 1.5 GiB available host memory and 6 GiB free workspace disk are required. No package installation is performed on the host.

The pinned image is Canonical's [Ubuntu 24.04 minimal release dated 2026-10-08](https://cloud-images.ubuntu.com/minimal/releases/noble/release-20261008/), `ubuntu-24.04-minimal-cloudimg-amd64.img`, SHA-256 `2f119f50bf29fe40e73b69ae2a736e8210d8146796f036e0324315b6b3a777df`. Verify its `SHA256SUMS.gpg` using the installed `/usr/share/keyrings/ubuntu-cloudimage-keyring.gpg` before use; Canonical documents the cloud-image signing fingerprint as `D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81` in its [checksum verification guide](https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/). The harness independently rechecks the pinned image hash.

```sh
bash tools/noderelease/build.sh "$PWD/scratchpad/node-v2/guest-bundle" linux-amd64
bash tools/nodekernel/vm/host.sh "$PWD/scratchpad/node-v2/vm-images/ubuntu-24.04-minimal-cloudimg-amd64.img" "$PWD/scratchpad/node-v2/guest-bundle"
```

The VM uses one vCPU, 768 MiB RAM, a 4 GiB sparse overlay and a 20-minute execution limit at reduced scheduling priority. It selects KVM when `/dev/kvm` is accessible, otherwise bounded TCG emulation. Its verified base image and FAT payload disk are read-only. There are no host port forwards, bridged devices, filesystem shares, or host firewall/network/service/account changes. Four missing Ubuntu packages (`nftables`, `libnftables1`, `libnftnl11`, `libjansson4`) are downloaded from the official archive, checked against pinned hashes taken from authenticated Ubuntu package metadata, and installed with `dpkg` only inside the guest. No host package installation or guest apt-index refresh is performed. Every disk, fixture, synthetic key and log remains in the workspace. No guest login or SSH access is required; guest SSH-key generation is disabled.

The guest checks actual service UIDs, capability masks, no-new-privileges, private state-file permissions, runtime peer-UID rejection, mTLS sessions, one-time enrollment, journal-file preservation, signed updates, intentional rollback and recovery from a deliberately failing signed runtime. It uses idle runtime configurations: this qualifies installation/supervision, not protocol traffic or fleet capacity. Existing protocol traffic tests remain required.

Only a serial line beginning `PENHOON_VM_RESULT PASS` confirms success. A timeout or an absent marker is a failed/incomplete qualification even if QEMU exits successfully. Logs and explicitly named disposable disks are retained for review; the harness never deletes unrelated files. Synthetic signing and enrollment secrets are not printed to the serial log. The fixture server refuses to run without the guest-only qualification marker.

The first software-emulated run on 9 October 2026 reached real systemd/NoCloud but stopped when guest `apt-get update` exceeded its 240-second limit, before node installation. That was a setup failure, not successful unit qualification. Offline prerequisite seeding removes that dependency from subsequent runs.

The second bounded run used offline prerequisites but entered the guest's emergency target after its boot-device discovery exceeded systemd's timeout for `/dev/disk/by-label/BOOT`. The test host had no `/dev/kvm`; the guest was stopped after that definite boot failure. Neither run qualifies the installed node units. The complete gate still needs a successful run on a host where the guest can boot and execute within its bounds; KVM is preferred.
