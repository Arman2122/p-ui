#!/usr/bin/env bash
set -euo pipefail

if [[ $(id -u) != 0 ]] || [[ $(readlink /proc/self/ns/net) == $(readlink /proc/1/ns/net) ]]; then
  echo 'Run this probe as root inside a private network and mount namespace.' >&2
  exit 1
fi
if [[ $(readlink /proc/self/ns/mnt) == $(readlink /proc/1/ns/mnt) ]]; then
  echo 'A private mount namespace is also required.' >&2
  exit 1
fi

mount --make-rprivate /
mount -t tmpfs -o mode=0755 penhoon-kernel-test /run
mkdir /run/netns
probe_dir=$(mktemp -d "$PWD/scratchpad/node-v2/kernel-probe.XXXXXX")
traffic="$PWD/tools/nodekernel/traffic.py"
cleanup() {
  local process
  for process in $(jobs -pr); do kill "$process" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT

ip link set lo up
sysctl -qw net.ipv4.ip_forward=1
for number in 1 2 3; do
  name="probe$number"
  ip netns add "$name"
  ip link add "phn${number}h" type veth peer name "phn${number}p"
  ip link set "phn${number}p" netns "$name"
  if [[ $number == 3 ]]; then subnet=198.18.0; else subnet="10.231.$number"; fi
  ip address add "$subnet.1/30" dev "phn${number}h"
  ip link set "phn${number}h" up
  ip -n "$name" address add "$subnet.2/30" dev "phn${number}p"
  ip -n "$name" link set "phn${number}p" up
  ip -n "$name" link set lo up
  ip -n "$name" route add default via "$subnet.1"
done

ip link add phnup type ifb
ip link set phnup up
tc qdisc add dev phnup root handle 1: htb
tc class add dev phnup parent 1: classid 1:10 htb rate 8mbit ceil 8mbit burst 4096b cburst 4096b quantum 1514
tc qdisc add dev phnup parent 1:10 handle 10: pfifo limit 256
for number in 1 2; do
  tc qdisc add dev "phn${number}h" clsact
  tc filter add dev "phn${number}h" ingress protocol ip pref 10 flower src_ip "10.231.$number.2/32" \
    action skbedit priority 1:10 action mirred egress redirect dev phnup
done

run_traffic() {
  local label=$1 port=$2
  ip netns exec probe3 python3 "$traffic" receive --port "$port" > "$probe_dir/$label.json" &
  local receiver=$!
  for _ in $(seq 1 100); do
    if [[ -s "$probe_dir/$label.json" ]]; then break; fi
    sleep 0.05
  done
  ip netns exec probe1 python3 "$traffic" send --port "$port" &
  local first=$!
  ip netns exec probe2 python3 "$traffic" send --port "$port" &
  local second=$!
  wait "$first"
  wait "$second"
  wait "$receiver"
}

run_traffic rate 18080
python3 - "$probe_dir/rate.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as stream:
    result = json.loads(stream.readlines()[-1])
assert result["connections"] == 2, result
assert result["bytes"] == 12 << 20, result
assert result["bytes"] * 8 <= 8_000_000 * result["elapsed"] + 8 * 131072, result
print(json.dumps({"shared_rate": result}))
PY

tc class change dev phnup parent 1: classid 1:10 htb rate 1gbit ceil 1gbit burst 65536b cburst 65536b quantum 1514
nft -f - <<'NFT'
table inet penhoon_probe {
  quota client_credit { until 1048576 bytes }
  counter admitted_upload { }
  chain forward {
    type filter hook forward priority 0; policy accept;
    iifname { "phn1h", "phn2h" } quota name "client_credit" counter name "admitted_upload" accept
    iifname { "phn1h", "phn2h" } drop
  }
}
NFT
run_traffic quota 18081
nft -j list table inet penhoon_probe > "$probe_dir/quota-kernel.json"
python3 - "$probe_dir/quota.json" "$probe_dir/quota-kernel.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as stream:
    result = json.loads(stream.readlines()[-1])
with open(sys.argv[2]) as stream:
    kernel = json.load(stream)
counter = next(item["counter"] for item in kernel["nftables"] if item.get("counter", {}).get("name") == "admitted_upload")
assert result["connections"] == 2, result
assert 65536 < result["bytes"] <= counter["bytes"] <= 1048576, (result, counter)
print(json.dumps({"shared_quota": result, "accepted_kernel_bytes": counter["bytes"]}))
PY
