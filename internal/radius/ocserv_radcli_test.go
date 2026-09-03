//go:build linux

package radius

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

/*
The OpenConnect core will authenticate and account through this server via the
radcli client compiled into the distribution's ocserv. This is the proof of
that path on a real daemon, gated on OCSERV_RADCLI_E2E=1 (root, ocserv and
openconnect installed, a network namespace for the client): Access-Accept
lands the chosen address on the client, interim records carry the bytes that
crossed the tunnel, and refusing the client then disconnecting it ends the
session with a Stop and keeps it out.
*/
func TestOcservRadcliAuthenticatesAndAccountsThroughTheServer(t *testing.T) {
	if os.Getenv("OCSERV_RADCLI_E2E") != "1" {
		t.Skip("OCSERV_RADCLI_E2E not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root for tun devices and a network namespace")
	}
	ocserv, err := exec.LookPath("ocserv")
	if err != nil {
		t.Skip("ocserv not installed")
	}
	openconnect, err := exec.LookPath("openconnect")
	if err != nil {
		t.Skip("openconnect not installed")
	}
	occtl, err := exec.LookPath("occtl")
	if err != nil {
		t.Skip("occtl not installed")
	}

	// ocserv's workers run as the ocserv user and reach sec-mod over a socket in
	// this directory; every parent of a t.TempDir is root-only.
	dir, err := os.MkdirTemp("", "pui-ocs-spike-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	_ = os.Chmod(dir, 0o755)
	// A plan whose two directions differ, so which one the daemon applied where
	// is visible: its dictionary and its own code disagree about the direction.
	sink := &spikeSink{password: "open-sesame", framed: netip.MustParseAddr("10.211.0.7"), upBps: 8_000_000, downBps: 64_000_000}
	srv, err := New("spike-secret", freeLowUDP(t), freeLowUDP(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(NASIdentifier("ocserv", 1), sink); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	certPEM, keyPEM, _ := spikeServerIdentity(t)
	write := func(name string, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	certFile, keyFile := write("server.crt", certPEM, 0o644), write("server.key", keyPEM, 0o600)

	/*
		radcli's three files: the client config, the secret per server, and a
		dictionary. The distribution's dictionary leaves the vendor includes
		commented out, and radcli silently drops an attribute it cannot name --
		so the speed limits never reach the daemon until this file adds them back.
	*/
	serversFile := write("servers", "127.0.0.1\tspike-secret\n", 0o600)
	dictionary := write("dictionary", `$INCLUDE /etc/radcli/dictionary
$INCLUDE /etc/radcli/dictionary.roaringpenguin
$INCLUDE /etc/radcli/dictionary.microsoft
`, 0o644)
	radcliConf := write("radiusclient.conf", fmt.Sprintf(`nas-identifier %s
authserver %s
acctserver %s
servers %s
dictionary %s
radius_timeout 3
radius_retries 2
bindaddr *
`, NASIdentifier("ocserv", 1), srv.AuthAddr(), srv.AcctAddr(), serversFile, dictionary), 0o600)

	port := freeTCPPort(t)
	occtlSock := filepath.Join(dir, "occtl.sock")
	// groupconfig=true is what makes ocserv APPLY what the Access-Accept carries
	// -- address, DNS, routes, rate limits. Without it the reply authenticates
	// and nothing else, and the client is given a pool address instead.
	write("ocserv.conf", fmt.Sprintf(`auth = "radius[config=%s,groupconfig=true]"
acct = "radius[config=%s]"
listen-host = 10.250.2.1
tcp-port = %d
udp-port = %d
run-as-user = ocserv
run-as-group = ocserv
socket-file = %s
occtl-socket-file = %s
use-occtl = true
isolate-workers = true
device = pocspike
pid-file = %s
max-clients = 0
max-same-clients = 2
server-cert = %s
server-key = %s
log-level = 2
auth-timeout = 40
cookie-timeout = 60
idle-timeout = 1200
dpd = 30
stats-report-time = 3
predictable-ips = true
ipv4-network = 10.211.0.0
ipv4-netmask = 255.255.255.0
route = 10.212.0.0/255.255.255.0
cisco-client-compat = true
dtls-legacy = true
`, radcliConf, radcliConf, port, port, filepath.Join(dir, "ocserv.sock"), occtlSock, filepath.Join(dir, "ocserv.pid"), certFile, keyFile), 0o600)

	// The client lives in its own namespace, reached over a veth pair: on one
	// host, a tunnel to yourself never crosses the tunnel.
	ns := "pui-ocs-spike"
	shell(t, "ip netns del "+ns+" 2>/dev/null; ip link del vocs0 2>/dev/null; true")
	for _, step := range [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", "vocs0", "type", "veth", "peer", "name", "vocs1"},
		{"ip", "link", "set", "vocs1", "netns", ns},
		{"ip", "addr", "add", "10.250.2.1/30", "dev", "vocs0"},
		{"ip", "link", "set", "vocs0", "up"},
		{"ip", "netns", "exec", ns, "ip", "addr", "add", "10.250.2.2/30", "dev", "vocs1"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "vocs1", "up"},
		{"ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"},
	} {
		run(t, step[0], step[1:]...)
	}
	shellOut(t, "ip addr add 10.212.0.1/32 dev lo 2>/dev/null; true")
	t.Cleanup(func() {
		shell(t, "ip netns del "+ns+" 2>/dev/null; ip link del vocs0 2>/dev/null; ip addr del 10.212.0.1/32 dev lo 2>/dev/null; true")
	})

	shellOut(t, "pkill -f '/ocserv --foreground --pid-file /tmp/TestOcserv'; true")
	server := exec.Command(ocserv, "--foreground", "--pid-file", filepath.Join(dir, "ocserv.pid"), "--config", filepath.Join(dir, "ocserv.conf"), "-d", "2")
	serverErr, _ := os.Create(filepath.Join(dir, "server.log"))
	server.Stdout, server.Stderr = serverErr, serverErr
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-server.Process.Pid, syscall.SIGTERM); _ = server.Wait() })
	waitFor(t, 15*time.Second, func() bool {
		_, err := os.Stat(occtlSock)
		return err == nil
	}, func() string {
		return "ocserv never opened its occtl socket:\n" + tailOf(filepath.Join(dir, "server.log"))
	})

	const payload = 300_000
	ln, err := net.Listen("tcp", "10.212.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, payload)) })}
	go func() { _ = origin.Serve(ln) }()
	t.Cleanup(func() { _ = origin.Close() })
	originPort := ln.Addr().(*net.TCPAddr).Port

	// The client pins the server's public key, as a profile from the panel would.
	client := exec.Command("ip", "netns", "exec", ns, openconnect, "--protocol=anyconnect", "--servercert", spkiPin(t, certPEM),
		"--user", "alice", "--passwd-on-stdin", "--interface", "ocspike", "--script", "/usr/share/vpnc-scripts/vpnc-script",
		"--non-inter", "--disable-ipv6", fmt.Sprintf("https://10.250.2.1:%d", port))
	client.Stdin = strings.NewReader("open-sesame\n")
	clientLog, _ := os.Create(filepath.Join(dir, "client.log"))
	client.Stdout, client.Stderr = clientLog, clientLog
	client.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-client.Process.Pid, syscall.SIGTERM); _ = client.Wait() })

	// 1. The address the sink chose is the address the client got.
	clientAddr := func() string { return shellOut(t, "ip netns exec "+ns+" ip -4 -o addr show ocspike 2>/dev/null") }
	waitFor(t, 30*time.Second, func() bool { return strings.Contains(clientAddr(), "10.211.0.7/") },
		func() string {
			return fmt.Sprintf("the client never got the chosen 10.211.0.7:\n%s\n--- sink saw logins %v, events %+v\n--- server log:\n%s\n--- client log:\n%s",
				clientAddr(), sink.seenLogins(), sink.all(), tailOf(filepath.Join(dir, "server.log")), tailOf(filepath.Join(dir, "client.log")))
		})
	if sink.count(Start) != 1 {
		t.Fatalf("Start records: %d, want 1", sink.count(Start))
	}
	users := shellOut(t, occtl+" -s "+occtlSock+" -j show users")
	t.Logf("occtl show users:\n%s", users)

	/*
		The speed plan the Accept carried, read back from the daemon: 8 megabits
		up and 64 down, from the client's side. ocserv's RX is what it receives
		FROM the client, so it holds the upload; TX is the download. This is the
		measurement the mapping in setSpeedLimits is built on, kept as a test so
		a library or daemon change that flips it is loud.
	*/
	for _, want := range []string{`"Limit RX":  "1.0 MB/s"`, `"Limit TX":  "8.0 MB/s"`} {
		if !strings.Contains(users, want) {
			t.Fatalf("the daemon did not apply the speed plan as %s:\n%s", want, users)
		}
	}

	// 2. Bytes through the tunnel show up in interim records.
	out := shellOut(t, fmt.Sprintf("ip netns exec %s curl -s -m 20 -o /dev/null -w '%%{http_code} %%{size_download}' http://10.212.0.1:%d/", ns, originPort))
	if !strings.HasPrefix(out, "200 ") {
		t.Fatalf("GET through the tunnel: %q\n--- client log:\n%s", out, tailOf(filepath.Join(dir, "client.log")))
	}
	waitFor(t, 25*time.Second, func() bool { return sink.maxOut() >= payload }, func() string {
		return fmt.Sprintf("interim records never carried the %d-byte body; last: %+v", payload, sink.last())
	})

	/*
		3. The panel refuses the client, then ends its session. This ocserv has
		no `terminate` verb -- 1.2.4's occtl offers only `disconnect`, which
		leaves the cookie valid, so the refusal has to come first or the client
		resumes without asking. Both halves together are the enforcement a
		quota needs, and the client must stay out for longer than a reconnect.
	*/
	sink.deny()
	t.Logf("disconnect: %s", shellOut(t, occtl+" -s "+occtlSock+" disconnect user alice"))
	waitFor(t, 20*time.Second, func() bool { return sink.count(Stop) == 1 }, func() string {
		return fmt.Sprintf("no Stop after disconnect; events: %+v\n--- server log:\n%s", sink.all(), tailOf(filepath.Join(dir, "server.log")))
	})
	if last := sink.last(); last.Status != Stop || last.Out < payload {
		t.Fatalf("the Stop record does not carry the session total: %+v", last)
	}
	connected := func() bool {
		return strings.Contains(shellOut(t, occtl+" -s "+occtlSock+" -j show users"), `"Username":  "alice"`)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if connected() {
			t.Fatalf("a refused client resumed its session:\n--- server log:\n%s\n--- client log:\n%s",
				tailOf(filepath.Join(dir, "server.log")), tailOf(filepath.Join(dir, "client.log")))
		}
		time.Sleep(time.Second)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// spkiPin is openconnect's --servercert form: the base64 SHA-256 of the
// certificate's SubjectPublicKeyInfo.
func spkiPin(t *testing.T, certPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "pin-sha256:" + base64.StdEncoding.EncodeToString(sum[:])
}
