//go:build linux

package banset

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The real firewall on the VM: gated on PUI_BANSET_E2E=1, root and nft, and
// skipped elsewhere. It uses loopback aliases as distinct sources.
func e2e(t *testing.T) {
	t.Helper()
	if os.Getenv("PUI_BANSET_E2E") != "1" {
		t.Skip("set PUI_BANSET_E2E=1 to run against the real firewall (needs root and nft)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
}

// echoListener answers every byte it is sent, so a flow that is still alive can
// be told from one the firewall has silenced.
func echoListener(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

func dialFrom(source string, port uint16) (net.Conn, error) {
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}, Timeout: 1500 * time.Millisecond}
	return d.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func echoes(c net.Conn) bool {
	_ = c.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	if _, err := c.Write([]byte("x")); err != nil {
		return false
	}
	buf := make([]byte, 1)
	_, err := c.Read(buf)
	return err == nil
}

func TestABannedSourceIsDroppedOnItsPortAloneAndItsOpenFlowDies(t *testing.T) {
	e2e(t)
	ctx := context.Background()
	m := HostManager()
	t.Cleanup(func() { _ = m.Converge(ctx, time.Now(), nil) })

	bannedPort := echoListener(t)
	otherPort := echoListener(t)

	open, err := dialFrom("127.0.0.2", bannedPort)
	if err != nil {
		t.Fatalf("dial before the ban: %v", err)
	}
	defer open.Close()
	if !echoes(open) {
		t.Fatal("the flow did not echo before the ban; the harness is wrong, not the firewall")
	}

	now := time.Now()
	ban := Entry{Source: netip.MustParseAddr("127.0.0.2"), Port: bannedPort, Until: now.Add(time.Minute)}
	if err := m.Converge(ctx, now, []Entry{ban}); err != nil {
		t.Fatalf("converge: %v", err)
	}

	if c, err := dialFrom("127.0.0.2", bannedPort); err == nil {
		_ = c.Close()
		t.Fatal("the banned source connected to the banned port")
	}
	if c, err := dialFrom("127.0.0.3", bannedPort); err != nil {
		t.Fatalf("another source was dropped on the banned port: %v", err)
	} else {
		_ = c.Close()
	}
	if c, err := dialFrom("127.0.0.2", otherPort); err != nil {
		t.Fatalf("the banned source was dropped on a port it is not banned on: %v", err)
	} else {
		_ = c.Close()
	}
	if echoes(open) {
		t.Fatal("the flow open before the ban still echoes; the drop must be terminal for an existing connection")
	}

	if err := m.Converge(ctx, time.Now(), nil); err != nil {
		t.Fatalf("release: %v", err)
	}
	if c, err := dialFrom("127.0.0.2", bannedPort); err != nil {
		t.Fatalf("the source is still dropped after its ban was released: %v", err)
	} else {
		_ = c.Close()
	}
}
