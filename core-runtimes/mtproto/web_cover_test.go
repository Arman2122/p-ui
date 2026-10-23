package mtproto

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
	network "github.com/Arman2122/mtg-proxy/network/v2"
)

func coverSOCKS(t *testing.T, target string) (string, <-chan string) {
	t.Helper()
	bound, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &trackedListener{Listener: bound, connections: map[*trackedConnection]bool{}}
	requests := make(chan string, 8)
	var group sync.WaitGroup
	group.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			group.Go(func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				var greeting [2]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil {
					return
				}
				if greeting[0] != 5 || greeting[1] == 0 {
					t.Error("invalid SOCKS greeting")
					return
				}
				methods := make([]byte, int(greeting[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return
				}
				var request [4]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil {
					return
				}
				if request[0] != 5 || request[1] != 1 || request[2] != 0 {
					t.Error("invalid SOCKS connect")
					return
				}
				var host string
				switch request[3] {
				case 3:
					var size [1]byte
					if _, err := io.ReadFull(conn, size[:]); err != nil {
						return
					}
					name := make([]byte, int(size[0]))
					if _, err := io.ReadFull(conn, name); err != nil {
						return
					}
					host = string(name)
				case 1, 4:
					size := 4
					if request[3] == 4 {
						size = 16
					}
					address := make([]byte, size)
					if _, err := io.ReadFull(conn, address); err != nil {
						return
					}
					host = net.IP(address).String()
				default:
					t.Error("invalid SOCKS address type")
					return
				}
				var port [2]byte
				if _, err := io.ReadFull(conn, port[:]); err != nil {
					return
				}
				endpoint := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
				requests <- endpoint
				if endpoint != "cover.example.com:443" {
					return
				}
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				defer upstream.Close()
				if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				done := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(done) }()
				_, _ = io.Copy(conn, upstream)
				_ = conn.Close()
				<-done
			})
		}
	})
	t.Cleanup(func() { _ = listener.Close(); group.Wait() })
	return listener.Addr().String(), requests
}

func TestWEBRoutedCoverUsesNodeSOCKSAndClosesIdleConnections(t *testing.T) {
	managed, config, _, account, authority := directWEBFixture(t, 0)
	certificate, key := authority.issue(t, "cover.example.com", 20)
	pair, err := tls.X509KeyPair([]byte(certificate), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	closed, idle := make(chan struct{}, 1), make(chan struct{}, 1)
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || request.TLS.ServerName != "cover.example.com" {
			t.Error("cover TLS hostname changed")
		}
		_, _ = io.WriteString(response, "routed-cover")
	}))
	cover.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	cover.Config.ConnState = func(_ net.Conn, status http.ConnState) {
		switch status {
		case http.StateClosed:
			select {
			case closed <- struct{}{}:
			default:
			}
		case http.StateIdle:
			select {
			case idle <- struct{}{}:
			default:
			}
		}
	}
	cover.StartTLS()
	t.Cleanup(cover.Close)
	proxyAddress, requests := coverSOCKS(t, cover.Listener.Addr().String())
	var environmentProxyRequests atomic.Int32
	poison := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		environmentProxyRequests.Add(1)
		response.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(poison.Close)
	t.Setenv("HTTPS_PROXY", poison.URL)
	t.Setenv("https_proxy", poison.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	managed.network = network.New(nil, "web-cover-test", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0)
	config.Inbounds[0].SOCKS5 = "socks5://" + proxyAddress
	applyManagedWEB(t, managed, config, account)
	process := managed.instances[config.Inbounds[0].ID]
	process.web.coverTransport.TLSClientConfig.RootCAs = authority.pool
	client, _ := directWEBTransport(t, process.web, authority, "")
	endpoint := "https://" + net.JoinHostPort("relay.example.com", strconv.Itoa(int(config.Inbounds[0].WEB.PublicPort))) + "/"
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "routed-cover" {
		t.Fatalf("cover bypassed configured node egress: %d %q %v", response.StatusCode, body, err)
	}
	select {
	case target := <-requests:
		if target != "cover.example.com:443" {
			t.Fatalf("cover target was replaced by environment proxy: %s", target)
		}
	case <-time.After(time.Second):
		t.Fatal("cover bypassed configured SOCKS network")
	}
	if environmentProxyRequests.Load() != 0 {
		t.Fatal("cover trusted environment proxy settings")
	}
	select {
	case <-idle:
	case <-time.After(time.Second):
		t.Fatal("cover connection did not become reusable")
	}
	if err := managed.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("WEB shutdown leaked its upstream idle connection")
	}
}

type canceledCoverNetwork struct {
	mtglib.Network
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (n *canceledCoverNetwork) DialContext(ctx context.Context, _, _ string) (essentials.Conn, error) {
	n.once.Do(func() { close(n.started) })
	<-ctx.Done()
	close(n.canceled)
	return nil, ctx.Err()
}

func TestWEBCoverCancellationStopsRoutedDial(t *testing.T) {
	managed, config, _, account, authority := directWEBFixture(t, 0)
	blocked := &canceledCoverNetwork{Network: network.New(nil, "web-cancel", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0), started: make(chan struct{}), canceled: make(chan struct{})}
	managed.network = blocked
	config.Inbounds[0].WEB.CoverURL = "https://cancel-cover.example.com"
	applyManagedWEB(t, managed, config, account)
	process := managed.instances[config.Inbounds[0].ID]
	client, _ := directWEBTransport(t, process.web, authority, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort("relay.example.com", strconv.Itoa(int(config.Inbounds[0].WEB.PublicPort)))+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("cover did not enter configured network")
	}
	cancel()
	select {
	case <-blocked.canceled:
	case <-time.After(time.Second):
		t.Fatal("cover request cancellation did not stop routed dial")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cover result=%v", err)
	}
}
