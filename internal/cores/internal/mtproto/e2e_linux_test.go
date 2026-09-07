//go:build linux

package mtproto

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

/*
The real sidecar, end to end. Set MTG_E2E_BINARY to an mtg-proxy build and this
drives the panel's own manager against it: the proxy and the relay come up off
one rendered file, the relay serves the cover site to a request without a
credential and the same bytes to one with a wrong one, a rate pushed through
the contract reads back from the daemon, and the kick answers. Nothing here can
prove a Telegram client connects; that stays with the operator.
*/
func TestRealSidecarEndToEnd(t *testing.T) {
	if os.Getenv("MTG_E2E_BINARY") == "" {
		t.Skip("MTG_E2E_BINARY not set")
	}
	c, inst := startRealWebInbound(t, 9, 24710)

	// The rendered file is one the real binary accepts whole: no unknown key.
	h, _ := c.Health(t.Context())
	if h.Instances[0].State != core.HealthRunning || h.Instances[0].Detail != "" {
		t.Fatalf("health = %+v, want running with nothing to report", h.Instances[0])
	}

	// Tier three: a request with no credential gets the cover site, and one with
	// a wrong bridge gets the same bytes. The relay requires PROXY v2 in front.
	plain := relayGet(t, 24712, "/")
	if plain.status != http.StatusOK || !slices.Contains([]string{"<html><body>a site someone would run</body></html>\n"}, plain.body) {
		t.Fatalf("GET / = %d %q, want the cover page", plain.status, plain.body)
	}
	wrong := relayGet(t, 24712, "/?bridge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if wrong.status != plain.status || wrong.body != plain.body {
		t.Fatalf("a wrong bridge must be byte-identical to the cover: %d %q vs %d %q", wrong.status, wrong.body, plain.status, plain.body)
	}

	// The contract round trip through the real daemon, in the contract's unit.
	want := map[string]core.RateLimit{"alice": {UpBps: 1_000_000, DownBps: 2_000_000}}
	if err := c.EnforceRates(t.Context(), inst, want); err != nil {
		t.Fatalf("EnforceRates: %v", err)
	}
	held, err := c.EnforcedRates(t.Context(), inst)
	if err != nil || held["alice"] != want["alice"] {
		t.Fatalf("EnforcedRates = %+v, %v; want %+v", held, err, want)
	}
	sessions, err := c.Sessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("Sessions = %v, %v; nobody is connected", sessions, err)
	}
	if n, err := c.CutSessions(t.Context(), "alice", netip.MustParseAddr("203.0.113.9")); err != nil || n != 0 {
		t.Fatalf("CutSessions = %d, %v; a place nobody is at is a kick of zero", n, err)
	}

	// A second client reaches the relay's profile set without a restart.
	bob := core.User{Email: "bob", Enable: true, Credentials: map[string]any{CredSecret: "ee" + "0123456789abcdef0123456789abcdef" + "7777772e636c6f7564666c6172652e636f6d"}}
	if err := c.AddUser(t.Context(), inst, bob); err != nil {
		t.Fatalf("add user: %v", err)
	}
	waitFor(t, "the relay to learn bob", func() bool {
		return slices.Contains(relayProfiles(t, 24713), "bob")
	})

	if err := c.DropInstance(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both processes to end", func() bool {
		_, err := relayProfilesErr(24713)
		return err != nil
	})
}

type relayResponse struct {
	status int
	body   string
}

// relayGet speaks to the relay the way its front proxy does: a PROXY v2 header
// declaring the browser's address, then plain HTTP/1.1 on the same socket.
func relayGet(t *testing.T, port int, path string) relayResponse {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 3*time.Second)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(proxyV2Header("203.0.113.44", 51000, "127.0.0.1", port)); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: web.example.com\r\nConnection: close\r\n\r\n", path)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read relay response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return relayResponse{status: resp.StatusCode, body: string(body)}
}

// proxyV2Header is a PROXY protocol v2 header for one TCP over IPv4 connection.
func proxyV2Header(srcIP string, srcPort int, dstIP string, dstPort int) []byte {
	hdr := []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A, 0x21, 0x11, 0x00, 12}
	hdr = append(hdr, net.ParseIP(srcIP).To4()...)
	hdr = append(hdr, net.ParseIP(dstIP).To4()...)
	hdr = binary.BigEndian.AppendUint16(hdr, uint16(srcPort))
	hdr = binary.BigEndian.AppendUint16(hdr, uint16(dstPort))
	return hdr
}

func relayProfiles(t *testing.T, port int) []string {
	t.Helper()
	profiles, err := relayProfilesErr(port)
	if err != nil {
		return nil
	}
	return profiles
}

func relayProfilesErr(port int) ([]string, error) {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/stats")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var stats struct {
		Profiles []string `json:"profiles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	return stats.Profiles, nil
}

// startRealWebInbound brings the proxy and the relay up off one rendered file
// for inbound id on ports base..base+3 (listen, backend, relay, stats).
func startRealWebInbound(t *testing.T, id, base int) (*Core, core.Instance) {
	t.Helper()
	binDir := t.TempDir()
	payload, err := os.ReadFile(os.Getenv("MTG_E2E_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, engine.GetBinaryName()), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	cover := t.TempDir()
	if err := os.WriteFile(filepath.Join(cover, "index.html"), []byte(coverPage), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUI_BIN_FOLDER", binDir)
	t.Cleanup(func() { engine.GetManager().StopAll() })

	c := New(Deps{})
	if err := c.Preflight(t.Context()); err != nil {
		t.Fatalf("preflight against the real binary: %v", err)
	}
	const secret = "ee8196fe6ed8b637d001f91d6952cfcdf07777772e636c6f7564666c6172652e636f6d"
	inst := core.Instance{
		ID: id, Kind: Kind, Tag: fmt.Sprintf("inbound-%d", id), Listen: "127.0.0.1", Port: base, Enable: true,
		Settings: fmt.Sprintf(`{"webEnable":true,"webHost":"web.example.com","webCoverDir":%q,"webBackendPort":%d,"webBindPort":%d,"webStatsPort":%d}`,
			cover, base+1, base+2, base+3),
		Users: []core.User{{Email: "alice", Enable: true, LimitIP: 2, Credentials: map[string]any{CredSecret: secret}}},
	}
	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, "the proxy and the relay to answer", func() bool {
		h, err := c.Health(t.Context())
		return err == nil && len(h.Instances) == 1 && h.Instances[0].Ready
	})
	return c, inst
}

const coverPage = "<html><body>a site someone would run</body></html>\n"

/*
Xray in front of the relay, the way the panel renders a fallback for a WEB
relay child: the relay's hostname, http/1.1, its loopback port, a PROXY v2
header. Set XRAY_E2E_BINARY beside MTG_E2E_BINARY. What the rules in the
fallback service refuse is measured here, not assumed: the cover page comes
back through Xray; a master that offers h2 gives a client that takes it
nothing; a stream without the PROXY header is refused by the relay; another
SNI gets nothing.
*/
func TestXrayFrontsTheRelay(t *testing.T) {
	xray := os.Getenv("XRAY_E2E_BINARY")
	if xray == "" || os.Getenv("MTG_E2E_BINARY") == "" {
		t.Skip("XRAY_E2E_BINARY and MTG_E2E_BINARY not both set")
	}
	_, _ = startRealWebInbound(t, 10, 24730)
	const relayPort, frontPort = 24732, 24734
	certFile, keyFile := selfSignedCert(t, "web.example.com")
	entry := func(xver int) map[string]any {
		return map[string]any{"name": "web.example.com", "alpn": "http/1.1", "dest": fmt.Sprintf("127.0.0.1:%d", relayPort), "xver": xver}
	}
	pinned := []string{"http/1.1"}

	stop := runXray(t, xray, xrayFront(frontPort, pinned, entry(2), certFile, keyFile))
	status, body, err := frontGet(frontPort, "web.example.com", pinned, "/")
	if err != nil || status != http.StatusOK || body != coverPage {
		t.Fatalf("through Xray: %d %q, %v; want the cover page", status, body, err)
	}
	if s2, b2, err := frontGet(frontPort, "web.example.com", pinned, "/?bridge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != nil || s2 != status || b2 != body {
		t.Fatalf("a wrong bridge through Xray must be byte-identical to the cover: %d %q, %v", s2, b2, err)
	}
	if _, _, err := frontGet(frontPort, "other.example.com", pinned, "/"); err == nil {
		t.Fatal("a fallback named for the relay's hostname answered another SNI")
	}
	stop()

	stop = runXray(t, xray, xrayFront(frontPort, []string{"h2", "http/1.1"}, entry(2), certFile, keyFile))
	if _, _, err := frontGet(frontPort, "web.example.com", []string{"h2", "http/1.1"}, "/"); err == nil {
		t.Fatal("a client that negotiated h2 with the master was served: the ALPN rule would be needless")
	}
	if status, body, err := frontGet(frontPort, "web.example.com", pinned, "/"); err != nil || status != http.StatusOK || body != coverPage {
		t.Fatalf("the same master over http/1.1: %d %q, %v; want the cover page", status, body, err)
	}
	stop()

	stop = runXray(t, xray, xrayFront(frontPort, pinned, entry(0), certFile, keyFile))
	if _, _, err := frontGet(frontPort, "web.example.com", pinned, "/"); err == nil {
		t.Fatal("the relay served a stream that carried no PROXY header")
	}
	stop()
}

func xrayFront(port int, alpn []string, fallback map[string]any, certFile, keyFile string) map[string]any {
	return map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{
				"clients":    []any{map[string]any{"id": "9d2a2a0e-1e7f-4f0b-9f0e-5c5e0d2d1c11"}},
				"decryption": "none",
				"fallbacks":  []any{fallback},
			},
			"streamSettings": map[string]any{
				"network": "tcp", "security": "tls",
				"tlsSettings": map[string]any{"alpn": alpn, "certificates": []any{map[string]any{"certificateFile": certFile, "keyFile": keyFile}}},
			},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
}

// runXray starts the binary on the config and returns a stop that is safe to
// call more than once; the port is awaited before returning.
func runXray(t *testing.T, binary string, cfg map[string]any) func() {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xray.json")
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "run", "-c", path)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(stop)
	port := cfg["inbounds"].([]any)[0].(map[string]any)["port"].(int)
	waitFor(t, "xray to listen", func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	return stop
}

// frontGet is a browser's request through the front: TLS to the port with the
// SNI and ALPN offer given, then GET path on whatever was negotiated.
func frontGet(port int, sni string, protos []string, path string) (int, string, error) {
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: sni, NextProtos: protos}, //nolint:gosec // a self-signed test certificate
		ForceAttemptHTTP2: slices.Contains(protos, "h2"),
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		},
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://%s:%d%s", sni, port, path))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func selfSignedCert(t *testing.T, host string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
