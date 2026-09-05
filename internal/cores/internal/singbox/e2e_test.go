package singbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// The e2e half runs where a sing-box built with with_v2ray_api exists; the
// VM has one, CI does not, so it skips there and says so.
func e2eBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("SINGBOX_E2E_BINARY")
	if bin == "" {
		t.Skip("SINGBOX_E2E_BINARY not set")
	}
	return bin
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// selfSigned writes a certificate for localhost the way an operator's would
// sit on disk: root-owned files the daemon must be handed a copy of.
func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func settingsJSON(t *testing.T, certFile, keyFile string) string {
	t.Helper()
	raw, err := json.Marshal(Settings{CertFile: certFile, KeyFile: keyFile, ServerName: "localhost", CongestionControl: "bbr"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// e2eUser: the conformance subject first, then numbered users.
func e2eUser(n int) core.User {
	email := coretest.Subject
	if n > 0 {
		email = fmt.Sprintf("user%d@example.com", n)
	}
	return core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUUID:     fmt.Sprintf("c0ffee00-0000-4000-8000-%012d", n),
		core.CredPassword: fmt.Sprintf("secret-password-%d", n),
	}}
}

// servedByDaemon reads what the daemon runs, not what the adapter holds: the
// users in the config file on disk, accepted only once the daemon's start time
// (its own API) is later than that file, which is the proof it loaded it.
// Stats counters cannot answer this: sing-box creates one on first connection.
func (c *Core) servedByDaemon(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, cur := range c.instances {
		if !cur.running {
			continue
		}
		at, err := startedAt(context.Background(), cur.ctl.apiPort, cur.ctl.secret)
		if err != nil {
			t.Logf("started-at: %v", err)
			continue
		}
		info, err := os.Stat(cur.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if at < info.ModTime().UnixMilli() {
			t.Logf("the daemon (started %d) predates the config on disk (%d)", at, info.ModTime().UnixMilli())
			continue
		}
		body, err := os.ReadFile(cur.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Inbounds []struct {
				Users []struct {
					Name string `json:"name"`
				} `json:"users"`
			} `json:"inbounds"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		for _, ib := range doc.Inbounds {
			for _, u := range ib.Users {
				if !slices.Contains(out, u.Name) {
					out = append(out, u.Name)
				}
			}
		}
	}
	return out
}

func (c *Core) settleNow(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.settleLocked(context.Background()); err != nil {
		t.Fatalf("settle: %v", err)
	}
}

/*
The conformance suite over the real daemon. Traffic and sessions are fed
through the two read seams, because nothing outside a client can make a QUIC
daemon count bytes; TestRealTrafficIsBilledAndSeen drives that path for real.
*/
func TestConformance(t *testing.T) {
	bin := e2eBinary(t)
	certFile, keyFile := selfSigned(t, t.TempDir())
	port := freePort(t)
	fed := map[string][2]int64{}
	var sessions []liveConnection
	realStats, realConns := readUserStats, readConnections
	readUserStats = func(ctx context.Context, statsPort int) (map[string]int64, error) {
		readings, err := realStats(ctx, statsPort)
		if err != nil {
			return nil, err
		}
		for email, v := range fed {
			readings["user>>>"+email+">>>traffic>>>uplink"] = v[0]
			readings["user>>>"+email+">>>traffic>>>downlink"] = v[1]
		}
		return readings, nil
	}
	readConnections = func(ctx context.Context, apiPort int, secret string) ([]liveConnection, error) {
		live, err := realConns(ctx, apiPort, secret)
		return append(live, sessions...), err
	}
	// A close of a fed connection removes it, as the daemon's would; a real one
	// still reaches the daemon, so the suite exercises CutSessions' selection and
	// the daemon's own answer alike.
	realClose := closeConn
	closeConn = func(ctx context.Context, apiPort int, secret, id string) error {
		for i := range sessions {
			if sessions[i].id == id {
				sessions = slices.Delete(sessions, i, i+1)
				return nil
			}
		}
		return realClose(ctx, apiPort, secret, id)
	}
	t.Cleanup(func() { readUserStats, readConnections, closeConn = realStats, realConns, realClose })

	var current *Core
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := New(Deps{Binary: bin, StateDir: t.TempDir(), ApplyDelay: time.Hour})
			if current == nil {
				current = c
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: KindTUIC, Tag: "tuic-1", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settingsJSON(t, certFile, keyFile)}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, e2eUser(i))
			}
			return inst
		},
		Starts:      func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
		Alive:       func() bool { return unitAlive(current.backend, 1) },
		FeedTraffic: func(email string, up, down int64) { fed[email] = [2]int64{up, down} },
		RestartSource: func() {
			fed = map[string][2]int64{}
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.epoch = newEpoch()
			}
			current.mu.Unlock()
		},
		ServedUsers: func() []string { return current.servedByDaemon(t) },
		Settle:      func() { current.settleNow(t) },
		FeedSession: func(email, source string, lastSeen int64) {
			// Fed again with a later stamp, the client reconnected: a new connection
			// id opened at that time replaces the old one, as the daemon would report.
			conn := liveConnection{id: fmt.Sprintf("%s|%s|%d", email, source, lastSeen), user: email, source: netip.MustParseAddr(source), createdAt: lastSeen}
			for i := range sessions {
				if sessions[i].user == email && sessions[i].source.String() == source {
					sessions[i] = conn
					return
				}
			}
			sessions = append(sessions, conn)
		},
	}
	for _, failure := range coretest.Check(rig) {
		t.Error(failure)
	}
	if current != nil {
		_ = current.StopAll(context.Background())
	}
}

// startClient runs a second sing-box as a TUIC client with a SOCKS/HTTP
// listener on loopback, the way a subscriber's app would connect.
func startClient(t *testing.T, bin string, serverPort int, user core.User) (proxyPort int) {
	t.Helper()
	proxyPort = freePort(t)
	cfg := map[string]any{
		"log":      map[string]any{"level": "warn"},
		"inbounds": []map[string]any{{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": proxyPort}},
		"outbounds": []map[string]any{{
			"type": "tuic", "tag": "out", "server": "127.0.0.1", "server_port": serverPort,
			"uuid": user.Credentials[core.CredUUID], "password": user.Credentials[core.CredPassword],
			"congestion_control": "bbr", "udp_relay_mode": "native",
			"tls": map[string]any{"enabled": true, "server_name": "localhost", "insecure": true, "alpn": []string{"h3"}},
		}},
	}
	body, _ := json.Marshal(cfg)
	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path, "-D", dir)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitPort(t, proxyPort)
	return proxyPort
}

/*
A cut reaches the real daemon: the connection stream names a held connection's
id, CloseConnection ends it, and the client on the far side sees its body break
off. The daemon's own answer, not the ledger's: a cut the core only claimed
would leave the read completing normally.
*/
func TestACutEndsTheRealConnection(t *testing.T) {
	bin := e2eBinary(t)
	certFile, keyFile := selfSigned(t, t.TempDir())
	port := freePort(t)
	c := New(Deps{Binary: bin, StateDir: t.TempDir(), ApplyDelay: time.Hour})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	alice := e2eUser(1)
	inst := core.Instance{ID: 3, Kind: KindTUIC, Tag: "tuic-3", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settingsJSON(t, certFile, keyFile), Users: []core.User{alice}}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// An origin that answers half and then holds, so the connection is live and
	// mid-transfer when it is cut. The handler is released before origin.Close
	// runs — Close waits for it — so the release is registered AFTER Close (LIFO).
	release := make(chan struct{})
	var releaseOnce sync.Once
	released := func() { releaseOnce.Do(func() { close(release) }) }
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2048")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(make([]byte, 1024))
	}))
	t.Cleanup(origin.Close)
	t.Cleanup(released)

	proxyPort := startClient(t, bin, port, alice)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 30 * time.Second}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("GET through the tunnel: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadFull(resp.Body, make([]byte, 1024)); err != nil {
		t.Fatalf("first half: %v", err)
	}
	sessions, _ := c.Sessions(context.Background())
	if !slices.ContainsFunc(sessions, func(s core.Session) bool { return s.Email == alice.Email && s.Source.String() == "127.0.0.1" }) {
		t.Fatalf("the held connection is not reported for %s: %+v", alice.Email, sessions)
	}

	cut, err := c.CutSessions(context.Background(), alice.Email, netip.MustParseAddr("127.0.0.1"))
	if err != nil || cut < 1 {
		t.Fatalf("CutSessions = (%d, %v), want at least the held connection closed", cut, err)
	}
	// The origin is still holding the second half, so a body that ends here ended
	// because the daemon closed the connection under it — and it must end NOW: a
	// read that only gives up at the client's own timeout is a cut that did nothing.
	start := time.Now()
	rest, err := io.ReadAll(resp.Body)
	if err == nil && len(rest) == 1024 {
		t.Fatal("the body completed after the cut: the daemon did not close the connection")
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Fatalf("the body read ended only after %v (%v): the daemon did not close the connection", waited.Round(time.Second), err)
	}
	released()
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d never opened", port)
}

/*
Real bytes through a real TUIC session, billed to the right user and seen as
a session from its source; then a user edit lands by reload, and what was
moved before the reload is not lost and not billed twice.
*/
func TestRealTrafficIsBilledAndSeen(t *testing.T) {
	bin := e2eBinary(t)
	certFile, keyFile := selfSigned(t, t.TempDir())
	port := freePort(t)
	c := New(Deps{Binary: bin, StateDir: t.TempDir(), ApplyDelay: time.Hour})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	alice := e2eUser(1)
	inst := core.Instance{ID: 2, Kind: KindTUIC, Tag: "tuic-2", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settingsJSON(t, certFile, keyFile), Users: []core.User{alice}}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := c.CollectTraffic(context.Background()); err != nil {
		t.Fatalf("baseline collect: %v", err)
	}

	const payload = 1 << 20
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(payload))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, payload/2))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(make([]byte, payload/2))
	}))
	t.Cleanup(origin.Close)

	proxyPort := startClient(t, bin, port, alice)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 30 * time.Second}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("GET through the tunnel: %v", err)
	}
	half := make([]byte, payload/2)
	if _, err := io.ReadFull(resp.Body, half); err != nil {
		t.Fatalf("first half: %v", err)
	}

	sessions, err := c.Sessions(context.Background())
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.Email == alice.Email && s.Source.String() == "127.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the open connection is not reported for %s from 127.0.0.1: %+v", alice.Email, sessions)
	}
	close(release)
	rest, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || len(rest) != payload/2 {
		t.Fatalf("second half: %d bytes, %v", len(rest), err)
	}

	totalDown := func(deltas []core.TrafficDelta) int64 {
		var n int64
		for _, d := range deltas {
			if d.Email == alice.Email {
				if d.Down < 0 || d.Up < 0 {
					t.Fatalf("negative delta %+v", d)
				}
				n += d.Down
			}
		}
		return n
	}
	first, err := c.CollectTraffic(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got := totalDown(first); got < payload {
		t.Fatalf("billed %d down bytes to %s, want at least the %d-byte body: %+v", got, alice.Email, payload, first)
	}

	// A second user is a config rewrite and a reload; a transfer that
	// happened before the reload must show up in the next collect exactly once.
	resp2, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp2.Body); err != nil {
		t.Fatalf("second body: %v", err)
	}
	_ = resp2.Body.Close()
	bob := e2eUser(2)
	if err := c.AddUser(context.Background(), inst, bob); err != nil {
		t.Fatalf("add user: %v", err)
	}
	c.settleNow(t)
	if served := c.servedByDaemon(t); !slices.Contains(served, bob.Email) || !slices.Contains(served, alice.Email) {
		t.Fatalf("after the reload the daemon serves %v", served)
	}
	c.mu.Lock()
	starts := c.starts
	c.mu.Unlock()
	if starts != 1 {
		t.Fatalf("a user edit restarted the daemon (%d starts); it must reload", starts)
	}
	second, err := c.CollectTraffic(context.Background())
	if err != nil {
		t.Fatalf("collect after reload: %v", err)
	}
	if got := totalDown(second); got < payload || got > 2*payload {
		t.Fatalf("after the reload %d down bytes were billed for one %d-byte transfer: %+v", got, payload, second)
	}
	third, _ := c.CollectTraffic(context.Background())
	if got := totalDown(third); got != 0 {
		t.Fatalf("an idle pass billed %d bytes", got)
	}
	if !strings.HasPrefix(c.instances[2].epoch, "") {
		t.Fatal("unreachable")
	}
}

// unitAlive asks the kernel about the unit's process, so Health is checked
// against what runs and not against what the adapter believes.
func unitAlive(b supervisor.Backend, id int) bool {
	st, err := b.Status(context.Background(), supervisor.UnitName(string(ID), id))
	if err != nil || st.PID <= 0 {
		return false
	}
	p, err := os.FindProcess(st.PID)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
