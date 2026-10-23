package mtproto

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/antireplay"
	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/events"
	"github.com/Arman2122/mtg-proxy/ipblocklist"
	"github.com/Arman2122/mtg-proxy/logger"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/mtglib/obfuscation"
	network "github.com/Arman2122/mtg-proxy/network/v2"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type telegramNetwork struct {
	mtglib.Network
	target string
}

type loopbackAllowlist struct{ mtglib.IPBlocklist }

func (loopbackAllowlist) Contains(ip net.IP) bool { return ip.IsLoopback() }

func (n telegramNetwork) Dial(_, _ string) (essentials.Conn, error) {
	return n.DialContext(context.Background(), "tcp", n.target)
}

func (n telegramNetwork) DialContext(ctx context.Context, _, _ string) (essentials.Conn, error) {
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", n.target)
	if err != nil {
		return nil, err
	}
	return essentials.WrapNetConn(connection), nil
}

func telegramEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
				_, _, plain, err := (obfuscation.Obfuscator{}).ReadHandshake(essentials.WrapNetConn(connection), mtglib.ModeSetOf(mtglib.SecretModePlain).Tags())
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(plain, plain)
			})
		}
	})
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	return listener.Addr().String()
}

func proxyClient(t *testing.T, account *limits.Account, target string, modes ...mtglib.SecretMode) essentials.Conn {
	t.Helper()
	mode := mtglib.SecretModeDD
	if len(modes) != 0 {
		mode = modes[0]
	}
	host := ""
	if mode == mtglib.SecretModeFakeTLS {
		host = "cover.example.com"
	}
	secret, err := mtglib.GenerateSecretMode(mode, host)
	if err != nil {
		t.Fatal(err)
	}
	ntw := telegramNetwork{Network: network.New(nil, "penhoon-test", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0), target: target}
	proxy, err := New(mtglib.ProxyOpts{
		Secrets: map[string]mtglib.Secret{"client": secret}, Network: ntw,
		AntiReplayCache: antireplay.NewStableBloomFilter(1<<20, 0.01), IPBlocklist: ipblocklist.NewNoop(), IPAllowlist: loopbackAllowlist{ipblocklist.NewNoop()},
		EventStream: events.NewNoopStream(), Logger: logger.NewNoopLogger(), ListenerModes: mtglib.ModeSetOf(mode),
	},
		func(name string) (*limits.Account, bool) { return account, name == "client" })
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		proxy.Shutdown()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- proxy.ServeWithPolicy(listener, mtglib.ListenerPolicy{Modes: mtglib.ModeSetOf(mode)})
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.Shutdown()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	transport := essentials.WrapNetConn(raw)
	if mode == mtglib.SecretModeFakeTLS {
		transport = fakeTLSClient(t, transport, secret)
	}
	client, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(transport, 2, obfuscation.TagPaddedIntermediate)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestAuthenticatedMTProtoStreamsShareQuota(t *testing.T) {
	account, err := limits.New(limits.Policy{
		Term: "mtproto", Revision: 1, Enabled: true, CreditCeiling: 4096,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := telegramEcho(t)
	first, second := proxyClient(t, account, target, mtglib.SecretModeFakeTLS), proxyClient(t, account, target)
	for _, client := range []essentials.Conn{first, second} {
		payload := bytes.Repeat([]byte("m"), 512)
		if _, err := client.Write(bytes.Clone(payload)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(client, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("MTProto payload changed")
		}
	}
	if _, err := second.Write(bytes.Repeat([]byte("x"), 4096)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 1)
	if n, err := second.Read(got); n != 0 || err == nil {
		t.Fatalf("quota leaked response bytes: %d,%v", n, err)
	}
	snapshot := account.Snapshot()
	if snapshot.AdmittedUpload+snapshot.AdmittedDownload != 4096 || snapshot.MeasuredUpload+snapshot.MeasuredDownload > 4096 {
		t.Fatalf("shared MTProto accounting=%+v", snapshot)
	}
}

func TestAuthenticatedMTProtoStreamsShareRate(t *testing.T) {
	account, err := limits.New(limits.Policy{
		Term: "mtproto", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 32768, BurstBytes: 256}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := telegramEcho(t)
	clients := []essentials.Conn{proxyClient(t, account, target), proxyClient(t, account, target)}
	started := time.Now()
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Go(func() {
			payload := bytes.Repeat([]byte("r"), 4096)
			if _, err := client.Write(bytes.Clone(payload)); err != nil {
				t.Error(err)
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(client, got); err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(got, payload) {
				t.Error("MTProto payload changed")
			}
		})
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed < 1750*time.Millisecond {
		t.Fatalf("aggregate upload exceeded rate: %s", elapsed)
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 8192 {
		t.Fatalf("upload admission=%d", snapshot.AdmittedUpload)
	}
}
