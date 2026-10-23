package xrayruntime

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCanonicalIdentityPreservesHTTPAndMixedUsernames(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			connection, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	t.Cleanup(func() { _ = echo.Close(); <-finished })
	clientID := uuid.NewString()
	account, err := limits.New(limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 8192, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	ports := []int{availablePort(t), availablePort(t)}
	identities := []ClientIdentity{{InboundTag: "http", Username: "existing-user", ClientID: clientID}, {InboundTag: "mixed", Username: "existing-user", ClientID: clientID}}
	config := map[string]any{
		"log":     map[string]any{"loglevel": "none"},
		"penhoon": Metadata{Version: 1, ClientIdentities: identities},
		"inbounds": []map[string]any{
			{"tag": "http", "protocol": "http", "listen": "127.0.0.1", "port": ports[0], "settings": map[string]any{"accounts": []map[string]string{{"user": "existing-user", "pass": "test-password"}}}},
			{"tag": "mixed", "protocol": "mixed", "listen": "127.0.0.1", "port": ports[1], "settings": map[string]any{"auth": "password", "accounts": []map[string]string{{"user": "existing-user", "pass": "test-password"}}}},
		},
		"outbounds": []map[string]any{{"tag": "deny", "protocol": "blackhole"}, {"tag": "allowed", "protocol": "freedom", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
		"routing":   map[string]any{"rules": []map[string]any{{"type": "field", "user": []string{clientID}, "outboundTag": "allowed"}}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var runtime Runtime
	lookup := func(id string) (*limits.Account, bool) { return account, id == clientID }
	if err := runtime.Apply(t.Context(), data, lookup); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	exchange := func(index int) {
		t.Helper()
		endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[index]))
		var connection net.Conn
		var reader io.Reader
		if index == 0 {
			connection, err = net.DialTimeout("tcp", endpoint, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", echo.Addr(), echo.Addr(), base64.StdEncoding.EncodeToString([]byte("existing-user:test-password"))); err != nil {
				t.Fatal(err)
			}
			buffered := bufio.NewReader(connection)
			response, err := http.ReadResponse(buffered, &http.Request{Method: http.MethodConnect})
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("HTTP CONNECT status=%d", response.StatusCode)
			}
			reader = buffered
		} else {
			dialer, err := proxy.SOCKS5("tcp", endpoint, &proxy.Auth{User: "existing-user", Password: "test-password"}, &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			connection, err = dialer.Dial("tcp", echo.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			reader = connection
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		payload := bytes.Repeat([]byte("a"), 1024)
		if _, err := connection.Write(payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(reader, got); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("mapped client forwarding: %v", err)
		}
	}
	exchange(0)
	exchange(1)
	if got := account.Snapshot(); got.AdmittedUpload != 2048 || got.AdmittedDownload != 2048 {
		t.Fatalf("canonical account usage=%+v", got)
	}
	config["penhoon"] = Metadata{Version: 1, ClientIdentities: identities[:1]}
	invalid, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Apply(t.Context(), invalid, lookup); err == nil || err.Error() != "Xray authenticated account has no client identity" {
		t.Fatalf("unbound account accepted: %v", err)
	}
	exchange(1)
	if got := account.Snapshot(); got.AdmittedUpload != 3072 || got.AdmittedDownload != 3072 {
		t.Fatalf("rejected update disrupted existing identity=%+v", got)
	}
}
