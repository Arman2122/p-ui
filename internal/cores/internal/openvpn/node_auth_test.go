package openvpn

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type nodeAuthClock struct {
	now time.Time
	err error
}

func (c nodeAuthClock) Now() (time.Time, error) { return c.now, c.err }

func TestNodeManagementUsesTrustedAuthorizationTime(t *testing.T) {
	wall := time.Now()
	client := NodeClient{ClientID: "client", Username: "alice", Password: "long-password", Addresses: []netip.Addr{netip.MustParseAddr("10.8.0.2")}}
	a := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{client}})
	a.Grants([]governor.Grant{{ClientID: "client", Policy: limits.Policy{Enabled: true, ExpiresUnixMilli: wall.Add(time.Hour).UnixMilli()}}})
	event := nodeEvent{kind: "CONNECT", cid: 1, env: map[string]string{"username": client.Username, "password": client.Password}}
	a.SetClock(nodeAuthClock{now: wall.Add(2 * time.Hour)})
	if _, err := a.authorize(t.Context(), event, wall); !errors.Is(err, errNodeAuthorization) {
		t.Fatal("wall rollback resurrected expired OpenVPN authentication")
	}
	a.SetClock(nodeAuthClock{err: limits.ErrClock})
	if _, err := a.authorize(t.Context(), event, wall); !errors.Is(err, errNodeAuthorization) {
		t.Fatal("untrusted time admitted OpenVPN authentication")
	}
	a.SetClock(nodeAuthClock{now: wall})
	if address, err := a.authorize(t.Context(), event, wall); err != nil || address != client.Addresses[0] {
		t.Fatalf("valid trusted authentication: %s %v", address, err)
	}
}

func TestNodeManagementReservesBeforeEstablishment(t *testing.T) {
	client := NodeClient{ClientID: "client", Username: "alice", Password: "long-password", Addresses: []netip.Addr{netip.MustParseAddr("10.8.0.2"), netip.MustParseAddr("10.8.0.3")}}
	a := NewNodeAuthenticator(NodeConfiguration{Subnet: netip.MustParsePrefix("10.8.0.0/24"), Clients: []NodeClient{client}})
	a.Grants([]governor.Grant{{ClientID: "client", Policy: limits.Policy{Enabled: true}}})
	server, daemon := net.Pipe()
	defer daemon.Close()
	done := make(chan error, 1)
	go func() { done <- a.Run(t.Context(), server, nil) }()
	reader := bufio.NewReader(daemon)
	read := func(want string) {
		t.Helper()
		_ = daemon.SetReadDeadline(time.Now().Add(time.Second))
		line, err := reader.ReadString('\n')
		if err != nil || line != want {
			t.Fatalf("management got %q %v, want %q", line, err, want)
		}
	}
	read("state on\n")
	read("hold release\n")
	fmt.Fprint(daemon, "SUCCESS: state\nSUCCESS: hold\n")
	login := func(kind string, cid uint32, password string, want string) {
		t.Helper()
		fmt.Fprintf(daemon, ">CLIENT:%s,%d,0\n>CLIENT:ENV,username=alice\n>CLIENT:ENV,password=%s\n>CLIENT:ENV,END\n", kind, cid, password)
		for _, line := range strings.SplitAfter(want, "\n") {
			if line != "" {
				read(line)
			}
		}
		fmt.Fprint(daemon, "SUCCESS: authentication\n")
	}
	login("CONNECT", 1, client.Password, "client-auth 1 0\nifconfig-push 10.8.0.2 255.255.255.0\nEND\n")
	login("CONNECT", 2, client.Password, "client-auth 2 0\nifconfig-push 10.8.0.3 255.255.255.0\nEND\n")
	login("CONNECT", 3, client.Password, "client-deny 3 0 authorization-unavailable\n")
	login("REAUTH", 1, client.Password, "client-auth 1 0\nifconfig-push 10.8.0.2 255.255.255.0\nEND\n")
	login("CONNECT", 4, "wrong-password", "client-deny 4 0 denied\n")
	fmt.Fprint(daemon, ">CLIENT:DISCONNECT,1\n>CLIENT:ENV,END\n")
	login("CONNECT", 5, client.Password, "client-auth 5 0\nifconfig-push 10.8.0.2 255.255.255.0\nEND\n")
	a.Grants(nil)
	login("REAUTH", 2, client.Password, "client-deny 2 0 authorization-unavailable\n")
	fmt.Fprint(daemon, ">CLIENT:ADDRESS,2,10.8.0.99,1\n")
	if err := <-done; !errors.Is(err, ErrNodeManagement) {
		t.Fatalf("unexpected address accepted: %v", err)
	}
}

func TestNodeManagementBoundsUntrustedEnvironment(t *testing.T) {
	for _, body := range []string{
		">CLIENT:CONNECT,1,0\n>CLIENT:ENV,username=alice\n>CLIENT:ENV,username=bob\n",
		">CLIENT:CONNECT,1,0\n>CLIENT:CONNECT,2,0\n",
		">CLIENT:CONNECT,18446744073709551615,0\n",
		">CLIENT:CONNECT,1,0\n" + strings.Repeat(">CLIENT:ENV,IV_DATA="+strings.Repeat("x", 4000)+"\n", 17),
	} {
		server, daemon := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- NewNodeAuthenticator(NodeConfiguration{}).Run(t.Context(), server, nil) }()
		reader := bufio.NewReader(daemon)
		_, _ = reader.ReadString('\n')
		_, _ = reader.ReadString('\n')
		fmt.Fprint(daemon, body)
		if err := <-done; !errors.Is(err, ErrNodeManagement) {
			t.Fatalf("invalid management event: %v", err)
		}
		_ = daemon.Close()
	}
}
