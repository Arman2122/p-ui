package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

/*
An SSH exit refuses to be added without the server's fingerprint, and the
operator had to compute it by hand; three refusals in one sitting on the prod
box. The scan reports exactly what the server presents, without logging in.
*/
func TestScanSSHHostKeyReportsWhatTheServerPresents(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	authAttempts := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			cfg := &gossh.ServerConfig{PasswordCallback: func(gossh.ConnMetadata, []byte) (*gossh.Permissions, error) {
				authAttempts++
				return nil, gossh.ErrNoAuth
			}}
			cfg.AddHostKey(signer)
			go func() {
				sc, _, _, err := gossh.NewServerConn(conn, cfg)
				if err == nil {
					_ = sc.Close()
				}
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	got, err := ScanSSHHostKey(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := gossh.FingerprintSHA256(signer.PublicKey()); got.Fingerprint != want || got.KeyType != "ssh-ed25519" {
		t.Fatalf("scan = %+v, want fingerprint %s of type ssh-ed25519", got, want)
	}
	if authAttempts != 0 {
		t.Fatalf("the scan tried to authenticate %d time(s); it must stop at the key exchange", authAttempts)
	}

	_ = ln.Close()
	if _, err := ScanSSHHostKey(context.Background(), "127.0.0.1", port); err == nil || !strings.Contains(err.Error(), "ssh host key") {
		t.Fatalf("a closed port scanned without an error: %v", err)
	}
}
