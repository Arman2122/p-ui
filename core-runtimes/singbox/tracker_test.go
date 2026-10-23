package singbox

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/anytls"
	"github.com/sagernet/sing-box/protocol/tuic"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

const clientUUID = "08776e62-5687-427a-97d7-ea7ddc0809cb"

func certificatePair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "node.test"},
		DNSNames: []string{"node.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}))
}

func freePort(t *testing.T, network string) uint16 {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := c.LocalAddr().(*net.UDPAddr).Port
		_ = c.Close()
		return uint16(port)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return uint16(port)
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
			wg.Go(func() { defer c.Close(); _ = c.SetDeadline(time.Now().Add(6 * time.Second)); _, _ = io.Copy(c, c) })
		}
	})
	t.Cleanup(func() { _ = l.Close(); wg.Wait() })
	return l.Addr().String()
}

func startPair(t *testing.T, kind string, a *limits.Account) (map[string]adapter.Outbound, []string) {
	t.Helper()
	return startPairWithAdmission(t, kind, func(id string) (*limits.Account, bool) { return a, id == "client-c" }, nil, "test-password")
}

func startPairWithAdmission(t *testing.T, kind string, lookup Lookup, demand admission.Handler, password string) (map[string]adapter.Outbound, []string) {
	t.Helper()
	cert, key := certificatePair(t)
	address := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	inTLS := option.InboundTLSOptionsContainer{TLS: &option.InboundTLSOptions{
		Enabled: true, Certificate: []string{cert}, Key: []string{key}, ALPN: []string{"h3"},
	}}
	outTLS := option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
		Enabled: true, ServerName: "node.test", Certificate: []string{cert}, ALPN: []string{"h3"},
	}}
	serverOptions := option.Options{
		Log:       &option.LogOptions{Disabled: true},
		Outbounds: []option.Outbound{{Type: "direct", Tag: "direct", Options: &option.DirectOutboundOptions{}}},
	}
	clientOptions := option.Options{Log: &option.LogOptions{Disabled: true}}
	tags := []string{"one", "two"}
	for _, tag := range tags {
		network := "tcp"
		if kind == "tuic" {
			network = "udp"
		}
		port := freePort(t, network)
		listen := option.ListenOptions{Listen: &address, ListenPort: port}
		remote := option.ServerOptions{Server: "127.0.0.1", ServerPort: port}
		var in, out any
		if kind == "tuic" {
			in = &option.TUICInboundOptions{
				ListenOptions: listen, InboundTLSOptionsContainer: inTLS,
				Users: []option.TUICUser{{Name: "client-c", UUID: clientUUID, Password: "test-password"}},
			}
			out = &option.TUICOutboundOptions{ServerOptions: remote, OutboundTLSOptionsContainer: outTLS, UUID: clientUUID, Password: password}
		} else {
			in = &option.AnyTLSInboundOptions{
				ListenOptions: listen, InboundTLSOptionsContainer: inTLS,
				Users: []option.AnyTLSUser{{Name: "client-c", Password: "test-password"}},
			}
			out = &option.AnyTLSOutboundOptions{ServerOptions: remote, OutboundTLSOptionsContainer: outTLS, Password: password}
		}
		serverOptions.Inbounds = append(serverOptions.Inbounds, option.Inbound{Type: kind, Tag: tag, Options: in})
		clientOptions.Outbounds = append(clientOptions.Outbounds, option.Outbound{Type: kind, Tag: tag, Options: out})
	}
	server, err := New(admission.WithHandler(context.Background(), demand), serverOptions, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	clientContext := service.ContextWithDefaultRegistry(Context(context.Background()))
	client, err := box.New(box.Options{Context: clientContext, Options: option.Options{
		Log:       &option.LogOptions{Disabled: true},
		Outbounds: []option.Outbound{{Type: "direct", Tag: "direct", Options: &option.DirectOutboundOptions{}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	clients := make(map[string]adapter.Outbound)
	// Standalone protocol clients avoid the host interface monitor resetting test sessions.
	for _, spec := range clientOptions.Outbounds {
		logger := client.LogFactory().NewLogger(spec.Tag)
		var out adapter.Outbound
		switch options := spec.Options.(type) {
		case *option.TUICOutboundOptions:
			out, err = tuic.NewOutbound(clientContext, client.Router(), logger, spec.Tag, *options)
		case *option.AnyTLSOutboundOptions:
			out, err = anytls.NewOutbound(clientContext, client.Router(), logger, spec.Tag, *options)
		}
		if err != nil {
			t.Fatal(err)
		}
		clients[spec.Tag] = out
		t.Cleanup(func() { _ = common.Close(out) })
		if lifecycle, ok := out.(adapter.Lifecycle); ok {
			for _, stage := range adapter.ListStartStages {
				if err := lifecycle.Start(stage); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return clients, tags
}

func dial(t *testing.T, client map[string]adapter.Outbound, tag, target string) net.Conn {
	t.Helper()
	out, ok := client[tag]
	if !ok {
		t.Fatal("outbound missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := out.DialContext(ctx, "tcp", M.ParseSocksaddr(target))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(4 * time.Second))
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func udpEchoServer(t *testing.T) string {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		data := make([]byte, 65535)
		for {
			n, remote, err := c.ReadFrom(data)
			if err != nil {
				return
			}
			if _, err := c.WriteTo(data[:n], remote); err != nil {
				return
			}
		}
	})
	t.Cleanup(func() { _ = c.Close(); wg.Wait() })
	return c.LocalAddr().String()
}

func TestRealProtocolsSharePacketQuota(t *testing.T) {
	for _, kind := range []string{"tuic", "anytls"} {
		t.Run(kind, func(t *testing.T) {
			target := udpEchoServer(t)
			remote, err := net.ResolveUDPAddr("udp", target)
			if err != nil {
				t.Fatal(err)
			}
			a, err := limits.New(limits.Policy{
				Term: "term-a", Revision: 1, Enabled: true, CreditCeiling: 4096,
				Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
			})
			if err != nil {
				t.Fatal(err)
			}
			client, tags := startPair(t, kind, a)
			for _, tag := range tags {
				c, err := client[tag].ListenPacket(context.Background(), M.ParseSocksaddr(target))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.Close() })
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				payload := bytes.Repeat([]byte("u"), 1024)
				if n, err := c.WriteTo(payload, remote); err != nil || n != len(payload) {
					t.Fatalf("write=%d,%v", n, err)
				}
				got := make([]byte, 65535)
				n, _, err := c.ReadFrom(got)
				if err != nil || !bytes.Equal(got[:n], payload) {
					t.Fatalf("packet echo length=%d err=%v", n, err)
				}
				_ = c.Close()
			}
			if got := a.Snapshot(); got.AdmittedUpload != 2048 || got.AdmittedDownload != 2048 {
				t.Fatalf("packet totals=%+v", got)
			}
		})
	}
}

func TestRealProtocolsShareClientQuota(t *testing.T) {
	for _, kind := range []string{"tuic", "anytls"} {
		t.Run(kind, func(t *testing.T) {
			target := echoServer(t)
			p := limits.Policy{
				Term: "term-a", Revision: 1, Enabled: true, CreditCeiling: 4096,
				Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
			}
			a, err := limits.New(p)
			if err != nil {
				t.Fatal(err)
			}
			client, tags := startPair(t, kind, a)
			payload := bytes.Repeat([]byte("q"), 1024)
			for _, tag := range tags {
				c := dial(t, client, tag, target)
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
			if got := a.Snapshot(); got.AdmittedUpload != 2048 || got.AdmittedDownload != 2048 {
				t.Fatalf("account=%+v", got)
			}
			c := dial(t, client, tags[0], target)
			_, _ = c.Write([]byte("no-more-credit"))
			n, err := c.Read(make([]byte, 1))
			if n != 0 || err == nil {
				t.Fatalf("exhausted read=%d,%v", n, err)
			}
		})
	}
}

func TestRealProtocolsShareClientRate(t *testing.T) {
	for _, kind := range []string{"tuic", "anytls"} {
		t.Run(kind, func(t *testing.T) {
			target := echoServer(t)
			a, err := limits.New(limits.Policy{
				Term: "term-a", Revision: 1, Enabled: true, QuotaUnlimited: true,
				Upload:   limits.Rate{Mode: limits.Limited, BitsPerSecond: 81920, BurstBytes: 1024},
				Download: limits.Rate{Mode: limits.Unlimited},
			})
			if err != nil {
				t.Fatal(err)
			}
			client, tags := startPair(t, kind, a)
			conns := []net.Conn{dial(t, client, tags[0], target), dial(t, client, tags[1], target)}
			start := time.Now()
			errs := make(chan error, 2)
			for i, c := range conns {
				go func() {
					payload := bytes.Repeat([]byte(strconv.Itoa(i)), 5120)
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
				t.Fatalf("shared transfer took %s", elapsed)
			}
		})
	}
}
