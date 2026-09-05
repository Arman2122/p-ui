package ssh

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestReservedDestinationsAreRefused(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "127.0.0.53", "::1", "::", "0.0.0.0", "255.255.255.255",
		"10.1.2.3", "172.16.5.5", "172.31.255.1", "192.168.1.1", "100.64.0.9", "169.254.10.10",
		"224.0.0.251", "fd00::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:192.168.0.1",
	} {
		if err := destinationRefused(netip.MustParseAddr(raw)); !errors.Is(err, errDestinationRefused) {
			t.Errorf("%s was allowed: %v", raw, err)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.32.0.1", "2606:4700:4700::1111"} {
		if err := destinationRefused(netip.MustParseAddr(raw)); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
	// The box's own addresses are the host too, whatever range they are in.
	for addr := range ownAddresses() {
		if err := destinationRefused(addr); !errors.Is(err, errDestinationRefused) {
			t.Errorf("own address %s was allowed: %v", addr, err)
		}
	}
}

/*
The product's own boundary: a client of an unrouted inbound cannot open a
forward to anything on the server itself, whether it names the address or a
name that resolves to it. The bridge path is not under test here; Xray's
template carries the same rule for it.
*/
func TestAForwardCannotReachTheHostItself(t *testing.T) {
	echo := echoListener(t)
	keys := &memoryHostKey{pem: testHostKeyPEM(t)}
	c := New(Deps{LoadHostKey: keys.load, StoreHostKey: keys.store})
	inst := core.Instance{ID: 7, Kind: Kind, Tag: "ssh-7", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{"transport":"tcp"}`, Users: []core.User{userN(0)}}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })

	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	port := strconv.Itoa(echo.Addr().(*net.TCPAddr).Port)
	for _, target := range []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port} {
		if fwd, err := client.Dial("tcp", target); err == nil {
			_ = fwd.Close()
			t.Fatalf("a forward to %s reached a loopback service", target)
		}
	}
}

// The policy sits on the default dialer alone: an injected dialer, which the
// tests and the exits use, is trusted as it is.
func TestTheDialerPolicyAppliesToTheDefaultDialerOnly(t *testing.T) {
	var dialled sync.Once
	custom := func(context.Context, string, string) (net.Conn, error) {
		dialled.Do(func() {})
		return nil, errors.New("custom")
	}
	off, _ := parseSettings(`{"transport":"tcp"}`)
	if _, err := New(Deps{Dial: custom}).dialerFor(off)(context.Background(), "tcp", "127.0.0.1:1"); err == nil || err.Error() != "custom" {
		t.Fatalf("an injected dialer was not used as given: %v", err)
	}
	if _, err := New(Deps{}).dialerFor(off)(context.Background(), "tcp", "127.0.0.1:1"); !errors.Is(err, errDestinationRefused) {
		t.Fatalf("the default dialer let a loopback destination through: %v", err)
	}
}
