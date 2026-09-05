package ssh

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

/*
The whole path, with a real router in the middle: an SSH client's forward
leaves through the exit a routing rule chose, and never around it.

Everything below the client is real -- a real xray process, the same loopback
SOCKS bridge the panel generates, and an exit that records being used -- because
the property under test is not "the dialer was called" but "the bytes went the
long way". Gated on XRAY_E2E_BINARY like every other test that needs the core.
*/
func TestAForwardLeavesThroughTheRoutedExit(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}

	destination := echoListener(t)
	exit := recordingSocks(t)
	bridgePort := freePort(t)
	startXray(t, bin, bridgePort, exit.port)

	settings := fmt.Sprintf(`{"transport":"tcp","routeThroughXray":true,"routeXrayPort":%d}`, bridgePort)
	_, inst := startCore(t, settings, userN(0))

	client, err := gossh.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inst.Port)), clientConfig("user0", "secret-0"))
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	conn, err := client.Dial("tcp", destination.Addr().String())
	if err != nil {
		t.Fatalf("the forward was refused with a working bridge: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write through the forward: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read back through the forward: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo returned %q", buf)
	}
	if exit.used.Load() == 0 {
		t.Fatal("the forward reached its destination without passing the exit, which is the leak this feature exists to close")
	}
}

/*
And with the router gone, the forward is refused rather than leaving from the
server's own address.

This is the half that cannot be proven by reading the code: the dialer is a
SOCKS client, and a SOCKS client whose proxy is down could plausibly fall back.
*/
func TestAForwardIsRefusedWhenTheRouterIsGone(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}

	destination := echoListener(t)
	exit := recordingSocks(t)
	bridgePort := freePort(t)
	stop := startXray(t, bin, bridgePort, exit.port)

	settings := fmt.Sprintf(`{"transport":"tcp","routeThroughXray":true,"routeXrayPort":%d}`, bridgePort)
	_, inst := startCore(t, settings, userN(0))
	client, err := gossh.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inst.Port)), clientConfig("user0", "secret-0"))
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	stop()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := client.Dial("tcp", destination.Addr().String())
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("a forward still succeeded after the router was stopped; with the switch on it must be refused, not dialled directly")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if exit.used.Load() != 0 {
		t.Fatal("the exit saw a connection after it should have been unreachable")
	}
}

// startXray runs a core carrying exactly what the panel generates for a routed
// inbound: the loopback bridge, and a rule sending that tag to one outbound.
func startXray(t *testing.T, bin string, bridgePort, exitPort int) func() {
	t.Helper()
	return startXrayConfig(t, bin, bridgePort, fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"listen": "127.0.0.1", "port": %d, "protocol": "socks", "settings": {"auth": "noauth", "udp": true, "ip": "127.0.0.1"}, "tag": "ssh-e2e"}
  ],
  "outbounds": [
    {"protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": %d}]}, "tag": "exit"},
    {"protocol": "freedom", "tag": "direct"}
  ],
  "routing": {"rules": [{"type": "field", "inboundTag": ["ssh-e2e"], "outboundTag": "exit"}]}
}`, bridgePort, exitPort))
}

type socksExit struct {
	port int
	used atomic.Int64
}

/*
recordingSocks is the exit: a SOCKS5 server that notes it was used and then
connects to whatever it was asked for. Small enough to read, which matters
because it is the witness the assertion rests on.
*/
func recordingSocks(t *testing.T) *socksExit {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	exit := &socksExit{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go exit.serve(conn)
		}
	}()
	return exit
}

func (e *socksExit) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil || header[0] != 5 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, int(header[1]))); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil || request[1] != 1 {
		return
	}
	var host string
	switch request[3] {
	case 1:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return
		}
		host = net.IP(addr).String()
	case 3:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes))))
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	e.used.Add(1)
	_ = conn.SetDeadline(time.Time{})
	go func() { _, _ = io.Copy(upstream, conn) }()
	_, _ = io.Copy(conn, upstream)
}
