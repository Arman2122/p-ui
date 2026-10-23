package nodeconfig

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
)

const (
	nativeCompilerNodeID   = "11111111-1111-4111-8111-111111111111"
	nativeCompilerClientID = "22222222-2222-4222-8222-222222222222"
)

func compileFixtureJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func compilerTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "compiler.test"}, DNSNames: []string{"compiler.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func nativeCompilerFixture(t *testing.T, kind core.Kind) Input {
	t.Helper()
	user := core.User{Email: "mutable-label@example.test", Enable: true, Credentials: map[string]any{core.CredUsername: "alice", core.CredPassword: "stable-client-password"}}
	inbound := Inbound{Instance: core.Instance{ID: 41, Kind: kind, Tag: "source-41", Listen: "0.0.0.0", Port: 31194, Enable: true}, TargetID: 7, Clients: []Client{{ClientID: nativeCompilerClientID, User: user}}, Assets: map[string]string{}, Egress: EgressBinding{Mode: EgressDirect}}
	input := Input{NodeID: nativeCompilerNodeID, NativeNetwork: &nativeruntime.NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.240.2"), GatewayIPv4: netip.MustParseAddr("172.31.240.3")}}
	asset := func(field, kind, value string) {
		inbound.Assets[field] = field
		input.Assets = append(input.Assets, Asset{ID: field, NodeID: input.NodeID, Kind: kind, Data: []byte(value)})
	}
	settings := map[string]any{"clients": []map[string]any{{"email": "unassigned-ghost", "username": "ghost", "password": "never-publish-ghost"}}}
	switch kind {
	case "wgkernel", "awgkernel":
		server, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		client, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		shared, err := wgtypes.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		settings["secretKey"], settings["address"], settings["dns"] = server.String(), []string{"10.0.0.1/24", "fd44::1/64"}, "1.1.1.1"
		user.Credentials[core.CredPublicKey], user.Credentials[core.CredPrivateKey] = client.PublicKey().String(), client.String()
		user.Credentials[core.CredPreSharedKey], user.Credentials[core.CredAllowedIPs], user.Credentials["keepAlive"] = shared.String(), []any{"10.0.0.2/32", "fd44::2/128"}, float64(25)
		if kind == "awgkernel" {
			settings["awg"] = map[string]any{"jc": 3, "jmin": 32, "jmax": 64, "s1": 12, "s2": 12, "s3": 12, "s4": 12, "h1": "101-103", "h2": 105, "h3": 107, "h4": 109, "headerProtectionKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), "rekeyTimeout": "5-7", "randomTrailers": true}
		}
		asset("secretKey", AssetWireGuardKey, server.String())
	case "openvpn", "ocserv":
		certificate, key := compilerTestCertificate(t)
		settings["subnet"], settings["dns"], settings["redirectGateway"] = "10.9.0.0/24", []string{"1.1.1.1"}, true
		settings["serverCert"], settings["serverKey"] = certificate, key
		asset("serverCert", AssetCertificate, certificate)
		asset("serverKey", AssetPrivateKey, key)
		if kind == "openvpn" {
			server := string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 server key", Bytes: bytes.Repeat([]byte{7}, 128)}))
			settings["proto"], settings["tlsCryptV2ServerKey"] = "udp", server
			asset("tlsCryptV2ServerKey", AssetTLSCryptV2, server)
			user.Credentials[core.CredTLSCryptV2] = string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 client key", Bytes: bytes.Repeat([]byte{9}, 384)}))
			inbound.Clients[0].Native.Addresses = []netip.Addr{netip.MustParseAddr("10.9.0.2"), netip.MustParseAddr("10.9.0.3")}
		} else {
			settings["camouflage"], settings["camouflageSecret"] = true, "keep-client-url-secret"
			inbound.Clients[0].Native.Subnet, inbound.Clients[0].Native.MaxSessions = netip.MustParsePrefix("10.9.0.8/29"), 3
		}
	}
	inbound.Instance.Settings = compileFixtureJSON(t, settings)
	input.Inbounds = []Inbound{inbound}
	return input
}

func TestNativeCompilerPreservesCredentialsAndAccountingIdentity(t *testing.T) {
	for _, kind := range []core.Kind{"wgkernel", "awgkernel", "openvpn", "ocserv"} {
		t.Run(string(kind), func(t *testing.T) {
			input := nativeCompilerFixture(t, kind)
			before := compileFixtureJSON(t, input)
			result, err := CompileNative(input)
			if err != nil || len(result.Runtimes) != 1 {
				t.Fatalf("compile %s: %+v %v", kind, result, err)
			}
			definition := result.Runtimes[0]
			if definition.ID != "native" || definition.Core != "native" || !reflect.DeepEqual(definition.ClientIDs, []string{nativeCompilerClientID}) || nativeruntime.ValidateConfiguration(definition.Configuration, definition.ClientIDs) != nil {
				t.Fatal("compiled definition lost canonical identity or runtime validity")
			}
			if strings.Contains(string(definition.Configuration), "unassigned-ghost") || strings.Contains(string(definition.Configuration), "mutable-label") {
				t.Fatal("unassigned or mutable accounting identity leaked into native config")
			}
			var config nativeruntime.Configuration
			if err := json.Unmarshal(definition.Configuration, &config); err != nil {
				t.Fatal(err)
			}
			if config.Network == nil || *config.Network != *input.NativeNetwork {
				t.Fatal("node-local native network binding was lost")
			}
			source := input.Inbounds[0]
			switch kind {
			case "wgkernel", "awgkernel":
				var wg nativeruntime.WireGuard
				if kind == "wgkernel" {
					wg = config.WireGuard[0]
				} else {
					wg = config.AmneziaWG[0].WireGuard
					if config.AmneziaWG[0].Parameters.H1 != uint64(103)<<32|101 || config.AmneziaWG[0].Parameters.RekeyTimeout != uint32(7)<<16|5 {
						t.Fatal("AWG human ranges changed meaning")
					}
				}
				peer := wg.Clients[0]
				if wg.ID != 7 || wg.Addresses[0] != netip.MustParsePrefix("10.0.0.1/32") || peer.ClientID != nativeCompilerClientID || peer.PublicKey != source.Clients[0].User.Credentials[core.CredPublicKey] || peer.PreSharedKey != source.Clients[0].User.Credentials[core.CredPreSharedKey] || peer.KeepAlive != 25 {
					t.Fatal("WireGuard credentials, node ID, address or keepalive changed")
				}
				if strings.Contains(string(definition.Configuration), source.Clients[0].User.Credentials[core.CredPrivateKey].(string)) {
					t.Fatal("client-only private key was delivered to the node")
				}
			case "openvpn":
				vpn := config.OpenVPN[0]
				if vpn.ID != 7 || vpn.Clients[0].ClientID != nativeCompilerClientID || vpn.Clients[0].Username != "alice" || vpn.Clients[0].Password != "stable-client-password" || len(vpn.Clients[0].Addresses) != 2 || vpn.TLSCryptV2ServerKey != string(input.Assets[2].Data) {
					t.Fatal("OpenVPN stable identity or multi-session binding changed")
				}
			case "ocserv":
				vpn := config.OpenConnect[0]
				if vpn.ID != 7 || vpn.CamouflageSecret != "keep-client-url-secret" || vpn.Clients[0].ClientID != nativeCompilerClientID || vpn.Clients[0].Username != "alice" || vpn.Clients[0].Password != "stable-client-password" || vpn.Clients[0].Subnet != netip.MustParsePrefix("10.9.0.8/29") || vpn.Clients[0].MaxSessions != 3 || len(result.Listeners) != 2 {
					t.Fatal("OpenConnect credentials, dormant pool or listener claims changed")
				}
			}
			if after := compileFixtureJSON(t, input); after != before {
				t.Fatal("compiler mutated caller-owned configuration or credentials")
			}
		})
	}
}

func TestNativeCompilerRejectsUnresolvedAndUnsupportedState(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Input)
		want   error
	}{
		{"missing key asset", func(in *Input) { in.Assets = nil }, ErrDependency},
		{"cross-node key", func(in *Input) { in.Assets[0].NodeID = nativeCompilerClientID }, ErrDependency},
		{"unknown settings", func(in *Input) {
			in.Inbounds[0].Instance.Settings = strings.TrimSuffix(in.Inbounds[0].Instance.Settings, "}") + `,"unknownRoutingSwitch":true}`
		}, ErrInvalid},
		{"unresolved egress", func(in *Input) { in.Inbounds[0].Egress = EgressBinding{Mode: EgressXray, BridgeID: "unavailable"} }, ErrUnsupported},
		{"implicit assignment source", func(in *Input) { in.Inbounds[0].Instance.Users = []core.User{in.Inbounds[0].Clients[0].User} }, ErrInvalid},
		{"global IP limit", func(in *Input) { in.Inbounds[0].Clients[0].User.LimitIP = 1 }, ErrUnsupported},
		{"routed subnet", func(in *Input) {
			in.Inbounds[0].Clients[0].User.Credentials[core.CredAllowedIPs] = []string{"10.0.0.0/24"}
		}, ErrUnsupported},
		{"fractional keepalive", func(in *Input) { in.Inbounds[0].Clients[0].User.Credentials["keepAlive"] = 0.5 }, ErrInvalid},
		{"specific WG listener", func(in *Input) { in.Inbounds[0].Instance.Listen = "192.0.2.10" }, ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := nativeCompilerFixture(t, "wgkernel")
			test.change(&input)
			result, err := CompileNative(input)
			if !errors.Is(err, test.want) || len(result.Runtimes)+len(result.Listeners)+len(result.Assets) != 0 {
				t.Fatalf("unsafe compile: %+v %v want %v", result, err, test.want)
			}
		})
	}
}

func TestNativeCompilerDoesNotTurnMissingAssetsIntoEmptyPlacement(t *testing.T) {
	input := nativeCompilerFixture(t, "wgkernel")
	input.Inbounds[0].Clients = nil
	result, err := CompileNative(input)
	if err != nil || len(result.Runtimes)+len(result.Listeners)+len(result.Assets) != 0 || len(result.Omissions) != 1 || result.Omissions[0].Reason != "no-enabled-clients" {
		t.Fatalf("explicit empty placement: %+v %v", result, err)
	}
	input.Assets = nil
	if _, err := CompileNative(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("missing source dependency was destructively omitted: %v", err)
	}
}

func TestNativeCompilerDiagnosesMissingClientBindings(t *testing.T) {
	for _, kind := range []core.Kind{"openvpn", "ocserv"} {
		t.Run(string(kind), func(t *testing.T) {
			input := nativeCompilerFixture(t, kind)
			input.Inbounds[0].Egress.Mode = ""
			if _, err := CompileNative(input); err != nil {
				t.Fatalf("qualified native default failed inheritance: %v", err)
			}
			input.Inbounds[0].Clients[0].Native = NativeClientBinding{}
			if _, err := CompileNative(input); !errors.Is(err, ErrClientBinding) || !errors.Is(err, ErrDependency) {
				t.Fatalf("missing client allocation lacks typed diagnosis: %v", err)
			}
			if kind == "openvpn" {
				input.Inbounds[0].Clients[0].Native.Addresses = []netip.Addr{netip.MustParseAddr("10.20.0.2")}
			} else {
				input.Inbounds[0].Clients[0].Native.Subnet = netip.MustParsePrefix("10.20.0.0/29")
			}
			if _, err := CompileNative(input); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrClientBinding) {
				t.Fatalf("invalid supplied binding was diagnosed as missing: %v", err)
			}
		})
	}
}
