#!/usr/bin/env bash
set -euo pipefail
if [[ $(id -u) != 0 ]] || [[ $(readlink /proc/self/ns/mnt) == $(readlink /proc/1/ns/mnt) ]]; then
  echo 'Run in a private mount namespace as root.' >&2
  exit 1
fi
root=$(pwd -P)
work=$(mktemp -d "$root/scratchpad/node-v2/install-activation.XXXXXX")
native_args=()
if [[ ${PENHOON_NATIVE_INSTALL_TEST:-0} == 1 ]]; then native_args=(--native); fi
mkdir "$work/release" "$work/mocks"
python3 - "$work" <<'PY'
import json
import pathlib
import sys
work = pathlib.Path(sys.argv[1])
agent = '''#!/usr/bin/python3
import json, os, pathlib, sys
args = sys.argv[1:]
command = args[0]
if "PUI_DB_DSN" in os.environ:
    raise SystemExit("database credentials leaked into node subprocess")
if command == "check":
    if "4-test-update" in sys.argv[0] and pathlib.Path("/run/fail-readiness").exists():
        raise SystemExit(42)
    raise SystemExit(0)
state = pathlib.Path(args[args.index("--state-dir")+1])
if command == "enroll":
    package = json.loads(pathlib.Path(args[args.index("--enrollment")+1]).read_text())
    (state / "public.json").write_text(json.dumps(package["configuration"]))
    (state / "credentials.db").write_text("synthetic fixture")
    count = state / "enrollment-count"
    count.write_text(str(int(count.read_text())+1 if count.exists() else 1))
elif command == "identity":
    print((state / "public.json").read_text())
elif command == "state":
    print(json.dumps({"enrolled": True, "identity": json.loads((state / "public.json").read_text())["identity"]}))
else:
    raise SystemExit("unexpected node operation")
'''
(work / "release/penhoon-agent").write_text(agent)
for name in ["xray", "ssh", "singbox", "mtproto", "native"]:
    (work / "release" / ("penhoon-"+name)).write_text("#!/bin/sh\nexit 0\n")
(work / "release/penhoon-release").write_text('''#!/usr/bin/python3
import pathlib, shutil, sys
args = sys.argv[1:]
state = pathlib.Path(args[args.index("--state-dir")+1])
if args[0] == "init":
    state.mkdir(mode=0o700)
elif args[0] == "fetch":
    if pathlib.Path("/run/fail-metadata").exists():
        raise SystemExit("synthetic expired repository metadata")
    source = pathlib.Path(pathlib.Path("/run/selected-release").read_text())
    output = pathlib.Path(args[args.index("--output")+1])
    shutil.copytree(source, output)
else:
    raise SystemExit("unexpected release helper operation")
''')
(work / "mocks/useradd").write_text('''#!/usr/bin/python3
import pathlib, sys
name = sys.argv[-1]
uid = 41000 + sum(1 for line in pathlib.Path("/etc/passwd").read_text().splitlines() if line.startswith("penhoon-"))
with open("/etc/passwd", "a") as stream:
    stream.write(f"{name}:x:{uid}:{uid}::/var/lib/{name}:/usr/sbin/nologin\\n")
with open("/etc/group", "a") as stream:
    stream.write(f"{name}:x:{uid}:\\n")
''')
(work / "mocks/runuser").write_text('''#!/usr/bin/python3
import os, sys
assert sys.argv[1:4] == ["-u", "penhoon-agent", "--"]
os.execv(sys.argv[4], sys.argv[4:])
''')
(work / "mocks/modprobe").write_text('''#!/usr/bin/python3
import pathlib, sys
assert sys.argv[1:] == ["wireguard"]
pathlib.Path("/run/native-module-request").write_text("wireguard")
''')
(work / "mocks/systemctl").write_text('''#!/usr/bin/python3
import os, pathlib, signal, sys
marker = pathlib.Path("/run/fail-first-activation")
if marker.exists():
    marker.unlink()
    raise SystemExit(42)
if sys.argv[1:] == ["is-active", "--quiet", "penhoon-agent.service"] and pathlib.Path("/run/fail-agent-active").exists():
    if pathlib.Path("/usr/local/lib/penhoon/current").resolve().name.startswith("5-"):
        raise SystemExit(3)
if sys.argv[1] == "start" and "penhoon-runtime@xray.service" in sys.argv:
    if pathlib.Path("/run/fail-all-start").exists():
        raise SystemExit(43)
    marker = pathlib.Path("/run/kill-updater")
    if marker.exists():
        marker.unlink()
        os.kill(os.getppid(), signal.SIGKILL)
with open("/run/systemctl-calls", "a") as stream:
    stream.write(" ".join(sys.argv[1:])+"\\n")
''')
for file in list((work / "release").iterdir()) + list((work / "mocks").iterdir()):
    file.chmod(0o755)
package = {"configuration": {"identity": {"ControlPlaneID": "test-plane", "NodeID": "test-node", "InstallationID": "test-install", "Serial": ""}}, "ticket": "synthetic", "expiresUnixMilli": 1}
(work / "enrollment.json").write_text(json.dumps(package))
(work / "tuf-root.json").write_text("synthetic bootstrap trust; metadata crypto is tested by Go fixtures")
PY
go run ./tools/noderelease keygen --key "$work/publisher.key" > "$work/publisher.pub"
go run ./tools/noderelease sign --key "$work/publisher.key" --dir "$work/release" --release integration-test --platform linux-amd64 --sequence 2
for specification in 3:3:test-update 3:3:conflicting 4:4:incompatible 4:3:test-update 5:3:test-update 6:3:test-update 7:3:authorized-rollback 8:3:test-update 9:1:experimental 10:2:experimental; do
  IFS=: read -r sequence format label <<< "$specification"
  directory="$work/release-$sequence-$label"
  mkdir "$directory"
  cp "$work/release/penhoon-"* "$directory/"
  go run ./tools/noderelease sign --key "$work/publisher.key" --dir "$directory" --release "$label" --platform linux-amd64 --sequence "$sequence" --state-format "$format"
done
mount --make-rprivate /
for directory in /etc /var/lib /usr/local /run; do mount -t tmpfs -o mode=0755 penhoon-install-test "$directory"; done
printf 'ID=debian\nVERSION_ID=12\n' > /etc/os-release
printf 'root:x:0:0::/root:/bin/sh\n' > /etc/passwd
printf 'root:x:0:\n' > /etc/group
printf 'passwd: files\ngroup: files\n' > /etc/nsswitch.conf
mkdir -p /run/systemd/system
for command in useradd runuser systemctl; do
  executable=$(command -v "$command")
  mount --bind "$work/mocks/$command" "$executable"
done
if [[ ${PENHOON_NATIVE_INSTALL_TEST:-0} == 1 ]]; then
  mount --bind "$work/mocks/modprobe" "$(command -v modprobe)"
fi
printf '%s' "$work/release" > /run/selected-release
python3 - <<'PY'
import importlib.util
import pathlib

spec = importlib.util.spec_from_file_location("account_installer_fixture", "deploy/node/install-node.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)
passwd = pathlib.Path("/etc/passwd")
groups = pathlib.Path("/etc/group")
original_passwd, original_groups = passwd.read_bytes(), groups.read_bytes()
for name in ("penhoon-master", "penhoon-agent", *("penhoon-" + core for core in installer.RUNTIMES), "penhoon-openvpn", "penhoon-ocserv"):
    for primary_gid, dedicated_gid in ((0, 0), (41000, 41001), (41000, None)):
        passwd.write_bytes(original_passwd + f"{name}:x:41000:{primary_gid}::/var/lib/{name}:/usr/sbin/nologin\n".encode())
        group_record = f"{name}:x:{dedicated_gid}:\n" if dedicated_gid is not None else "unrelated:x:41000:\n"
        groups.write_bytes(original_groups + group_record.encode())
        before = passwd.read_bytes(), groups.read_bytes()
        try:
            installer.account(name)
        except ValueError as error:
            assert str(error) == "service account requires its dedicated primary group", (name, primary_gid, dedicated_gid, error)
        else:
            raise AssertionError(("accepted privileged or mismatched primary group", name, primary_gid, dedicated_gid))
        assert (passwd.read_bytes(), groups.read_bytes()) == before
        assert not pathlib.Path("/etc/penhoon/node.env").exists()
        assert not pathlib.Path("/run/systemctl-calls").exists()
        assert not pathlib.Path("/usr/local/lib/penhoon").exists()
    passwd.write_bytes(original_passwd + f"{name}:x:41000:41000::/var/lib/{name}:/usr/sbin/nologin\n".encode())
    groups.write_bytes(original_groups + f"{name}:x:41000:\n".encode())
    before = passwd.read_bytes(), groups.read_bytes()
    adopted = installer.account(name)
    assert adopted.pw_name == name and adopted.pw_uid == 41000 and adopted.pw_gid == 41000
    assert (passwd.read_bytes(), groups.read_bytes()) == before
passwd.write_bytes(original_passwd)
groups.write_bytes(original_groups)
print("Installer dedicated primary group rejection passed for all service accounts")
PY
if [[ ${PENHOON_NATIVE_INSTALL_TEST:-0} == 1 ]]; then
  python3 - "$work" <<'PY'
import importlib.util
import pathlib
import pwd
import sys
import types
work = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("native_installer_fixture", "deploy/node/install-node.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)
original = installer.shutil.which
installer.shutil.which = lambda name, path=None: None if name == "ocserv" else original(name, path=path)
options = types.SimpleNamespace(native=True, dry_run=False, release_dir=str(work / "release"), trusted_key=str(work / "publisher.pub"),
    enrollment=str(work / "enrollment.json"), tuf_root=str(work / "tuf-root.json"), metadata_url="https://updates.example/metadata", targets_url="https://updates.example/targets")
try:
    installer.install(options)
except ValueError as error:
    assert str(error) == "required system package is missing: ocserv", error
else:
    raise AssertionError("native installation accepted a missing daemon")
assert all(not account.pw_name.startswith("penhoon-") for account in pwd.getpwall())
assert not pathlib.Path("/usr/local/lib/penhoon").exists()
installer.shutil.which = original
passwd, groups = pathlib.Path("/etc/passwd"), pathlib.Path("/etc/group")
original_passwd, original_groups = passwd.read_bytes(), groups.read_bytes()
for primary_gid, dedicated_gid in ((0, 0), (41000, 41001), (41000, None)):
    passwd.write_bytes(original_passwd + f"penhoon-agent:x:41000:{primary_gid}::/var/lib/penhoon-agent:/usr/sbin/nologin\n".encode())
    group_record = f"penhoon-agent:x:{dedicated_gid}:\n" if dedicated_gid is not None else "unrelated:x:41000:\n"
    groups.write_bytes(original_groups + group_record.encode())
    before = passwd.read_bytes(), groups.read_bytes()
    try:
        installer.install(options)
    except ValueError as error:
        assert str(error) == "service account requires its dedicated primary group", (primary_gid, dedicated_gid, error)
    else:
        raise AssertionError(("native installer adopted an invalid primary group", primary_gid, dedicated_gid))
    assert (passwd.read_bytes(), groups.read_bytes()) == before
    assert not pathlib.Path("/etc/penhoon/node.env").exists()
    assert not pathlib.Path("/run/systemctl-calls").exists()
    assert not pathlib.Path("/var/lib/penhoon-agent").exists()
    assert not pathlib.Path("/usr/local/lib/penhoon/current").exists()
passwd.write_bytes(original_passwd)
groups.write_bytes(original_groups)
print("Native installer rejected invalid primary groups before account, environment or activation changes")
PY
fi
activate_node() {
  local operation=$1 directory=$2 sequence
  printf '%s' "$directory" > /run/selected-release
  sequence=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["sequence"])' "$directory/manifest.json")
  python3 deploy/node/install-node.py "--$operation" --sequence "$sequence"
}
touch /run/fail-first-activation
if PUI_DB_DSN=must-not-reach-node python3 deploy/node/install-node.py "${native_args[@]}" --release-dir "$work/release" --trusted-key "$work/publisher.pub" --enrollment "$work/enrollment.json" --tuf-root "$work/tuf-root.json" --metadata-url https://updates.example/metadata --targets-url https://updates.example/targets > "$work/first.log" 2>&1; then
  echo 'Fault injection did not interrupt activation.' >&2
  exit 1
fi
PUI_DB_DSN=must-not-reach-node python3 deploy/node/install-node.py "${native_args[@]}" --release-dir "$work/release" --trusted-key "$work/publisher.pub" --enrollment "$work/enrollment.json" --tuf-root "$work/tuf-root.json" --metadata-url https://updates.example/metadata --targets-url https://updates.example/targets
python3 - <<'PY'
import json
import os
import pathlib
import pwd
import grp
state = pathlib.Path("/var/lib/penhoon-agent")
assert (state / "enrollment-count").read_text() == "1"
assert not (state / "enrollment.json").exists()
progress = json.loads(pathlib.Path("/usr/local/lib/penhoon/install-progress.json").read_text())
assert progress["phase"] == "active", progress
assert pathlib.Path("/usr/local/lib/penhoon/current").is_symlink()
native = os.getenv("PENHOON_NATIVE_INSTALL_TEST") == "1"
endpoints = json.loads(pathlib.Path("/etc/penhoon/runtimes.json").read_text())
assert len(endpoints) == 4 + int(native)
assert "penhoon-agent.service" in pathlib.Path("/run/systemctl-calls").read_text()
environment = dict(line.split("=", 1) for line in pathlib.Path("/etc/penhoon/node.env").read_text().splitlines())
if native:
    assert progress["native"] is True
    assert any(item["id"] == "native" and item["core"] == "native" and item["uid"] == 0 for item in endpoints)
    assert pathlib.Path("/run/native-module-request").read_text() == "wireguard"
    assert "penhoon-native.service" in pathlib.Path("/run/systemctl-calls").read_text()
    identifiers = set()
    for name in ("openvpn", "ocserv"):
        account = pwd.getpwnam("penhoon-" + name)
        group = grp.getgrgid(account.pw_gid)
        assert account.pw_uid != 0 and account.pw_gid != 0 and group.gr_name == account.pw_name
        assert account.pw_shell == "/usr/sbin/nologin" and account.pw_dir == "/var/lib/" + account.pw_name
        assert sum(line.startswith(account.pw_name + ":") for line in pathlib.Path("/etc/passwd").read_text().splitlines()) == 1
        assert account.pw_uid not in identifiers
        identifiers.add(account.pw_uid)
        prefix = "PUI_NATIVE_" + name.upper()
        assert environment[prefix + "_UID"] == str(account.pw_uid)
        assert environment[prefix + "_GID"] == str(account.pw_gid)
        assert environment[prefix + "_DIR"] == "/run/penhoon-" + name
else:
    assert not any(name.startswith("PUI_NATIVE_") for name in environment)
PY
python3 - <<'PY'
import pathlib
for name in ("agent", "xray", "ssh", "singbox", "mtproto", "native"):
    state = pathlib.Path("/var/lib/penhoon-" + name)
    state.mkdir(exist_ok=True)
    (state / "accounting.db").write_bytes(b"durable quota state; never restore an older backup")
PY
activate_node update "$work/release-3-test-update"
for directory in release release-3-conflicting release-4-incompatible; do
  if activate_node update "$work/$directory" > "$work/$directory.rejected" 2>&1; then
    echo "Invalid update was accepted: $directory" >&2
    exit 1
  fi
done
grep -q 'high-water mark' "$work/release.rejected"
grep -q 'high-water mark' "$work/release-3-conflicting.rejected"
grep -q 'unsupported durable state format' "$work/release-4-incompatible.rejected"
touch /run/fail-readiness
if activate_node update "$work/release-4-test-update" > "$work/readiness.log" 2>&1; then
  echo 'Readiness failure was accepted.' >&2
  exit 1
fi
grep -q 'previous release restored' "$work/readiness.log"
python3 - <<'PY'
import json, pathlib
base = pathlib.Path("/usr/local/lib/penhoon")
state = json.loads((base / "release-state.json").read_text())
assert state["active"]["sequence"] == 3 and state["highWater"]["sequence"] == 4 and state["pending"] is None, state
assert (base / "current").resolve().name == state["active"]["directory"]
PY
rm /run/fail-readiness
activate_node update "$work/release-4-test-update"
touch /run/fail-agent-active
if activate_node update "$work/release-5-test-update" > "$work/inactive.log" 2>&1; then
  echo 'An inactive agent was accepted because another runtime was active.' >&2
  exit 1
fi
grep -q 'previous release restored' "$work/inactive.log"
rm /run/fail-agent-active
touch /run/fail-all-start
if activate_node update "$work/release-5-test-update" > "$work/recovery.log" 2>&1; then
  echo 'Failed rollback was accepted.' >&2
  exit 1
fi
grep -q 'recovery remains pending' "$work/recovery.log"
rm /run/fail-all-start
python3 deploy/node/install-node.py --recover
python3 - "$work/release-6-test-update" <<'PY'
import importlib.util, json, pathlib, sys, types
spec = importlib.util.spec_from_file_location("installer", "deploy/node/install-node.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
base = pathlib.Path("/usr/local/lib/penhoon")
save = module.save_ledger
injected = 0
def fail_commit(base, ledger):
    global injected
    if injected == 0 and ledger["pending"] is None and ledger["active"]["sequence"] == 6:
        save(base, ledger)
        injected = 1
        raise OSError("injected directory-sync failure after commit rename")
    if injected == 1 and ledger["pending"] is None and ledger["active"]["sequence"] == 4:
        injected = 2
        raise OSError("injected disk write failure during recovery")
    save(base, ledger)
module.save_ledger = fail_commit
pathlib.Path("/run/selected-release").write_text(sys.argv[1])
try:
    module.update_locked(base, types.SimpleNamespace(recover=False, rollback=False, sequence=6))
except ValueError as error:
    assert "recovery remains pending" in str(error), error
else:
    raise AssertionError("commit failure was not propagated")
state = json.loads((base / "release-state.json").read_text())
assert injected == 2 and state["active"]["sequence"] == 6 and (base / "activation.json").is_file(), state
assert (base / "current").resolve().name.startswith("4-"), state
module.save_ledger = save
module.recover_activation(base, module.load_ledger(base))
state = json.loads((base / "release-state.json").read_text())
assert state["active"]["sequence"] == 4 and state["highWater"]["sequence"] == 6 and state["pending"] is None, state
PY
touch /run/kill-updater
if activate_node update "$work/release-6-test-update" > "$work/crash.log" 2>&1; then
  echo 'Crash injection failed.' >&2
  exit 1
fi
python3 deploy/node/install-node.py --recover
activate_node update "$work/release-6-test-update"
if activate_node rollback "$work/release-4-test-update" > "$work/old-rollback.log" 2>&1; then
  echo 'Unsigned downgrade authorization was accepted.' >&2
  exit 1
fi
activate_node rollback "$work/release-7-authorized-rollback"
activate_node rollback "$work/release-7-authorized-rollback"
if activate_node update "$work/release-5-test-update" > "$work/downgrade.log" 2>&1; then
  echo 'Rollback lowered the accepted sequence.' >&2
  exit 1
fi
for sequence in 9 10; do
  for operation in update rollback; do
    if activate_node "$operation" "$work/release-$sequence-experimental" > "$work/format-$sequence-$operation.log" 2>&1; then
      echo "Experimental state format activation was accepted: $sequence" >&2
      exit 1
    fi
    grep -q 'unsupported durable state format' "$work/format-$sequence-$operation.log"
  done
done
python3 - "$work" <<'PY'
import importlib.util
import json
import pathlib
import subprocess
import sys

work = pathlib.Path(sys.argv[1])
base = pathlib.Path("/usr/local/lib/penhoon")
spec = importlib.util.spec_from_file_location("format_installer", "deploy/node/install-node.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

def snapshot():
    paths = [pathlib.Path(name) for name in ("/etc/passwd", "/etc/group", "/etc/penhoon/node.env", "/etc/penhoon/node.json", "/run/systemctl-calls", "/var/lib/penhoon-agent/credentials.db", "/var/lib/penhoon-agent/enrollment-count")]
    paths += [base / name for name in ("release-state.json", "activation.json")]
    paths += [pathlib.Path("/var/lib/penhoon-" + name, "accounting.db") for name in ("agent", "xray", "ssh", "singbox", "mtproto", "native")]
    return str((base / "current").resolve()), {str(path): path.read_bytes() if path.exists() else None for path in paths}

def refused(name, arguments):
    before = snapshot()
    result = subprocess.run(["python3", "deploy/node/install-node.py", *arguments], capture_output=True, text=True)
    (work / ("format-boundary-" + name + ".log")).write_text(result.stdout + result.stderr)
    assert result.returncode != 0 and "unsupported durable state format" in result.stderr, result
    assert snapshot() == before, "format refusal mutated service, account, release or accounting state: " + name

for sequence in (9, 10):
    source = work / f"release-{sequence}-experimental"
    pathlib.Path("/run/selected-release").write_text(str(source))
    refused(f"{sequence}-bootstrap", ["--release-dir", str(source), "--trusted-key", str(work / "publisher.pub"), "--enrollment", str(work / "enrollment.json")])
    for operation in ("update", "rollback"):
        refused(f"{sequence}-{operation}", ["--" + operation, "--sequence", str(sequence)])
    encoded = (source / "manifest.json").read_bytes()
    manifest = json.loads(encoded)
    destination = installer.stage_release(base, source, manifest, encoded)
    old = installer.release_record(destination, manifest, encoded)
    active = json.loads((base / "release-state.json").read_bytes())["active"]
    for side in ("target", "fallback"):
        pending = {"target": old if side == "target" else active, "fallback": old if side == "fallback" else active, "previous": None}
        installer.write_file(base / "activation.json", json.dumps(pending).encode(), 0o600)
        refused(f"{sequence}-recover-{side}", ["--recover"])
        installer.clear_activation(base)
PY
touch /run/fail-metadata
if activate_node update "$work/release-8-test-update" > "$work/expired.log" 2>&1; then
  echo 'Update proceeded without fresh authorized metadata.' >&2
  exit 1
fi
grep -q 'synthetic expired repository metadata' "$work/expired.log"
chmod o+w /usr/local/lib/penhoon/current/penhoon-release
if python3 deploy/node/install-node.py --recover > "$work/permissions.log" 2>&1; then
  echo 'An unprotected installed release helper was accepted.' >&2
  exit 1
fi
grep -q 'installed release assets must remain protected and root-owned' "$work/permissions.log"
chmod o-w /usr/local/lib/penhoon/current/penhoon-release
python3 - <<'PY'
import json, pathlib
base = pathlib.Path("/usr/local/lib/penhoon")
state = json.loads((base / "release-state.json").read_text())
assert state["active"]["sequence"] == 7 and state["previous"]["sequence"] == 6 and state["highWater"]["sequence"] == 7 and state["pending"] is None, state
for name in ("agent", "xray", "ssh", "singbox", "mtproto", "native"):
    assert pathlib.Path("/var/lib/penhoon-" + name, "accounting.db").read_bytes() == b"durable quota state; never restore an older backup"
assert pathlib.Path("/var/lib/penhoon-agent/enrollment-count").read_text() == "1"
PY
echo "Installer activation/retry filesystem test passed with mocked account and service commands: $work"
