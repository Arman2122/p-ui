#!/usr/bin/env python3
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import secrets
import stat
import subprocess
import time
import uuid

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("node_installer", HERE / "node/install-node.py")
node = importlib.util.module_from_spec(spec)
spec.loader.exec_module(node)
ROLE_FILE = pathlib.Path("/etc/penhoon/role.json")
BASE = pathlib.Path("/usr/local/lib/penhoon-master")
STATE = pathlib.Path("/var/lib/penhoon-master")
BOOTSTRAP = pathlib.Path("/var/lib/penhoon-bootstrap")
ENV_FILE = pathlib.Path("/etc/penhoon/master.env")
CONTROL_FILE = pathlib.Path("/var/lib/penhoon-master/control.json")
LEGACY_PATHS = ("/usr/local/p-ui/p-ui", "/etc/default/p-ui", "/etc/p-ui")


def run(*args, environment=None, **kwargs):
    return subprocess.run(args, check=True, env=environment or node.ENVIRONMENT, **kwargs)


def protected_bytes(path, maximum, private=False):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & (0o077 if private else 0o022) or info.st_size > maximum:
            raise ValueError("role inputs must be protected, single-link root-owned files")
        return stream.read(maximum + 1)


def protected_json(path, private=False):
    return json.loads(protected_bytes(path, 131072, private), object_pairs_hook=node.unique_object)


def existing_role(role):
    if ROLE_FILE.exists() or ROLE_FILE.is_symlink():
        record = protected_json(ROLE_FILE)
        if not isinstance(record, dict) or type(record.get("version")) is not int or record["version"] != 1:
            raise ValueError("unsupported installation role format; existing state preserved")
        if record.get("role") != role:
            raise ValueError("role conversion is not supported; this installation belongs to " + str(record.get("role")))
        return record
    if any(pathlib.Path(path).exists() for path in LEGACY_PATHS):
        raise ValueError("ordinary panel installations cannot be converted; install a fresh role on a separate host")
    if pathlib.Path("/var/lib/penhoon-agent/credentials.db").exists() or BASE.exists():
        raise ValueError("unowned existing panel or node state cannot be adopted")
    return None


def save_role(record):
    node.write_file(ROLE_FILE, json.dumps(record).encode(), 0o600)


def panel_environment(role, dsn, local_node_id=""):
    if not dsn.strip() or any(char in dsn for char in "\x00\n\r"):
        raise ValueError("a one-line PostgreSQL DSN is required")
    values = {
        "PUI_ROLE": role,
        "PUI_DB_DSN": dsn,
        "PUI_FLEET_CONFIG": str(CONTROL_FILE),
        "PUI_LOG_FOLDER": "/var/log/penhoon-master",
        "PUI_BIN_FOLDER": str(BASE),
    }
    if local_node_id:
        values["PUI_LOCAL_FLEET_NODE_ID"] = local_node_id
    return values


def save_environment(values):
    content = "".join(key + "=" + json.dumps(value, ensure_ascii=False) + "\n" for key, value in values.items())
    node.write_file(ENV_FILE, content.encode(), 0o600)


def load_environment(role):
    values = {}
    for line in protected_bytes(ENV_FILE, 131072, True).decode().splitlines():
        key, separator, value = line.partition("=")
        if not separator or not re.fullmatch(r"PUI_[A-Z_]+", key):
            raise ValueError("invalid managed panel environment")
        values[key] = json.loads(value)
        if not isinstance(values[key], str):
            raise ValueError("invalid panel environment value")
    if values.get("PUI_ROLE") != role:
        raise ValueError("panel environment role conflicts with the installation")
    return values


def panel(values, *arguments, **kwargs):
    environment = dict(node.ENVIRONMENT, **values)
    return run("runuser", "-u", "penhoon-master", "--", str(BASE / "p-ui"), *arguments, environment=environment, **kwargs)


def host_configuration(options):
    if not options.control_address or not options.enrollment_url:
        raise ValueError("panel installation requires --control-address and --enrollment-url")
    config = {"version": 1, "authorityFile": str(STATE / "authority.json"), "controlListen": options.control_listen,
              "enrollmentListen": options.enrollment_listen, "controlAddress": options.control_address,
              "enrollmentURL": options.enrollment_url}
    if options.server_name:
        config["serverName"] = options.server_name
    for listen in (options.control_listen, options.enrollment_listen):
        if split_endpoint(listen)[1] < 1024:
            raise ValueError("the unprivileged master requires control listener ports of 1024 or higher")
    if options.role == "combined":
        loopback_package({"configuration": {"controlAddress": options.control_address, "enrollmentURL": options.enrollment_url, "serverName": options.server_name}}, config)
    return config


def split_endpoint(value):
    host, separator, port = value.rpartition(":")
    host = host.removeprefix("[").removesuffix("]")
    if not separator or not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError("invalid management endpoint")
    return host, int(port)


def loopback_address(listen):
    host, port = split_endpoint(listen)
    if host not in ("", "0.0.0.0", "::", "127.0.0.1", "::1"):
        raise ValueError("combined control listeners must accept loopback connections")
    return ("[::1]" if host in ("::", "::1") else "127.0.0.1") + ":" + str(port)


def loopback_package(package, host):
    config = package["configuration"]
    advertised, _ = split_endpoint(config["controlAddress"])
    name = config.get("serverName") or advertised
    if not name or "\x00" in name:
        raise ValueError("combined enrollment requires a verified TLS server name")
    config["serverName"] = name
    config["controlAddress"] = loopback_address(host["controlListen"])
    config["enrollmentURL"] = "https://" + loopback_address(host["enrollmentListen"]) + "/enroll"
    return package


def prepare_request(path, config, installation_id):
    expected_id = str(uuid.uuid5(uuid.UUID(installation_id), "local-node-enrollment"))
    if path.exists() or path.is_symlink():
        request = protected_json(path, True)
        if request.get("configuration") != config or request.get("requestId") != expected_id or request.get("name") != "local-node":
            raise ValueError("saved enrollment request does not match this installation")
        return request
    request = {"version": 1, "requestId": expected_id, "name": "local-node", "configuration": config,
               "enrollment": {"NodeID": str(uuid.uuid5(uuid.UUID(expected_id), "node")),
                              "InstallationID": str(uuid.uuid5(uuid.UUID(expected_id), "installation")),
                              "Ticket": secrets.token_urlsafe(32), "ExpiresUnixMilli": int(time.time() * 1000) + 600000}}
    node.write_file(path, json.dumps(request).encode(), 0o600)
    return request


def node_options(options, package=None):
    values = vars(options).copy()
    values["enrollment"] = str(package) if package else options.enrollment
    return argparse.Namespace(**values)


def node_enrolled(identity):
    binary = pathlib.Path("/usr/local/lib/penhoon/current/penhoon-agent")
    if not binary.exists() or not pathlib.Path("/var/lib/penhoon-agent/credentials.db").exists():
        return False
    result = run("runuser", "-u", "penhoon-agent", "--", str(binary), "state", "--state-dir", "/var/lib/penhoon-agent", stdout=subprocess.PIPE)
    saved = json.loads(result.stdout, object_pairs_hook=node.unique_object)
    if saved.get("identity") != identity:
        raise ValueError("existing node identity differs from this installation")
    return saved.get("enrolled") is True


def node_install_active():
    progress = pathlib.Path("/usr/local/lib/penhoon/install-progress.json")
    return progress.exists() and protected_json(progress, True).get("phase") == "active"


def check_node_services(native):
    binary = pathlib.Path("/usr/local/lib/penhoon/current/penhoon-agent")
    node.runtime_ready(binary)
    units = ["penhoon-agent.service", *("penhoon-runtime@" + name + ".service" for name in node.RUNTIMES)]
    if native:
        units.append("penhoon-native.service")
    for unit in units:
        run("systemctl", "is-active", "--quiet", unit, timeout=30)


def validate_host():
    release = {}
    for line in pathlib.Path("/etc/os-release").read_text().splitlines():
        key, separator, value = line.partition("=")
        if separator:
            release[key] = value.strip('"')
    distro, version = release.get("ID"), release.get("VERSION_ID", "")
    if not (distro == "ubuntu" and version in ("22.04", "24.04", "26.04") or distro == "debian" and version.split(".")[0].isdigit() and int(version.split(".")[0]) >= 12):
        raise ValueError("role installation requires supported Ubuntu or Debian")
    if not pathlib.Path("/run/systemd/system").is_dir():
        raise ValueError("systemd is required")
    for command in ("openssl", "runuser", "useradd", "systemctl", "pg_dump", "pg_restore"):
        if node.shutil.which(command, path=node.ENVIRONMENT["PATH"]) is None:
            raise ValueError("required management package is missing: " + command)


def verify_node_inputs(options):
    if not options.release_dir or not options.trusted_key:
        raise ValueError("node installation requires --release-dir and --trusted-key")
    source = pathlib.Path(options.release_dir).resolve(strict=True)
    manifest, encoded, _ = node.verify(source, pathlib.Path(options.trusted_key), node.platform_name(), not options.dry_run)
    node.check_assets(source, manifest)
    if not options.dry_run:
        if not options.tuf_root or not options.metadata_url or not options.targets_url:
            raise ValueError("node installation requires authenticated TUF root and repository endpoints")
        root_data = protected_bytes(pathlib.Path(options.tuf_root), 512000)
        return {"manifest": hashlib.sha256(encoded).hexdigest(), "publisher": hashlib.sha256(protected_bytes(pathlib.Path(options.trusted_key), 8192)).hexdigest(),
                "tufRoot": hashlib.sha256(root_data).hexdigest(), "metadataURL": options.metadata_url, "targetsURL": options.targets_url, "native": options.native}
    return None


def install_management_tools():
    destination = pathlib.Path("/usr/local/lib/penhoon-install")
    node.root_directory(destination)
    for relative in ("install.py", "role-menu.sh", "master/penhoon-master.service", "node/install-node.py", "node/penhoon-agent.service", "node/penhoon-runtime@.service", "node/penhoon-native.service"):
        target = destination / relative
        node.root_directory(target.parent)
        node.write_file(target, (HERE / relative).read_bytes(), 0o755 if relative.endswith((".py", ".sh")) else 0o644)
    node.root_directory(pathlib.Path("/usr/local/bin"))
    node.write_file(pathlib.Path("/usr/local/bin/penhoon"), (HERE / "role-menu.sh").read_bytes(), 0o755)


def install_panel(options, record, values, config):
    node.root_directory(BASE)
    account = node.account("penhoon-master")
    node.root_directory(STATE.parent)
    STATE.mkdir(mode=0o700, exist_ok=True)
    info = STATE.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, account.pw_uid):
        raise ValueError("master state has unexpected ownership")
    os.chown(STATE, account.pw_uid, account.pw_gid)
    os.chmod(STATE, 0o700)
    logs = pathlib.Path("/var/log/penhoon-master")
    node.root_directory(logs.parent)
    logs.mkdir(mode=0o700, exist_ok=True)
    info = logs.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, account.pw_uid):
        raise ValueError("master logs have unexpected ownership")
    os.chown(logs, account.pw_uid, account.pw_gid)
    os.chmod(logs, 0o700)
    node.write_file(BASE / "p-ui", options.panel_data, 0o755)
    save_environment(values)
    panel(values, "preflight", stdout=subprocess.DEVNULL)
    if not CONTROL_FILE.exists():
        arguments = ["fleet", "init", "--config", str(CONTROL_FILE), "--authority", config["authorityFile"],
                     "--control-listen", config["controlListen"], "--enrollment-listen", config["enrollmentListen"],
                     "--control-address", config["controlAddress"], "--enrollment-url", config["enrollmentURL"]]
        if config.get("serverName"):
            arguments.extend(("--server-name", config["serverName"]))
        panel(values, *arguments)
    else:
        if json.loads(CONTROL_FILE.read_bytes(), object_pairs_hook=node.unique_object) != config:
            raise ValueError("master control configuration cannot be changed by an installation retry")
    unit = pathlib.Path("/etc/systemd/system/penhoon-master.service")
    node.root_directory(unit.parent)
    node.write_file(unit, (HERE / "master/penhoon-master.service").read_bytes(), 0o644)
    run("systemctl", "daemon-reload")
    run("systemctl", "enable", "--now", "penhoon-master.service")
    run("systemctl", "is-active", "--quiet", "penhoon-master.service")
    record["phase"] = "panel-active"
    save_role(record)


def install_combined(options, record, values, config):
    node.root_directory(BOOTSTRAP)
    os.chmod(BOOTSTRAP, 0o700)
    request_path = BOOTSTRAP / "local-node-request.json"
    package_path = BOOTSTRAP / "local-node-enrollment.json"
    identity = record.get("localIdentity")
    if identity and node_install_active() and node_enrolled(identity):
        check_node_services(options.native)
        request_path.unlink(missing_ok=True)
        package_path.unlink(missing_ok=True)
        node.sync_directory(BOOTSTRAP)
    else:
        request = prepare_request(request_path, config, record["installationId"])
        if package_path.exists() or package_path.is_symlink():
            package = protected_json(package_path, True)
        else:
            result = panel(values, "fleet", "enroll", "--config", str(CONTROL_FILE), "--request-stdin",
                           input=json.dumps(request).encode(), stdout=subprocess.PIPE)
            package = loopback_package(json.loads(result.stdout, object_pairs_hook=node.unique_object), config)
            node.write_file(package_path, json.dumps(package).encode(), 0o600)
        expected = request["enrollment"]
        identity = package["configuration"]["identity"]
        if identity["NodeID"] != expected["NodeID"] or identity["InstallationID"] != expected["InstallationID"] or package["ticket"] != expected["Ticket"]:
            raise ValueError("bootstrap package does not match its durable enrollment request")
        record["localIdentity"] = identity
        save_role(record)
        node.install(node_options(options, package_path))
        if not node_enrolled(identity):
            raise ValueError("local node has not confirmed enrollment; bootstrap retained")
        request_path.unlink()
        package_path.unlink()
        node.sync_directory(BOOTSTRAP)
    record["localFleetNodeID"] = identity["NodeID"]
    values["PUI_LOCAL_FLEET_NODE_ID"] = identity["NodeID"]
    save_environment(values)
    save_role(record)
    run("systemctl", "restart", "penhoon-master.service")
    run("systemctl", "is-active", "--quiet", "penhoon-master.service")


def install(options):
    record = existing_role(options.role)
    if options.panel_command:
        if options.role == "node" or record is None:
            raise ValueError("a managed panel role is required")
        args = options.panel_arguments
        if args and args[0] == "--":
            args = args[1:]
        panel(load_environment(options.role), options.panel_command, *args)
        return
    if options.role == "node":
        node_inputs = verify_node_inputs(options)
        if not options.dry_run:
            if not options.enrollment:
                raise ValueError("node installation requires a private enrollment package")
            protected_json(pathlib.Path(options.enrollment), True)
            if record and record.get("node") != node_inputs:
                raise ValueError("same-role reinstall requires the original node release and trust inputs; use the signed updater")
            node.root_directory(ROLE_FILE.parent)
            if record is None:
                record = {"version": 1, "role": "node", "installationId": str(uuid.uuid4()), "phase": "prepared", "node": node_inputs}
                save_role(record)
            install_management_tools()
        if record and record.get("phase") == "active" and not options.dry_run:
            saved = protected_json(pathlib.Path("/usr/local/lib/penhoon/install-progress.json"), True)
            package = protected_json(pathlib.Path(options.enrollment), True)
            if saved.get("identity") != package["configuration"]["identity"]:
                raise ValueError("same-role reinstall cannot change node identity")
            check_node_services(options.native)
        else:
            node.install(node_options(options))
        if not options.dry_run:
            record["phase"] = "active"
            save_role(record)
        return
    if not options.panel_binary or not re.fullmatch(r"[0-9a-f]{64}", options.panel_sha256 or "") or not options.database_dsn_file:
        raise ValueError("panel roles require --panel-binary, authenticated --panel-sha256 and protected --database-dsn-file")
    options.panel_data = node.regular_bytes(pathlib.Path(options.panel_binary), 512 * 1024 * 1024)
    if hashlib.sha256(options.panel_data).hexdigest() != options.panel_sha256:
        raise ValueError("panel binary checksum does not match the authenticated release")
    dsn = protected_bytes(pathlib.Path(options.database_dsn_file), 65536, True).decode().strip()
    if options.role == "combined" and options.enrollment:
        raise ValueError("combined enrollment is created by this installation; do not supply an external package")
    config = host_configuration(options)
    values = panel_environment(options.role, dsn, record.get("localFleetNodeID", "") if record else "")
    desired = {"binarySHA256": options.panel_sha256, "databaseSHA256": hashlib.sha256(dsn.encode()).hexdigest(), "configuration": config}
    if record and record.get("panel") != desired:
        raise ValueError("installation retry requires the same role, panel release, database and control configuration")
    node_inputs = None
    if options.role == "combined":
        node_inputs = verify_node_inputs(options)
        if record and record.get("node") != node_inputs:
            raise ValueError("combined retry requires the original node release and trust inputs; use the signed updater")
    elif options.native:
        raise ValueError("master-only installation cannot enable native runtimes")
    if options.dry_run:
        if options.role == "combined":
            node.install(node_options(options))
        print(json.dumps({"role": options.role, "packages": ["python3", "openssl", "postgresql-client", "ca-certificates"], "panelUser": "penhoon-master", "localRuntime": False, "nodeServices": options.role == "combined"}))
        return
    if os.geteuid() != 0:
        raise ValueError("installation requires root")
    validate_host()
    node.root_directory(ROLE_FILE.parent)
    if record is None:
        record = {"version": 1, "role": options.role, "installationId": str(uuid.uuid4()), "phase": "prepared", "panel": desired, "node": node_inputs}
        save_role(record)
    install_management_tools()
    install_panel(options, record, values, config)
    if options.role == "combined":
        install_combined(options, record, values, config)
    record["phase"] = "active"
    save_role(record)
    print("Penhoon " + options.role + " installation ready.")


def main():
    parser = argparse.ArgumentParser(description="Fresh or same-role Penhoon installation; role conversion is not supported")
    parser.add_argument("--role", required=True, choices=("master", "node", "combined"))
    parser.add_argument("--panel-binary")
    parser.add_argument("--panel-sha256")
    parser.add_argument("--database-dsn-file")
    parser.add_argument("--control-listen", default=":7443")
    parser.add_argument("--enrollment-listen", default=":7444")
    parser.add_argument("--control-address")
    parser.add_argument("--enrollment-url")
    parser.add_argument("--server-name", default="")
    for name in ("release-dir", "trusted-key", "tuf-root", "metadata-url", "targets-url", "enrollment"):
        parser.add_argument("--" + name)
    parser.add_argument("--native", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--panel-command", choices=("setting", "preflight", "role"))
    parser.add_argument("panel_arguments", nargs=argparse.REMAINDER)
    options = parser.parse_args()
    try:
        if options.dry_run or options.panel_command:
            install(options)
        else:
            if os.geteuid() != 0:
                raise ValueError("installation requires root")
            node.root_directory(pathlib.Path("/run/penhoon-installer"))
            descriptor = os.open("/run/penhoon-installer/role.lock", os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            with os.fdopen(descriptor, "wb") as lock:
                info = os.fstat(lock.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o077:
                    raise ValueError("invalid role installation lock")
                fcntl.flock(lock, fcntl.LOCK_EX)
                install(options)
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit(str(error)) from None


if __name__ == "__main__":
    main()
