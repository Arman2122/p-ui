package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func TestCSRIdentityCannotChooseAnotherNode(t *testing.T) {
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "administrator"},
		URIs:    []*url.URL{{Scheme: "spiffe", Host: "attacker", Path: "/node/administrator"}},
	}, nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{ControlPlaneID: uuid.NewString(), NodeID: uuid.NewString(), InstallationID: uuid.NewString()}
	now := time.Now()
	certPEM, err := Issue(ca, signer, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}), p, now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := principalFromCertificate(cert, p.ControlPlaneID, now)
	if err != nil || actual.NodeID != p.NodeID || actual.InstallationID != p.InstallationID {
		t.Fatalf("principal=%+v err=%v", actual, err)
	}
	if cert.Subject.CommonName != p.NodeID || cert.IsCA || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("untrusted CSR attributes leaked: %+v", cert.Subject)
	}
	if cert.NotAfter.After(now.Add(Lifetime)) || cert.NotAfter.Before(now.Add(Lifetime-time.Second)) {
		t.Fatalf("expiry=%v", cert.NotAfter)
	}
	if _, err := principalFromCertificate(cert, uuid.NewString(), now); !errors.Is(err, ErrIdentity) {
		t.Fatalf("wrong plane=%v", err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"control.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, &serverKey.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	trust.AddCert(ca)
	a, b := net.Pipe()
	server := tls.Server(a, &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{serverDER}, PrivateKey: serverKey}},
		ClientCAs: trust, ClientAuth: tls.RequireAndVerifyClientCert,
	})
	client := tls.Client(b, &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: trust, ServerName: "control.test",
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: nodeKey}},
	})
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.HandshakeContext(ctx) }()
	if err := client.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state := server.ConnectionState()
	peerContext := peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{State: state}})
	principal, err := FromContext(peerContext, p.ControlPlaneID)
	if err != nil || principal.NodeID != p.NodeID || principal.InstallationID != p.InstallationID {
		t.Fatalf("TLS principal=%+v err=%v", principal, err)
	}
	if err := principal.Require(uuid.NewString(), p.InstallationID); !errors.Is(err, ErrIdentity) {
		t.Fatalf("foreign node claim=%v", err)
	}
	if err := principal.Require(p.NodeID, uuid.NewString()); !errors.Is(err, ErrIdentity) {
		t.Fatalf("foreign installation claim=%v", err)
	}
	state.VerifiedChains = nil
	forged := peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{State: state}})
	if _, err := FromContext(forged, p.ControlPlaneID); !errors.Is(err, ErrIdentity) {
		t.Fatalf("unverified peer=%v", err)
	}
}
