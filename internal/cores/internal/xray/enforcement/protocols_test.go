package enforcement

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type protocolProfile struct {
	name, inbound, outbound    string
	settings                   map[string]any
	client                     func(int) map[string]any
	serverStream, clientStream map[string]any
	networks                   []string
}

func protocolTLS(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "runtime.test"}, DNSNames: []string{"runtime.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))), "\n")
	secret := strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))), "\n")
	return map[string]any{"alpn": []string{"h3"}, "certificates": []map[string]any{{"certificate": certificate, "key": secret}}}, map[string]any{"serverName": "runtime.test", "alpn": []string{"h3"}, "disableSystemRoot": true, "certificates": []map[string]any{{"certificate": certificate, "usage": "verify"}}}
}

func protocolProfiles(t *testing.T) []protocolProfile {
	t.Helper()
	const identity = "55834271-a5db-44c0-8a74-f988601d427b"
	remote := func(port int, fields map[string]any) map[string]any {
		fields["address"], fields["port"] = "127.0.0.1", port
		return map[string]any{"servers": []map[string]any{fields}}
	}
	serverKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	clientKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 16))
	serverTLS, clientTLS := protocolTLS(t)
	return []protocolProfile{
		{name: "vmess", inbound: "vmess", outbound: "vmess", settings: map[string]any{"clients": []map[string]any{{"id": identity, "email": "client-c"}}}, client: func(port int) map[string]any {
			return map[string]any{"vnext": []map[string]any{{"address": "127.0.0.1", "port": port, "users": []map[string]any{{"id": identity, "security": "aes-128-gcm"}}}}}
		}, networks: []string{"tcp", "udp"}},
		{name: "trojan", inbound: "trojan", outbound: "trojan", settings: map[string]any{"clients": []map[string]any{{"password": "test-password", "email": "client-c"}}}, client: func(port int) map[string]any { return remote(port, map[string]any{"password": "test-password"}) }, networks: []string{"tcp", "udp"}},
		{name: "shadowsocks-aead", inbound: "shadowsocks", outbound: "shadowsocks", settings: map[string]any{"network": "tcp,udp", "clients": []map[string]any{{"method": "aes-128-gcm", "password": "test-password", "email": "client-c"}}}, client: func(port int) map[string]any {
			return remote(port, map[string]any{"method": "aes-128-gcm", "password": "test-password"})
		}, networks: []string{"tcp", "udp"}},
		{name: "shadowsocks-2022", inbound: "shadowsocks", outbound: "shadowsocks", settings: map[string]any{"network": "tcp,udp", "method": "2022-blake3-aes-128-gcm", "password": serverKey, "clients": []map[string]any{{"password": clientKey, "email": "client-c"}}}, client: func(port int) map[string]any {
			return remote(port, map[string]any{"method": "2022-blake3-aes-128-gcm", "password": serverKey + ":" + clientKey})
		}, networks: []string{"tcp", "udp"}},
		{name: "http", inbound: "http", outbound: "http", settings: map[string]any{"accounts": []map[string]any{{"user": "client-c", "pass": "test-password"}}}, client: func(port int) map[string]any {
			return remote(port, map[string]any{"users": []map[string]any{{"user": "client-c", "pass": "test-password"}}})
		}, networks: []string{"tcp"}},
		{name: "mixed", inbound: "mixed", outbound: "socks", settings: map[string]any{"auth": "password", "udp": true, "accounts": []map[string]any{{"user": "client-c", "pass": "test-password"}}}, client: func(port int) map[string]any {
			return remote(port, map[string]any{"users": []map[string]any{{"user": "client-c", "pass": "test-password"}}})
		}, networks: []string{"tcp", "udp"}},
		{name: "hysteria2", inbound: "hysteria", outbound: "hysteria", settings: map[string]any{"version": 2, "clients": []map[string]any{{"auth": "test-password", "email": "client-c"}}}, client: func(port int) map[string]any {
			return map[string]any{"version": 2, "address": "127.0.0.1", "port": port}
		}, serverStream: map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": map[string]any{"version": 2}}, clientStream: map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": clientTLS, "hysteriaSettings": map[string]any{"version": 2, "auth": "test-password"}}, networks: []string{"tcp", "udp"}},
	}
}

func outboundCore(t *testing.T, profile protocolProfile, port int) *core.Instance {
	t.Helper()
	outbound := map[string]any{"protocol": profile.outbound, "settings": profile.client(port)}
	if profile.clientStream != nil {
		outbound["streamSettings"] = profile.clientStream
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "outbounds": []map[string]any{outbound}})
	if err != nil {
		t.Fatal(err)
	}
	var input conf.Config
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	config, err := input.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := core.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func udpEcho(t *testing.T) string {
	t.Helper()
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		data := make([]byte, 65535)
		for {
			n, peer, err := connection.ReadFrom(data)
			if err != nil {
				return
			}
			_, _ = connection.WriteTo(data[:n], peer)
		}
	}()
	t.Cleanup(func() { _ = connection.Close(); <-done })
	return connection.LocalAddr().String()
}

func TestXrayProtocolQuotaAtAuthenticatedDispatch(t *testing.T) {
	for _, profile := range protocolProfiles(t) {
		for _, network := range profile.networks {
			t.Run(profile.name+"/"+network, func(t *testing.T) {
				target := echoServer(t)
				if network == "udp" {
					target = udpEcho(t)
				}
				account, err := limits.New(limits.Policy{Term: "shared", Revision: 1, Enabled: true, CreditCeiling: 4096, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
				if err != nil {
					t.Fatal(err)
				}
				ports := []int{freePort(t), freePort(t)}
				var inbounds []map[string]any
				for i, port := range ports {
					inbound := map[string]any{"tag": "test-" + strconv.Itoa(i), "listen": "127.0.0.1", "port": port, "protocol": profile.inbound, "settings": profile.settings}
					if profile.serverStream != nil {
						inbound["streamSettings"] = profile.serverStream
					}
					inbounds = append(inbounds, inbound)
				}
				startConfiguredCore(t, func(id string) (*limits.Account, bool) { return account, id == "client-c" }, inbounds)
				destination, err := xnet.ParseDestination(network + ":" + target)
				if err != nil {
					t.Fatal(err)
				}
				var last *core.Instance
				payload := bytes.Repeat([]byte("q"), 1024)
				for _, port := range ports {
					last = outboundCore(t, profile, port)
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					connection, err := core.Dial(ctx, last, destination)
					if err != nil {
						t.Fatal(err)
					}
					stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
					defer stop()
					t.Cleanup(func() { _ = connection.Close() })
					if _, err := connection.Write(payload); err != nil {
						t.Fatal(err)
					}
					got := make([]byte, len(payload))
					if _, err := io.ReadFull(connection, got); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						t.Fatal("protocol echo differs")
					}
					_ = connection.Close()
				}
				if actual := account.Snapshot(); actual.AdmittedUpload != 2048 || actual.AdmittedDownload != 2048 {
					t.Fatalf("protocol quota attribution=%+v", actual)
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				connection, err := core.Dial(ctx, last, destination)
				if err != nil {
					t.Fatal(err)
				}
				stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
				defer stop()
				defer connection.Close()
				_, _ = connection.Write([]byte("blocked"))
				var one [1]byte
				if n, err := connection.Read(one[:]); n != 0 || err == nil {
					t.Fatalf("exhausted protocol forwarded %d bytes: %v", n, err)
				}
				if actual := account.Snapshot(); actual.AdmittedUpload != 2048 || actual.AdmittedDownload != 2048 {
					t.Fatalf("exhausted protocol authority grew: %+v", actual)
				}
			})
		}
	}
}
