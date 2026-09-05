package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
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
	srv := newServer(7, "ssh-7", Settings{Transport: TransportTCP}, testSigner(t), (&net.Dialer{}).DialContext, nil)
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
