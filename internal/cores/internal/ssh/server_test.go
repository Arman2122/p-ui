package ssh

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/policy"
)

// admitByCap stands in for the panel: it decides a login from the sources the
// core reports it already holds, which is the same rule policy.Decide applies.
func admitByCap(_ context.Context, a core.Admission) error {
	held := make([]string, 0, len(a.Held))
	for _, src := range a.Held {
		held = append(held, src.String())
	}
	if policy.Admit(held, a.Source.String(), a.LimitIP) {
		return nil
	}
	return core.ErrDeviceLimit
}

// echoListener is the destination every forward in these tests dials.
func echoListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// startCore runs one inbound on loopback. The dialer is injected so a forward
// may reach the echo listener beside it, which the default policy refuses.
func startCore(t *testing.T, settings string, users ...core.User) (*Core, core.Instance) {
	t.Helper()
	keys := &memoryHostKey{pem: testHostKeyPEM(t)}
	var failures []AuthFailure
	var mu sync.Mutex
	c := New(Deps{LoadHostKey: keys.load, StoreHostKey: keys.store, Dial: (&net.Dialer{Timeout: dialTimeout}).DialContext, Admit: admitByCap, AuthFailure: func(f AuthFailure) {
		mu.Lock()
		failures = append(failures, f)
		mu.Unlock()
	}})
	inst := core.Instance{ID: 7, Kind: Kind, Tag: "ssh-7", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: settings, Users: users}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	return c, inst
}

func clientConfig(user, password string) *gossh.ClientConfig {
	return &gossh.ClientConfig{
		User: user, Auth: []gossh.AuthMethod{gossh.Password(password)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	}
}

func dialTCP(t *testing.T, inst core.Instance, user, password string) (*gossh.Client, error) {
	t.Helper()
	return gossh.Dial("tcp", net.JoinHostPort(inst.Listen, itoa(inst.Port)), clientConfig(user, password))
}

/*
dialFrom is a second device: the same listener, reached from another loopback
address. Bound as the LOCAL address on purpose — a dial TO 127.0.0.2 never
reaches a listener on 127.0.0.1, and its refusal at TCP once passed for the cap's.
A transport failure is therefore fatal here; only an SSH refusal is an error.
*/
func dialFrom(t *testing.T, inst core.Instance, local, user, password string) (*gossh.Client, error) {
	t.Helper()
	d := &net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}}
	raw, err := d.Dial("tcp", net.JoinHostPort(inst.Listen, itoa(inst.Port)))
	if err != nil {
		t.Fatalf("tcp from %s: %v", local, err)
	}
	cc, chans, reqs, err := gossh.NewClientConn(raw, raw.RemoteAddr().String(), clientConfig(user, password))
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return gossh.NewClient(cc, chans, reqs), nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// roundTrip forwards to the echo listener and pushes payload through it.
func roundTrip(t *testing.T, client *gossh.Client, echo net.Addr, payload []byte) {
	t.Helper()
	fwd, err := client.Dial("tcp", echo.String())
	if err != nil {
		t.Fatalf("direct-tcpip: %v", err)
	}
	defer fwd.Close()
	if _, err := fwd.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(fwd, got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("the forward corrupted the payload")
	}
}

/*
The product end to end: a real SSH client logs in with a minted username and
password, opens a direct-tcpip forward to a local echo server, and the core
bills both directions to the client, shows the session with its source, refuses
a revoked client's next forward, and holds the client's device cap.
*/
func TestForwardIsCountedAndCapped(t *testing.T) {
	echo := echoListener(t)
	alice := userN(0)
	alice.LimitIP = 1
	c, inst := startCore(t, `{"transport":"tcp"}`, alice)

	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	payload := bytes.Repeat([]byte("penhoon"), 1000)
	if _, err := c.CollectTraffic(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	roundTrip(t, client, echo.Addr(), payload)

	deltas, err := c.CollectTraffic(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(deltas) != 1 || deltas[0].Email != alice.Email || deltas[0].Tag != "ssh-7" {
		t.Fatalf("deltas = %+v, want one for %s on ssh-7", deltas, alice.Email)
	}
	if deltas[0].Up != int64(len(payload)) || deltas[0].Down != int64(len(payload)) {
		t.Fatalf("billed up=%d down=%d, want %d each", deltas[0].Up, deltas[0].Down, len(payload))
	}
	sessions, _ := c.Sessions(context.Background())
	if len(sessions) != 1 || sessions[0].Email != alice.Email || sessions[0].Source.String() != "127.0.0.1" || sessions[0].LastSeenUnixMilli == 0 {
		t.Fatalf("sessions = %+v, want alice from 127.0.0.1 with a last-seen", sessions)
	}
	online, _ := c.OnlineEmails(context.Background())
	if len(online) != 1 || online[0] != alice.Email {
		t.Fatalf("online = %v", online)
	}

	// A second device: the cap counts distinct sources, and 127.0.0.2 is one.
	// The refusal is the panel's, asked through Deps.Admit at the handshake.
	second, err := dialFrom(t, inst, "127.0.0.2", "user0", "secret-0")
	if err == nil {
		second.Close()
		t.Fatal("a second source was admitted past a device cap of 1")
	}
	if _, err := dialTCP(t, inst, "user0", "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}

	// Revocation: the user is removed and the live connection goes with it.
	if err := c.RemoveUser(context.Background(), inst, alice.Email); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	if _, err := client.Dial("tcp", echo.Addr().String()); err == nil {
		t.Fatal("a removed client opened a new forward")
	}
}

/*
CutSessions ends one client's connections from ONE source, so the device over a
cap is dropped while the ones inside it stay up. The whole point of by-source: a
cut by email alone would take both.
*/
func TestCutSessionsEndsOnlyTheNamedSource(t *testing.T) {
	alice := userN(0)
	c, inst := startCore(t, `{"transport":"tcp"}`, alice)

	first, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatalf("dial 127.0.0.1: %v", err)
	}
	defer first.Close()
	second, err := dialFrom(t, inst, "127.0.0.2", "user0", "secret-0")
	if err != nil {
		t.Fatalf("login from 127.0.0.2: %v", err)
	}
	defer second.Close()
	if sessions, _ := c.Sessions(context.Background()); len(sessions) != 2 {
		t.Fatalf("want two sessions before the cut, got %+v", sessions)
	}

	cut, err := c.CutSessions(context.Background(), alice.Email, netip.MustParseAddr("127.0.0.2"))
	if err != nil || cut != 1 {
		t.Fatalf("CutSessions = (%d, %v), want (1, nil)", cut, err)
	}
	sessions, _ := c.Sessions(context.Background())
	if len(sessions) != 1 || sessions[0].Source.String() != "127.0.0.1" {
		t.Fatalf("after cutting 127.0.0.2 only the 127.0.0.1 device must remain, got %+v", sessions)
	}
	// Cutting the same source again is nothing to do, not an error.
	if again, err := c.CutSessions(context.Background(), alice.Email, netip.MustParseAddr("127.0.0.2")); err != nil || again != 0 {
		t.Fatalf("a second cut of the same source = (%d, %v), want (0, nil)", again, err)
	}
}

// A rate is applied inside the core: with the ladder set, moving bytes takes
// the time the limit implies, and clearing it makes the same move instant.
func TestRateIsEnforcedInProcess(t *testing.T) {
	echo := echoListener(t)
	c, inst := startCore(t, `{"transport":"tcp"}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	payload := bytes.Repeat([]byte("x"), 96*1024)
	// 256 kbit/s = 32 KiB/s each way; a 96 KiB round trip needs about two
	// seconds beyond the burst, which is far above the unlimited case.
	if err := c.EnforceRates(context.Background(), inst, map[string]core.RateLimit{subjectEmail(): {UpBps: 256_000, DownBps: 256_000}}); err != nil {
		t.Fatalf("EnforceRates: %v", err)
	}
	start := time.Now()
	roundTrip(t, client, echo.Addr(), payload)
	limited := time.Since(start)

	if err := c.EnforceRates(context.Background(), inst, map[string]core.RateLimit{}); err != nil {
		t.Fatalf("EnforceRates clear: %v", err)
	}
	start = time.Now()
	roundTrip(t, client, echo.Addr(), payload)
	unlimited := time.Since(start)
	if limited < time.Second || unlimited > limited/2 {
		t.Fatalf("limited round trip took %s and unlimited %s; the rate is not being enforced", limited, unlimited)
	}
}

func subjectEmail() string { return userN(0).Email }

// The two WebSocket dialects on one port: a bare 101 for the HTTP-Injector
// family and RFC 6455 framing for a real WebSocket client.
func TestWebSocketTransports(t *testing.T) {
	echo := echoListener(t)
	_, inst := startCore(t, `{"transport":"ws","path":"/tunnel"}`, userN(0))
	addr := net.JoinHostPort(inst.Listen, itoa(inst.Port))

	t.Run("bare 101", func(t *testing.T) {
		raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer raw.Close()
		if _, err := io.WriteString(raw, "GET /tunnel HTTP/1.1\r\nHost: example\r\nUpgrade: websocket\r\n\r\n"); err != nil {
			t.Fatalf("payload: %v", err)
		}
		status := make([]byte, len(bare101))
		if _, err := io.ReadFull(raw, status); err != nil || !strings.HasPrefix(string(status), "HTTP/1.1 101") {
			t.Fatalf("status = %q, %v", status, err)
		}
		sc, chans, reqs, err := gossh.NewClientConn(raw, addr, clientConfig("user0", "secret-0"))
		if err != nil {
			t.Fatalf("ssh over the bare socket: %v", err)
		}
		client := gossh.NewClient(sc, chans, reqs)
		defer client.Close()
		roundTrip(t, client, echo.Addr(), []byte("through the bare upgrade"))
	})

	t.Run("framed", func(t *testing.T) {
		ws, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/tunnel", nil)
		if err != nil {
			t.Fatalf("framed dial: %v", err)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		sc, chans, reqs, err := gossh.NewClientConn(&wsConn{ws: ws}, addr, clientConfig("user0", "secret-0"))
		if err != nil {
			t.Fatalf("ssh over framed websocket: %v", err)
		}
		client := gossh.NewClient(sc, chans, reqs)
		defer client.Close()
		roundTrip(t, client, echo.Addr(), []byte("through rfc 6455 frames"))
	})

	t.Run("wrong path", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/elsewhere")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 off the configured path", resp.StatusCode)
		}
	})
}

// WSS terminates TLS on the core's own listener with the pair the inbound names.
func TestWSSTransport(t *testing.T) {
	echo := echoListener(t)
	certFile, keyFile := selfSignedFiles(t)
	settings, _ := json.Marshal(map[string]string{"transport": "wss", "certFile": certFile, "keyFile": keyFile})
	_, inst := startCore(t, string(settings), userN(0))
	addr := net.JoinHostPort(inst.Listen, itoa(inst.Port))
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // a self-signed test pair
	ws, resp, err := dialer.Dial("wss://"+addr+"/", nil)
	if err != nil {
		t.Fatalf("wss dial: %v", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	sc, chans, reqs, err := gossh.NewClientConn(&wsConn{ws: ws}, addr, clientConfig("user0", "secret-0"))
	if err != nil {
		t.Fatalf("ssh over wss: %v", err)
	}
	client := gossh.NewClient(sc, chans, reqs)
	defer client.Close()
	roundTrip(t, client, echo.Addr(), []byte("through tls"))
}

// The share is the whole config, and its URI is what the subscription carries.
func TestShareCarriesEverythingAClientNeeds(t *testing.T) {
	c := New(Deps{})
	share, err := c.RenderClient(core.Instance{Tag: "ssh-ws", Port: 8880, Settings: `{"transport":"ws","path":"/t"}`}, userN(0), "vpn.example.com")
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}
	for _, want := range []string{"Host: vpn.example.com", "Port: 8880", "Username: user0", "Password: secret-0", "Path: /t", "[crlf]Host: vpn.example.com[crlf]", "TCP only"} {
		if !strings.Contains(share.Body, want) {
			t.Errorf("share body lacks %q:\n%s", want, share.Body)
		}
	}
	if share.Kind != "file" || share.Filename != "user0.txt" {
		t.Fatalf("share = kind %q file %q", share.Kind, share.Filename)
	}
	// No name: what a client app displays is the subscription's to stamp, and
	// the only name a core could reach for is the inbound's tag.
	if share.URI != "ssh://user0:secret-0@vpn.example.com:8880?path=%2Ft&transport=ws" {
		t.Fatalf("URI = %q", share.URI)
	}
	if strings.Contains(share.URI, "#") {
		t.Fatalf("the core named the link itself: %q", share.URI)
	}
}
