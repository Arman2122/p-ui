package mtproto

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/gorilla/websocket"

	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type webTestAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pool        *x509.CertPool
}

func newWEBTestAuthority(t *testing.T) webTestAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	model := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "WEB test authority"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, model, model, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return webTestAuthority{certificate: certificate, key: key, pool: pool}
}

func (a webTestAuthority) issue(t *testing.T, host string, serial int64) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	model := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, model, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func directWEBFixture(t *testing.T, ipLimit int) (*Managed, Configuration, mtglib.Secret, *limits.Account, webTestAuthority) {
	t.Helper()
	public := os.Getenv("PENHOON_WEB_HTTPS_PUBLIC") == "1"
	if public {
		for _, kind := range []string{"net", "mnt"} {
			self, err := os.Readlink("/proc/self/ns/" + kind)
			initial, e := os.Readlink("/proc/1/ns/" + kind)
			if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
				t.Fatal("refusing public WEB fixture outside private namespaces")
			}
		}
		if output, err := exec.CommandContext(t.Context(), "ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
			t.Fatalf("private loopback: %v %s", err, output)
		}
	}
	managed, config, secret, account := managedWEBFixture(t, ipLimit)
	authority := newWEBTestAuthority(t)
	web := config.Inbounds[0].WEB
	_, port, err := net.SplitHostPort(web.Bind)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	web.PublicPort = uint16(number)
	web.ServerCert, web.ServerKey = authority.issue(t, web.Host, 2)
	if public {
		web.Bind = net.JoinHostPort("0.0.0.0", port)
	}
	applyManagedWEB(t, managed, config, account)
	return managed, config, secret, account, authority
}

func directWEBAddress(t *testing.T, runtime *webRuntime) string {
	t.Helper()
	_, port, err := net.SplitHostPort(runtime.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func directWEBTransport(t *testing.T, runtime *webRuntime, authority webTestAuthority, source string) (*http.Client, websocket.Dialer) {
	t.Helper()
	address := directWEBAddress(t, runtime)
	if ip := net.ParseIP(source); ip != nil && ip.To4() == nil {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		address = net.JoinHostPort("::1", port)
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: time.Second}
		if source != "" {
			dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
		}
		return dialer.DialContext(ctx, network, address)
	}
	config := &tls.Config{RootCAs: authority.pool, ServerName: "relay.example.com", MinVersion: tls.VersionTLS12}
	transport := &http.Transport{DialContext: dial, TLSClientConfig: config}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}, websocket.Dialer{NetDialContext: dial, TLSClientConfig: config, HandshakeTimeout: 5 * time.Second}
}

func directWEBSession(t *testing.T, runtime *webRuntime, config *WEB, authority webTestAuthority, secret mtglib.Secret, source string, headers http.Header) (essentials.Conn, *websocket.Conn) {
	t.Helper()
	client, dialer := directWEBTransport(t, runtime, authority, source)
	origin := net.JoinHostPort(config.Host, strconv.Itoa(int(config.PublicPort)))
	if config.PublicPort == 443 {
		origin = config.Host
	}
	return webSessionTransport(t, secret, client, dialer, "https://"+origin, "wss://"+origin, origin, headers)
}

func TestDirectWEBTLSRotationPreservesNativeAndCarriers(t *testing.T) {
	managed, config, secret, account, authority := directWEBFixture(t, 0)
	process := managed.instances[config.Inbounds[0].ID]
	native := managedNativeClient(t, process, secret)
	web, carrier := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secret, "", nil)
	roundTripMTProto(t, native)
	roundTripMTProto(t, web)
	config.Inbounds[0].WEB.ServerCert, config.Inbounds[0].WEB.ServerKey = authority.issue(t, "relay.example.com", 3)
	applyManagedWEB(t, managed, config, account)
	if managed.instances[config.Inbounds[0].ID] != process {
		t.Fatal("certificate rotation restarted native or WEB sessions")
	}
	roundTripMTProto(t, native)
	roundTripMTProto(t, web)
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", directWEBAddress(t, process.web), &tls.Config{RootCAs: authority.pool, ServerName: "relay.example.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	serial := connection.ConnectionState().PeerCertificates[0].SerialNumber
	_ = connection.Close()
	if serial.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("new handshake retained old certificate: %s", serial)
	}
	config.Inbounds[0].WEB.ServerCert, config.Inbounds[0].WEB.ServerKey = authority.issue(t, "wrong.example.com", 4)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if certificate, err := config.Inbounds[0].WEB.Certificate(); certificate != nil || !errors.Is(err, configuration.ErrMTProtoWEBTLS) {
		t.Fatalf("mismatched hostname certificate accepted: %v", err)
	}
	if err := managed.Apply(t.Context(), encoded, func(string) (*limits.Account, bool) { return account, true }); !errors.Is(err, configuration.ErrMTProtoWEBTLS) || managed.instances[config.Inbounds[0].ID] != process {
		t.Fatal("invalid TLS update changed the working runtime")
	}
	roundTripMTProto(t, native)
	roundTripMTProto(t, web)
	if err := managed.Close(); err != nil {
		t.Fatal(err)
	}
	_ = carrier.SetReadDeadline(time.Now().Add(time.Second))
	for range 16 {
		_, _, err := carrier.ReadMessage()
		if err == nil {
			continue
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("direct HTTPS shutdown left a carrier alive")
		}
		return
	}
	t.Fatal("direct HTTPS shutdown kept sending carrier frames")
}

func TestDirectWEBTLSRejectsHostname(t *testing.T) {
	managed, config, _, _, authority := directWEBFixture(t, 0)
	process := managed.instances[config.Inbounds[0].ID]
	address := directWEBAddress(t, process.web)
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, &tls.Config{RootCAs: authority.pool, ServerName: "wrong.example.com", MinVersion: tls.VersionTLS12})
	if connection != nil {
		_ = connection.Close()
	}
	var hostname x509.HostnameError
	if !errors.As(err, &hostname) {
		t.Fatalf("TLS did not verify relay hostname: %v", err)
	}
	client, _ := directWEBTransport(t, process.web, authority, "")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://relay.example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "wrong.example.com:" + strconv.Itoa(int(config.Inbounds[0].WEB.PublicPort))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("wrong HTTPS authority served: %d", response.StatusCode)
	}
}

func TestDirectWEBTLSRejectsProxyPrefix(t *testing.T) {
	managed, config, _, _, authority := directWEBFixture(t, 0)
	process := managed.instances[config.Inbounds[0].ID]
	address := directWEBAddress(t, process.web)
	raw, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(raw, "PROXY TCP4 203.0.113.7 127.0.0.1 41234 443\r\n"); err != nil {
		t.Fatal(err)
	}
	spoof := tls.Client(raw, &tls.Config{RootCAs: authority.pool, ServerName: "relay.example.com", MinVersion: tls.VersionTLS12})
	err = spoof.HandshakeContext(t.Context())
	if err == nil || spoof.ConnectionState().HandshakeComplete {
		t.Fatal("direct TLS accepted a PROXY-protocol prefix")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("direct TLS did not reject the proxy prefix promptly")
	}
}

func TestDirectWEBTLSUsesSocketAddressDespiteForwardedHeaders(t *testing.T) {
	managed, config, secret, _, authority := directWEBFixture(t, 1)
	process := managed.instances[config.Inbounds[0].ID]
	native := managedNativeClient(t, process, secret)
	roundTripMTProto(t, native)
	web, _ := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secret, "127.0.0.1", nil)
	roundTripMTProto(t, web)
	headers := http.Header{"X-Forwarded-For": []string{"127.0.0.1"}, "X-Real-Ip": []string{"127.0.0.1"}, "Forwarded": []string{"for=127.0.0.1"}, "X-Forwarded-Host": []string{"relay.example.com"}}
	spoof, _ := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secret, "127.0.0.2", headers)
	_, _ = spoof.Write(bytes.Repeat([]byte{42}, 8192))
	if n, err := spoof.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("direct WEB trusted forwarded IP headers: %d %v", n, err)
	}
	roundTripMTProto(t, native)
	roundTripMTProto(t, web)
}

func TestDirectWEBTLSAndNativeShareQuotaAndRate(t *testing.T) {
	for _, mode := range []string{"quota", "rate"} {
		t.Run(mode, func(t *testing.T) {
			managed, config, secret, account, authority := directWEBFixture(t, 0)
			policy := account.Snapshot().Policy
			policy.Revision++
			if mode == "quota" {
				policy.QuotaUnlimited, policy.CreditCeiling = false, 4096
			} else {
				policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 32768, BurstBytes: 256}
			}
			if err := account.Apply(policy); err != nil {
				t.Fatal(err)
			}
			process := managed.instances[config.Inbounds[0].ID]
			native := managedNativeClient(t, process, secret)
			web, _ := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secret, "", nil)
			if mode == "quota" {
				roundTripMTProto(t, native)
				roundTripMTProto(t, web)
				if _, err := web.Write(bytes.Repeat([]byte{18}, 4096)); err != nil {
					t.Fatal(err)
				}
				if n, err := web.Read(make([]byte, 1)); n != 0 || err == nil {
					t.Fatalf("HTTPS bypassed native quota: %d %v", n, err)
				}
				if snapshot := account.Snapshot(); snapshot.AdmittedUpload+snapshot.AdmittedDownload != 4096 || snapshot.MeasuredUpload+snapshot.MeasuredDownload > 4096 {
					t.Fatalf("HTTPS/native accounting: %+v", snapshot)
				}
				return
			}
			started := time.Now()
			var group sync.WaitGroup
			for _, client := range []essentials.Conn{native, web} {
				group.Go(func() {
					payload := bytes.Repeat([]byte{19}, 4096)
					if _, err := client.Write(bytes.Clone(payload)); err != nil {
						t.Error(err)
						return
					}
					got := make([]byte, len(payload))
					if _, err := io.ReadFull(client, got); err != nil || !bytes.Equal(got, payload) {
						t.Errorf("HTTPS rate echo: %v", err)
					}
				})
			}
			group.Wait()
			if elapsed := time.Since(started); elapsed < 1750*time.Millisecond {
				t.Fatalf("HTTPS/native aggregate rate exceeded: %s", elapsed)
			}
			if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 8192 {
				t.Fatalf("HTTPS/native admitted upload=%d", snapshot.AdmittedUpload)
			}
		})
	}
}

func TestDirectWEBTLSConfigurationRequiresTLSForPublicBind(t *testing.T) {
	managed, config, _, _, _ := directWEBFixture(t, 0)
	original := *config.Inbounds[0].WEB
	for _, test := range []struct {
		name   string
		change func(*WEB)
		want   error
	}{
		{"public plaintext", func(web *WEB) { web.Bind = "0.0.0.0:34443"; web.ServerCert, web.ServerKey = "", "" }, nil},
		{"partial identity", func(web *WEB) { web.ServerKey = "" }, configuration.ErrMTProtoWEBTLS},
		{"unadvertised direct port", func(web *WEB) { web.PublicPort = 0 }, nil},
		{"ambiguous bind", func(web *WEB) { web.Bind = "127.0.0.1:034443" }, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			web := original
			test.change(&web)
			config.Inbounds[0].WEB = &web
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			err = managed.Validate(encoded)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("TLS validation=%v want %v", err, test.want)
				}
			} else if err == nil || err.Error() != "invalid WEB relay origin or bounds" {
				t.Fatalf("unsafe WEB mode validation=%v", err)
			}
		})
	}
}

func TestDirectWEBTLSIPv6SocketAttribution(t *testing.T) {
	managed, config, secret, account, authority := directWEBFixture(t, 0)
	_, port, err := net.SplitHostPort(config.Inbounds[0].WEB.Bind)
	if err != nil {
		t.Fatal(err)
	}
	config.Inbounds[0].WEB.Bind = net.JoinHostPort("::1", port)
	applyManagedWEB(t, managed, config, account)
	process := managed.instances[config.Inbounds[0].ID]
	web, _ := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secret, "::1", nil)
	roundTripMTProto(t, web)
	if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 512 || snapshot.AdmittedDownload != 512 {
		t.Fatalf("IPv6 HTTPS accounting=%+v", snapshot)
	}
}
