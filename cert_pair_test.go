package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/*
`p-ui cert` refuses a pair the panel cannot serve, and says so with its exit
status.

It stored two paths and never opened them, and it printed "failed:" on a write
that did not happen while still returning normally -- so the command exited 0
whatever occurred. Every caller is a script: install.sh picks the panel's URL
scheme from this, and update.sh does the same. Both read a success they were
never given, so an operator handing over a key that belongs to a different
certificate was told the certificate was applied, got an https:// URL, and found
a panel serving plain HTTP with the reason discarded to /dev/null.

Measured on the VM against the real binary before this existed: a mismatched
pair printed four success lines and exited 0.
*/
func writePair(t *testing.T, dir, name, cn string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPath := filepath.Join(dir, name+".crt")
	keyPath := filepath.Join(dir, name+".key")
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certPath, keyPath
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	body := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

/*
The validation is asserted on its own, without a database: updateCert opens the
pair before it stores anything, and standing Postgres up here would test the
setting layer instead of the check.
*/
func TestACertificateAndKeyThatDoNotPairAreRefused(t *testing.T) {
	dir := t.TempDir()
	certA, keyA := writePair(t, dir, "a", "a.example.com")
	_, keyB := writePair(t, dir, "b", "b.example.com")

	if _, err := loadCertificatePair(certA, keyA); err != nil {
		t.Fatalf("a matched pair was refused: %v", err)
	}

	err := mustFail(t, certA, keyB)
	if !strings.Contains(err.Error(), "not a usable pair") {
		t.Fatalf("the refusal does not name the problem: %v", err)
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("the refusal drops the reason the operator needs: %v", err)
	}
}

// A path that does not resolve is the same class of failure, and the operator
// gets the filename back.
func TestAMissingCertificateFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	_, keyA := writePair(t, dir, "a", "a.example.com")
	missing := filepath.Join(dir, "nope.crt")

	err := mustFail(t, missing, keyA)
	if !strings.Contains(err.Error(), "nope.crt") {
		t.Fatalf("the refusal does not name the missing file: %v", err)
	}
}

// The reset form clears both and has nothing to load, so it must not refuse.
func TestClearingTheCertificateLoadsNothing(t *testing.T) {
	if _, err := loadCertificatePair("", ""); err != nil {
		t.Fatalf("clearing the certificate was refused: %v", err)
	}
}

// And one half without the other is refused before any file is opened.
func TestOneHalfOfAPairIsRefused(t *testing.T) {
	dir := t.TempDir()
	certA, _ := writePair(t, dir, "a", "a.example.com")
	if _, err := loadCertificatePair(certA, ""); err == nil {
		t.Fatal("a certificate with no key was accepted")
	}
}

func mustFail(t *testing.T, cert, key string) error {
	t.Helper()
	_, err := loadCertificatePair(cert, key)
	if err == nil {
		t.Fatal("the pair was accepted; p-ui cert exits 0 and the installer announces https for a panel that cannot serve it")
	}
	return err
}
