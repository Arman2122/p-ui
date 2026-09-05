package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
)

// memoryHostKey stands in for the Setting row the panel keeps the key in.
type memoryHostKey struct {
	mu  sync.Mutex
	pem string
}

var testHostKey struct {
	once sync.Once
	pem  string
}

// testHostKeyPEM is one host key set for the whole package, both families, so
// a test that starts a core mints nothing.
func testHostKeyPEM(t *testing.T) string {
	t.Helper()
	testHostKey.once.Do(func() {
		_, ed, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		for _, priv := range []any{ed, rsaKey} {
			block, err := gossh.MarshalPrivateKey(priv, "test")
			if err != nil {
				t.Fatal(err)
			}
			testHostKey.pem += string(pem.EncodeToMemory(block))
		}
	})
	return testHostKey.pem
}

func (m *memoryHostKey) load() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pem, nil
}

func (m *memoryHostKey) store(pem string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pem = pem
	return nil
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

func userN(n int) core.User {
	email := coretest.Subject
	if n > 0 {
		email = fmt.Sprintf("user%d@example.com", n)
	}
	return core.User{
		Email: email, Enable: true,
		Credentials: map[string]any{core.CredUsername: fmt.Sprintf("user%d", n), core.CredPassword: fmt.Sprintf("secret-%d", n)},
	}
}

// feedTraffic sets the cumulative bytes as if the client had moved them.
func (c *Core) feedTraffic(email string, up, down int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cur := range c.instances {
		u, d := cur.srv.countersFor(email)
		u.Store(up)
		d.Store(down)
		return
	}
}

// restartSource is what a panel restart does to an in-process core: every
// counter is back at zero and the epoch is new.
func (c *Core) restartSource() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cur := range c.instances {
		cur.srv.resetTraffic()
	}
	c.epoch += "'"
}

// feedSession registers a connection the suite controls, so Sessions can be
// checked against something it put there.
func (c *Core) feedSession(email, source string, lastSeen int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cur := range c.instances {
		cur.srv.mu.Lock()
		var found *conn
		for existing := range cur.srv.conns {
			if existing.email == email && existing.source.String() == source {
				found = existing
			}
		}
		if found == nil {
			found = &conn{email: email, source: netip.MustParseAddr(source)}
			cur.srv.conns[found] = struct{}{}
		}
		found.lastSeen.Store(lastSeen)
		cur.srv.mu.Unlock()
		return
	}
}

func (c *Core) servedUsers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, cur := range c.instances {
		out = append(out, cur.srv.servedEmails()...)
	}
	return out
}

// The conformance suite every core passes, over a real listener on loopback.
func TestConformance(t *testing.T) {
	keys := &memoryHostKey{}
	port := freePort(t)
	// The suite builds two more cores for its registry checks; the hooks must
	// act on the first, which is the one it binds and drives.
	var current *Core
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := New(Deps{LoadHostKey: keys.load, StoreHostKey: keys.store})
			if current == nil {
				current = c
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: Kind, Tag: "ssh-1", Listen: "127.0.0.1", Port: port, Enable: true, Settings: `{"transport":"tcp"}`}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, userN(i))
			}
			return inst
		},
		Starts:        func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
		Alive:         func() bool { return listenerAnswers(port) },
		FeedTraffic:   func(email string, up, down int64) { current.feedTraffic(email, up, down) },
		RestartSource: func() { current.restartSource() },
		ServedUsers:   func() []string { return current.servedUsers() },
		FeedSession:   func(email, source string, lastSeen int64) { current.feedSession(email, source, lastSeen) },
	}
	for _, failure := range coretest.Check(rig) {
		t.Error(failure)
	}
	if current != nil {
		_ = current.StopAll(t.Context())
	}
}

// listenerAnswers is the rig's own view of the daemon: a TCP connect to the
// port, which succeeds only while the accept loop holds it.
func listenerAnswers(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
