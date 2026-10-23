import argparse
import json
import socket
import threading
import time


def receive(args):
    server = socket.socket()
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("0.0.0.0", args.port))
    server.listen(2)
    server.settimeout(10)
    lock = threading.Lock()
    result = {"bytes": 0, "first": None, "last": None, "connections": 0}

    def drain(connection):
        connection.settimeout(3)
        with connection:
            while True:
                try:
                    data = connection.recv(65536)
                except socket.timeout:
                    return
                if not data:
                    return
                now = time.monotonic()
                with lock:
                    result["bytes"] += len(data)
                    result["first"] = result["first"] or now
                    result["last"] = now

    print("READY", flush=True)
    workers = []
    connections = []
    try:
        for _ in range(2):
            connection, _ = server.accept()
            result["connections"] += 1
            connections.append(connection)
        for connection in connections:
            connection.sendall(b"g")
            thread = threading.Thread(target=drain, args=(connection,))
            workers.append(thread)
            thread.start()
    finally:
        server.close()
    for thread in workers:
        thread.join()
    result["elapsed"] = (result["last"] or 0) - (result["first"] or 0)
    print(json.dumps(result), flush=True)


def send(args):
    with socket.create_connection(("198.18.0.2", args.port), timeout=5) as connection:
        if connection.recv(1) != b"g":
            raise RuntimeError("receiver synchronization failed")
        connection.settimeout(3)
        remaining = 6 << 20
        data = b"p" * 32768
        while remaining:
            try:
                count = connection.send(data[: min(remaining, len(data))])
            except socket.timeout:
                return
            remaining -= count


parser = argparse.ArgumentParser()
parser.add_argument("mode", choices=["send", "receive"])
parser.add_argument("--port", type=int, required=True)
options = parser.parse_args()
receive(options) if options.mode == "receive" else send(options)
