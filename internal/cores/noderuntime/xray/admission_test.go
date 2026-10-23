package xrayruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestOnlyAuthenticatedDeniedXrayAttemptsRequestAuthority(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepts atomic.Int32
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			wg.Go(func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				_, _ = io.Copy(conn, conn)
			})
		}
	})
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	client := uuid.NewString()
	port := availablePort(t)
	config := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"penhoon":   Metadata{Version: 1, ClientIdentities: []ClientIdentity{{InboundTag: "node-inbound", Username: "original-name", ClientID: client}}},
		"inbounds":  []map[string]any{{"tag": "node-inbound", "protocol": "mixed", "listen": "127.0.0.1", "port": port, "settings": map[string]any{"auth": "password", "accounts": []map[string]string{{"user": "original-name", "pass": "correct-password"}}}}},
		"outbounds": []map[string]any{{"protocol": "freedom", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var wrongScope atomic.Bool
	ctx := admission.WithHandler(t.Context(), func(_ context.Context, id, inbound string) error {
		calls.Add(1)
		if id != client || inbound != "node-inbound" {
			wrongScope.Store(true)
		}
		return nil
	})
	account, err := limits.New(limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 8192, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	var funded atomic.Bool
	var runtime Runtime
	if err := runtime.Apply(ctx, data, func(id string) (*limits.Account, bool) { return account, id == client && funded.Load() }); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	exchange := func(password string) bool {
		t.Helper()
		dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), &proxy.Auth{User: "original-name", Password: password}, &net.Dialer{Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		attempt, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		conn, err := dialer.(proxy.ContextDialer).DialContext(attempt, "tcp", listener.Addr().String())
		if err != nil {
			return false
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		payload := []byte("client-payload")
		if _, err := conn.Write(payload); err != nil {
			return false
		}
		got := make([]byte, len(payload))
		_, err = io.ReadFull(conn, got)
		return err == nil && bytes.Equal(got, payload)
	}
	if exchange("wrong-password") || calls.Load() != 0 || accepts.Load() != 0 {
		t.Fatal("bad credential requested or received authority")
	}
	if exchange("correct-password") || calls.Load() != 1 || wrongScope.Load() || accepts.Load() != 0 {
		t.Fatalf("auth-only attempt calls=%d scope=%v outbound=%d", calls.Load(), wrongScope.Load(), accepts.Load())
	}
	funded.Store(true)
	if !exchange("correct-password") || calls.Load() != 1 || accepts.Load() != 1 {
		t.Fatalf("separately funded attempt calls=%d outbound=%d", calls.Load(), accepts.Load())
	}
	if got := account.Snapshot(); got.MeasuredUpload != 14 || got.MeasuredDownload != 14 {
		t.Fatalf("funded traffic counters=%+v", got)
	}
}
