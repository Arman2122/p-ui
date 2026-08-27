package ldaputil

import (
	"net"
	"strconv"
	"testing"
	"time"
)

/*
A directory that accepts the connection and never answers must not hang us.

ldap.DialURL bounds neither the dial nor the requests on the connection it
returns, and neither call site set a timeout. A directory that completes the TCP
handshake and then goes silent -- a hung LDAP server, a stateful firewall that
dropped the flow, a load balancer holding the socket open -- left the bind
blocked with no deadline at all.

The LDAP sync job runs on a cron, so that is one goroutine and one socket leaked
per tick, on a panel that otherwise looks healthy, until it runs out of file
descriptors and every other subsystem starts failing for a reason that has
nothing to do with LDAP.

The test dials a listener that accepts and then does nothing. Without the
timeout it never returns; ten seconds is the harness noticing. F371.
*/
func TestASilentDirectoryDoesNotHangTheSync(t *testing.T) {
	// The real 15s is a production figure, not a property of this test.
	restore := dialTimeout
	dialTimeout = 500 * time.Millisecond
	t.Cleanup(func() { dialTimeout = restore })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept and hold, answering nothing. Exactly a wedged directory.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}

	cfg := Config{
		Host: host, Port: port,
		BindDN: "cn=admin,dc=example,dc=com", Password: "x",
		BaseDN: "dc=example,dc=com", UserAttr: "mail", FlagField: "vlessEnabled",
	}

	done := make(chan error, 1)
	go func() {
		_, ferr := FetchVlessFlags(cfg)
		done <- ferr
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a directory that answers nothing returned success")
		}
	case <-time.After(dialTimeout + 10*time.Second):
		t.Fatal("FetchVlessFlags is still waiting on a directory that will never answer; the sync job leaks this goroutine and its socket every tick")
	}
}
