package singbox

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The whole path, with a real router in the middle: a TUIC client's traffic
leaves through the exit a routing rule chose, and never around it.

Everything below the client is real -- a real sing-box serving the inbound, a
real xray carrying the loopback bridge the panel generates, and an exit that
records being used -- because the property under test is not "the config said
socks" but "the bytes went the long way".
*/
func TestDaemonTrafficLeavesThroughTheRoutedExit(t *testing.T) {
	bin := e2eBinary(t)
	xrayBin := os.Getenv("XRAY_E2E_BINARY")
	if xrayBin == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}

	exit := recordingSocks(t)
	bridgePort := freePort(t)
	startXray(t, xrayBin, bridgePort, exit.port, "tuic-3")

	certFile, keyFile := selfSigned(t, t.TempDir())
	port := freePort(t)
	c := New(Deps{Binary: bin, StateDir: t.TempDir(), ApplyDelay: time.Hour})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	alice := e2eUser(1)
	inst := core.Instance{
		ID: 3, Kind: KindTUIC, Tag: "tuic-3", Listen: "127.0.0.1", Port: port, Enable: true,
		Settings: bridgedSettings(t, certFile, keyFile, bridgePort), Users: []core.User{alice},
	}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("through"))
	}))
	t.Cleanup(origin.Close)

	proxyPort := startClient(t, bin, port, alice)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   30 * time.Second,
	}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("GET through the tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "through" {
		t.Fatalf("origin returned %q", body)
	}
	if exit.used.Load() == 0 {
		t.Fatal("the daemon reached the origin without passing the exit, which is the leak this feature exists to close")
	}
}

// bridgedSettings is the inbound as the panel stores it once the switch is on
// and the bridge port has been allocated.
func bridgedSettings(t *testing.T, certFile, keyFile string, bridgePort int) string {
	t.Helper()
	raw, err := json.Marshal(Settings{
		CertFile: certFile, KeyFile: keyFile, ServerName: "localhost", CongestionControl: "bbr",
		RouteThroughXray: true, RouteXrayPort: bridgePort,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// startXray runs a core carrying exactly what the panel generates for a routed
// inbound: the loopback bridge, and a rule sending that tag to one outbound.
func startXray(t *testing.T, bin string, bridgePort, exitPort int, tag string) {
	t.Helper()
	config := fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"listen": "127.0.0.1", "port": %d, "protocol": "socks", "settings": {"auth": "noauth", "udp": false}, "tag": %q}
  ],
  "outbounds": [
    {"protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": %d}]}, "tag": "exit"},
    {"protocol": "freedom", "tag": "direct"}
  ],
  "routing": {"rules": [{"type": "field", "inboundTag": [%q], "outboundTag": "exit"}]}
}`, bridgePort, tag, exitPort, tag)

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
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	waitPort(t, bridgePort)
}

type socksExit struct {
	port int
	used atomic.Int64
}

// recordingSocks is the exit: a SOCKS5 server that notes it was used and then
// connects to whatever it was asked for. It is the witness the assertion rests
// on, so it is short enough to read.
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
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
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
