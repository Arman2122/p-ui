package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
)

func testSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

/*
dialWithin bounds the whole login, handshake included. gossh.Dial's Timeout
covers the TCP connect alone, so a connection the server accepts into its
backlog and never serves would hang a test rather than fail it.
*/
func dialWithin(t *testing.T, addr string, within time.Duration) (*gossh.Client, error) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", addr, within)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Now().Add(within))
	sc, chans, reqs, err := gossh.NewClientConn(raw, addr, clientConfig("user0", "secret-0"))
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return gossh.NewClient(sc, chans, reqs), nil
}

// flakyListener fails its next Accept calls the way a descriptor shortage does,
// then serves normally. A closed listener it is not.
type flakyListener struct {
	net.Listener
	failures atomic.Int32
}

func (f *flakyListener) Accept() (net.Conn, error) {
	if f.failures.Add(-1) >= 0 {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: errors.New("too many open files")}
	}
	return f.Listener.Accept()
}

/*
A failed accept used to end the accept loop, and the inbound was dead until
the next reconcile with nothing but a log line to say so. The listener now
backs off and carries on: after the failure a real client still gets in.
*/
func TestTheListenerSurvivesAFailedAccept(t *testing.T) {
	echo := echoListener(t)
	srv := newServer(7, "ssh-7", Settings{Transport: TransportTCP}, []gossh.Signer{testSigner(t)}, (&net.Dialer{}).DialContext, nil, nil)
	if err := srv.setUsers([]core.User{userN(0)}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	flaky := &flakyListener{Listener: ln}
	flaky.failures.Store(2)
	srv.serve(flaky)
	t.Cleanup(srv.stop)

	client, err := dialWithin(t, ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("the listener did not recover from a failed accept: %v", err)
	}
	defer client.Close()
	if flaky.failures.Load() >= 0 {
		t.Fatal("the client got in before the injected failures were consumed; the test proves nothing")
	}
	roundTrip(t, client, echo.Addr(), []byte("still serving"))
}

// serveOnLoopback is a bare server on a fresh loopback port, for tests that
// tune the package's bounds rather than drive a whole core.
func serveOnLoopback(t *testing.T) (*server, string) {
	t.Helper()
	srv := newServer(7, "ssh-7", Settings{Transport: TransportTCP}, []gossh.Signer{testSigner(t)}, (&net.Dialer{}).DialContext, nil, nil)
	if err := srv.setUsers([]core.User{userN(0)}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.serve(ln)
	t.Cleanup(srv.stop)
	return srv, ln.Addr().String()
}

// A peer that connects and never logs in is cut off at the grace time rather
// than holding its goroutine and descriptor for as long as it likes.
func TestAnUnfinishedHandshakeIsDropped(t *testing.T) {
	was := handshakeTimeout
	handshakeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = was })
	_, addr := serveOnLoopback(t)

	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	body, err := io.ReadAll(raw)
	if err != nil {
		t.Fatalf("the server kept a silent peer past the grace time: %v", err)
	}
	if !strings.HasPrefix(string(body), "SSH-2.0-") {
		t.Fatalf("the server did not even greet: %q", body)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the silent peer was dropped only after %s", took)
	}
}

/*
A peer that stops answering is dropped after the configured misses, so a
phone that changed networks releases its device slot instead of holding it
until the kernel gives up hours later. The client here never services its
request channel, which is exactly what a dead peer looks like from the server.
*/
func TestAPeerThatStopsAnsweringIsDropped(t *testing.T) {
	wasInterval, wasTimeout, wasMisses := keepaliveInterval, keepaliveTimeout, keepaliveMisses
	keepaliveInterval, keepaliveTimeout, keepaliveMisses = 100*time.Millisecond, 100*time.Millisecond, 2
	t.Cleanup(func() { keepaliveInterval, keepaliveTimeout, keepaliveMisses = wasInterval, wasTimeout, wasMisses })
	srv, addr := serveOnLoopback(t)

	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	sc, _, _, err := gossh.NewClientConn(raw, addr, clientConfig("user0", "secret-0"))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer sc.Close()
	// The client sees its login complete a hair before the server records it.
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.online()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("online after login = %v", srv.online())
		}
		time.Sleep(10 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(srv.online()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a peer that answered nothing for many keepalive rounds still holds its session")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// One connection cannot open forwards without limit; the cap is per connection
// and a closed forward gives its slot back.
func TestForwardsPerConnectionAreCapped(t *testing.T) {
	was := maxForwardsPerConn
	maxForwardsPerConn = 2
	t.Cleanup(func() { maxForwardsPerConn = was })
	echo := echoListener(t)
	_, inst := startCore(t, `{"transport":"tcp"}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	first, err := client.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := client.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	third, err := client.Dial("tcp", echo.Addr().String())
	if err == nil {
		_ = third.Close()
		t.Fatal("a third forward was opened past a cap of two")
	}
	if !strings.Contains(err.Error(), "too many open forwards") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	_ = second.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		again, err := client.Dial("tcp", echo.Addr().String())
		if err == nil {
			_ = again.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a closed forward did not give its slot back: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
