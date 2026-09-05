package ssh

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

/*
UDPGW through the router, with a real xray between. One echo on loopback
proves it both ways: a rule sinking UDP from the bridge silences the echo,
which a direct dial would have reached, and the same rule pointed at freedom
makes it answer. Gated on XRAY_E2E_BINARY like the TCP proof beside it.
*/
func TestUDPGWFollowsTheRouter(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}
	echo := udpEcho(t)
	bridgePort := freeLoopbackPort(t)
	stop := startXrayConfig(t, bin, bridgePort, udpRouterConfig(bridgePort, "sink"))

	settings := fmt.Sprintf(`{"transport":"tcp","udpgw":true,"routeThroughXray":true,"routeXrayPort":%d}`, bridgePort)
	_, inst := startCore(t, settings, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	sunk := openUDPGW(t, client, udpgwDefaultPort)
	if _, err := sunk.Write(udpgwFrame(0, 1, echo, []byte("sunk"))); err != nil {
		t.Fatal(err)
	}
	if got, err := readWithin(sunk, 1500*time.Millisecond); err == nil {
		t.Fatalf("the echo answered while the router sank UDP from this inbound: the datagram went around the router: %q", got)
	}
	_ = sunk.Close()
	stop()

	startXrayConfig(t, bin, bridgePort, udpRouterConfig(bridgePort, "direct"))
	through := openUDPGW(t, client, udpgwDefaultPort)
	defer through.Close()
	if _, err := through.Write(udpgwFrame(0, 2, echo, []byte("through"))); err != nil {
		t.Fatal(err)
	}
	_, conid, from, got := readUDPGWFrame(t, through)
	if conid != 2 || from != echo || string(got) != "through" {
		t.Fatalf("reply = conid %d from %s %q; want conid 2 from %s with the payload", conid, from, got, echo)
	}
}

// udpRouterConfig is the bridge as the panel generates it, with UDP from it
// sent to one outbound and everything else left to the first.
func udpRouterConfig(bridgePort int, udpOutbound string) string {
	return fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"listen": "127.0.0.1", "port": %d, "protocol": "socks", "settings": {"auth": "noauth", "udp": true, "ip": "127.0.0.1"}, "tag": "ssh-e2e"}
  ],
  "outbounds": [
    {"protocol": "freedom", "tag": "direct"},
    {"protocol": "blackhole", "tag": "sink"}
  ],
  "routing": {"rules": [{"type": "field", "inboundTag": ["ssh-e2e"], "network": "udp", "outboundTag": %q}]}
}`, bridgePort, udpOutbound)
}

// freeLoopbackPort is free on TCP and UDP both, as a bridge port must be.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	for range 8 {
		port := freePort(t)
		pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = pc.Close()
		return port
	}
	t.Fatal("no loopback port free on both transports")
	return 0
}

// startXrayConfig runs one xray on config and returns when the bridge port
// answers; the returned stop is idempotent and also runs at cleanup.
func startXrayConfig(t *testing.T, bin string, bridgePort int, config string) func() {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	logFile, _ := os.Create(filepath.Join(dir, "xray.log"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(bridgePort)), time.Second)
		if err == nil {
			_ = conn.Close()
			return stop
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(filepath.Join(dir, "xray.log"))
			t.Fatalf("xray never opened the bridge port:\n%s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
