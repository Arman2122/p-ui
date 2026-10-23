package controlplane

import (
	"bytes"
	"crypto"
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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

type AuthorityMaterial struct {
	Version           uint32 `json:"version"`
	ControlPlaneID    string `json:"controlPlaneId"`
	CA                []byte `json:"ca"`
	CAKey             []byte `json:"caKey"`
	ServerCertificate []byte `json:"serverCertificate"`
	ServerKey         []byte `json:"serverKey"`
}

var dnsName = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

func certificateSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err == nil {
		serial.SetBit(serial, 127, 1)
	}
	return serial, err
}

func makeServerCertificate(ca *x509.Certificate, key crypto.Signer, names []string, now time.Time) ([]byte, []byte, error) {
	if len(names) == 0 || len(names) > 32 {
		return nil, nil, ErrInvalid
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	end := now.Add(90 * 24 * time.Hour)
	if ca.NotAfter.Before(end) {
		end = ca.NotAfter
	}
	leaf := &x509.Certificate{SerialNumber: serial, NotBefore: now.Add(-5 * time.Minute), NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else if len(name) <= 253 && dnsName.MatchString(name) {
			leaf.DNSNames = append(leaf.DNSNames, name)
		} else {
			return nil, nil, ErrInvalid
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, serverKey.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), nil
}

func NewAuthority(names []string, now time.Time) (AuthorityMaterial, error) {
	var out AuthorityMaterial
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return out, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return out, err
	}
	plane := uuid.NewString()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Penhoon node authority " + plane}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return out, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return out, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return out, err
	}
	out = AuthorityMaterial{Version: 1, ControlPlaneID: plane, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), CAKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
	out.ServerCertificate, out.ServerKey, err = makeServerCertificate(ca, key, names, now)
	return out, err
}

func (m AuthorityMaterial) issuer(now time.Time) (*Issuer, error) {
	if m.Version != 1 || !validID(m.ControlPlaneID) {
		return nil, ErrAuthority
	}
	caBlock, rest := pem.Decode(m.CA)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrAuthority
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA || now.Before(ca.NotBefore) || !now.Before(ca.NotAfter) {
		return nil, ErrAuthority
	}
	keyBlock, rest := pem.Decode(m.CAKey)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrAuthority
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, ErrAuthority
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrAuthority
	}
	public, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(public, ca.RawSubjectPublicKeyInfo) {
		return nil, ErrAuthority
	}
	issuer := &Issuer{Certificate: ca, Signer: signer, TrustBundle: slices.Clone(m.CA)}
	if !validIssuer(issuer) || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, ErrAuthority
	}
	return issuer, nil
}

func (m AuthorityMaterial) TLS(names []string, now time.Time) (*tls.Config, *Issuer, error) {
	issuer, err := m.issuer(now)
	if err != nil {
		return nil, nil, err
	}
	ca := issuer.Certificate
	certificate, err := tls.X509KeyPair(m.ServerCertificate, m.ServerKey)
	if err != nil || len(certificate.Certificate) != 1 {
		return nil, nil, ErrAuthority
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || leaf.IsCA {
		return nil, nil, ErrAuthority
	}
	certificate.Leaf = leaf
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, name := range names {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return nil, nil, ErrAuthority
		}
	}
	if len(names) == 0 {
		return nil, nil, ErrInvalid
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: roots}, issuer, nil
}

func CreateAuthorityFile(path string, names []string) error {
	if !filepath.IsAbs(path) {
		return ErrInvalid
	}
	material, err := NewAuthority(names, time.Now())
	if err != nil {
		return err
	}
	return writeAuthority(path, material, false)
}

func LoadAuthorityFile(path string) (AuthorityMaterial, error) {
	var material AuthorityMaterial
	file, err := privatefile.Open(path, os.O_RDONLY, 0)
	if err != nil {
		return material, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return material, err
	}
	if len(encoded) > 64<<10 {
		return material, ErrAuthority
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&material); err != nil {
		return material, ErrAuthority
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return material, ErrAuthority
	}
	return material, privatefile.SyncDirectory(filepath.Dir(path))
}
