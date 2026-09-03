package ssh

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/Arman2122/p-ui/internal/core"
)

func exitRow(t *testing.T, id int, remote core.Instance, fingerprint string) core.Exit {
	t.Helper()
	settings, _ := json.Marshal(ExitSettings{Host: remote.Listen, Port: remote.Port, Username: "user0", Password: "secret-0", HostKeyFingerprint: fingerprint})
	return core.Exit{ID: id, Kind: Kind, Enable: true, Settings: string(settings)}
}

func waitSocks(t *testing.T, port int, echo net.Addr, want bool) net.Conn {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("socks dialer: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := dialer.Dial("tcp", echo.String())
		if (err == nil) == want {
			return conn
		}
		if conn != nil {
			_ = conn.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("socks %d reachable=%v after 10s, want %v (last error %v)", port, err == nil, want, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

/*
The exit half, driven against this package's own inbound as the remote: the
row is reconciled, a SOCKS CONNECT on the row's port reaches the echo listener
through the SSH tunnel, a wrong host-key fingerprint is refused, the port
never starts anything on its own, and dropping the row closes the listener.
*/
func TestExitForwardsThroughTheTunnel(t *testing.T) {
	echo := echoListener(t)
	remoteCore, remote := startCore(t, `{"transport":"tcp"}`, userN(0))
	fingerprint := remoteCore.HostKeyFingerprint()
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Fatalf("host key fingerprint = %q", fingerprint)
	}

	c := New(Deps{})
	t.Cleanup(func() { _ = c.StopAll(context.Background()) })
	exit := exitRow(t, 3, remote, fingerprint)
	port := core.ExitSocksPortFor(exit.ID)

	handle, err := c.ExitHandle(context.Background(), exit)
	if err != nil || handle.SocksPort != port || !handle.TCPOnly {
		t.Fatalf("ExitHandle = %+v, %v", handle, err)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
		t.Fatal("ExitHandle started a listener; it must stay pure")
	}
	if err := c.ValidateExit(exit); err != nil {
		t.Fatalf("ValidateExit: %v", err)
	}
	if err := c.ReconcileExits(context.Background(), []core.Exit{exit}); err != nil {
		t.Fatalf("ReconcileExits: %v", err)
	}

	conn := waitSocks(t, port, echo.Addr(), true)
	payload := []byte("through the ssh exit")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != string(payload) {
		t.Fatalf("read back %q, %v", got, err)
	}
	_ = conn.Close()

	// The same row again keeps the client; a changed fingerprint is a new,
	// refused dial, and the SOCKS port then answers "unreachable".
	if err := c.ReconcileExits(context.Background(), []core.Exit{exit}); err != nil {
		t.Fatalf("second ReconcileExits: %v", err)
	}
	wrong := exitRow(t, 3, remote, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err := c.ReconcileExits(context.Background(), []core.Exit{wrong}); err != nil {
		t.Fatalf("ReconcileExits with a wrong fingerprint must not fail synchronously: %v", err)
	}
	waitSocks(t, port, echo.Addr(), false)

	if err := c.ReconcileExits(context.Background(), nil); err != nil {
		t.Fatalf("ReconcileExits drop: %v", err)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
		t.Fatal("the SOCKS listener outlived its row")
	}
}

func TestExitSettingsAreRefusedWithoutAPin(t *testing.T) {
	for name, tt := range map[string]struct {
		settings ExitSettings
		want     string
	}{
		"no host":        {ExitSettings{Username: "u", Password: "p", HostKeyFingerprint: "SHA256:x"}, "a host is required"},
		"no credentials": {ExitSettings{Host: "h", Username: "u", HostKeyFingerprint: "SHA256:x"}, "a password or a private key"},
		"no fingerprint": {ExitSettings{Host: "h", Username: "u", Password: "p"}, "fingerprint is required"},
		"bad key":        {ExitSettings{Host: "h", Username: "u", PrivateKey: "not a key", HostKeyFingerprint: "SHA256:x"}, "private key"},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.settings)
			err := New(Deps{}).ValidateExit(core.Exit{Kind: Kind, Settings: string(raw)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateExit = %v, want %q", err, tt.want)
			}
		})
	}
}
