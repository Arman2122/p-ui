#!/usr/bin/env python3
import argparse
import contextlib
import fcntl
import grp
import hashlib
import json
import os
import pathlib
import pwd
import re
import shutil
import stat
import subprocess
import tempfile
import time
import uuid

BINARIES = ("penhoon-agent", "penhoon-xray", "penhoon-ssh", "penhoon-singbox", "penhoon-mtproto", "penhoon-native", "penhoon-release")
RUNTIMES = ("xray", "ssh", "singbox", "mtproto")
STATE_FORMAT = 3
ENVIRONMENT = {"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LANG": "C.UTF-8"}


def run(*arguments, **options):
    return subprocess.run(arguments, check=True, env=ENVIRONMENT, **options)


def guard_role():
    role_file = pathlib.Path("/etc/penhoon/role.json")
    if role_file.exists() or role_file.is_symlink():
        descriptor = os.open(role_file, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o022 or info.st_size > 131072:
                raise ValueError("invalid installation role file")
            record = json.loads(stream.read(131073), object_pairs_hook=unique_object)
            if not isinstance(record, dict) or type(record.get("version")) is not int or record["version"] != 1:
                raise ValueError("unsupported installation role format; existing state preserved")
            role = record.get("role")
        if role not in ("node", "combined"):
            raise ValueError("the headless node installer cannot change this host's role")
    elif any(pathlib.Path(path).exists() for path in ("/usr/local/p-ui/p-ui", "/etc/default/p-ui", "/etc/p-ui")):
        raise ValueError("ordinary panel installations cannot be converted to nodes")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate manifest key")
        result[key] = value
    return result


def regular_bytes(path, maximum):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > maximum:
            raise ValueError("unexpected file type or size")
        return stream.read(maximum + 1)


def verify(source, key, platform, require_root_key=False):
    encoded = regular_bytes(source / "manifest.json", 65536)
    signature = regular_bytes(source / "manifest.sig", 64)
    if len(signature) != 64:
        raise ValueError("invalid release signature length")
    descriptor = os.open(key, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 8192 or require_root_key and (info.st_uid != 0 or info.st_mode & 0o022):
            raise ValueError("the trusted publisher key must be a protected regular file")
        public_key = stream.read(8193)
    with tempfile.TemporaryDirectory(prefix="penhoon-verify-") as temporary:
        message = pathlib.Path(temporary) / "message"
        message.write_bytes(b"penhoon-node-release/v1\0" + encoded)
        key_copy = pathlib.Path(temporary) / "publisher.pem"
        key_copy.write_bytes(public_key)
        signature_copy = pathlib.Path(temporary) / "signature"
        signature_copy.write_bytes(signature)
        der = run("openssl", "pkey", "-pubin", "-in", str(key_copy), "-outform", "DER", stdout=subprocess.PIPE).stdout
        if len(der) != 44 or not der.startswith(bytes.fromhex("302a300506032b6570032100")):
            raise ValueError("an Ed25519 publisher key is required")
        run("openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(key_copy), "-rawin", "-in", str(message), "-sigfile", str(signature_copy), stdout=subprocess.DEVNULL)
    return parse_manifest(encoded, platform), encoded, signature


def parse_manifest(encoded, platform):
    manifest = json.loads(encoded, object_pairs_hook=unique_object)
    if set(manifest) != {"version", "release", "sequence", "platform", "stateFormat", "assets"}:
        raise ValueError("unsupported manifest fields")
    if type(manifest["version"]) is not int or manifest["version"] != 1 or manifest["platform"] != platform or type(manifest["sequence"]) is not int or not 0 < manifest["sequence"] < 2**64:
        raise ValueError("unsupported release version or platform")
    if type(manifest["stateFormat"]) is not int or manifest["stateFormat"] != STATE_FORMAT:
        raise ValueError("unsupported durable state format; format 3 required, existing journals preserved")
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,95}", manifest["release"]):
        raise ValueError("invalid release name")
    assets = manifest["assets"]
    if len(assets) != len(BINARIES) or {item["name"] for item in assets} != set(BINARIES):
        raise ValueError("incomplete release")
    for asset in assets:
        if set(asset) != {"name", "size", "sha256"} or type(asset["size"]) is not int or not 0 < asset["size"] <= 512 * 1024 * 1024 or not re.fullmatch(r"[0-9a-f]{64}", asset["sha256"]):
            raise ValueError("invalid release asset")
    return manifest


def check_assets(source, manifest, destination=None, protected=False):
    for asset in manifest["assets"]:
        descriptor = os.open(source / asset["name"], os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size != asset["size"]:
                raise ValueError("release asset size changed")
            if protected and (info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o022):
                raise ValueError("installed release assets must remain protected and root-owned")
            writer = open(destination / asset["name"], "xb") if destination else contextlib.nullcontext(None)
            with writer as output:
                digest = hashlib.sha256()
                count = 0
                while data := stream.read(1024 * 1024):
                    count += len(data)
                    if count > asset["size"]:
                        raise ValueError("release asset grew during copy")
                    digest.update(data)
                    if output:
                        output.write(data)
                if count != asset["size"] or digest.hexdigest() != asset["sha256"]:
                    raise ValueError("release asset checksum mismatch")
                if output:
                    output.flush()
                    os.fchmod(output.fileno(), 0o755)
                    os.fsync(output.fileno())


def account(name):
    try:
        value = pwd.getpwnam(name)
    except KeyError:
        run("useradd", "--system", "--user-group", "--home-dir", "/var/lib/" + name, "--shell", "/usr/sbin/nologin", name)
        value = pwd.getpwnam(name)
    if value.pw_uid == 0 or value.pw_dir != "/var/lib/" + name or value.pw_shell not in ("/usr/sbin/nologin", "/sbin/nologin", "/bin/false"):
        raise ValueError("refusing to adopt an interactive or privileged account")
    try:
        group = grp.getgrnam(name)
    except KeyError as error:
        raise ValueError("service account requires its dedicated primary group") from error
    if group.gr_gid == 0 or group.gr_gid != value.pw_gid:
        raise ValueError("service account requires its dedicated primary group")
    return value


def root_directory(path):
    current = pathlib.Path("/")
    for part in path.parts[1:]:
        current /= part
        try:
            current.mkdir(mode=0o755)
        except FileExistsError:
            pass
        info = current.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError("managed installation directories must be root-owned and protected")


def write_file(path, data, mode):
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".penhoon-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fchmod(stream.fileno(), mode)
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def sync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def platform_name():
    machine = os.uname().machine
    names = {"x86_64": "amd64", "aarch64": "arm64", "armv7l": "armv7", "armv6l": "armv6", "armv5tel": "armv5", "i686": "386", "s390x": "s390x"}
    if machine not in names:
        raise ValueError("unsupported node architecture")
    return "linux-" + names[machine]


def stage_release(base, source, manifest, encoded, signature=None):
    releases = base / "releases"
    root_directory(releases)
    destination = releases / f'{manifest["sequence"]}-{manifest["release"]}-{manifest["platform"]}'
    if destination.exists():
        root_directory(destination)
        if regular_bytes(destination / "manifest.json", 65536) != encoded:
            raise ValueError("existing release does not match the installation transaction")
        check_assets(destination, manifest, protected=True)
        return destination
    temporary = pathlib.Path(tempfile.mkdtemp(prefix=".staging-", dir=releases))
    try:
        check_assets(source, manifest, temporary)
        write_file(temporary / "manifest.json", encoded, 0o644)
        if signature is not None:
            write_file(temporary / "manifest.sig", signature, 0o644)
        os.chmod(temporary, 0o755)
        sync_directory(temporary)
        os.rename(temporary, destination)
        sync_directory(releases)
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)
    return destination


def release_record(destination, manifest, encoded):
    return {"directory": destination.name, "manifest": hashlib.sha256(encoded).hexdigest(), "sequence": manifest["sequence"], "stateFormat": manifest["stateFormat"]}


def record_release(base, record):
    if set(record) != {"directory", "manifest", "sequence", "stateFormat"} or not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,191}", record["directory"]):
        raise ValueError("invalid recorded release")
    destination = base / "releases" / record["directory"]
    root_directory(destination)
    encoded = regular_bytes(destination / "manifest.json", 65536)
    manifest = parse_manifest(encoded, platform_name())
    if release_record(destination, manifest, encoded) != record:
        raise ValueError("recorded release identity changed")
    check_assets(destination, manifest, protected=True)
    return destination


def switch_release(base, destination):
    current = base / "current"
    if current.exists() and not current.is_symlink():
        raise ValueError("current release path is not a managed symlink")
    pointer = base / (".current-" + uuid.uuid4().hex)
    try:
        pointer.symlink_to(destination)
        os.replace(pointer, current)
        sync_directory(base)
    finally:
        pointer.unlink(missing_ok=True)


def runtime_ready(binary):
    for attempt in range(6):
        try:
            run("runuser", "-u", "penhoon-agent", "--", str(binary), "check", "--runtimes", "/etc/penhoon/runtimes.json", timeout=15)
            return
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            if attempt == 5:
                raise
            time.sleep(1)


def activate_release(base, record, native):
    destination = record_release(base, record)
    units = [f"penhoon-runtime@{runtime}.service" for runtime in RUNTIMES]
    if native:
        units.append("penhoon-native.service")
    run("systemctl", "stop", "penhoon-agent.service", timeout=60)
    run("systemctl", "stop", *units, timeout=120)
    switch_release(base, destination)
    run("systemctl", "reset-failed", *units, "penhoon-agent.service", timeout=30)
    run("systemctl", "start", *units, timeout=120)
    runtime_ready(destination / "penhoon-agent")
    run("systemctl", "start", "penhoon-agent.service", timeout=60)
    for unit in [*units, "penhoon-agent.service"]:
        run("systemctl", "is-active", "--quiet", unit, timeout=30)


def save_ledger(base, ledger):
    write_file(base / "release-state.json", json.dumps(ledger).encode(), 0o600)


def protected_json(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
        raise ValueError("release state must be a protected root-owned regular file")
    return json.loads(regular_bytes(path, 16384), object_pairs_hook=unique_object)


def clear_activation(base):
    (base / "activation.json").unlink(missing_ok=True)
    sync_directory(base)


def fetch_authorized_release(base, helper, sequence):
    temporary = pathlib.Path(tempfile.mkdtemp(prefix=".authorized-", dir=base))
    destination = temporary / "release"
    try:
        run(str(helper), "fetch", "--state-dir", str(base / "tuf"), "--sequence", str(sequence), "--output", str(destination), stdout=subprocess.DEVNULL, timeout=600)
        encoded = regular_bytes(destination / "manifest.json", 65536)
        manifest = parse_manifest(encoded, platform_name())
        if manifest["sequence"] != sequence:
            raise ValueError("TUF release differs from the requested sequence")
        staged = stage_release(base, destination, manifest, encoded)
        return staged, manifest, encoded
    finally:
        shutil.rmtree(temporary)


def load_ledger(base):
    ledger = protected_json(base / "release-state.json")
    if set(ledger) != {"version", "active", "previous", "highWater", "pending", "native"} or ledger["version"] != 1 or type(ledger["native"]) is not bool:
        raise ValueError("invalid release state")
    record_release(base, ledger["active"])
    high = ledger["highWater"]
    if set(high) != {"sequence", "manifest"} or type(high["sequence"]) is not int or high["sequence"] < ledger["active"]["sequence"] or not re.fullmatch(r"[0-9a-f]{64}", high["manifest"]):
        raise ValueError("invalid release sequence high-water mark")
    if (base / "activation.json").exists():
        pending = protected_json(base / "activation.json")
        if set(pending) != {"target", "fallback", "previous"} or ledger["active"] not in (pending["target"], pending["fallback"]):
            raise ValueError("activation journal differs from the release state")
        record_release(base, pending["target"])
        record_release(base, pending["fallback"])
        if pending["target"]["stateFormat"] != pending["fallback"]["stateFormat"]:
            raise ValueError("activation journal crosses a state format boundary")
        target = pending["target"]
        if target["sequence"] == high["sequence"] and target["manifest"] != high["manifest"]:
            raise ValueError("activation journal conflicts with the accepted release sequence")
        if target["sequence"] >= high["sequence"]:
            ledger["highWater"] = {"sequence": target["sequence"], "manifest": target["manifest"]}
        ledger["pending"] = pending
    elif ledger["pending"] is not None:
        raise ValueError("pending activation is missing its durable journal")
    elif (base / "current").resolve(strict=True) != base / "releases" / ledger["active"]["directory"]:
        raise ValueError("active release pointer differs from its durable record")
    return ledger


def recover_activation(base, ledger):
    pending = ledger["pending"]
    if not pending:
        return
    if set(pending) != {"target", "fallback", "previous"} or ledger["active"] not in (pending["target"], pending["fallback"]):
        raise ValueError("invalid interrupted activation")
    activate_release(base, pending["fallback"], ledger["native"])
    ledger["active"], ledger["previous"], ledger["pending"] = pending["fallback"], pending["previous"], None
    save_ledger(base, ledger)
    clear_activation(base)


def update_locked(base, options):
    ledger = load_ledger(base)
    if ledger["pending"]:
        recover_activation(base, ledger)
        print("Interrupted activation recovered to the last committed release; rerun the requested operation.")
        return
    if options.recover:
        print("No interrupted release activation.")
        return
    helper = record_release(base, ledger["active"]) / "penhoon-release"
    destination, manifest, encoded = fetch_authorized_release(base, helper, options.sequence)
    digest = hashlib.sha256(encoded).hexdigest()
    high = ledger["highWater"]
    if manifest["sequence"] < high["sequence"] or manifest["sequence"] == high["sequence"] and digest != high["manifest"]:
        raise ValueError("release sequence is older than or conflicts with the accepted high-water mark")
    if manifest["stateFormat"] != ledger["active"]["stateFormat"]:
        raise ValueError("durable state format change requires a qualified migration workflow")
    if digest == ledger["active"]["manifest"]:
        print("The verified release is already active.")
        return
    if options.rollback:
        if not ledger["previous"]:
            raise ValueError("no previous committed release is available")
        previous_dir = record_release(base, ledger["previous"])
        previous_manifest = parse_manifest(regular_bytes(previous_dir / "manifest.json", 65536), platform_name())
        if manifest["assets"] != previous_manifest["assets"]:
            raise ValueError("signed rollback assets must match the previous committed release")
    target = release_record(destination, manifest, encoded)
    ledger["highWater"] = {"sequence": manifest["sequence"], "manifest": digest}
    previous = ledger["active"]
    pending = {"target": target, "fallback": previous, "previous": ledger["previous"]}
    write_file(base / "activation.json", json.dumps(pending).encode(), 0o600)
    ledger["pending"] = pending
    save_ledger(base, ledger)
    try:
        activate_release(base, target, ledger["native"])
        ledger["active"], ledger["previous"], ledger["pending"] = target, previous, None
        save_ledger(base, ledger)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        ledger["active"], ledger["pending"] = previous, pending
        try:
            recover_activation(base, ledger)
        except (OSError, ValueError, subprocess.SubprocessError) as rollback_error:
            raise ValueError(f"activation failed ({error}); recovery remains pending ({rollback_error}); run --recover") from error
        raise ValueError(f"activation failed; previous release restored; node credentials and accounting state preserved: {error}") from error
    clear_activation(base)
    print("Headless node release activated; durable node state preserved.")


def update(options):
    guard_role()
    if os.geteuid() != 0:
        raise ValueError("release activation requires root")
    if options.enrollment or options.native or options.trusted_key or options.dry_run or options.release_dir or options.tuf_root or options.metadata_url or options.targets_url:
        raise ValueError("update, rollback, and recovery use the installed identity, runtimes, and publisher trust")
    base = pathlib.Path("/usr/local/lib/penhoon")
    root_directory(base)
    descriptor = os.open(base / ".install.lock", os.O_WRONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "wb") as locked:
        info = os.fstat(locked.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o077:
            raise ValueError("invalid installer lock file")
        fcntl.flock(locked, fcntl.LOCK_EX)
        update_locked(base, options)


def install(options):
    guard_role()
    if not options.release_dir or not options.trusted_key:
        raise ValueError("installation requires --release-dir and --trusted-key")
    source = pathlib.Path(options.release_dir).resolve(strict=True)
    key = pathlib.Path(options.trusted_key).resolve(strict=True)
    platform = platform_name()
    manifest, encoded, signature = verify(source, key, platform, not options.dry_run)
    check_assets(source, manifest)
    if options.dry_run:
        print(json.dumps({"release": manifest["release"], "platform": platform, "runtimes": [*RUNTIMES, *( ["native"] if options.native else [])]}))
        return
    if not options.tuf_root or not options.metadata_url or not options.targets_url:
        raise ValueError("installation requires independently authenticated --tuf-root, --metadata-url and --targets-url")
    if os.geteuid() != 0:
        raise ValueError("installation requires root")
    release_info = {}
    for line in pathlib.Path("/etc/os-release").read_text().splitlines():
        if "=" in line:
            name, value = line.split("=", 1)
            release_info[name] = value.strip('"')
    distro = release_info.get("ID")
    version = release_info.get("VERSION_ID", "")
    if not (distro == "ubuntu" and version in ("22.04", "24.04", "26.04") or distro == "debian" and version.split(".")[0].isdigit() and int(version.split(".")[0]) >= 12):
        raise ValueError("the node installer requires a supported Ubuntu or Debian release")
    if not pathlib.Path("/run/systemd/system").is_dir():
        raise ValueError("systemd is required")
    if not options.enrollment:
        raise ValueError("a private enrollment package is required")
    enrollment = regular_bytes(pathlib.Path(options.enrollment), 128 * 1024)
    enrollment_identity = json.loads(enrollment, object_pairs_hook=unique_object)["configuration"]["identity"]
    required = ("openssl", "systemctl", "runuser", "useradd", "nft", "ip")
    if options.native:
        required += ("openvpn", "ocserv", "occtl")
    for executable in required:
        if shutil.which(executable, path=ENVIRONMENT["PATH"]) is None:
            raise ValueError("required system package is missing: " + executable)
    base = pathlib.Path("/usr/local/lib/penhoon")
    root_directory(base)
    root_directory(pathlib.Path("/etc/penhoon"))
    root_directory(pathlib.Path("/etc/systemd/system"))
    lock = os.open(base / ".install.lock", os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock, "wb") as locked:
        info = os.fstat(locked.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o077:
            raise ValueError("invalid installer lock file")
        fcntl.flock(locked, fcntl.LOCK_EX)
        progress_file = base / "install-progress.json"
        key_data = regular_bytes(key, 8192)
        root_data = regular_bytes(pathlib.Path(options.tuf_root), 512000)
        progress = {"manifest": hashlib.sha256(encoded).hexdigest(), "identity": enrollment_identity, "publisher": hashlib.sha256(key_data).hexdigest(), "tufRoot": hashlib.sha256(root_data).hexdigest(), "metadataURL": options.metadata_url, "targetsURL": options.targets_url, "native": options.native, "phase": "prepared"}
        if progress_file.exists():
            previous = json.loads(regular_bytes(progress_file, 8192), object_pairs_hook=unique_object)
            if any(previous.get(field) != progress[field] for field in ("manifest", "identity", "publisher", "tufRoot", "metadataURL", "targetsURL", "native")) or previous.get("phase") == "active":
                raise ValueError("existing installation requires its original recovery or update workflow")
            progress = previous
        elif pathlib.Path("/var/lib/penhoon-agent/credentials.db").exists() or (base / "current").is_symlink() or (base / "current").exists():
            raise ValueError("existing node state cannot be adopted by a fresh installation")
        write_file(progress_file, json.dumps(progress).encode(), 0o600)
        write_file(base / "publisher.pem", key_data, 0o644)
        agent = account("penhoon-agent")
        runtime_users = {runtime: account("penhoon-" + runtime) for runtime in RUNTIMES}
        native_users = {core: account("penhoon-" + core) for core in ("openvpn", "ocserv")} if options.native else {}
        bootstrap = base / "bootstrap"
        root_directory(bootstrap)
        bootstrap_dir = stage_release(bootstrap, source, manifest, encoded, signature)
        helper = bootstrap_dir / "penhoon-release"
        if not (base / "tuf").exists():
            copied_root = base / ".bootstrap-root.json"
            write_file(copied_root, root_data, 0o600)
            try:
                run(str(helper), "init", "--state-dir", str(base / "tuf"), "--root", str(copied_root), "--metadata-url", options.metadata_url, "--targets-url", options.targets_url, "--platform", platform, timeout=30)
            finally:
                copied_root.unlink(missing_ok=True)
        destination, authorized, authorized_data = fetch_authorized_release(base, helper, manifest["sequence"])
        if authorized_data != encoded:
            raise ValueError("bootstrap bundle differs from the TUF-authorized release")
        manifest = authorized
        record = release_record(destination, manifest, encoded)
        record_release(base, record)
        state = pathlib.Path("/var/lib/penhoon-agent")
        root_directory(state.parent)
        state.mkdir(mode=0o700, exist_ok=True)
        info = state.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, agent.pw_uid):
            raise ValueError("agent state directory has unexpected ownership")
        os.chown(state, agent.pw_uid, agent.pw_gid)
        os.chmod(state, 0o700)
        package = state / "enrollment.json"
        write_file(package, enrollment, 0o600)
        os.chown(package, agent.pw_uid, agent.pw_gid)
        binary = str(destination / "penhoon-agent")
        try:
            enrolled = False
            if (state / "credentials.db").exists():
                saved = json.loads(run("runuser", "-u", "penhoon-agent", "--", binary, "state", "--state-dir", str(state), stdout=subprocess.PIPE).stdout)
                if saved["identity"] != enrollment_identity:
                    raise ValueError("enrollment identity differs from the existing vault")
                enrolled = saved["enrolled"]
            if not enrolled:
                try:
                    run("runuser", "-u", "penhoon-agent", "--", binary, "enroll", "--state-dir", str(state), "--enrollment", str(package))
                except subprocess.CalledProcessError:
                    run("runuser", "-u", "penhoon-agent", "--", binary, "recover", "--state-dir", str(state))
        finally:
            package.unlink(missing_ok=True)
        progress["phase"] = "enrolled"
        write_file(progress_file, json.dumps(progress).encode(), 0o600)
        public = run("runuser", "-u", "penhoon-agent", "--", binary, "identity", "--state-dir", str(state), stdout=subprocess.PIPE).stdout
        write_file(pathlib.Path("/etc/penhoon/node.json"), public, 0o644)
        endpoints = [{"id": runtime, "core": runtime, "socket": f"/run/penhoon-{runtime}/control.sock", "uid": user.pw_uid} for runtime, user in runtime_users.items()]
        if options.native:
            run("modprobe", "wireguard")
            endpoints.append({"id": "native", "core": "native", "socket": "/run/penhoon-native/control.sock", "uid": 0})
        write_file(pathlib.Path("/etc/penhoon/runtimes.json"), json.dumps(endpoints).encode(), 0o644)
        environment = f"PENHOON_AGENT_UID={agent.pw_uid}\n"
        for core, user in native_users.items():
            prefix = "PUI_NATIVE_" + core.upper()
            environment += f"{prefix}_UID={user.pw_uid}\n{prefix}_GID={user.pw_gid}\n{prefix}_DIR=/run/penhoon-{core}\n"
        write_file(pathlib.Path("/etc/penhoon/node.env"), environment.encode(), 0o644)
        for name in ("penhoon-agent.service", "penhoon-runtime@.service", "penhoon-native.service"):
            write_file(pathlib.Path("/etc/systemd/system") / name, (pathlib.Path(__file__).parent / name).read_bytes(), 0o644)
        switch_release(base, destination)
        progress["phase"] = "configured"
        write_file(progress_file, json.dumps(progress).encode(), 0o600)
        run("systemctl", "daemon-reload")
        units = [f"penhoon-runtime@{runtime}.service" for runtime in RUNTIMES]
        if options.native:
            units.append("penhoon-native.service")
        run("systemctl", "enable", "--now", *units)
        runtime_ready(binary)
        run("systemctl", "enable", "--now", "penhoon-agent.service")
        for unit in [*units, "penhoon-agent.service"]:
            run("systemctl", "is-active", "--quiet", unit, timeout=30)
        save_ledger(base, {"version": 1, "active": record, "previous": None, "highWater": {"sequence": manifest["sequence"], "manifest": record["manifest"]}, "pending": None, "native": options.native})
        progress["phase"] = "active"
        write_file(progress_file, json.dumps(progress).encode(), 0o600)
        print("Headless node installed and enrolled.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Install or activate a signed Penhoon headless-node release")
    parser.add_argument("--release-dir")
    parser.add_argument("--trusted-key")
    parser.add_argument("--tuf-root")
    parser.add_argument("--metadata-url")
    parser.add_argument("--targets-url")
    parser.add_argument("--sequence", type=int)
    parser.add_argument("--enrollment")
    parser.add_argument("--native", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    operations = parser.add_mutually_exclusive_group()
    operations.add_argument("--update", action="store_true")
    operations.add_argument("--rollback", action="store_true")
    operations.add_argument("--recover", action="store_true")
    arguments = parser.parse_args()
    try:
        if (arguments.update or arguments.rollback) and (arguments.sequence is None or not 0 < arguments.sequence < 2**64):
            raise ValueError("update and rollback require an authorized --sequence")
        if not (arguments.update or arguments.rollback) and arguments.sequence is not None:
            raise ValueError("--sequence is only valid with update or rollback")
        if arguments.update or arguments.rollback or arguments.recover:
            update(arguments)
        else:
            install(arguments)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit(str(error)) from None
