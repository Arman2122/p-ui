package xrayruntime

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
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func hotTLS(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hot.test"}, DNSNames: []string{"hot.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	return map[string]any{"alpn": []string{"h3"}, "certificates": []map[string]any{{"certificate": certificate, "key": secret}}}, map[string]any{"serverName": "hot.test", "alpn": []string{"h3"}, "disableSystemRoot": true, "certificates": []map[string]any{{"certificate": certificate, "usage": "verify"}}}
}

func hotOutbound(t *testing.T, kind, secret string, port int, tls map[string]any) *core.Instance {
	t.Helper()
	server := map[string]any{"address": "127.0.0.1", "port": port}
	settings := map[string]any{"servers": []map[string]any{server}}
	switch kind {
	case "vless", "vmess":
		server["users"] = []map[string]any{{"id": secret, "encryption": "none", "security": "aes-128-gcm"}}
		settings = map[string]any{"vnext": []map[string]any{server}}
	case "trojan":
		server["password"] = secret
	case "shadowsocks":
		server["password"], server["method"] = secret, "aes-128-gcm"
	case "shadowsocks2022":
		server["password"], server["method"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))+":"+secret, "2022-blake3-aes-128-gcm"
	case "hysteria":
		settings = map[string]any{"version": 2, "address": "127.0.0.1", "port": port}
	}
	outbound := map[string]any{"protocol": kind, "settings": settings}
	if kind == "shadowsocks2022" {
		outbound["protocol"] = "shadowsocks"
	}
	if kind == "hysteria" {
		outbound["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": tls, "hysteriaSettings": map[string]any{"version": 2, "auth": secret}}
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

func hotCoreDial(t *testing.T, instance *core.Instance, network, target string) net.Conn {
	t.Helper()
	destination, err := xnet.ParseDestination(network + ":" + target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	connection, err := core.Dial(ctx, instance, destination)
	if err != nil {
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	t.Cleanup(func() { stop(); _ = connection.Close() })
	return connection
}

func TestXrayHotProtocolsPreserveUnchangedTCPAndUDP(t *testing.T) {
	for _, kind := range []string{"vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022", "hysteria"} {
		t.Run(kind, func(t *testing.T) {
			port := availablePort(t)
			tcpTarget, udpTarget := hotEcho(t, "tcp"), hotEcho(t, "udp")
			serverTLS, clientTLS := hotTLS(t)
			a, b := hotDescriptor(uuid.NewString()), hotDescriptor(uuid.NewString())
			aSecret, bSecret := uuid.NewString(), uuid.NewString()
			if kind == "shadowsocks2022" {
				first, second := uuid.MustParse(aSecret), uuid.MustParse(bSecret)
				aSecret, bSecret = base64.StdEncoding.EncodeToString(first[:]), base64.StdEncoding.EncodeToString(second[:])
			}
			oldSecret := aSecret
			accounts := map[string]*limits.Account{a.ClientID: hotAccount(t), b.ClientID: hotAccount(t)}
			lookup := func(id string) (*limits.Account, bool) { account, ok := accounts[id]; return account, ok }
			broker := admission.New("xray", func() (time.Time, error) { return time.Now(), nil })
			var runtime Runtime
			t.Cleanup(func() { _ = runtime.Close() })
			revision := uint64(0)
			apply := func(includeA bool) {
				t.Helper()
				descriptors := []admission.Descriptor{b}
				if includeA {
					descriptors = append(descriptors, a)
				}
				users := make([]map[string]any, 0, len(descriptors))
				identities := make([]ClientIdentity, 0, len(descriptors))
				for _, descriptor := range descriptors {
					secret := bSecret
					if descriptor.ClientID == a.ClientID {
						secret = aSecret
					}
					user := map[string]any{"email": descriptor.ClientID}
					switch kind {
					case "vless", "vmess":
						user["id"] = secret
					case "trojan":
						user["password"] = secret
					case "shadowsocks":
						user["password"], user["method"] = secret, "aes-128-gcm"
					case "shadowsocks2022":
						user["password"] = secret
					case "hysteria":
						user["auth"] = secret
					}
					users = append(users, user)
					identities = append(identities, ClientIdentity{InboundTag: "hot", Username: descriptor.ClientID, ClientID: descriptor.ClientID})
				}
				settings := map[string]any{"clients": users}
				if kind == "vless" {
					settings["decryption"] = "none"
				}
				if kind == "shadowsocks" {
					settings["network"] = "tcp,udp"
				}
				if kind == "shadowsocks2022" {
					settings["network"], settings["method"], settings["password"] = "tcp,udp", "2022-blake3-aes-128-gcm", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
				}
				if kind == "hysteria" {
					settings["version"] = 2
				}
				inbound := map[string]any{"tag": "hot", "listen": "127.0.0.1", "port": port, "protocol": kind, "settings": settings}
				if kind == "shadowsocks2022" {
					inbound["protocol"] = "shadowsocks"
				}
				if kind == "hysteria" {
					inbound["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": serverTLS, "hysteriaSettings": map[string]any{"version": 2}}
				}
				data, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "penhoon": Metadata{Version: 1, ClientIdentities: identities}, "inbounds": []map[string]any{inbound}, "outbounds": []map[string]any{{"protocol": "freedom", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}})
				if err != nil {
					t.Fatal(err)
				}
				binding, err := broker.Prepare(descriptors)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.InvalidateAuthentication(t.Context(), []admission.Subject{{ClientID: a.ClientID, InboundTag: "hot"}}); err != nil {
					t.Fatal(err)
				}
				if err := runtime.Apply(admission.WithBinding(t.Context(), binding), data, lookup); err != nil {
					t.Fatal(err)
				}
				revision++
				if err := broker.Commit(revision, binding); err != nil {
					t.Fatal(err)
				}
			}
			apply(false)
			instance := runtime.instance
			wireBSecret := bSecret
			if kind == "hysteria" {
				routed := uuid.MustParse(bSecret)
				routed[6] ^= 1
				wireBSecret = routed.String()
			}
			bClient := hotOutbound(t, kind, wireBSecret, port, clientTLS)
			bTCP, bUDP := hotCoreDial(t, bClient, "tcp", tcpTarget), hotCoreDial(t, bClient, "udp", udpTarget)
			expected := uint64(0)
			checkB := func() {
				t.Helper()
				if runtime.instance != instance {
					t.Fatal("hot protocol authentication restarted unchanged runtime")
				}
				for _, conn := range []net.Conn{bTCP, bUDP} {
					if err := hotExchange(conn, []byte("stable-b")); err != nil {
						t.Fatalf("unchanged protocol connection interrupted: %v", err)
					}
				}
				expected += 16
				hotMeasured(t, accounts[b.ClientID], expected)
			}
			checkB()
			apply(true)
			checkB()
			aClient := hotOutbound(t, kind, aSecret, port, clientTLS)
			aTCP := hotCoreDial(t, aClient, "tcp", tcpTarget)
			if err := hotExchange(aTCP, []byte("old-a")); err != nil {
				t.Fatal(err)
			}
			a.CredentialGeneration++
			aSecret = uuid.NewString()
			if kind == "shadowsocks2022" {
				secret := uuid.MustParse(aSecret)
				aSecret = base64.StdEncoding.EncodeToString(secret[:])
			}
			apply(true)
			checkB()
			if err := hotExchange(aTCP, []byte("retired-a")); err == nil {
				t.Fatal("retired protocol authentication retained funded stream")
			}
			freshClient := hotOutbound(t, kind, aSecret, port, clientTLS)
			freshA := hotCoreDial(t, freshClient, "tcp", tcpTarget)
			if err := hotExchange(freshA, []byte("new-a")); err != nil {
				t.Fatal(err)
			}
			a.PlacementRevision++
			apply(true)
			checkB()
			if err := hotExchange(freshA, []byte("retired-placement")); err == nil {
				t.Fatal("placement-only change retained authenticated stream")
			}
			apply(false)
			checkB()
			a.CredentialGeneration++
			aSecret = oldSecret
			apply(true)
			checkB()
			if err := hotExchange(aTCP, []byte("must-not-revive")); err == nil {
				t.Fatal("reused credential revived old protocol stream")
			}
			concurrentHotAuthentication(t, bClient, tcpTarget, udpTarget, func() {
				a.CredentialGeneration++
				apply(a.CredentialGeneration%2 == 0)
			})
			expected += 64 * uint64(len("concurrent-b"))
			hotMeasured(t, accounts[b.ClientID], expected)
		})
	}
}

func concurrentHotAuthentication(t *testing.T, client *core.Instance, tcpTarget, udpTarget string, publish func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	start, ready, results := make(chan struct{}), make(chan struct{}, 2), make(chan error, 2)
	for _, target := range []string{"tcp:" + tcpTarget, "udp:" + udpTarget} {
		destination, err := xnet.ParseDestination(target)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			ready <- struct{}{}
			<-start
			for range 32 {
				connection, err := core.Dial(ctx, client, destination)
				if err != nil {
					results <- err
					return
				}
				err = hotExchange(connection, []byte("concurrent-b"))
				_ = connection.Close()
				if err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	<-ready
	<-ready
	close(start)
	for range 32 {
		publish()
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent authentication publication interrupted unchanged client: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent authentication publication did not complete")
		}
	}
}
