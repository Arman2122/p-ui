#!/usr/bin/env bash
set -euo pipefail
environment=$(systemd-detect-virt)
case "$environment" in
  qemu) [[ -f /run/penhoon-vm-qualification ]] || exit 80; result=PENHOON_VM_RESULT ;;
  systemd-nspawn) [[ -f /run/penhoon-namespace-qualification ]] || exit 80; result=PENHOON_SYSTEMD_NAMESPACE_RESULT ;;
  *) exit 81 ;;
esac
export PENHOON_QUALIFICATION_RESULT="$result"
[[ $(cat /proc/1/comm) == systemd ]] || exit 82
root=/opt/penhoon-qualification
cd "$root"
report_failure() {
  local status=${1:-$?}
  if ((status != 0)); then
    echo "$result FAIL exit=$status"
    journalctl --no-pager -n 100 -u penhoon-agent.service -u 'penhoon-runtime@*' || true
  fi
}
trap report_failure EXIT
export DEBIAN_FRONTEND=noninteractive
echo "$result PHASE prerequisites"
(cd prerequisites && sha256sum -c SHA256SUMS)
timeout 120 dpkg -i prerequisites/libjansson4_*.deb prerequisites/libnftnl11_*.deb prerequisites/libnftables1_*.deb prerequisites/nftables_*.deb
cp fixture/ca.crt /usr/local/share/ca-certificates/penhoon-qualification.crt
update-ca-certificates >/dev/null
./nodeguest serve --dir "$root/fixture" > fixture.log 2>&1 &
fixture_pid=$!
trap 'status=$?; kill "$fixture_pid" 2>/dev/null || true; report_failure "$status"' EXIT
for attempt in $(seq 1 30); do
  if curl --fail --silent --cacert fixture/ca.crt https://localhost:7444/stats > initial-stats.json; then break; fi
  sleep 1
done
export PUI_DB_DSN=synthetic-must-not-reach-node
echo "$result PHASE install"
python3 deploy/node/install-node.py --release-dir "$root/fixture/bundles/1" --trusted-key "$root/fixture/publisher.pem" --enrollment "$root/fixture/enrollment.json" --tuf-root "$root/fixture/root.json" --metadata-url https://localhost:7444/metadata --targets-url https://localhost:7444/targets
runuser -u penhoon-agent -- /usr/local/lib/penhoon/current/penhoon-agent check --runtimes /etc/penhoon/runtimes.json
if runuser -u penhoon-ssh -g penhoon-agent -- /usr/local/lib/penhoon/current/penhoon-agent check --runtimes /etc/penhoon/runtimes.json > unauthorized.log 2>&1; then
  echo 'A different runtime user controlled a peer runtime.' >&2
  exit 83
fi
python3 - <<'PY'
import json, os, pathlib, pwd, subprocess
state = {}
for name in ["agent", "xray", "ssh", "singbox", "mtproto"]:
    unit = "penhoon-agent.service" if name == "agent" else "penhoon-runtime@"+name+".service"
    pid = int(subprocess.check_output(["systemctl", "show", "--property=MainPID", "--value", unit]))
    assert pid > 0, unit
    fields = dict(line.split(":", 1) for line in pathlib.Path(f"/proc/{pid}/status").read_text().splitlines() if ":" in line)
    expected = pwd.getpwnam("penhoon-"+name).pw_uid
    assert expected != 0 and int(fields["Uid"].split()[1]) == expected, unit
    assert fields["NoNewPrivs"].strip() == "1", unit
    assert int(fields["CapEff"], 16) == (0 if name == "agent" else 1<<10), unit
    assert b"PUI_DB_DSN=" not in pathlib.Path(f"/proc/{pid}/environ").read_bytes(), unit
    directory = pathlib.Path("/var/lib/penhoon-"+name)
    assert directory.stat().st_mode & 0o777 == 0o700, directory
    (directory / "qualification-sentinel").write_bytes(b"preserve durable local state")
    for file in directory.glob("*.db"):
        assert file.stat().st_mode & 0o777 == 0o600, file
        state[str(file)] = file.stat().st_ino
pathlib.Path("state-inodes.json").write_text(json.dumps(state))
PY
sleep 12
systemctl restart penhoon-agent.service
echo "$result PHASE update"
python3 deploy/node/install-node.py --update --sequence 2
python3 deploy/node/install-node.py --rollback --sequence 3
echo "$result PHASE failed-activation"
if python3 deploy/node/install-node.py --update --sequence 4 > failed-update.log 2>&1; then
  echo 'A deliberately failing signed runtime passed activation.' >&2
  exit 84
fi
grep -q 'previous release restored' failed-update.log
runuser -u penhoon-agent -- /usr/local/lib/penhoon/current/penhoon-agent check --runtimes /etc/penhoon/runtimes.json
sleep 12
curl --fail --silent --cacert fixture/ca.crt https://localhost:7444/stats > final-stats.json
python3 - <<'PY'
import json, os, pathlib, subprocess
base = pathlib.Path("/usr/local/lib/penhoon")
ledger = json.loads((base / "release-state.json").read_text())
assert ledger["active"]["sequence"] == 3 and ledger["highWater"]["sequence"] == 4 and ledger["pending"] is None, ledger
assert not (base / "activation.json").exists()
stats = json.loads(pathlib.Path("final-stats.json").read_text())
assert stats["enrollments"] == 1 and len(stats["certificateSerials"]) == 1 and stats["sessions"] >= 3 and stats["heartbeats"] >= 2, stats
assert stats["journalRuntimes"] == 4 and stats["authorityReports"] >= 4 and stats["timeSynchronizations"] >= 4 and stats["creditPolls"] >= 4, stats
for name in ["agent", "xray", "ssh", "singbox", "mtproto"]:
    assert pathlib.Path("/var/lib/penhoon-"+name, "qualification-sentinel").read_bytes() == b"preserve durable local state"
for name, inode in json.loads(pathlib.Path("state-inodes.json").read_text()).items():
    assert pathlib.Path(name).stat().st_ino == inode, name
assert not pathlib.Path("/var/lib/penhoon-agent/enrollment.json").exists()
version = subprocess.check_output(["systemctl", "--version"], text=True).splitlines()[0]
report = {"systemd":version,"nonrootServices":5,"enrollments":stats["enrollments"],"mtlsSessions":stats["sessions"],"heartbeats":stats["heartbeats"],"signedUpdate":True,"rollback":True,"failedActivationRecovery":True,"stateFilesPreserved":True,"nativeEnabled":False}
report.update({name: stats[name] for name in ["journalRuntimes", "authorityReports", "timeSynchronizations", "creditPolls"]})
with open("qualification-result.json", "w") as output:
    json.dump({"result": "PASS", "kind": os.environ["PENHOON_QUALIFICATION_RESULT"], "evidence": report}, output)
    output.flush()
    os.fsync(output.fileno())
print(os.environ["PENHOON_QUALIFICATION_RESULT"] + " PASS " + json.dumps(report),flush=True)
PY
if [[ "$environment" == qemu ]]; then sync; fi
