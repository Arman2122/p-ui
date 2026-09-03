//go:build linux

package radius

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

/*
The OpenVPN core authenticates and accounts through this server via the
distribution's openvpn-auth-radius plugin. This is the proof of that path on a
real daemon, gated on OPENVPN_PLUGIN_E2E=1 (root, openvpn and the plugin
installed, a network namespace for the client so the tunnel is really crossed):
Access-Accept lands the pushed tunnel address on the client, interim records
carry bytes, a management kill ends the session with a Stop.
*/
func TestOpenVPNPluginAuthenticatesAndAccountsThroughTheServer(t *testing.T) {
	if os.Getenv("OPENVPN_PLUGIN_E2E") != "1" {
		t.Skip("OPENVPN_PLUGIN_E2E not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root for tun devices and a network namespace")
	}
	openvpn, err := exec.LookPath("openvpn")
	if err != nil {
		t.Skip("openvpn not installed")
	}
	plugin := "/usr/lib/openvpn/radiusplugin.so"
	if _, err := os.Stat(plugin); err != nil {
		t.Skipf("%s not installed", plugin)
	}

	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	sink := &spikeSink{password: "open-sesame", framed: netip.MustParseAddr("10.209.0.7")}
	// radiusplugin keeps the port in a four-character buffer: 18239 becomes 1823
	// on the wire (seen with tcpdump), so the listener stays below 10000.
	srv, err := New("spike-secret", freeLowUDP(t), freeLowUDP(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(NASIdentifier("openvpn", 1), sink); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	certPEM, keyPEM, fingerprint := spikeServerIdentity(t)
	write := func(name string, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	certFile, keyFile := write("server.crt", certPEM, 0o644), write("server.key", keyPEM, 0o600)
	serverKey := filepath.Join(dir, "tc2-server.key")
	run(t, openvpn, "--genkey", "tls-crypt-v2-server", serverKey)
	clientKey := filepath.Join(dir, "tc2-client.key")
	run(t, openvpn, "--tls-crypt-v2", serverKey, "--genkey", "tls-crypt-v2-client", clientKey, "p-ui:alice")
	clientKeyBody, _ := os.ReadFile(clientKey)

	_, authPort, _ := net.SplitHostPort(srv.AuthAddr())
	_, acctPort, _ := net.SplitHostPort(srv.AcctAddr())
	pluginCnf := write("radiusplugin.cnf", fmt.Sprintf(`NAS-Identifier=%s
Service-Type=5
Framed-Protocol=1
NAS-Port-Type=5
NAS-IP-Address=127.0.0.1
OpenVPNConfig=%s
subnet=255.255.255.0
overwriteccfiles=true
server
{
	acctport=%s
	authport=%s
	name=127.0.0.1
	retry=1
	wait=1
	sharedsecret=spike-secret
}
`, NASIdentifier("openvpn", 1), filepath.Join(dir, "server.conf"), acctPort, authPort), 0o600)
	ccd := filepath.Join(dir, "ccd")
	_ = os.MkdirAll(ccd, 0o755)
	port := freeUDPPort(t)
	mgmt := filepath.Join(dir, "mgmt.sock")
	write("server.conf", fmt.Sprintf(`port %d
proto udp
dev povspike
dev-type tun
topology subnet
server 10.209.0.0 255.255.255.0
push "route 10.210.0.0 255.255.255.0"
ca %s
cert %s
key %s
dh none
tls-crypt-v2 %s
verify-client-cert none
username-as-common-name
plugin %s %s
client-config-dir %s
tmp-dir %s
management %s unix
status %s 2
disable-dco
data-ciphers AES-256-GCM:CHACHA20-POLY1305
auth SHA256
keepalive 5 30
persist-key
persist-tun
log-append %s
verb 3
`, port, certFile, certFile, keyFile, serverKey, plugin, pluginCnf, ccd, dir, mgmt, filepath.Join(dir, "status.log"), filepath.Join(dir, "server.log")), 0o600)

	// The client lives in its own namespace, reached over a veth pair: on one
	// host, a tunnel to yourself never crosses the tunnel.
	ns := "pui-ovpn-spike"
	shell(t, "ip netns del "+ns+" 2>/dev/null; ip link del vspike0 2>/dev/null; true")
	for _, step := range [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", "vspike0", "type", "veth", "peer", "name", "vspike1"},
		{"ip", "link", "set", "vspike1", "netns", ns},
		{"ip", "addr", "add", "10.250.0.1/30", "dev", "vspike0"},
		{"ip", "link", "set", "vspike0", "up"},
		{"ip", "netns", "exec", ns, "ip", "addr", "add", "10.250.0.2/30", "dev", "vspike1"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "vspike1", "up"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"},
	} {
		run(t, step[0], step[1:]...)
	}
	shellOut(t, "ip addr add 10.210.0.1/32 dev lo 2>/dev/null; true")
	t.Cleanup(func() {
		shell(t, "ip netns del "+ns+" 2>/dev/null; ip link del vspike0 2>/dev/null; ip addr del 10.210.0.1/32 dev lo 2>/dev/null; true")
	})

	// A daemon left over from an earlier run still owns the device name; the
	// pattern cannot match this test binary's own command line.
	shellOut(t, "pkill -f '/openvpn --config /tmp/TestOpenVPNPlugin'; true")
	server := exec.Command(openvpn, "--config", filepath.Join(dir, "server.conf"))
	serverErr, _ := os.Create(filepath.Join(dir, "server.err"))
	server.Stdout, server.Stderr = serverErr, serverErr
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-server.Process.Pid, syscall.SIGTERM); _ = server.Wait() })
	waitFor(t, 15*time.Second, func() bool {
		return fileContains(filepath.Join(dir, "server.log"), "Initialization Sequence Completed")
	}, func() string { return "server never came up:\n" + tailOf(filepath.Join(dir, "server.log")) })

	// Something only reachable through the tunnel: an HTTP server on the host's
	// 10.210.0.1, which the client's namespace has no route to except the push.
	const payload = 300_000
	origin := &http.Server{Addr: "10.210.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, payload)) })}
	ln, err := net.Listen("tcp", "10.210.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = origin.Serve(ln) }()
	t.Cleanup(func() { _ = origin.Close() })
	originPort := ln.Addr().(*net.TCPAddr).Port

	write("auth.txt", "alice\nopen-sesame\n", 0o600)
	write("client.ovpn", fmt.Sprintf(`client
dev povclient
dev-type tun
proto udp
remote 10.250.0.1 %d
nobind
peer-fingerprint %s
<tls-crypt-v2>
%s</tls-crypt-v2>
auth-user-pass %s
data-ciphers AES-256-GCM:CHACHA20-POLY1305
auth SHA256
disable-dco
persist-tun
log-append %s
verb 3
`, port, fingerprint, string(clientKeyBody), filepath.Join(dir, "auth.txt"), filepath.Join(dir, "client.log")), 0o600)
	client := exec.Command("ip", "netns", "exec", ns, openvpn, "--config", filepath.Join(dir, "client.ovpn"))
	clientErr, _ := os.Create(filepath.Join(dir, "client.err"))
	client.Stdout, client.Stderr = clientErr, clientErr
	client.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-client.Process.Pid, syscall.SIGTERM); _ = client.Wait() })

	// 1. The address the sink chose is the address the client got: readiness
	// is read off the device, not off a log whose flushing is the daemon's.
	clientAddr := func() string { return shellOut(t, "ip netns exec "+ns+" ip -4 -o addr show povclient 2>/dev/null") }
	waitFor(t, 30*time.Second, func() bool { return strings.Contains(clientAddr(), "10.209.0.7/") },
		func() string {
			return fmt.Sprintf("the client never got the pushed 10.209.0.7:\n%s\n--- sink saw logins %v, events %+v\n--- plugin lines:\n%s\n--- client log:\n%s\n--- namespace:\n%s",
				clientAddr(), sink.seenLogins(), sink.all(),
				shellOut(t, "grep -i 'radius\\|auth\\|ccd\\|ifconfig' "+dir+"/server.log | tail -20; cat "+dir+"/radiusplugin.cnf; ls -la "+dir+"/ccd"),
				tailOf(filepath.Join(dir, "client.log")),
				shellOut(t, "ip netns exec "+ns+" ip -br addr; ps -o pid,args -C openvpn"))
		})
	if sink.count(Start) != 1 {
		t.Fatalf("Start records: %d, want 1", sink.count(Start))
	}

	// 2. Bytes through the tunnel show up in interim records.
	out := shellOut(t, fmt.Sprintf("ip netns exec %s curl -s -m 20 -o /dev/null -w '%%{http_code} %%{size_download}' http://10.210.0.1:%d/", ns, originPort))
	if !strings.HasPrefix(out, "200 ") {
		t.Fatalf("GET through the tunnel: %q", out)
	}
	waitFor(t, 25*time.Second, func() bool { return sink.maxOut() >= payload }, func() string {
		return fmt.Sprintf("interim records never carried the %d-byte body; last: %+v", payload, sink.last())
	})

	// 3. A management kill ends the session with a Stop the sink hears.
	mgmtConn, err := net.Dial("unix", mgmt)
	if err != nil {
		t.Fatal(err)
	}
	_ = mgmtConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprintf(mgmtConn, "kill alice\n")
	buf := make([]byte, 4096)
	n, _ := mgmtConn.Read(buf)
	_ = mgmtConn.Close()
	if !strings.Contains(string(buf[:n]), "SUCCESS") && !strings.Contains(string(buf[:n]), "INFO") {
		t.Logf("management answered: %q", buf[:n])
	}
	waitFor(t, 20*time.Second, func() bool { return sink.count(Stop) == 1 }, func() string {
		return fmt.Sprintf("no Stop after the kill; events: %+v", sink.all())
	})
	if last := sink.last(); last.Status != Stop || last.Out < payload {
		t.Fatalf("the Stop record does not carry the session total: %+v", last)
	}
}

type spikeSink struct {
	mu       sync.Mutex
	password string
	framed   netip.Addr
	denied   bool
	events   []Event
	logins   []string
}

func (s *spikeSink) Authenticate(_ context.Context, username, password string, _ Peer) Verdict {
	s.mu.Lock()
	s.logins = append(s.logins, username+":"+password)
	denied := s.denied
	s.mu.Unlock()
	if denied {
		return Verdict{Reason: "client is depleted"}
	}
	if username != "alice" || password != s.password {
		return Verdict{Reason: "wrong login"}
	}
	return Verdict{Accept: true, FramedIP: s.framed, InterimSeconds: 3}
}

// deny is the panel deciding a client may no longer log in -- depleted,
// expired or removed. What a daemon does with a session already open is
// then the core's own job, which is what the proofs measure.
func (s *spikeSink) deny() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.denied = true
}

func (s *spikeSink) Account(_ context.Context, ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *spikeSink) count(st Status) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Status == st {
			n++
		}
	}
	return n
}

func (s *spikeSink) maxOut() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m int64
	for _, e := range s.events {
		if e.Out > m {
			m = e.Out
		}
	}
	return m
}

func (s *spikeSink) last() Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return Event{}
	}
	return s.events[len(s.events)-1]
}

func (s *spikeSink) seenLogins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logins...)
}

func (s *spikeSink) all() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func spikeServerIdentity(t *testing.T) (certPEM, keyPEM, fingerprint string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pui-openvpn-spike"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	sum := sha256.Sum256(der)
	hexed := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(hexed); i += 2 {
		parts = append(parts, hexed[i:i+2])
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		strings.Join(parts, ":")
}

func freeLowUDP(t *testing.T) string {
	t.Helper()
	// The ephemeral range starts at 32768, so a bind to :0 never answers low.
	for port := 2000 + int(time.Now().UnixNano()%6000); port < 9999; port++ {
		addr := "127.0.0.1:" + strconv.Itoa(port)
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return addr
	}
	t.Fatal("no free UDP port in 2000-9999")
	return ""
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func shell(t *testing.T, script string) {
	t.Helper()
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("sh -c %q: %v\n%s", script, err, out)
	}
}

func shellOut(t *testing.T, script string) string {
	t.Helper()
	out, _ := exec.Command("sh", "-c", script).CombinedOutput()
	return string(out)
}

func tailOf(path string) string {
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return strings.Join(lines, "\n")
}

func fileContains(path, needle string) bool {
	body, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(body), needle)
}

// The diagnosis is a closure so it reads the daemon, the sink and the logs as
// they are when the wait gives up. Eager arguments describe the moment before
// the wait began, which is the one moment that explains nothing.
func waitFor(t *testing.T, d time.Duration, cond func() bool, why func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal(why())
}
