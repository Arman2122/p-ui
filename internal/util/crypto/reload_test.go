package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func selfSignedPEM(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writePair(t *testing.T, certFile, keyFile string, certPEM, keyPEM []byte, at time.Time) {
	t.Helper()
	for _, f := range []struct {
		path string
		body []byte
	}{{certFile, certPEM}, {keyFile, keyPEM}} {
		if err := os.WriteFile(f.path, f.body, 0o600); err != nil {
			t.Fatalf("write %s: %v", f.path, err)
		}
		if err := os.Chtimes(f.path, at, at); err != nil {
			t.Fatalf("chtimes %s: %v", f.path, err)
		}
	}
}

func served(t *testing.T, r *CertReloader) []byte {
	t.Helper()
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	return c.Certificate[0]
}

/*
The panel and subscription listeners loaded their pair once into a static
tls.Config, so an ACME renewal served the old certificate until someone
restarted the panel. The reloader re-reads the files when they change and keeps
the last good pair through a half-written renewal. Roster review 2026-09-03,
finding 7.
*/
func TestCertReloaderServesTheRenewedPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	certA, keyA := selfSignedPEM(t, "before-renewal")
	certB, keyB := selfSignedPEM(t, "after-renewal")
	t0 := time.Now().Add(-time.Minute).Truncate(time.Second)
	writePair(t, certFile, keyFile, certA, keyA, t0)
	initial, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("initial pair: %v", err)
	}
	r := NewCertReloader(certFile, keyFile, initial)
	r.interval = 0
	if !bytes.Equal(served(t, r), initial.Certificate[0]) {
		t.Fatal("the initial pair is not what is served")
	}

	writePair(t, certFile, keyFile, certB, keyB, t0.Add(10*time.Second))
	renewed, _ := tls.LoadX509KeyPair(certFile, keyFile)
	if !bytes.Equal(served(t, r), renewed.Certificate[0]) {
		t.Fatal("the renewed pair on disk is not what is served")
	}

	// A renewal caught between the two file writes: the new certificate with
	// the old key does not load, and the last good pair keeps serving.
	writePair(t, certFile, keyFile, certA, keyB, t0.Add(20*time.Second))
	if !bytes.Equal(served(t, r), renewed.Certificate[0]) {
		t.Fatal("a pair that fails to load replaced the last good pair")
	}
	if r.Err() == nil {
		t.Fatal("a pair that fails to load left no error to report")
	}
}
