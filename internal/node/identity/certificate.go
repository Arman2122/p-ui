package identity

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIdentity = errors.New("invalid node certificate identity")
	ErrCSR      = errors.New("invalid node certificate request")
)

const Lifetime = 12 * time.Hour

type Principal struct {
	ControlPlaneID string
	NodeID         string
	InstallationID string
	Serial         string
}

func validID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

func (p Principal) URI() (*url.URL, error) {
	if !validID(p.ControlPlaneID) || !validID(p.NodeID) || !validID(p.InstallationID) {
		return nil, ErrIdentity
	}
	return &url.URL{Scheme: "spiffe", Host: p.ControlPlaneID, Path: "/node/" + p.NodeID + "/installation/" + p.InstallationID}, nil
}

func principalFromCertificate(cert *x509.Certificate, plane string, now time.Time) (Principal, error) {
	if cert == nil || cert.IsCA || len(cert.URIs) != 1 || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return Principal{}, ErrIdentity
	}
	u := cert.URIs[0]
	parts := strings.Split(u.Path, "/")
	if u.Scheme != "spiffe" || u.Host != plane || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		len(parts) != 5 || parts[1] != "node" || parts[3] != "installation" {
		return Principal{}, ErrIdentity
	}
	p := Principal{ControlPlaneID: plane, NodeID: parts[2], InstallationID: parts[4], Serial: cert.SerialNumber.Text(16)}
	if _, err := p.URI(); err != nil {
		return Principal{}, err
	}
	return p, nil
}

func Issue(ca *x509.Certificate, key crypto.Signer, requestPEM []byte, p Principal, now time.Time) ([]byte, error) {
	u, err := p.URI()
	if err != nil {
		return nil, err
	}
	if ca == nil || key == nil || !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 || now.Before(ca.NotBefore) || !now.Before(ca.NotAfter) {
		return nil, ErrIdentity
	}
	want, err := x509.MarshalPKIXPublicKey(ca.PublicKey)
	if err != nil {
		return nil, err
	}
	got, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil || !bytes.Equal(want, got) {
		return nil, ErrIdentity
	}
	request, err := parseRequest(requestPEM)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	serial.SetBit(serial, 127, 1)
	end := now.Add(Lifetime)
	if ca.NotAfter.Before(end) {
		end = ca.NotAfter
	}
	start := now.Add(-5 * time.Minute)
	if ca.NotBefore.After(start) {
		start = ca.NotBefore
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: p.NodeID},
		URIs: []*url.URL{u}, NotBefore: start, NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, request.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func parseRequest(requestPEM []byte) (*x509.CertificateRequest, error) {
	if len(requestPEM) > 8192 {
		return nil, ErrCSR
	}
	block, rest := pem.Decode(requestPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrCSR
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, ErrCSR
	}
	switch public := request.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if public.Curve != elliptic.P256() && public.Curve != elliptic.P384() {
			return nil, ErrCSR
		}
	case ed25519.PublicKey:
	case *rsa.PublicKey:
		if public.N.BitLen() < 3072 || public.N.BitLen() > 4096 {
			return nil, ErrCSR
		}
	default:
		return nil, ErrCSR
	}
	if request.CheckSignature() != nil {
		return nil, ErrCSR
	}
	return request, nil
}

func RequestKeyDigest(requestPEM []byte) ([]byte, error) {
	request, err := parseRequest(requestPEM)
	if err != nil {
		return nil, err
	}
	encoded, err := x509.MarshalPKIXPublicKey(request.PublicKey)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}
