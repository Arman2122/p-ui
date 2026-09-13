package singbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// Check both supported protocols even when no sing-box inbound is currently enabled.
func validateUpdateProtocols(ctx context.Context, binary string) error {
	dir, err := os.MkdirTemp("", "singbox-update-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		return err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0o600); err != nil {
		return err
	}
	for _, kind := range Kinds() {
		inst := core.Instance{
			ID: 1, Kind: kind, Tag: "update-check", Listen: "127.0.0.1", Port: 19001,
			Users: []core.User{{Email: "update-check", Enable: true, Credentials: map[string]any{
				core.CredUUID: "00000000-0000-4000-8000-000000000001", core.CredPassword: "validation-only-password",
			}}},
		}
		settings := Settings{ServerName: "localhost", CongestionControl: "bbr", RouteThroughXray: true, RouteXrayPort: 19004}
		body, err := renderConfig(inst, settings, control{statsPort: 19002, apiPort: 19003, secret: "validation-only"}, certPath, keyPath)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, string(kind)+".json")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return err
		}
		if err := checkConfig(ctx, binary, path); err != nil {
			return fmt.Errorf("singbox: update cannot support %s with panel accounting and control; installed binary preserved: %w", kind, err)
		}
	}
	return nil
}
