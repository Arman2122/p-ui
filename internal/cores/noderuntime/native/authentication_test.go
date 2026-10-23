package nativeruntime

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"maps"
	"math/big"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func nativeAuthenticationConfig(t *testing.T) (Configuration, []admission.Descriptor) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}))
	descriptors := []admission.Descriptor{}
	for _, tag := range []string{"vpn-tunnel", "ocserv-tunnel"} {
		descriptors = append(descriptors, admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: tag})
	}
	config := Configuration{
		OpenVPN: []OpenVPN{{
			ID: 1, InboundTag: descriptors[0].InboundTag, Port: 31194, Listen: netip.IPv4Unspecified(), Proto: "udp", Subnet: netip.MustParsePrefix("10.240.1.0/24"), MTU: 1400,
			ServerCert: certificate, ServerKey: privateKey, TLSCryptV2ServerKey: string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 server key", Bytes: bytes.Repeat([]byte{1}, 128)})),
			Clients: []OpenVPNClient{{ClientID: descriptors[0].ClientID, Username: "alice", Password: "valid-password", Addresses: []netip.Addr{netip.MustParseAddr("10.240.1.2")}}},
		}},
		OpenConnect: []OpenConnect{{
			ID: 2, InboundTag: descriptors[1].InboundTag, Port: 31443, Listen: netip.IPv4Unspecified(), MTU: 1400, ServerCert: certificate, ServerKey: privateKey,
			Clients: []OpenConnectClient{{ClientID: descriptors[1].ClientID, Username: "bob", Password: "valid-password", Subnet: netip.MustParsePrefix("10.240.2.0/29"), MaxSessions: 2}},
		}},
	}
	return config, descriptors
}

func TestNativeAuthenticationOnlyConfigurationCreatesNoForwardingAssignment(t *testing.T) {
	config, descriptors := nativeAuthenticationConfig(t)
	runtime := asRuntime(t, config)
	runtime.Admissions = descriptors
	compiled, err := compile(runtime)
	if err != nil || len(compiled.assignments) != 0 || len(compiled.selectors) != 2 {
		t.Fatalf("authentication publication created forwarding authority=%+v err=%v", compiled.assignments, err)
	}
	grant := governor.Grant{ID: uuid.NewString(), ClientID: descriptors[0].ClientID, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 1024, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
	runtime.Grants = []governor.Grant{grant}
	compiled, err = compile(runtime)
	if err != nil || len(compiled.assignments) != 1 || compiled.assignments[0].Grant.ID != grant.ID || len(compiled.assignments[0].Selectors) != 1 {
		t.Fatalf("partial installed authority funded another identity=%+v err=%v", compiled.assignments, err)
	}
}

func TestNativeAuthenticationScopeRejectsMissingOrForeignSubjects(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Configuration, *[]admission.Descriptor)
	}{
		{"missing client proof", func(_ *Configuration, d *[]admission.Descriptor) { *d = (*d)[:1] }},
		{"wrong inbound", func(_ *Configuration, d *[]admission.Descriptor) { (*d)[0].InboundTag = "other" }},
		{"missing inbound identity", func(c *Configuration, _ *[]admission.Descriptor) { c.OpenVPN[0].InboundTag = "" }},
		{"duplicate inbound identity", func(c *Configuration, _ *[]admission.Descriptor) {
			c.OpenConnect[0].InboundTag = c.OpenVPN[0].InboundTag
		}},
		{"foreign client", func(_ *Configuration, d *[]admission.Descriptor) { (*d)[0].ClientID = uuid.NewString() }},
		{"unfunded WireGuard", func(c *Configuration, d *[]admission.Descriptor) {
			wireguard, _, _ := testConfig(t)
			wireguard.WireGuard[0].Clients[0].ClientID = (*d)[0].ClientID
			c.WireGuard = wireguard.WireGuard
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, descriptors := nativeAuthenticationConfig(t)
			test.change(&config, &descriptors)
			runtime := asRuntime(t, config)
			runtime.Admissions = descriptors
			if _, err := compile(runtime); !errors.Is(err, configuration.ErrConfiguration) {
				t.Fatalf("unproven native authentication scope accepted=%v", err)
			}
		})
	}
}

func TestNativeAuthenticationFailedRetryReplacesProofsAndRetainsUnchangedClient(t *testing.T) {
	_, descriptors := nativeAuthenticationConfig(t)
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	runtime := &configuration.Runtime{Admissions: descriptors}
	binding, _ := broker.Prepare(descriptors)
	_, original, err := captureNativeAuthentication(admission.WithBinding(t.Context(), binding), runtime)
	if err != nil || broker.Commit(1, binding) != nil {
		t.Fatalf("initial proof capture=%v", err)
	}
	descriptors[0].CredentialGeneration++
	failed, _ := broker.Prepare(descriptors)
	_, failedProofs, err := captureNativeAuthentication(admission.WithBinding(t.Context(), failed), runtime)
	if err != nil {
		t.Fatal(err)
	}
	retry, _ := broker.Prepare(descriptors)
	_, retried, err := captureNativeAuthentication(admission.WithBinding(t.Context(), retry), runtime)
	if err != nil || maps.Equal(failedProofs, retried) {
		t.Fatalf("failed same-descriptor retry reused retired candidate=%v", err)
	}
	a := admission.Subject{ClientID: descriptors[0].ClientID, InboundTag: descriptors[0].InboundTag}
	b := admission.Subject{ClientID: descriptors[1].ClientID, InboundTag: descriptors[1].InboundTag}
	if original[a].Current() || failedProofs[a].Current() || original[b] != retried[b] || !original[b].Current() {
		t.Fatal("proof preparation resurrected changed identity or retired unchanged client")
	}
	if err := broker.Commit(3, retry); err != nil || !retried[a].Current() || failedProofs[a].Current() {
		t.Fatalf("retry proof commit=%v", err)
	}
	if _, _, err := captureNativeAuthentication(context.Background(), runtime); !errors.Is(err, admission.ErrUnavailable) {
		t.Fatalf("native candidate silently lost its proof binding=%v", err)
	}
}
