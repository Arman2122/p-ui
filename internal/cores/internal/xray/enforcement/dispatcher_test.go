package enforcement

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
	"golang.org/x/net/proxy"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func echoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(c, c)
			})
		}
	})
	t.Cleanup(func() { _ = l.Close(); wg.Wait() })
	return l.Addr().String()
}

func startCore(t *testing.T, lookup Lookup, ports ...int) {
	t.Helper()
	inbounds := make([]map[string]any, 0, len(ports))
	for i, port := range ports {
		inbounds = append(inbounds, map[string]any{
			"tag": "inbound-" + strconv.Itoa(i), "listen": "127.0.0.1", "port": port, "protocol": "socks",
			"settings": map[string]any{"auth": "password", "accounts": []map[string]string{{"user": "client-c", "pass": "test-password"}}},
		})
	}
	startConfiguredCore(t, lookup, inbounds)
}

func startConfiguredCore(t *testing.T, lookup Lookup, inbounds []map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"log":      map[string]string{"loglevel": "none"},
		"inbounds": inbounds, "outbounds": []map[string]any{{
			"protocol": "freedom", "tag": "direct",
			"settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var input conf.Config
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	cfg, err := input.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(cfg); err != nil {
		t.Fatal(err)
	}
	server, err := core.NewWithContext(WithAccounts(context.Background(), lookup), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
}

func dialClient(t *testing.T, port int, target string) net.Conn {
	t.Helper()
	d, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		&proxy.Auth{User: "client-c", Password: "test-password"}, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRealXraySharesQuotaAcrossInbounds(t *testing.T) {
	target := echoServer(t)
	a, err := limits.New(limits.Policy{
		Term: "term-a", Revision: 1, Enabled: true, CreditCeiling: 4096,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	ports := []int{freePort(t), freePort(t)}
	startCore(t, func(id string) (*limits.Account, bool) { return a, id == "client-c" }, ports...)
	payload := bytes.Repeat([]byte("q"), 1024)
	for _, port := range ports {
		c := dialClient(t, port, target)
		if _, err := c.Write(payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("echo differs")
		}
		_ = c.Close()
	}
	s := a.Snapshot()
	if s.AdmittedUpload != 2048 || s.AdmittedDownload != 2048 {
		t.Fatalf("traffic=%+v", s)
	}
	c := dialClient(t, ports[0], target)
	_, _ = c.Write([]byte("must-not-forward"))
	one := make([]byte, 1)
	if n, err := c.Read(one); n != 0 || err == nil {
		t.Fatalf("exhausted read=(%d,%v)", n, err)
	}
	if got := a.Snapshot(); got.AdmittedUpload != 2048 || got.AdmittedDownload != 2048 {
		t.Fatalf("quota grew: %+v", got)
	}
}

func TestRealXraySharesRateAcrossConnections(t *testing.T) {
	target := echoServer(t)
	a, err := limits.New(limits.Policy{
		Term: "term-a", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload:   limits.Rate{Mode: limits.Limited, BitsPerSecond: 81920, BurstBytes: 1024},
		Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	ports := []int{freePort(t), freePort(t)}
	startCore(t, func(id string) (*limits.Account, bool) { return a, id == "client-c" }, ports...)
	conns := []net.Conn{dialClient(t, ports[0], target), dialClient(t, ports[1], target)}
	payload := bytes.Repeat([]byte("r"), 5120)
	start := time.Now()
	errs := make(chan error, 2)
	for _, c := range conns {
		go func() {
			if _, err := c.Write(payload); err != nil {
				errs <- err
				return
			}
			got := make([]byte, len(payload))
			_, err := io.ReadFull(c, got)
			if err == nil && !bytes.Equal(got, payload) {
				err = io.ErrUnexpectedEOF
			}
			errs <- err
		}()
	}
	for range conns {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 850*time.Millisecond {
		t.Fatalf("shared 10 KiB transferred in %s", elapsed)
	}
	if got := a.Snapshot().AdmittedUpload; got != 10240 {
		t.Fatalf("uploaded=%d", got)
	}
}
