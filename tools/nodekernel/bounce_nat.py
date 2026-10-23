#!/usr/bin/env python3
"""Isolated native forwarding/NAT qualification and comparison probes."""
import json
import os
import pathlib
import socket
import socketserver
import subprocess
import sys
import threading

native_process = None


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def serve():
    class Echo(socketserver.BaseRequestHandler):
        def handle(self):
            if self.client_address[0] != "172.31.50.1":
                return
            while data := self.request.recv(32768):
                self.request.sendall(data)

    tcp = socketserver.ThreadingTCPServer(("172.31.50.2", 31412), Echo)
    threading.Thread(target=tcp.serve_forever, daemon=True).start()
    udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    udp.bind(("172.31.50.2", 31412))
    print("READY", flush=True)
    while True:
        data, peer = udp.recvfrom(65535)
        if peer[0] == "172.31.50.1":
            udp.sendto(data, peer)


def client():
    payload = b"n" * 16384
    with socket.create_connection(("172.31.50.2", 31412), timeout=4) as conn:
        conn.sendall(payload)
        received = b""
        while len(received) < len(payload):
            part = conn.recv(len(payload) - len(received))
            if not part:
                raise RuntimeError("TCP echo closed")
            received += part
        assert received == payload
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as conn:
        conn.settimeout(4)
        conn.sendto(payload[:1024], ("172.31.50.2", 31412))
        data, _ = conn.recvfrom(2048)
        assert data == payload[:1024]
    print("NAT_TCP_UDP_OK", flush=True)


def client_denied():
    try:
        conn = socket.create_connection(("172.31.50.2", 31412), timeout=2)
    except (TimeoutError, OSError):
        print("HOST_FIREWALL_DENIED", flush=True)
        return
    conn.close()
    raise RuntimeError("private download stage bypassed the first-pass host firewall")


def main():
    global native_process
    if os.geteuid() != 0:
        raise RuntimeError("root needed in private namespaces")
    for kind in ("net", "mnt"):
        if os.readlink("/proc/self/ns/" + kind) == os.readlink("/proc/1/ns/" + kind):
            raise RuntimeError("refusing host namespace")
    run("mount", "--make-rprivate", "/")
    run("mount", "-t", "tmpfs", "-o", "mode=0755", "bounce-nat-test", "/run")
    pathlib.Path("/run/netns").mkdir()
    run("ip", "link", "set", "lo", "up")
    for ns, host, peer, network in (("client", "phvtest", "peer0", "10.239.1"), ("origin", "wan0", "peer1", "172.31.50")):
        run("ip", "netns", "add", ns)
        run("ip", "link", "add", host, "type", "veth", "peer", "name", peer)
        run("ip", "link", "set", peer, "netns", ns)
        run("ip", "address", "add", network + ".1/30", "dev", host)
        run("ip", "link", "set", host, "up")
        run("ip", "-n", ns, "address", "add", network + ".2/30", "dev", peer)
        run("ip", "-n", ns, "link", "set", peer, "up")
        run("ip", "-n", ns, "link", "set", "lo", "up")
        run("ip", "-n", ns, "route", "add", "default", "via", network + ".1")
        run("sysctl", "-qw", f"net.ipv4.conf.{host}.forwarding=1", f"net.ipv4.conf.{host}.rp_filter=0")
    production = os.getenv("PENHOON_NATIVE_HELPER_BINARY", "")
    out_name, in_name = "phvbout", "phvbin"
    if production:
        out_name, in_name = "phvdownout", "phvdownin"
        run("ip", "link", "set", "phvtest", "down")
        native_process = subprocess.Popen([production, "-test.run=^TestNativeForwardingServiceHelper$"], env={**os.environ, "PENHOON_NATIVE_FORWARD_TEST": "1", "GORACE": "atexit_sleep_ms=0"}, stdout=subprocess.PIPE, text=True)
        if native_process.stdout.readline().strip() != "READY":
            raise RuntimeError("native forwarding runtime failed startup")
    else:
        run("ip", "link", "add", "phvbout", "type", "veth", "peer", "name", "phvbin")
        for name in ("phvbout", "phvbin"):
            run("ip", "link", "set", name, "up")
        peer = json.loads(subprocess.check_output(["ip", "-j", "link", "show", "phvbin"]))[0]
        run("ip", "neigh", "add", "169.254.234.2", "lladdr", peer["address"], "nud", "permanent", "dev", "phvbout")
        run("sysctl", "-qw", "net.ipv4.conf.all.rp_filter=0", "net.ipv4.conf.phvbin.rp_filter=0", "net.ipv4.conf.phvbin.accept_local=1", "net.ipv4.conf.phvbin.forwarding=1")
        run("ip", "rule", "add", "priority", "100", "iif", "phvbin", "table", "50101")
        run("ip", "route", "add", "table", "50101", "10.239.1.2/32", "dev", "phvtest")
        run("ip", "route", "add", "table", "50101", "unreachable", "default")
        run("ip", "route", "replace", "10.239.1.2/32", "via", "169.254.234.2", "dev", "phvbout", "src", "10.239.1.1", "onlink")
        run("tc", "qdisc", "add", "dev", "phvbout", "root", "handle", "1:", "htb", "default", "1")
        run("tc", "class", "add", "dev", "phvbout", "parent", "1:", "classid", "1:1", "htb", "rate", "100mbit", "ceil", "100mbit", "burst", "65536b", "quantum", "1514")
    allow = "accept" if "--allow-new" in sys.argv else "drop"
    stage = '''chain raw { type filter hook prerouting priority raw; policy accept;
      iifname "phvbin" notrack
    }''' if "--notrack" in sys.argv and not production else ""
    untracked = f'iifname "{in_name}" ct state untracked meta mark 0x1234 counter name stage_mark accept\n iifname "{in_name}" ct state untracked drop' if "--notrack" in sys.argv or production else ""
    rules = f'''table ip bounce_nat {{
      {stage}
      counter stage_est {{}}
      counter stage_new {{}}
      counter stage_invalid {{}}
      counter stage_mark {{}}
      chain mark_reply {{ type filter hook prerouting priority mangle; policy accept;
        iifname "wan0" meta mark set 0x1234
      }}
      chain postrouting {{ type nat hook postrouting priority srcnat; policy accept;
        oifname "wan0" ip saddr 10.239.1.2 snat to 172.31.50.1
      }}
      chain forward {{ type filter hook forward priority filter; policy drop;
        iifname "phvtest" oifname "wan0" accept
        iifname "wan0" oifname "{out_name}" ct state established,related accept
        {untracked}
        iifname "{in_name}" ct state established,related counter name stage_est accept
        iifname "{in_name}" ct state new counter name stage_new {allow}
        iifname "{in_name}" counter name stage_invalid {allow}
      }}
    }}'''
    run("nft", "-f", "-", input=rules, text=True)
    server = subprocess.Popen(["ip", "netns", "exec", "origin", sys.executable, __file__, "serve"], stdout=subprocess.PIPE, text=True)
    try:
        assert server.stdout.readline().strip() == "READY"
        result = subprocess.run(["ip", "netns", "exec", "client", sys.executable, __file__, "client"])
        run("nft", "list", "table", "ip", "bounce_nat")
        result.check_returncode()
        if production or "--notrack" in sys.argv:
            run("nft", "insert", "rule", "ip", "bounce_nat", "forward", "iifname", "wan0", "drop")
            run("ip", "netns", "exec", "client", sys.executable, __file__, "denied")
    finally:
        server.terminate()
        server.wait(timeout=5)


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "serve":
        serve()
    elif len(sys.argv) > 1 and sys.argv[1] == "client":
        client()
    elif len(sys.argv) > 1 and sys.argv[1] == "denied":
        client_denied()
    else:
        try:
            main()
        finally:
            if native_process is not None:
                native_process.terminate()
                print(native_process.communicate(timeout=5)[0])
                native_process.wait(timeout=5)
