//go:build linux

package mtproto

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	binary := os.Getenv("MTG_E2E_BINARY")
	if binary == "" {
		t.Skip("MTG_E2E_BINARY not set")
	}
	binDir := t.TempDir()
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, engine.GetBinaryName()), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	cover := t.TempDir()
	if err := os.WriteFile(filepath.Join(cover, "index.html"), []byte("<html><body>a site someone would run</body></html>\n"), 0o644); err != nil {
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
		ID: 9, Kind: Kind, Tag: "inbound-9", Listen: "127.0.0.1", Port: 24710, Enable: true,
		Settings: `{"webEnable":true,"webHost":"web.example.com","webCoverDir":"` + cover + `",` +
			`"webBackendPort":24711,"webBindPort":24712,"webStatsPort":24713}`,
		Users: []core.User{{Email: "alice", Enable: true, LimitIP: 2, Credentials: map[string]any{CredSecret: secret}}},
	}
	if err := c.Reconcile(t.Context(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, "the proxy and the relay to answer", func() bool {
		h, err := c.Health(t.Context())
		return err == nil && len(h.Instances) == 1 && h.Instances[0].Ready
	})

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
