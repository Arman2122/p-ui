#!/usr/bin/env python3
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid


def run(*arguments, **options):
    return subprocess.run(arguments, check=True, **options)


def safe_member(name):
    value = pathlib.PurePosixPath(name)
    if value.is_absolute() or ".." in value.parts:
        raise ValueError("root archive path escapes its destination")
    return value


def extract_root(archive_path, root):
    root.mkdir()
    with tarfile.open(archive_path, "r:xz") as archive:
        members = archive.getmembers()
        for member in members:
            value = safe_member(member.name)
            if member.isfile() or member.isdir():
                archive.extract(member, root, numeric_owner=True, filter="fully_trusted")
            elif not (member.issym() or member.islnk()) and (not value.parts or value.parts[0] != "dev"):
                raise ValueError("unexpected special root archive entry")
        for member in members:
            if member.islnk():
                target = root / safe_member(member.linkname)
                if not target.is_file() or target.is_symlink():
                    raise ValueError("invalid root archive hardlink")
                os.link(target, root / safe_member(member.name))
        for member in members:
            if member.issym():
                destination = root / safe_member(member.name)
                if not destination.parent.resolve().is_relative_to(root.resolve()):
                    raise ValueError("root archive symlink parent escapes")
                destination.symlink_to(member.linkname)
                os.lchown(destination, member.uid, member.gid)


def validate_uid_range(base):
    for file in ("/etc/passwd", "/etc/group"):
        for line in pathlib.Path(file).read_text().splitlines():
            fields = line.split(":")
            if len(fields) > 2 and fields[2].isdigit() and base <= int(fields[2]) < base + 65536:
                raise ValueError("qualification UID/GID range is in use")
    for file in ("/etc/subuid", "/etc/subgid"):
        if not pathlib.Path(file).exists():
            continue
        for line in pathlib.Path(file).read_text().splitlines():
            _, start, length = line.split(":")
            if int(start) < base + 65536 and base < int(start) + int(length):
                raise ValueError("qualification range overlaps assigned subordinate IDs")
    initial = pathlib.Path("/proc/1/ns/user").readlink()
    for process in pathlib.Path("/proc").iterdir():
        if not process.name.isdigit():
            continue
        try:
            if (process / "ns/user").readlink() == initial:
                fields = dict(line.split(":", 1) for line in (process / "status").read_text().splitlines() if ":" in line)
                identifiers = " ".join(fields.get(field, "") for field in ("Uid", "Gid", "Groups"))
                if any(base <= int(identifier) < base + 65536 for identifier in identifiers.split()):
                    raise ValueError("qualification range overlaps a live host process identity")
                continue
            for file in ("uid_map", "gid_map"):
                for line in (process / file).read_text().splitlines():
                    _, start, length = map(int, line.split())
                    if start < base + 65536 and base < start + length:
                        raise ValueError("qualification range overlaps a live user namespace")
        except FileNotFoundError:
            continue


def cleanup_cgroup(group):
    if group.parent != pathlib.Path("/sys/fs/cgroup") or not group.name.startswith("penhoon-node-v2-qualification-"):
        raise ValueError("invalid owned cgroup cleanup path")
    if not group.exists():
        return
    (group / "cgroup.kill").write_text("1")
    deadline = time.monotonic() + 10
    while "populated 1" in (group / "cgroup.events").read_text():
        if time.monotonic() >= deadline:
            raise RuntimeError("test descendants remain; cgroup retained for inspection")
        time.sleep(0.1)
    for directory in sorted((item for item in group.rglob("*") if item.is_dir()), key=lambda item: len(item.parts), reverse=True):
        directory.rmdir()
    group.rmdir()


def read_guest_evidence(work):
    root = work / "rootfs"
    journal = root / "var/log/journal"
    if not journal.resolve().is_relative_to(root.resolve()):
        raise ValueError("guest journal path escapes its root")
    result = run("journalctl", "--directory=" + str(journal), "--no-pager", "--unit=penhoon-qualification.service", stdout=subprocess.PIPE)
    data = result.stdout.decode(errors="replace")
    (work / "guest-journal.log").write_text(data)
    marker = "PENHOON_SYSTEMD_NAMESPACE_RESULT PASS "
    for line in data.splitlines():
        if marker in line:
            evidence = json.loads(line.split(marker, 1)[1])
            if evidence.get("nativeEnabled") is not False or evidence.get("nonrootServices") != 5 or evidence.get("enrollments") != 1 or not all(evidence.get(field) is True for field in ("signedUpdate", "rollback", "failedActivationRecovery", "stateFilesPreserved")):
                raise ValueError("guest result does not satisfy the qualification gate")
            if evidence.get("mtlsSessions", 0) < 3 or evidence.get("heartbeats", 0) < 2:
                raise ValueError("guest control session evidence is incomplete")
            if evidence.get("journalRuntimes") != 4 or any(evidence.get(field, 0) < 4 for field in ("authorityReports", "timeSynchronizations", "creditPolls")):
                raise ValueError("guest journal, clock, authority, or credit protocol evidence is incomplete")
            (work / "qualification-evidence.json").write_text(json.dumps({"source": "guest persistent journal", "environment": "systemd-nspawn shared test-host kernel", "evidence": evidence}))
            return line
    raise ValueError("guest journal has no successful namespace qualification result")


def main():
    workspace = pathlib.Path.cwd().resolve()
    if os.geteuid() != 0 or len(sys.argv) != 2:
        raise ValueError("run as root with one built node bundle argument")
    bundle = pathlib.Path(sys.argv[1]).resolve(strict=True)
    if not bundle.is_relative_to(workspace / "scratchpad/node-v2"):
        raise ValueError("bundle is outside the qualification workspace")
    archive = workspace / "scratchpad/node-v2/vm-images/ubuntu-24.04-minimal-cloudimg-amd64-root.tar.xz"
    with archive.open("rb") as stream:
        if hashlib.file_digest(stream, "sha256").hexdigest() != "e322e57b71859d444258e18118e48550c243b68cb28befca629ea2573ef47ee6":
            raise ValueError("Ubuntu root archive hash mismatch")
    uid_base = 524288
    validate_uid_range(uid_base)
    work = pathlib.Path(tempfile.mkdtemp(prefix="systemd-namespace-", dir=workspace / "scratchpad/node-v2"))
    root = work / "rootfs"
    print("Preparing isolated systemd root:", work, flush=True)
    extract_root(archive, root)
    payload = root / "opt/penhoon-qualification"
    payload.mkdir(parents=True)
    copied_bundle = payload / "input-bundle"
    copied_bundle.mkdir()
    for binary in bundle.iterdir():
        if binary.name.startswith("penhoon-") and binary.is_file():
            shutil.copy2(binary, copied_bundle / binary.name)
    run("go", "build", "-trimpath", "-o", str(payload / "nodeguest"), "./tools/nodeguest", env={**os.environ, "CGO_ENABLED": "0"})
    run(str(payload / "nodeguest"), "prepare", "--bundle", str(copied_bundle), "--dir", str(payload / "fixture"))
    shutil.copytree(workspace / "deploy/node", payload / "deploy/node", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copy2(workspace / "tools/nodekernel/vm/guest.sh", payload / "guest.sh")
    run("bash", "tools/nodekernel/vm/fetch-prerequisites.sh")
    shutil.copytree(workspace / "scratchpad/node-v2/vm-prerequisites", payload / "prerequisites")
    (root / "etc/fstab").write_text("# Filesystems are private mounts supplied by the qualification launcher.\n")
    (root / "etc/machine-id").write_text("")
    units = root / "etc/systemd/system"
    for unit in ("cloud-init-local.service", "cloud-init.service", "cloud-config.service", "cloud-final.service", "systemd-networkd-wait-online.service", "systemd-udevd.service", "systemd-udev-trigger.service", "snapd.service", "snapd.socket", "snapd.seeded.service", "ssh.service", "ssh.socket", "apt-daily.service", "apt-daily-upgrade.service"):
        destination = units / unit
        destination.unlink(missing_ok=True)
        destination.symlink_to("/dev/null")
    runner = payload / "run-qualification.sh"
    runner.write_text("#!/bin/bash\ntouch /run/penhoon-namespace-qualification\nbash /opt/penhoon-qualification/guest.sh\nstatus=$?\nsystemctl poweroff\nexit $status\n")
    runner.chmod(0o755)
    (units / "penhoon-qualification.service").write_text("[Unit]\nDescription=Disposable isolated systemd qualification\nAfter=network.target\n[Service]\nType=oneshot\nExecStart=/opt/penhoon-qualification/run-qualification.sh\nTimeoutStartSec=600\nStandardOutput=journal+console\nStandardError=journal+console\n[Install]\nWantedBy=multi-user.target\n")
    wanted = units / "multi-user.target.wants"
    wanted.mkdir(exist_ok=True)
    (wanted / "penhoon-qualification.service").symlink_to("../penhoon-qualification.service")
    nspawn = workspace / "scratchpad/node-v2/namespace-tools/extracted/usr/bin/systemd-nspawn"
    group = pathlib.Path("/sys/fs/cgroup") / ("penhoon-node-v2-qualification-" + uuid.uuid4().hex)
    group.mkdir()
    try:
        (group / "memory.max").write_text(str(768 * 1024 * 1024))
        (group / "memory.swap.max").write_text("0")
        (group / "cpu.max").write_text("100000 100000")
        (group / "pids.max").write_text("512")
        (work / "owned-cgroup").write_text(str(group))
        def enter_cgroup():
            (group / "cgroup.procs").write_text(str(os.getpid()))
        command = ["unshare", "--mount", "--propagation", "private", "bash", str(workspace / "tools/nodekernel/namespace/launch.sh"), str(nspawn), str(root), str(uid_base)]
        print("Running shared-kernel systemd qualification in", group.name, flush=True)
        with (work / "systemd.log").open("wb") as output:
            process = subprocess.Popen(command, preexec_fn=enter_cgroup, stdout=output, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
            try:
                status = process.wait(timeout=600)
            except subprocess.TimeoutExpired:
                (group / "cgroup.kill").write_text("1")
                status = process.wait(timeout=10)
        data = (work / "systemd.log").read_text(errors="replace")
        (work / "exit-status").write_text(str(status))
        if status != 0:
            print(data[-14000:], file=sys.stderr)
            raise RuntimeError(f"isolated systemd qualification failed (exit {status}); evidence: {work}")
        print(read_guest_evidence(work))
        print("Shared-kernel namespace evidence retained:", work)
    finally:
        cleanup_cgroup(group)


if __name__ == "__main__":
    main()
