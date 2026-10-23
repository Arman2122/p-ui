package enforcement

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func tlsVLESSConnections(t *testing.T, account *limits.Account) []net.Conn {
	return configuredTLSVLESSConnections(t, account, "", echoServer(t))
}

func configuredTLSVLESSConnections(t *testing.T, account *limits.Account, flow, targetAddress string) []net.Conn {
	t.Helper()
	serverTLS, clientTLS := protocolTLS(t)
	for _, settings := range []map[string]any{serverTLS, clientTLS} {
		settings["alpn"] = []string{"http/1.1"}
		settings["minVersion"], settings["maxVersion"] = "1.3", "1.3"
	}
	const id = "55834271-a5db-44c0-8a74-f988601d427b"
	profile := protocolProfile{inbound: "vless", outbound: "vless", settings: map[string]any{"decryption": "none", "clients": []map[string]any{{"id": id, "email": "client-c", "flow": flow}}}, client: func(port int) map[string]any {
		return map[string]any{"vnext": []map[string]any{{"address": "127.0.0.1", "port": port, "users": []map[string]any{{"id": id, "encryption": "none", "flow": flow}}}}}
	}, serverStream: map[string]any{"network": "raw", "security": "tls", "tlsSettings": serverTLS}, clientStream: map[string]any{"network": "raw", "security": "tls", "tlsSettings": clientTLS}}
	ports := []int{freePort(t), freePort(t)}
	inbounds := make([]map[string]any, len(ports))
	for i, port := range ports {
		inbounds[i] = map[string]any{"tag": "tls-" + strconv.Itoa(i), "listen": "127.0.0.1", "port": port, "protocol": profile.inbound, "settings": profile.settings, "streamSettings": profile.serverStream}
	}
	startConfiguredCore(t, func(client string) (*limits.Account, bool) { return account, client == "client-c" }, inbounds)
	target, err := xnet.ParseDestination("tcp:" + targetAddress)
	if err != nil {
		t.Fatal(err)
	}
	connections := make([]net.Conn, 0, len(ports))
	for _, port := range ports {
		client := outboundCore(t, profile, port)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		connection, err := core.Dial(ctx, client, target)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		t.Cleanup(func() { stop(); _ = connection.Close(); cancel() })
		connections = append(connections, connection)
	}
	return connections
}

func tlsEcho(connection net.Conn, payload []byte) error {
	if _, err := connection.Write(payload); err != nil {
		return err
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, received); err != nil {
		return err
	}
	if !bytes.Equal(received, payload) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func exactTLSAccounting(t *testing.T, account *limits.Account, want uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		got := account.Snapshot()
		if got.AdmittedUpload == want && got.AdmittedDownload == want && got.MeasuredUpload == want && got.MeasuredDownload == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS payload accounting=%+v want %d in each direction", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTLS13VLESSSharedAccountingAndQuota(t *testing.T) {
	account, err := limits.New(limits.Policy{Term: "tls-term", Revision: 1, Enabled: true, CreditCeiling: 4096, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	connections := tlsVLESSConnections(t, account)
	payload := bytes.Repeat([]byte("t"), 1024)
	for _, connection := range connections {
		if err := tlsEcho(connection, payload); err != nil {
			t.Fatal(err)
		}
	}
	exactTLSAccounting(t, account, 2048)
	_, _ = connections[0].Write([]byte("exhausted"))
	var one [1]byte
	if n, err := io.ReadFull(connections[0], one[:]); n != 0 || err == nil {
		t.Fatalf("TLS quota exhaustion forwarded %d bytes: %v", n, err)
	}
	exactTLSAccounting(t, account, 2048)
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Fatalf("normal TLS proxy shutdown: %v", err)
		}
	}
}

func TestTLS13VLESSSharedConfiguredRate(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		t.Run(direction, func(t *testing.T) {
			policy := limits.Policy{Term: "tls-term", Revision: 1, Enabled: true, QuotaUnlimited: true, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}
			rate := limits.Rate{Mode: limits.Limited, BitsPerSecond: 81920, BurstBytes: 1024}
			if direction == "upload" {
				policy.Upload = rate
			} else {
				policy.Download = rate
			}
			account, err := limits.New(policy)
			if err != nil {
				t.Fatal(err)
			}
			connections := tlsVLESSConnections(t, account)
			payload := bytes.Repeat([]byte("r"), 5120)
			started := time.Now()
			results := make(chan error, len(connections))
			for _, connection := range connections {
				go func() { results <- tlsEcho(connection, payload) }()
			}
			for range connections {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			if elapsed := time.Since(started); elapsed < 850*time.Millisecond {
				t.Fatalf("shared TLS %s 10KiB transferred in %s", direction, elapsed)
			}
			exactTLSAccounting(t, account, 10240)
			for _, connection := range connections {
				if err := connection.Close(); err != nil {
					t.Fatalf("normal TLS proxy shutdown: %v", err)
				}
			}
		})
	}
}
