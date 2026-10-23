package mtproto

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
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
	"github.com/Arman2122/mtg-proxy/nodeweb"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	proxyproto "github.com/pires/go-proxyproto"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type webStream struct {
	*websocket.Conn
	buffer []byte
}

func (s *webStream) Write(data []byte) (int, error) {
	encoded, err := (nodeweb.Limits{}).EncodeBatch(nil, []nodeweb.Frame{{Type: nodeweb.TypeData, StreamID: 1, Payload: bytes.Clone(data)}}, nodeweb.ClientToRelay)
	if err != nil {
		return 0, err
	}
	if err := s.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *webStream) Read(data []byte) (int, error) {
	for len(s.buffer) == 0 {
		_, payload, err := s.ReadMessage()
		if err != nil {
			return 0, err
		}
		frames, err := (nodeweb.Limits{}).DecodeBatch(payload, nodeweb.RelayToClient)
		if err != nil {
			return 0, err
		}
		for _, frame := range frames {
			if frame.StreamID == 1 && frame.Type == nodeweb.TypeClose {
				return 0, io.EOF
			}
			if frame.StreamID == 1 && frame.Type == nodeweb.TypeData {
				s.buffer = append(s.buffer, frame.Payload...)
			}
		}
	}
	n := copy(data, s.buffer)
	s.buffer = s.buffer[n:]
	return n, nil
}

func (s *webStream) SetDeadline(deadline time.Time) error {
	if err := s.SetReadDeadline(deadline); err != nil {
		return err
	}
	return s.SetWriteDeadline(deadline)
}

func webClient(t *testing.T, account *limits.Account, target string) essentials.Conn {
	t.Helper()
	client, _, _ := webClientRuntime(t, account, target)
	return client
}

func webClientRuntime(t *testing.T, account *limits.Account, target string) (essentials.Conn, *webRuntime, *websocket.Conn) {
	t.Helper()
	secret, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	secrets := mtglib.SecretConfig{Secrets: map[string]mtglib.Secret{id: secret}}
	ntw := telegramNetwork{Network: network.New(nil, "penhoon-test", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0), target: target}
	options := mtglib.ProxyOpts{
		Network: ntw, Secrets: secrets.Secrets, AntiReplayCache: antireplay.NewNoop(), IPBlocklist: ipblocklist.NewNoop(),
		IPAllowlist: allowAll{ipblocklist.NewNoop()}, EventStream: events.NewNoopStream(), Logger: logger.NewNoopLogger(), ListenerModes: mtglib.ModeSetOf(mtglib.SecretModeDD),
	}
	proxy, err := New(options, func(name string) (*limits.Account, bool) { return account, name == id })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Shutdown)
	inbound := Inbound{ID: uuid.NewString(), WEB: &WEB{Bind: "127.0.0.1:0", Host: "relay.example.com", CoverURL: "https://cover.example.com"}}
	runtime, err := startWEB(t.Context(), inbound, secrets, proxy, options.Network, options.Logger, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.close)
	obfuscated, carrier := webSession(t, runtime, secret)
	return obfuscated, runtime, carrier
}

func webSession(t *testing.T, runtime *webRuntime, secret mtglib.Secret) (essentials.Conn, *websocket.Conn) {
	t.Helper()
	address := runtime.listener.Addr().String()
	dial := func(ctx context.Context, network, destination string) (net.Conn, error) {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, destination)
		if err != nil {
			return nil, err
		}
		header := &proxyproto.Header{
			Version: 2, Command: proxyproto.PROXY, TransportProtocol: proxyproto.TCPv4,
			SourceAddr: &net.TCPAddr{IP: net.IPv4(203, 0, 113, 42), Port: 40123}, DestinationAddr: conn.RemoteAddr(),
		}
		if _, err := header.WriteTo(conn); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
	transport := &http.Transport{DialContext: dial}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	dialer := websocket.Dialer{NetDialContext: dial, HandshakeTimeout: 5 * time.Second}
	return webSessionTransport(t, secret, client, dialer, "http://"+address, "ws://"+address, "relay.example.com", nil)
}

func webSessionTransport(t *testing.T, secret mtglib.Secret, client *http.Client, dialer websocket.Dialer, httpOrigin, socketOrigin, authority string, headers http.Header) (essentials.Conn, *websocket.Conn) {
	t.Helper()
	capability, err := nodeweb.Derive(secret, "relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpOrigin+"/?bridge="+capability.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("WEB bootstrap HTTP %d: %v", response.StatusCode, err)
	}
	if !bytes.Contains(body, []byte(`"host":"`+authority+`"`)) || !strings.Contains(response.Header.Get("Content-Security-Policy"), "wss://"+authority+";") {
		t.Fatal("WEB bootstrap or CSP lost configured public authority")
	}
	_, tail, found := strings.Cut(string(body), `"token":"`)
	if !found {
		t.Fatal("missing WEB bootstrap token")
	}
	token, _, found := strings.Cut(tail, `"`)
	if !found {
		t.Fatal("invalid WEB bootstrap token")
	}
	hello, err := (nodeweb.Limits{}).EncodeBatch(nil, []nodeweb.Frame{{Type: nodeweb.TypeHello, Payload: []byte{1}}}, nodeweb.ClientToRelay)
	if err != nil {
		t.Fatal(err)
	}
	request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, httpOrigin+"/api/v1/session", bytes.NewReader(hello))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("WEB session HTTP %d", response.StatusCode)
	}
	token = response.Header.Get("X-Session-Token")
	dialer.Subprotocols = []string{"tproxy-v1." + token}
	socketHeaders := headers.Clone()
	if socketHeaders == nil {
		socketHeaders = make(http.Header)
	}
	socketHeaders.Set("Origin", "https://"+authority)
	carrier, response, err := dialer.DialContext(t.Context(), socketOrigin+"/api/v1/ws", socketHeaders)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = carrier.Close() })
	stream := &webStream{Conn: carrier}
	if err := stream.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	open, err := (nodeweb.Limits{}).EncodeBatch(nil, []nodeweb.Frame{{Type: nodeweb.TypeOpen, StreamID: 1}}, nodeweb.ClientToRelay)
	if err != nil {
		t.Fatal(err)
	}
	if err := carrier.WriteMessage(websocket.BinaryMessage, open); err != nil {
		t.Fatal(err)
	}
	obfuscated, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(essentials.WrapNetConn(stream), 2, obfuscation.TagPaddedIntermediate)
	if err != nil {
		t.Fatal(err)
	}
	return obfuscated, carrier
}

func TestWEBListenerFailureAndShutdownCloseCarriers(t *testing.T) {
	account, err := limits.New(limits.Policy{
		Term: "web-shutdown", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, web, carrier := webClientRuntime(t, account, telegramEcho(t))
	managed := &Managed{instances: map[string]*running{"web": {web: web}}}
	if err := managed.Check(); err != nil {
		t.Fatal(err)
	}
	_ = web.listener.Close()
	deadline := time.Now().Add(3 * time.Second)
	for managed.Check() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := managed.Check(); err == nil {
		t.Fatal("stopped WEB listener remained healthy")
	}
	web.close()
	if err := carrier.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for range 16 {
		_, _, err := carrier.ReadMessage()
		if err == nil {
			continue
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("WEB shutdown left a carrier alive")
		}
		return
	}
	t.Fatal("WEB shutdown kept sending carrier frames")
}

func TestWEBAndNativeMTProtoShareOneQuota(t *testing.T) {
	account, err := limits.New(limits.Policy{
		Term: "web-shared", Revision: 1, Enabled: true, CreditCeiling: 4096,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := telegramEcho(t)
	clients := []essentials.Conn{proxyClient(t, account, target), webClient(t, account, target)}
	for _, client := range clients {
		payload := bytes.Repeat([]byte{17}, 512)
		if _, err := client.Write(bytes.Clone(payload)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(client, got); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("WEB/native echo: %v", err)
		}
	}
	if _, err := clients[1].Write(bytes.Repeat([]byte{18}, 4096)); err != nil {
		t.Fatal(err)
	}
	if n, err := clients[1].Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("WEB quota leaked bytes: %d %v", n, err)
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload+snapshot.AdmittedDownload != 4096 || snapshot.MeasuredUpload+snapshot.MeasuredDownload > 4096 {
		t.Fatalf("WEB/native quota accounting: %+v", snapshot)
	}
}

func TestWEBAndNativeMTProtoShareOneRate(t *testing.T) {
	account, err := limits.New(limits.Policy{
		Term: "web-rate", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 32768, BurstBytes: 256}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := telegramEcho(t)
	clients := []essentials.Conn{proxyClient(t, account, target), webClient(t, account, target)}
	started := time.Now()
	var group sync.WaitGroup
	for _, client := range clients {
		group.Go(func() {
			payload := bytes.Repeat([]byte{19}, 4096)
			if _, err := client.Write(bytes.Clone(payload)); err != nil {
				t.Error(err)
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(client, got); err != nil || !bytes.Equal(payload, got) {
				t.Errorf("WEB/native rate echo: %v", err)
			}
		})
	}
	group.Wait()
	if elapsed := time.Since(started); elapsed < 1750*time.Millisecond {
		t.Fatalf("WEB/native aggregate rate exceeded: %s", elapsed)
	}
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 8192 {
		t.Fatalf("WEB/native upload=%d", snapshot.AdmittedUpload)
	}
}
